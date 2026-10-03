package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/httptestserver"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/oauthtestserver"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func testAuthorizer(t *testing.T) *mcpauth.Authorizer {
	t.Helper()
	// WithInteractive(true): these tests simulate a terminal-attached,
	// interactive run with a browser available, which is exactly what
	// production's newMcpAuthorizer sets for a terminal session (R2-08);
	// without it the setup-path interactive flow is now correctly skipped.
	return mcpauth.NewAuthorizer(mcpauth.NewTokenStore(t.TempDir()),
		mcpauth.WithBrowserOpener(oauthtestserver.NewAutoFollowOpener()),
		mcpauth.WithInteractive(true),
	)
}

// TestSetupAuthorizesHttpServerAfterChallenge proves the real boundary the
// phase's integration contract names: a config directory holding one
// challenged endpoint-based server, with no static credential and no
// stored token, still has its tools registered and callable through
// setupMcpManager's lazy cache-miss path — the production wiring in
// mcp_http_schema_cache.go and mcp_oauth.go, not the Authorizer called
// directly. Phase 4's own httptestserver fixture is configured for the
// challenge; phase 5's oauthtestserver fixture answers discovery through
// exchange; no second MCP-over-HTTP fake is introduced.
func TestSetupAuthorizesHttpServerAfterChallenge(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.FixedAccessToken = "fixed-access-token-for-test" })

	hs := httptestserver.New()
	defer hs.Close()
	// The composed chain's resource server is hs, so that is the RFC 9728
	// resource identifier the authorization fixture must declare; clai
	// refuses a document describing anything else (sign-off review, B2).
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.RequireBearerToken = "fixed-access-token-for-test"
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", hs.URL)

	got, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), testAuthorizer(t))
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	tool, ok := got["mcp_httpecho_echo"]
	if !ok {
		t.Fatalf("mcp_httpecho_echo not registered; got: %v", toolNames(got))
	}
	res, err := tool.Call(pub_models.Input{"text": "hello"})
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if res != "hello" {
		t.Errorf("result = %q, want %q", res, "hello")
	}
}

// TestSetupToolCallSucceedsDespiteUnwritableTokenStore pins R1-09 through
// the real setup-path boundary (handshakeHttpServerWithAuth): a token
// store that cannot be written must not discard the valid, freshly issued
// access token the interactive flow just obtained. Before the fix,
// AuthorizeInteractive and doRefresh both returned a zero TokenEntry on a
// save failure, so this exact scenario failed the tool call even though
// the OAuth exchange itself succeeded.
func TestSetupToolCallSucceedsDespiteUnwritableTokenStore(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.FixedAccessToken = "unwritable-store-token" })

	hs := httptestserver.New()
	defer hs.Close()
	// The composed chain's resource server is hs, so that is the RFC 9728
	// resource identifier the authorization fixture must declare; clai
	// refuses a document describing anything else (sign-off review, B2).
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.RequireBearerToken = "unwritable-store-token"
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	// store.dir is a path through a regular file, not a directory, so
	// every Save deterministically fails regardless of the test's uid
	// (the same trick mcpauth's own TestTokenStoreUnwritableIsTypedError
	// uses).
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	store := mcpauth.NewTokenStore(filepath.Join(blocker, "mcpAuth"))
	authz := mcpauth.NewAuthorizer(store,
		mcpauth.WithBrowserOpener(oauthtestserver.NewAutoFollowOpener()),
		mcpauth.WithInteractive(true),
	)

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", hs.URL)

	got, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), authz)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	tool, ok := got["mcp_httpecho_echo"]
	if !ok {
		t.Fatalf("mcp_httpecho_echo not registered; got: %v", toolNames(got))
	}
	res, err := tool.Call(pub_models.Input{"text": "still works"})
	if err != nil {
		t.Fatalf("tool call must succeed despite an unwritable token store: %v", err)
	}
	if res != "still works" {
		t.Errorf("result = %q, want %q", res, "still works")
	}
}

