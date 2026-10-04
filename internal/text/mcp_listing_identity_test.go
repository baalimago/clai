package text

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// TestToolsListAgreesWithProductionSetupForAuthScopedCommandServer proves
// R1-16's phase-7 half through the real production composition root
// rather than warming the cache with the listing's own identity builder,
// which can never catch an identity mismatch between setup and the
// listing (the worklog's own cross-phase invariant). A command-based
// server declaring "auth":{"scopes":[...]} is resolved by setupMcpManager's
// real lazy cache-miss path (resolveLazyServerViaCache, keyed by
// BuildIdentity), and `clai tools` (keyed by BuildIdentityWithScopes) must
// still find its cached tools. Before D40 unified the two builders for
// every command-based server, this diverged silently and permanently.
func TestToolsListAgreesWithProductionSetupForAuthScopedCommandServer(t *testing.T) {
	bin := testServerBinary(t)
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	mcpDir := filepath.Join(configDir, "mcpServers")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	conf := fmt.Sprintf(`{"command":%q,"auth":{"scopes":["x"]},"startup":"lazy"}`, bin)
	if err := os.WriteFile(filepath.Join(mcpDir, "stdioecho.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cache, err := newMcpSchemaCache()
	if err != nil {
		t.Fatalf("newMcpSchemaCache: %v", err)
	}
	if _, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil); err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}

	tools.Init()
	var listErr error
	got := testboil.CaptureStdout(t, func(t *testing.T) { listErr = tools.List() })
	if listErr != nil {
		t.Fatalf("tools.List: %v", listErr)
	}
	if !strings.Contains(got, "mcp_stdioecho_echo") {
		t.Fatalf("expected the auth-scoped command server's production-warmed cache entry visible to the listing, got:\n%s", got)
	}
}
