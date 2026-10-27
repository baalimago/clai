package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/text"
)

// Test_e2e_profile_runs_without_a_prompt pins that an agent profile runs on
// its defaults with no prompt and nothing piped in ('clai -prp reviewer.json
// q'), while a plain query without a prompt is still an error.
func Test_e2e_profile_runs_without_a_prompt(t *testing.T) {
	e2etest.SetupMainTestConfigDir(t)
	profile := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(profile, []byte(`{"model":"test","prompt":"You are an agent with defaults."}`), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-cm", "test", "-r", "-prp", profile, "q"})
	})
	if status != 0 {
		t.Fatalf("status = %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !strings.Contains(stdout, text.ProfileDefaultPrompt) {
		t.Fatalf("stdout = %q, want the model to receive %q", stdout, text.ProfileDefaultPrompt)
	}

	stdout, stderr = e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-cm", "test", "-r", "q"})
	})
	if status == 0 {
		t.Fatalf("a plain query without a prompt succeeded: stdout=%q", stdout)
	}
	if !strings.Contains(stderr, "found no prompt") {
		t.Fatalf("stderr = %q, want the no-prompt error", stderr)
	}
}
