package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/clai/pkg/text/models"
)

// mcpStartupServerNames collects the server name of every
// *claierr.McpServerStartupError in err's chain. errors.Join exposes no
// iteration API, so the walk descends the stdlib unwrap contract directly.
func mcpStartupServerNames(err error) []string {
	var names []string
	var visit func(error)
	visit = func(e error) {
		if e == nil {
			return
		}
		if startup, ok := e.(*claierr.McpServerStartupError); ok {
			names = append(names, startup.ServerName)
			return
		}
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, sub := range multi.Unwrap() {
				visit(sub)
			}
			return
		}
		visit(errors.Unwrap(e))
	}
	visit(err)
	return names
}

// Test_AgentSetup_ExplicitMcpFailure_JoinedTyped pins the phase-8 D13 Setup
// contract through the real public surface: servers named via WithMcpServers
// are load-bearing, so a spawn failure fails Setup with a joined, typed,
// per-server error instead of warn-and-degrade. Setup returning nil means
// every explicitly requested server is running (worklog
// 2026-09-05-error-propagation, D13).
func Test_AgentSetup_ExplicitMcpFailure_JoinedTyped(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithMcpServers([]models.McpServer{
			{Name: "explicit-broken-a", Command: "/nonexistent-clai-test-binary-a"},
			{Name: "explicit-broken-b", Command: "/nonexistent-clai-test-binary-b"},
		}),
	)

	err := a.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup must fail when an explicitly requested MCP server cannot start")
	}
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
	}
	names := mcpStartupServerNames(err)
	if !slices.Equal(names, []string{"explicit-broken-a", "explicit-broken-b"}) {
		t.Errorf("startup errors name %v, want [explicit-broken-a explicit-broken-b]", names)
	}
}

// Test_AgentSetup_AmbientMcpFailure_Degrades pins the D13 ambient side: a
// server discovered from the config directory is not load-bearing, so its
// startup failure keeps the warn-and-degrade contract — Setup returns nil and
// the run proceeds without the server (worklog 2026-09-05-error-propagation,
// D13).
func Test_AgentSetup_AmbientMcpFailure_Degrades(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	a := newCmdBanAgent(t, WithModel("mock_test"))

	mcpDir := filepath.Join(a.cfgDir, "mcpServers")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", mcpDir, err)
	}
	broken := []byte(`{"command":"/nonexistent-clai-test-binary","args":[]}`)
	if err := os.WriteFile(filepath.Join(mcpDir, "broken.json"), broken, 0o644); err != nil {
		t.Fatalf("WriteFile(broken.json): %v", err)
	}

	if err := a.Setup(context.Background()); err != nil {
		t.Fatalf("a broken ambient server must not fail Setup: %v", err)
	}
	chat, err := a.Query(context.Background(), cmdBanChatForPrompt("hello without tools"))
	if err != nil {
		t.Fatalf("Agent.Query after degraded setup: %v", err)
	}
	if len(chat.Messages) == 0 {
		t.Error("expected the run to proceed and produce messages")
	}
}
