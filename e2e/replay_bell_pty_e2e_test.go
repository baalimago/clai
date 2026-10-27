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

// Test_e2e_dirscope_replay_honors_notification_bell pins the bell against
// theme.json: dir-replay runs through the adapter's default Run, which rings
// the completion bell, so an unloaded theme would ring against the user's
// explicit opt-out. stdout is a pipe and stderr a terminal, so the bell also
// pins its routing: it rings on the terminal, never into the redirected
// reply.
func Test_e2e_dirscope_replay_honors_notification_bell(t *testing.T) {
	for _, tc := range []struct {
		name string
		bell bool
	}{
		{name: "bell disabled in theme", bell: false},
		{name: "bell enabled in theme", bell: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := filepath.Join(claiBinDir(t), "clai")
			confDir := e2etest.SetupMainTestConfigDir(t)
			t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
			e2etest.ChdirTemp(t)
			writeReplayTheme(t, confDir, tc.bell)

			if status := harness.Run(strings.Split("-r -cm mock_test q hello", " ")); status != 0 {
				t.Fatalf("seed query status %d", status)
			}

			master, slave := openPTY(t)
			defer master.Close()
			cmd := exec.Command(bin, "dre")
			cmd.Stderr = slave
			stdout, err := cmd.Output()
			slave.Close()
			if err != nil {
				t.Fatalf("clai dre failed: %v", err)
			}
			terminal, _ := io.ReadAll(master)

			if strings.Contains(string(stdout), "\a") {
				t.Fatalf("bell rung into redirected stdout: %q", stdout)
			}
			if got := strings.Contains(string(terminal), "\a"); got != tc.bell {
				t.Fatalf("bell rung on terminal stderr: %v, want %v, stderr: %q", got, tc.bell, terminal)
			}
		})
	}
}
