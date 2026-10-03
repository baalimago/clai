package mcp

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestStdioConnReapKillsGrandchildOnSelfExit: nothing cancels the run, so the
// sweep must come from the reaper, not from the cancellation hook.
func TestStdioConnReapKillsGrandchildOnSelfExit(t *testing.T) {
	requireProcFS(t)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	sink := &recordingSink{}
	conn, err := NewStdioConn(t.Context(), pub_models.McpServer{
		Name:    "self-exiting",
		Command: testServerBinary(t),
		Env: map[string]string{
			"TEST_SERVER_SPAWN_GRANDCHILD":    "300",
			"TEST_SERVER_GRANDCHILD_PID_FILE": pidFile,
			"TEST_SERVER_EXIT":                "1",
		},
	}, sink)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	pid := awaitGrandchildPid(t, pidFile)

	awaitProcessGone(t, pid)

	if _, exited := sink.snapshot(); len(exited) == 0 {
		t.Error("an unexpected exit while the run context is alive must be reported")
	}
}

func awaitGrandchildPid(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if convErr != nil {
				t.Fatalf("grandchild pid %q: %v", raw, convErr)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the fixture never reported its grandchild pid")
	return 0
}

func awaitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Errorf("grandchild %d survived the reap", pid)
	}
}
