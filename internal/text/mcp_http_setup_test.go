package text

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/httptestserver"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// httpSetupTestContext returns a context plus its cancel. The caller must
// defer cancel() itself, textually after its own defer srv.Close(cl): defer
// runs LIFO, so cancel() then runs before Close, closing the endpoint-based
// lazy server's connection (held open for the life of this context, by
// design, to serve calls for the rest of a real run) before the fixture
// server shuts down. t.Context() cannot be used here: it is not cancelled
// until after the test's own defers finish, which would leave the
// server-initiated stream connected when srv.Close() runs and hang it
// waiting for an idle connection.
func httpSetupTestContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func writeHttpServerConfig(t *testing.T, mcpDir, name, url string) {
	t.Helper()
	conf := fmt.Sprintf(`{"url":%q}`, url)
	if err := os.WriteFile(filepath.Join(mcpDir, name+".json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write http server config: %v", err)
	}
}

// TestSetupRegistersHttpServerToolsAndCallsOne pins phase 4's end-to-end
// contract: a config directory holding one url server has its tools
// registered under the existing mcp_<server>_<tool> prefix through the
// connector the connector selects for an endpoint-based server, and a call
// to one of them succeeds, with no child process involved anywhere in the
// path.
func TestSetupRegistersHttpServerToolsAndCallsOne(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", srv.URL)

	got, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), nil)
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

// waitForStream polls until srv reports at least one open server-initiated
// stream, or fails the test after a bounded wait.
func waitForStream(t *testing.T, srv *httptestserver.Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.StreamCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server-initiated stream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHttpSchemaCacheWarmHitIssuesNoRequestUntilCalled pins R1-15: the
// warm-cache endpoint hit branch is driven through the real production
// composition root (setupMcpManager), twice against the same cache, rather
// than through a hand-built seam. The cold run captures an entry; the warm
// run must register the same tools while issuing zero further POSTs to the
// endpoint, a tool call on the warm run must still reach the endpoint, and
// an unknown-tool call on the warm run must still invalidate the entry.
func TestHttpSchemaCacheWarmHitIssuesNoRequestUntilCalled(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) {
		c.Tools = []httptestserver.Tool{{Name: "echo", Description: "echo text"}, {Name: "ghost", Description: "will vanish"}}
		c.UnknownToolName = "ghost"
	})

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", srv.URL)
	cache := testSchemaCache(t)

	coldCtx, coldCancel := httpSetupTestContext()
	defer coldCancel()
	coldGot, err := setupMcpManager(coldCtx, mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("cold run: %v", err)
	}
	if _, ok := coldGot["mcp_httpecho_echo"]; !ok {
		t.Fatalf("cold run: mcp_httpecho_echo not registered; got: %v", toolNames(coldGot))
	}

	baseline := srv.PostRequestCount()

	warmCtx, warmCancel := httpSetupTestContext()
	defer warmCancel()
	warmGot, err := setupMcpManager(warmCtx, mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("warm run: %v", err)
	}
	echo, ok := warmGot["mcp_httpecho_echo"]
	if !ok {
		t.Fatalf("warm run: mcp_httpecho_echo not registered; got: %v", toolNames(warmGot))
	}
	ghost, ok := warmGot["mcp_httpecho_ghost"]
	if !ok {
		t.Fatalf("warm run: mcp_httpecho_ghost not registered; got: %v", toolNames(warmGot))
	}
	if got := srv.PostRequestCount(); got != baseline {
		t.Fatalf("warm run issued %d POSTs beyond the cold run's baseline %d, want 0 (a cache hit must connect nothing)", got-baseline, baseline)
	}

	res, err := echo.Call(pub_models.Input{"text": "hello"})
	if err != nil {
		t.Fatalf("warm run tool call: %v", err)
	}
	if res != "hello" {
		t.Errorf("result = %q, want %q", res, "hello")
	}
	if got := srv.PostRequestCount(); got <= baseline {
		t.Fatalf("warm run's tool call issued no POST at all, want it to reach the endpoint")
	}

	identity := schemacache.BuildIdentityWithScopes(pub_models.McpServer{Name: "httpecho", Url: srv.URL})
	if _, ok := cache.Lookup(identity); !ok {
		t.Fatal("entry missing before the unknown-tool call")
	}
	if _, err := ghost.Call(pub_models.Input{"text": "hi"}); err == nil {
		t.Fatal("expected the unknown-tool call to fail")
	}
	if _, ok := cache.Lookup(identity); ok {
		t.Fatal("entry still present after a warm-run unknown-tool call failure")
	}
}

// TestHttpSchemaCacheListChangedInvalidatesEntry pins the first of the
// endpoint-based cache's two invalidation signals: a
// notifications/tools/list_changed notification received on the
// connection invalidates the entry.
func TestHttpSchemaCacheListChangedInvalidatesEntry(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", srv.URL)
	cache := testSchemaCache(t)

	if _, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil); err != nil {
		t.Fatalf("cold run: %v", err)
	}

	identity := schemacache.BuildIdentityWithScopes(pub_models.McpServer{Name: "httpecho", Url: srv.URL})
	if _, ok := cache.Lookup(identity); !ok {
		t.Fatal("no entry captured after the cold run")
	}

	waitForStream(t, srv)
	if err := srv.PushNotification("notifications/tools/list_changed", nil); err != nil {
		t.Fatalf("PushNotification: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := cache.Lookup(identity); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("entry was never invalidated after a tools/list_changed notification")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHttpSchemaCacheUnknownToolInvalidatesEntry pins the second
// invalidation signal: a tools/call response diagnosed as an unknown-tool
// failure invalidates the entry, without recording the failure itself.
func TestHttpSchemaCacheUnknownToolInvalidatesEntry(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	ctx, cancel := httpSetupTestContext()
	defer cancel()
	// "ghost" is listed by tools/list (so it is cached and registered) but
	// the server answers tools/call for it as unknown, simulating a tool
	// that existed when the list was captured and vanished since.
	srv.Configure(func(c *httptestserver.Config) {
		c.Tools = []httptestserver.Tool{{Name: "echo", Description: "echo text"}, {Name: "ghost", Description: "will vanish"}}
		c.UnknownToolName = "ghost"
	})

	mcpDir := t.TempDir()
	writeHttpServerConfig(t, mcpDir, "httpecho", srv.URL)
	cache := testSchemaCache(t)

	got, err := setupMcpManager(ctx, mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("cold run: %v", err)
	}
	ghost, ok := got["mcp_httpecho_ghost"]
	if !ok {
		t.Fatalf("mcp_httpecho_ghost not registered; got: %v", toolNames(got))
	}

	identity := schemacache.BuildIdentityWithScopes(pub_models.McpServer{Name: "httpecho", Url: srv.URL})
	if _, ok := cache.Lookup(identity); !ok {
		t.Fatal("no entry captured after the cold run")
	}

	if _, err := ghost.Call(pub_models.Input{"text": "hi"}); err == nil {
		t.Fatal("expected the unknown-tool call to fail")
	}

	if _, ok := cache.Lookup(identity); ok {
		t.Fatal("entry still present after an unknown-tool call failure")
	}
}