// TestSetupNonInteractiveSkipsAuthorizeInteractive pins R2-08 through the
// real setup-path boundary: a non-interactive run (outputIsTerminal false
// — every pkg/agent SDK consumer's posture) must never attempt the
// browser-redirect/printed-URL flow at all, so it can neither block nor
// print to anything. poisonReader would block this test forever if
// AuthorizeInteractive's printed-URL fallback were ever reached (its
// write end is never written to or closed); the outer deadline is what
// would fire if the fix regressed, which this test treats as the failure
// signal rather than a correctness bound in its own right.
func TestSetupNonInteractiveSkipsAuthorizeInteractive(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	hs := httptestserver.New()
	defer hs.Close()
	// The composed chain's resource server is hs, so that is the RFC 9728
	// resource identifier the authorization fixture must declare; clai
	// refuses a document describing anything else (sign-off review, B2).
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.RequireBearerToken = "irrelevant-nothing-can-supply-it"
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	poisonReader, poisonWriter := io.Pipe()
	defer poisonWriter.Close()

	authz := newMcpAuthorizer(t.TempDir(), poisonReader, false, &recordingSuccessSink{})
	if authz.Interactive {
		t.Fatal("newMcpAuthorizer(outputIsTerminal=false) must build a non-interactive Authorizer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mcpDir := t.TempDir()
	// auth_timeout_seconds is set explicitly, well past this test's own
	// 3s deadline: D22 says an explicit value always wins over the
	// terminal-ness default, so this is what would give
	// AuthorizeInteractive a real window to reach the printed-URL fallback
	// (and so block on poisonReader) if the Interactive gate itself were
	// missing — proving this test exercises the gate, not merely the
	// non-terminal default's own zero bound.
	conf := fmt.Sprintf(`{"url":%q,"auth_timeout_seconds":30}`, hs.URL)
	if err := os.WriteFile(filepath.Join(mcpDir, "httpecho.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write http server config: %v", err)
	}

	done := make(chan struct{})
	var got map[string]pub_models.LLMTool
	var err error
	go func() {
		got, err = setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), authz)
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("setupMcpManager did not return before the deadline: a non-interactive run attempted the interactive flow and blocked (R2-08)")
	}
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if _, ok := got["mcp_httpecho_echo"]; ok {
		t.Fatal("tool registered despite no credential being available to a non-interactive run")
	}
}

// TestSchemaCacheNeverPersistsAuthFailure pins invariant 2 against this
// phase's own failure mode: a challenged server whose authorization never
// completes (registration is rejected here) leaves no entry in the schema
// cache, since only content-determined data — a successful tools/list — is
// ever captured.
func TestSchemaCacheNeverPersistsAuthFailure(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.RegistrationRejects = true })

	hs := httptestserver.New()
	defer hs.Close()
	// The composed chain's resource server is hs, so that is the RFC 9728
	// resource identifier the authorization fixture must declare; clai
	// refuses a document describing anything else (sign-off review, B2).
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.RequireBearerToken = "whatever-token"
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	cacheDir := t.TempDir()
	cache, err := schemacache.New(cacheDir)
	if err != nil {
		t.Fatalf("schemacache.New: %v", err)
	}

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", hs.URL)

	// The server is ambient (config-directory discovered), so its failure
	// keeps today's warn-and-degrade posture (D13) rather than failing
	// setupMcpManager outright: the assertion that matters here is that
	// nothing was cached, not that setup itself errored.
	got, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, cache, testAuthorizer(t))
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if _, ok := got["mcp_httpecho_echo"]; ok {
		t.Fatal("tool registered despite a rejected registration")
	}

	entries, readErr := os.ReadDir(cacheDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read cache dir: %v", readErr)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			t.Fatalf("auth failure was persisted to the schema cache: %s", e.Name())
		}
	}
}

