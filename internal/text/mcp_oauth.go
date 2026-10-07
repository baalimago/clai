package text

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// defaultAuthTimeoutOnTerminal is the auth-timeout parameter's
// terminal-session default (D22).
const defaultAuthTimeoutOnTerminal = 120 * time.Second

// resolveAuthTimeout applies the auth-timeout parameter (D22): a
// server-configured value always wins, including an explicit 0 (always
// fail fast); otherwise the default follows whether the session's output
// is a terminal.
func resolveAuthTimeout(server pub_models.McpServer, outputIsTerminal bool) time.Duration {
	if server.AuthTimeoutSeconds != nil {
		return time.Duration(*server.AuthTimeoutSeconds) * time.Second
	}
	if outputIsTerminal {
		return defaultAuthTimeoutOnTerminal
	}
	return 0
}

// httpChallengeResolver builds the AuthResolver an endpoint-based server's
// lazily resolved tools carry (phase 6): driving the interactive OAuth flow
// is the one action worth taking while a mid-run authorization wait is
// open. authz nil (no authorizer configured for the run) disables it,
// leaving the tool-call site to just wait out the bound with nothing to
// drive, exactly as a command-based server's tools already do.
//
// A non-interactive run keeps its resolver, so the tool-call site still
// gives the model the actionable "run clai mcp auth" result rather than the
// command-based wording: AuthorizeInteractive itself refuses before opening
// a browser or binding a loopback listener (sign-off review, B2 — the
// mid-run twin of the setup path's R2-08).
func httpChallengeResolver(authz *mcpauth.Authorizer, server pub_models.McpServer) mcp.AuthResolver {
	if authz == nil {
		return nil
	}
	return mcp.AuthResolverFunc(func(ctx context.Context, challenge *claierr.AuthChallengeError) error {
		_, err := authz.AuthorizeInteractive(ctx, server, challenge)
		return err
	})
}

// Interactivity follows the run's configured terminal signal, not the host
// process stdout. An empty config directory disables stored authorization.
func newMcpAuthorizer(confDir string, trustInput io.Reader, outputIsTerminal bool, sink mcp.ServerLogSink) *mcpauth.Authorizer {
	if confDir == "" {
		return nil
	}
	store := mcpauth.NewTokenStore(path.Join(confDir, mcpauth.TokenStoreDirName))
	return mcp.NewAuthorizer(mcp.AuthorizerConfig{
		Store: store, Input: trustInput, Output: authOutputFor(sink), Interactive: outputIsTerminal,
	})
}

// authPrintWriter is implemented by a ServerLogSink that also exposes the
// writer it buffers or forwards lines to (mcpLogSink today). Feature-
// detected the same way notifyMcpSetupSucceeded already checks for an
// optional sink capability.
type authPrintWriter interface {
	AuthPrintWriter() io.Writer
}

func authOutputFor(sink mcp.ServerLogSink) io.Writer {
	w, ok := sink.(authPrintWriter)
	if !ok || w.AuthPrintWriter() == nil {
		return io.Discard
	}
	return w.AuthPrintWriter()
}

// staticHttpDecorator resolves only the static half of the
// credential-precedence chain (auth.token_command, then auth.token_env):
// no network request, no interactive flow. It is what an eager HTTP
// server's connection is decorated with; the full chain, including the
// interactive fallback a bare connect's challenge drives, is the lazy
// path's (resolveLazyHttpServerViaCache and dialHttpServerWithAuth).
func staticHttpDecorator(ctx context.Context, server pub_models.McpServer) (mcp.RequestDecorator, error) {
	token, _, ok, err := mcpauth.ResolveStaticCredential(ctx, server, mcp.LoadEnvFile)
	if err != nil || !ok {
		return nil, err
	}
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }, nil
}

// dialHttpServerWithAuth connects server, trying only the cached half of
// the credential-precedence chain (auth.token_command, auth.token_env, or a
// warm, still-fresh token store entry). authz nil keeps the server
// unauthenticated exactly as before phase 5: a challenge then surfaces
// unchanged, since D15's "unset is skipped silently" rule has nothing left
// to try.
//
// It is this server's lazy Connector's dial function, so per D20 it
// performs exactly one resolution attempt and never the interactive flow
// itself: a mid-run AuthChallengeError must fail resolution fast, with no
// resolution in flight while the tool-call site's bounded wait runs
// (internal/text/tool_executor.go). That wait, on success, drives
// mcpauth.Authorizer.AuthorizeInteractive directly (httpChallengeResolver)
// and saves the resulting token to the store, so the follow-up resolution
// this same dial function performs finds a fresh token store hit here and
// connects with no challenge at all — no decorator needs threading back in.
func dialHttpServerWithAuth(ctx context.Context, authz *mcpauth.Authorizer, server pub_models.McpServer, sink mcp.ServerLogSink, opts ...mcp.ConnectorOption) (mcp.Conn, error) {
	dialOpts := append([]mcp.ConnectorOption{}, opts...)
	if authz != nil {
		decorator, _, ok, err := authz.ResolveCached(ctx, server)
		if err != nil && !ok {
			return nil, err
		}
		if err != nil {
			// R1-09: ok is true here only when ResolveCached degraded a
			// token-store write failure rather than discarding a valid,
			// freshly refreshed token; the run continues with it, with the
			// write failure surfaced rather than swallowed.
			ancli.Warnf("%v\n", err)
		}
		if ok {
			dialOpts = append(dialOpts, mcp.WithHttpRequestDecorator(mcp.RequestDecorator(decorator)))
		}
	}

	connector := mcp.NewHttpConnector(ctx, server, sink, dialOpts...)
	return connector.Conn(ctx)
}

