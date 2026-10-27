package tooling

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// TestExplicitMcpServerExitReportsOneLine pins the error a profile's MCP
// server prints when its process exits before the handshake, as an npx
// package that fails to install does: one line naming the server and
// pointing at its log, without the setup layers' wrapping chain.
func TestExplicitMcpServerExitReportsOneLine(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := e2etest.SetupMainTestConfigDir(t)
	profile := fmt.Sprintf(`{"model":"test","use_tools":true,"mcp_servers":{"broken":{"command":%q,"env":{"TEST_SERVER_CRASH_TAIL":"1"}}}}`, testServerBinary(t))
	if err := os.WriteFile(filepath.Join(confDir, "profiles", "crashy.json"), []byte(profile), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split("-r -cm test -p crashy q hello", " "))
	})
	if status != 1 {
		t.Fatalf("status = %d, want 1; stdout=%q stderr=%q", status, stdout, stderr)
	}
	out := stdout + stderr
	want := "mcp server 'broken' failed to start at stage 'connect': it exited before answering; its log above says why"
	if !strings.Contains(out, want) {
		t.Errorf("output = %q, want it to contain %q", out, want)
	}
	for _, chain := range []string{"failed to create text querier", "failed to select text querier", "failed to start explicitly requested MCP servers", "is closed"} {
		if strings.Contains(out, chain) {
			t.Errorf("output = %q, want no %q", out, chain)
		}
	}
}