// TestLazyHttpMidRunChallengeRetriesAfterInteractiveAuth is a supplementary
// test beyond the phase's declared names (phase 5's own convention: add a
// cheap one where the declared set left an easy gap): it proves phase 6's
// real production wiring for the HTTP transport, end to end, mirroring
// TestStdioConnectReclassifiesAuthPromptAsChallenge's stdio coverage. A
// cache-hit lazy server's Connector (newAuthenticatingHttpConnector) is
// exactly what a mid-run first tool call resolves; per D20 its dial
// function (dialHttpServerWithAuth) no longer drives the interactive flow
// itself, so a bare attempt with no stored credential fails fast with
// *claierr.AuthChallengeError. Driving httpChallengeResolver — the
// tool-call site's AuthResolver — against that challenge runs the real
// interactive flow and saves a token; the connector's very next
// resolution then succeeds because its own ResolveCached call now hits
// that fresh token store entry, with no decorator threaded back in by
// hand.
func TestLazyHttpMidRunChallengeRetriesAfterInteractiveAuth(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.FixedAccessToken = "mid-run-fixed-token" })

	hs := httptestserver.New()
	defer hs.Close()
	// The composed chain's resource server is hs, so that is the RFC 9728
	// resource identifier the authorization fixture must declare; clai
	// refuses a document describing anything else (sign-off review, B2).
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.RequireBearerToken = "mid-run-fixed-token"
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	authz := testAuthorizer(t)
	server := pub_models.McpServer{Name: "httpecho", Url: hs.URL}
	connector := newAuthenticatingHttpConnector(ctx, authz, server, nil)

	_, err := connector.Conn(ctx)
	var challenge *claierr.AuthChallengeError
	if !errors.As(err, &challenge) {
		t.Fatalf("first mid-run resolution err = %v, want *claierr.AuthChallengeError", err)
	}

	resolver := httpChallengeResolver(authz, server)
	if resolver == nil {
		t.Fatal("httpChallengeResolver returned nil for a non-nil authorizer")
	}
	if err := resolver.ResolveChallenge(ctx, challenge); err != nil {
		t.Fatalf("ResolveChallenge: %v", err)
	}

	conn, err := connector.Conn(ctx)
	if err != nil {
		t.Fatalf("retried mid-run resolution: %v", err)
	}
	raw, err := conn.Call(ctx, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "hi"}})
	if err != nil {
		t.Fatalf("tools/call after interactive auth: %v", err)
	}
	if !strings.Contains(string(raw), "hi") {
		t.Fatalf("tools/call result = %s, want it to echo the input", raw)
	}
}

// TestMcpHttpStatusErrorNeverLeaksAccessToken pins R1-10's claierr.McpHttpStatusError
// row: that type's Message is documented as a verbatim response body, so
// the risk is not the type itself but whether the request-side secret (the
// real access token this connection authenticates every call with) ever
// gets embedded alongside it. The fixture is reconfigured mid-test to fail
// after a real token is already in use, so the failing request really did
// carry that token as its Authorization header.
func TestMcpHttpStatusErrorNeverLeaksAccessToken(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.FixedAccessToken = "mcp-http-status-secret-token" })

	hs := httptestserver.New()
	defer hs.Close()
	// The composed chain's resource server is hs, so that is the RFC 9728
	// resource identifier the authorization fixture must declare; clai
	// refuses a document describing anything else (sign-off review, B2).
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.RequireBearerToken = "mcp-http-status-secret-token"
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", hs.URL)

	got, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), testAuthorizer(t))
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	tool, ok := got["mcp_httpecho_echo"]
	if !ok {
		t.Fatalf("mcp_httpecho_echo not registered; got: %v", toolNames(got))
	}

	// Flip the fixture to fail now that the connection already holds the
	// real access token: the next call's request still carries it.
	hs.Configure(func(c *httptestserver.Config) { c.FailStatus = 500; c.FailMessage = "internal error" })

	_, callErr := tool.Call(pub_models.Input{"text": "hi"})
	if callErr == nil {
		t.Fatal("expected the call to fail once the fixture starts failing")
	}
	var statusErr *claierr.McpHttpStatusError
	if !errors.As(callErr, &statusErr) {
		t.Fatalf("got %v (%T), want *claierr.McpHttpStatusError", callErr, callErr)
	}
	if strings.Contains(statusErr.Error(), "mcp-http-status-secret-token") {
		t.Errorf("McpHttpStatusError leaked the access token: %v", statusErr)
	}
}

