package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// Test_AgentSetup_ExplicitMcpCredentialFailure_FailsSetup pins D42/R2-05
// through the real public surface: an explicit server's credential-source
// failure must reach the same strict/degrade fork a spawn failure does.
// Before the fix, *mcpauth.CredentialSourceError never unwrapped to
// claierr.ErrMcpServerStartup, the only sentinel setupTooling classifies
// on, so this exact scenario returned Setup() == nil with the server's
// tools silently absent.
func Test_AgentSetup_ExplicitMcpCredentialFailure_FailsSetup(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithMcpServers([]models.McpServer{
			{
				Name: "explicit-http-cred",
				Url:  "http://127.0.0.1:1/mcp",
				Auth: &models.McpServerAuth{TokenCommand: []string{"/nonexistent-clai-test-binary-cred"}},
			},
		}),
	)

	err := a.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup must fail when an explicit server's credential source fails")
	}
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
	}
	names := mcpStartupServerNames(err)
	if !slices.Contains(names, "explicit-http-cred") {
		t.Errorf("startup errors name %v, want it to include explicit-http-cred", names)
	}
}

// Test_AgentSetup_CmdBanAppliesToMcpCredentialCommand pins R1-05: a
// credential command is subject to the same command-ban policy a
// tool-call context carries, so a banned command must never be spawned as
// a credential helper, including on the public WithMcpServers surface the
// ban policy's own context attachment did not reach before the fix.
func Test_AgentSetup_CmdBanAppliesToMcpCredentialCommand(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithCmdBanList("sh"),
		WithMcpServers([]models.McpServer{
			{
				Name: "explicit-http-banned-cred",
				Url:  "http://127.0.0.1:1/mcp",
				Auth: &models.McpServerAuth{TokenCommand: []string{"sh", "-c", "echo token"}},
			},
		}),
	)

	err := a.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup must fail: the credential command matches the ban policy and must never run")
	}
	if !strings.Contains(err.Error(), "banned by policy") {
		t.Fatalf("err = %v, want it to name the ban-policy refusal", err)
	}
}

// Test_AgentSetup_CmdBanAppliesToAmbientLazyHttpCredentialCommand pins
// R1-05's other half: an ambient (config-directory) endpoint-based server
// defaults to lazy (D16/D18), so its credential resolution runs through
// the connector's own runCtx rather than the eager path's staticHttpDecorator
// call. Both must carry the ban policy. The command is never spawned,
// proven the same way mcpauth's own TestCredentialCommandBannedIsNeverSpawned
// does: a marker file the command would create stays absent.
func Test_AgentSetup_CmdBanAppliesToAmbientLazyHttpCredentialCommand(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	markerPath := filepath.Join(t.TempDir(), "marker")
	a := newCmdBanAgent(t, WithModel("mock_test"), WithCmdBanList("sh"))

	mcpDir := filepath.Join(a.cfgDir, "mcpServers")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", mcpDir, err)
	}
	conf := fmt.Sprintf(`{"url":"http://127.0.0.1:1/mcp","auth":{"token_command":["sh","-c","touch %s"]}}`, markerPath)
	if err := os.WriteFile(filepath.Join(mcpDir, "ambient-http.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("WriteFile(ambient-http.json): %v", err)
	}

	// Ambient, so a credential failure degrades rather than failing Setup
	// (D13); the assertion that matters is that the command never ran.
	if err := a.Setup(context.Background()); err != nil {
		t.Fatalf("an ambient server's credential failure must not fail Setup: %v", err)
	}
	if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
		t.Fatal("banned credential command still ran on the ambient lazy path: marker file exists")
	}
}
