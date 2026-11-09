package mcp

import (
	"context"
	"errors"
	"os/exec"
	"syscall"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
)

// reapProcessTree waits for the spawned server, then kills every process
// still sharing its group. A kill failure is reported, not returned: the only
// caller has already handed the process over.
func reapProcessTree(ctx context.Context, pid int, serverName string, cmd *exec.Cmd) {
	// A cancelled teardown kills the process, so a wait error is expected.
	_ = cmd.Wait()
	if err := killProcessGroup(pid); err != nil {
		runenv.Failuref(ctx, runenv.SubsystemMCP, "mcp_%v: %v", serverName, err)
	}
}

// killProcessGroup SIGKILLs every process sharing pgid; a group that is
// already empty is the state the kill exists to reach, not an error.
func killProcessGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return diag.Errorf("kill process group %d: %w", pid, err)
	}
	return nil
}
