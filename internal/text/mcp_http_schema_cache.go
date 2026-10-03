package text

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// resolveLazyHttpServerViaCache is the schema-cache-aware path for a
// lazy-resolved endpoint-based server. A hit registers its tools wired to
// an unresolved Connector, so no transport is constructed. A miss connects
// now, registers from the live handshake, and captures the entry, exactly
// mirroring resolveLazyServerViaCache's command-based behaviour. Either
// way the registered connector is wrapped so a later tools/call diagnosed
// as an unknown-tool failure, or a notifications/tools/list_changed
// observed on the connection, invalidates the entry (phase 4's
// endpoint-based half of the schema cache). authz resolves the
// credential-precedence chain and the interactive fallback a bare
// connect's challenge drives (phase 5); nil keeps the server
// unauthenticated exactly as before this phase.
func resolveLazyHttpServerViaCache(ctx context.Context, cache *schemacache.Cache, server pub_models.McpServer, sink mcp.ServerLogSink, registrar mcp.ToolRegistrar, authz *mcpauth.Authorizer, outputIsTerminal bool) error {
	// BuildIdentityWithScopes, not BuildIdentity: the requested scopes
	// (auth.scopes) change which tools an endpoint exposes, so they are part
	// of this identity (phase 5). A server with no Auth block produces the
	// same identity BuildIdentity would.
	identity := schemacache.BuildIdentityWithScopes(server)
	timeout := time.Duration(server.TimeoutSeconds) * time.Second
	// outputIsTerminal resolves the auth-timeout parameter (phase 6, D22). A
	// cache hit's tools carry the interactive-flow AuthResolver
	// (httpChallengeResolver), since that path's Connector resolves
	// mid-run, on the first tool call.
	authTimeout := resolveAuthTimeout(server, outputIsTerminal)

	if cache != nil {
		if rec, ok := cache.Lookup(identity); ok {
			connector := newAuthenticatingHttpConnector(ctx, authz, server, sink, connectorOptsFor(server)...)
			invalidating := newCacheInvalidatingConnector(ctx, connector, cache, identity, server.Name)
			return mcp.RegisterTools(rec.Tools, server.Name, invalidating, timeout, registrar, httpChallengeResolver(authz, server), authTimeout)
		}
	}

	connCtx, cancel := context.WithCancel(ctx)
	// Only cancel on failure; on success the connection must stay alive to
	// serve tool calls for the rest of the run, cleaned up when ctx ends.
	var connected bool
	defer func() {
		if !connected {
			cancel()
		}
	}()

	conn, serverInfo, toolsArray, err := handshakeHttpServerWithAuth(ctx, connCtx, authz, server, sink)
	if err != nil {
		return err
	}

	if err := mcp.RegisterTools(toolsArray, server.Name, mcp.NewResolvedConnector(newCacheInvalidatingConn(conn, cache, identity, server.Name)), timeout, registrar, nil, authTimeout); err != nil {
		return err
	}
	connected = true

	if cache != nil {
		// Capture before watching (R1-27): a list_changed notification
		// arriving in the gap would otherwise invalidate an entry that does
		// not exist yet (Invalidate is then a no-op), and Capture would go
		// on to write an entry already known stale. Watched under connCtx,
		// not the run context: on a RegisterTools failure above, cancel()
		// already ran (connected stays false) and this code is unreached,
		// but should RegisterTools ever succeed and a later defect cancel
		// connCtx directly, the watcher stops with the connection it
		// watches instead of outliving it for the rest of the run.
		if captureErr := cache.Capture(identity, mcp.ProtocolVersion, serverInfo, toolsArray); captureErr != nil {
			ancli.Warnf("failed to persist mcp schema cache entry for %q: %v\n", server.Name, captureErr)
		}
		if watcher, ok := conn.(mcp.NotificationWatcher); ok {
			go watchForToolsListChanged(connCtx, watcher, cache, identity, server.Name)
		}
	}
	return nil
}

// cacheInvalidatingConnector wraps a Connector so the Conn it resolves is
// itself wrapped for cache invalidation, and so the tools-list-changed
// watcher is started exactly once regardless of how many callers resolve
// it.
type cacheInvalidatingConnector struct {
	inner      mcp.Connector
	cache      *schemacache.Cache
	identity   schemacache.Identity
	serverName string
	runCtx     context.Context
	once       sync.Once
}

