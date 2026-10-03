package text

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestStdioSchemaCacheUnknownToolInvalidatesEntry pins R2-16's stdio half of
// the unknown-tool invalidation signal: a tools/call response diagnosed as
// unknown, over the real spawned process (not a hand-built seam), invalidates
// the entry, exactly as the endpoint-based TestHttpSchemaCacheUnknownToolInvalidatesEntry
// already proves for HTTP. Before this fix, resolveLazyServerViaCache (the
// stdio path) wrapped neither the cache-hit connector nor the cache-miss
// conn, so this invalidation could never fire for a command-based server.
func TestStdioSchemaCacheUnknownToolInvalidatesEntry(t *testing.T) {
	bin := testServerBinary(t)
	mcpDir := t.TempDir()
	env := map[string]string{"TEST_SERVER_UNKNOWN_TOOL_NAME": "ghost"}
	conf := fmt.Sprintf(`{"command":%q,"env":{"TEST_SERVER_UNKNOWN_TOOL_NAME":"ghost"},"startup":"lazy"}`, bin)
	if err := os.WriteFile(filepath.Join(mcpDir, "stdioecho.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cache := testSchemaCache(t)

	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	ghost, ok := got["mcp_stdioecho_ghost"]
	if !ok {
		t.Fatalf("mcp_stdioecho_ghost not registered; got: %v", toolNames(got))
	}

	identity := schemacache.BuildIdentity(pub_models.McpServer{Name: "stdioecho", Command: bin, Env: env})
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

// TestStdioSchemaCacheListChangedInvalidatesEntry pins R2-16's stdio half of
// the tools-list-changed invalidation signal: a
// notifications/tools/list_changed notification received on a stdio
// connection invalidates the entry, exactly as
// TestHttpSchemaCacheListChangedInvalidatesEntry already proves for HTTP.
func TestStdioSchemaCacheListChangedInvalidatesEntry(t *testing.T) {
	bin := testServerBinary(t)
	mcpDir := t.TempDir()
	env := map[string]string{"TEST_SERVER_LIST_CHANGED_TRIGGER_TOOL": "trigger"}
	conf := fmt.Sprintf(`{"command":%q,"env":{"TEST_SERVER_LIST_CHANGED_TRIGGER_TOOL":"trigger"},"startup":"lazy"}`, bin)
	if err := os.WriteFile(filepath.Join(mcpDir, "stdioecho.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cache := testSchemaCache(t)

	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	trigger, ok := got["mcp_stdioecho_trigger"]
	if !ok {
		t.Fatalf("mcp_stdioecho_trigger not registered; got: %v", toolNames(got))
	}

	identity := schemacache.BuildIdentity(pub_models.McpServer{Name: "stdioecho", Command: bin, Env: env})
	if _, ok := cache.Lookup(identity); !ok {
		t.Fatal("no entry captured after the cold run")
	}

	if _, err := trigger.Call(pub_models.Input{"text": "hi"}); err != nil {
		t.Fatalf("trigger call: %v", err)
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