// handshakeHttpServerWithAuth performs the full initialize/notify/tools-list
// handshake (mcp.Handshake) over a fresh HttpConn under connCtx, decorated
// with whatever authz.ResolveCached already resolves, falling back to the
// interactive flow exactly once when that bare attempt is challenged. It
// is the cache-miss lazy path's own connect: unlike dialHttpServerWithAuth
// it needs the handshake's tools/list result, not just a connected Conn,
// so it drives mcp.NewHttpConn and mcp.Handshake directly rather than going
// through a Connector.
func handshakeHttpServerWithAuth(ctx, connCtx context.Context, authz *mcpauth.Authorizer, server pub_models.McpServer, sink mcp.ServerLogSink) (conn mcp.Conn, serverInfo, toolsArray []byte, err error) {
	opts, err := httpConnOptsFor(ctx, authz, server)
	if err != nil {
		return nil, nil, nil, err
	}
	conn = mcp.NewHttpConn(connCtx, server, sink, opts...)
	serverInfo, toolsArray, err = runBoundedHandshake(ctx, conn, server)
	if err == nil {
		return conn, serverInfo, toolsArray, nil
	}

	var challenge *claierr.AuthChallengeError
	// authz.Interactive gates the browser-redirect/printed-URL flow on the
	// setup path (R2-08): it is read nowhere before this fix, so a
	// non-interactive run (every pkg/agent SDK consumer, since
	// userConf.OutputIsTerminal is always false there) fell through to
	// AuthorizeInteractive anyway, which could block indefinitely on a
	// loopback redirect and print to the process's real stdout — behaviour
	// a library consumer must never observe.
	if authz == nil || !authz.Interactive || !errors.As(err, &challenge) {
		return nil, nil, nil, err
	}
	// The setup-path interactive flow is bounded by the resolved
	// auth-timeout (R2-08): AuthorizeInteractive's own loopback wait has no
	// bound of its own otherwise, so a redirect that never arrives would
	// block agent.Setup indefinitely.
	authCtx, authCancel := context.WithTimeout(ctx, resolveAuthTimeout(server, authz.Interactive))
	decorator2, authErr := authz.AuthorizeInteractive(authCtx, server, challenge)
	authCancel()
	conn.Close() // R1-29: the bare, challenged probe is no longer needed either way.
	if authErr != nil {
		var writeErr *mcpauth.TokenStoreWriteError
		if !errors.As(authErr, &writeErr) || decorator2 == nil {
			return nil, nil, nil, authErr
		}
		// R1-09: the exchange itself succeeded; only persisting it failed.
		// The freshly issued token is still used for this connection
		// instead of being discarded, with the write failure surfaced
		// rather than swallowed.
		ancli.Warnf("%v\n", authErr)
	}
	conn2 := mcp.NewHttpConn(connCtx, server, sink, mcp.WithRequestDecorator(mcp.RequestDecorator(decorator2)))
	serverInfo2, toolsArray2, err2 := runBoundedHandshake(ctx, conn2, server)
	if err2 != nil {
		conn2.Close() // R1-29: the second attempt's connection on a failed retry.
		return nil, nil, nil, err2
	}
	return conn2, serverInfo2, toolsArray2, nil
}

// httpConnOptsFor resolves the credential-precedence chain's cached half
// (no interactive flow: that only runs once a bare connect is actually
// challenged) into the HttpConnOption a fresh connection is built with.
func httpConnOptsFor(ctx context.Context, authz *mcpauth.Authorizer, server pub_models.McpServer) ([]mcp.HttpConnOption, error) {
	if authz == nil {
		return nil, nil
	}
	decorator, _, ok, err := authz.ResolveCached(ctx, server)
	if err != nil && !ok {
		return nil, err
	}
	if err != nil {
		// R1-09: as in dialHttpServerWithAuth, ok is true here only when a
		// valid, freshly refreshed token survived a token-store write
		// failure; the run continues with it instead of discarding it.
		ancli.Warnf("%v\n", err)
	}
	if !ok {
		return nil, nil
	}
	return []mcp.HttpConnOption{mcp.WithRequestDecorator(mcp.RequestDecorator(decorator))}, nil
}

// runBoundedHandshake wraps conn's handshake in the connect-bound, not the
// connection-level handshake bound: this is the cache-miss connect, the
// same one a stdio Connector bounds internally, and connect_timeout_seconds
// must have an effect on it or a hung endpoint blocks cold setup for the
// 30s handshake default regardless of what the server configures (R1-14,
// mirroring resolveLazyServerViaCache's stdio fix).
func runBoundedHandshake(ctx context.Context, conn mcp.Conn, server pub_models.McpServer) (serverInfo, toolsArray []byte, err error) {
	connectCtx, cancel := context.WithTimeout(ctx, mcp.ConnectBoundOf(server))
	defer cancel()
	serverInfo, toolsArray, err = mcp.Handshake(connectCtx, conn, server.Name)
	if err != nil {
		return nil, nil, mcp.ReportAsConnectStage(err, connectCtx, server.Name)
	}
	return serverInfo, toolsArray, nil
}

// newAuthenticatingHttpConnector wraps dialHttpServerWithAuth in this
// package's own single-flight memoisation (mcp.NewConnectorFromDial), so a
// cache hit's lazily resolved Connector performs the same credential
// resolution and interactive fallback on its first real tool call, shared
// by every concurrent caller that arrives while that first resolution is
// in flight.
func newAuthenticatingHttpConnector(runCtx context.Context, authz *mcpauth.Authorizer, server pub_models.McpServer, sink mcp.ServerLogSink, opts ...mcp.ConnectorOption) mcp.Connector {
	return mcp.NewConnectorFromDial(runCtx, func(ctx context.Context) (mcp.Conn, error) {
		return dialHttpServerWithAuth(ctx, authz, server, sink, opts...)
	})
}