func newCacheInvalidatingConnector(runCtx context.Context, inner mcp.Connector, cache *schemacache.Cache, identity schemacache.Identity, serverName string) mcp.Connector {
	return &cacheInvalidatingConnector{inner: inner, cache: cache, identity: identity, serverName: serverName, runCtx: runCtx}
}

func (w *cacheInvalidatingConnector) Conn(ctx context.Context) (mcp.Conn, error) {
	conn, err := w.inner.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if watcher, ok := conn.(mcp.NotificationWatcher); ok {
		w.once.Do(func() { go watchForToolsListChanged(w.runCtx, watcher, w.cache, w.identity, w.serverName) })
	}
	return newCacheInvalidatingConn(conn, w.cache, w.identity, w.serverName), nil
}

// watchForToolsListChanged invalidates identity's cache entry the moment
// watcher reports a notifications/tools/list_changed notification. It
// returns when runCtx ends; nothing else stops it, since an endpoint's
// NotificationWatcher channel is never closed before then.
func watchForToolsListChanged(runCtx context.Context, watcher mcp.NotificationWatcher, cache *schemacache.Cache, identity schemacache.Identity, serverName string) {
	for {
		select {
		case method, ok := <-watcher.Notifications():
			if !ok {
				return
			}
			if method == "notifications/tools/list_changed" {
				if err := cache.Invalidate(identity); err != nil {
					ancli.Warnf("failed to invalidate mcp schema cache entry for %q: %v\n", serverName, err)
				}
			}
		case <-runCtx.Done():
			return
		}
	}
}

// cacheInvalidatingConn wraps a live Conn so a tools/call diagnosed as an
// unknown-tool failure invalidates identity's cache entry before the
// result reaches the caller. Nothing else about the call is altered: the
// invalidation is a side effect, never a second outcome.
type cacheInvalidatingConn struct {
	inner      mcp.Conn
	cache      *schemacache.Cache
	identity   schemacache.Identity
	serverName string
}

func newCacheInvalidatingConn(inner mcp.Conn, cache *schemacache.Cache, identity schemacache.Identity, serverName string) mcp.Conn {
	return &cacheInvalidatingConn{inner: inner, cache: cache, identity: identity, serverName: serverName}
}

func (w *cacheInvalidatingConn) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	raw, err := w.inner.Call(ctx, method, params)
	if method == "tools/call" && w.cache != nil {
		toolName, _ := params["name"].(string)
		if isUnknownToolFailure(raw, err, toolName) {
			if invErr := w.cache.Invalidate(w.identity); invErr != nil {
				ancli.Warnf("failed to invalidate mcp schema cache entry for %q: %v\n", w.serverName, invErr)
			}
		}
	}
	return raw, err
}

func (w *cacheInvalidatingConn) Notify(ctx context.Context, method string, params map[string]any) error {
	return w.inner.Notify(ctx, method, params)
}

func (w *cacheInvalidatingConn) Close() error { return w.inner.Close() }

// isUnknownToolFailure reports whether a tools/call outcome is the
// unknown-tool signal the README's "Schema cache for endpoint-based
// servers" section defines concretely: a JSON-RPC error whose code is the
// method-not-found code and whose message names the tool, or whose code is
// the invalid-params code, names the tool and additionally carries an
// unknown/not-found marker, or a result whose isError is true and whose
// text names the tool as unknown. Nothing else counts as this signal; an
// ordinary tool failure (e.g. a validation error from the tool itself,
// which the invalid-params code also covers) does not invalidate anything
// (R1-20: -32602 alone is not enough, since a server's validation error for
// a known tool uses exactly that code).
func isUnknownToolFailure(raw json.RawMessage, err error, toolName string) bool {
	if toolName == "" {
		return false
	}
	if err != nil {
		var rpcErr *claierr.McpRPCError
		if !errors.As(err, &rpcErr) {
			return false
		}
		if !strings.Contains(rpcErr.Message, toolName) {
			return false
		}
		if rpcErr.Code == -32601 {
			return true
		}
		if rpcErr.Code == -32602 {
			return strings.Contains(strings.ToLower(rpcErr.Message), "unknown")
		}
		return false
	}

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if jsonErr := json.Unmarshal(raw, &result); jsonErr != nil || !result.IsError {
		return false
	}
	var text strings.Builder
	for _, c := range result.Content {
		text.WriteString(c.Text)
	}
	combined := text.String()
	return strings.Contains(combined, toolName) && strings.Contains(strings.ToLower(combined), "unknown")
}
