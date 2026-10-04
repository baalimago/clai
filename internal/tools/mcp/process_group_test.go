package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestStdioConnTeardownKillsGrandchild: a cancelled run must leave no process
// behind; without the group sweep the grandchild runs until reboot.
func TestStdioConnTeardownKillsGrandchild(t *testing.T) {
	requireProcFS(t)
	ctx, cancel := context.WithCancel(context.Background())
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	conn, err := NewStdioConn(ctx, pub_models.McpServer{
		Name:    "grandparent",
		Command: testServerBinary(t),
		Env: map[string]string{
			"TEST_SERVER_SPAWN_GRANDCHILD":    "300",
			"TEST_SERVER_GRANDCHILD_PID_FILE": pidFile,
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Call(ctx, "initialize", nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read grandchild pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("grandchild pid %q: %v", raw, err)
	}

	cancel()

	awaitProcessGone(t, pid)
}

func requireProcFS(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("process liveness is read through /proc")
	}
}

// processAlive reports whether pid is a live process. A zombie is a
// reaped-but-unwaited process, so the state comes from /proc rather than a
// zero signal.
func processAlive(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return syscall.Kill(pid, 0) == nil
	}
	stat := string(raw)
	idx := strings.LastIndex(stat, ")")
	if idx < 0 || idx+2 >= len(stat) {
		return true
	}
	return stat[idx+2] != 'Z' && stat[idx+2] != 'X'
}