// TestLazyHttpCacheMissConnectBoundAppliesToHandshake pins R1-14's HTTP
// half: handshakeHttpServerWithAuth/runHandshake used to bound only the
// connection-level handshake bound (30s default), ignoring
// connect_timeout_seconds entirely, exactly mirroring the stdio miss path's
// own R1-14 fix (TestLazyCacheMissConnectBoundAppliesToStdioHandshake). A
// 1s connect_timeout_seconds against a fake that never answers must fire
// well under the 30s default.
func TestLazyHttpCacheMissConnectBoundAppliesToHandshake(t *testing.T) {
	hs := httptestserver.New()
	defer hs.Close()
	hs.Configure(func(c *httptestserver.Config) { c.HangForever = true })

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	conf := fmt.Sprintf(`{"url":%q,"connect_timeout_seconds":1}`, hs.URL)
	if err := os.WriteFile(filepath.Join(mcpDir, "hangs.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write http server config: %v", err)
	}

	start := time.Now()
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), nil); err != nil {
			t.Fatalf("an ambient-posture connect failure must degrade, not fail setup: %v", err)
		}
	})
	elapsed := time.Since(start)

	// Generous upper margin, well short of the 30s handshake-bound default
	// the unfixed code fell back to.
	if elapsed > 15*time.Second {
		t.Fatalf("elapsed %v, want the 1s connect_timeout_seconds to fire well before the 30s handshake-bound default", elapsed)
	}
	if !strings.Contains(stdout, "stage 'connect'") {
		t.Errorf("stdout = %q, want a connect-stage failure", stdout)
	}
}

// TestMidRunChallengeOnNonInteractiveRunNeverOpensBrowser pins the sign-off
// review's B2 item on this file: the setup path was gated on
// authz.Interactive (R2-08), but the mid-run path — httpChallengeResolver,
// the AuthResolver a lazily resolved tool carries — was not. A headless
// pkg/agent consumer that merely set auth_timeout_seconds therefore opened
// a browser and bound a loopback listener inside its own process, and then
// blocked on a redirect nothing was going to drive. The gate now lives at
// the root, in AuthorizeInteractive, so this path inherits it: the resolver
// is still built (the tool-call site must keep giving the model the
// endpoint-based actionable result, not the command-based wording) but it
// refuses before anything leaves the process.
func TestMidRunChallengeOnNonInteractiveRunNeverOpensBrowser(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	hs := httptestserver.New()
	defer hs.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	opener := &countingBrowserOpener{}
	authz := mcpauth.NewAuthorizer(mcpauth.NewTokenStore(t.TempDir()),
		mcpauth.WithBrowserOpener(opener),
	)
	server := pub_models.McpServer{Name: "httpecho", Url: hs.URL}

	resolver := httpChallengeResolver(authz, server)
	if resolver == nil {
		t.Fatal("httpChallengeResolver returned nil, so the tool-call site would report a command-based server")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := resolver.ResolveChallenge(ctx, &claierr.AuthChallengeError{
		ServerName:       "httpecho",
		ResourceMetadata: as.ProtectedResourceURL(),
	})
	var disabled *mcpauth.InteractiveAuthDisabledError
	if !errors.As(err, &disabled) {
		t.Fatalf("got %v (%T), want *mcpauth.InteractiveAuthDisabledError", err, err)
	}
	if opener.opens != 0 {
		t.Errorf("opened a browser %d times on a headless run, want 0", opener.opens)
	}
	if as.RequestCount() != 0 {
		t.Errorf("issued %d authorization-server requests on a headless run, want 0", as.RequestCount())
	}
}

// countingBrowserOpener records hand-offs without performing one, so a
// headless run can be shown never to have reached the browser.
type countingBrowserOpener struct{ opens int }

func (o *countingBrowserOpener) Open(string) error { o.opens++; return nil }
