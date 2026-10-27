package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// Test_e2e_reply_only_keeps_the_transcript_off_stdout pins -reply-only: a
// tool-using query writes only the reply to stdout, so a parent agent or a
// pipe reads the answer, and the tool calls and their output go to stderr.
func Test_e2e_reply_only_keeps_the_transcript_off_stdout(t *testing.T) {
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	e2etest.SetupMainTestConfigDir(t)
	workDir := t.TempDir()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir(%q): %v", workDir, err)
	}

	prompt := "look tool_pwd"
	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split("-cm test -r -t pwd q -reply-only "+prompt, " "))
	})
	if status != 0 {
		t.Fatalf("status = %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !strings.Contains(stdout, "done after tool for: "+prompt) {
		t.Fatalf("stdout lacks the reply: %q", stdout)
	}
	for _, transcript := range []string{"Call: 'pwd'", workDir} {
		if strings.Contains(stdout, transcript) {
			t.Errorf("stdout holds transcript %q: %q", transcript, stdout)
		}
		if !strings.Contains(stderr, transcript) {
			t.Errorf("stderr lacks transcript %q: %q", transcript, stderr)
		}
	}
}
