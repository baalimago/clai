//go:build linux

package e2e

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// Test_e2e_reply_only_shows_progress_on_a_terminal_stderr pins
// clai -reply-only q ... > reply.md: the file gets the reply alone, and the
// terminal on stderr shows the tool activity the way a terminal stdout would,
// then says where the reply went.
func Test_e2e_reply_only_shows_progress_on_a_terminal_stderr(t *testing.T) {
	bin := filepath.Join(claiBinDir(t), "clai")
	e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	workDir := e2etest.ChdirTemp(t)

	master, slave := openPTY(t)
	defer master.Close()
	prompt := "look tool_pwd"
	cmd := exec.Command(bin, append([]string{"-cm", "test", "-t", "pwd", "q", "-reply-only"}, strings.Fields(prompt)...)...)
	cmd.Stderr = slave
	stdout, err := cmd.Output()
	slave.Close()
	if err != nil {
		t.Fatalf("clai failed: %v", err)
	}
	terminal, _ := io.ReadAll(master)

	if got, want := string(stdout), "done after tool for: "+prompt+"\n"; got != want {
		t.Fatalf("stdout = %q, want only the reply %q", got, want)
	}
	for _, want := range []string{"▸ pwd", workDir, "reply written to stdout (1 line)"} {
		if !strings.Contains(string(terminal), want) {
			t.Errorf("terminal lacks %q: %q", want, terminal)
		}
	}
	if strings.Contains(string(terminal), "Call: 'pwd'") {
		t.Errorf("terminal shows the raw transcript instead of the progress window: %q", terminal)
	}
}
