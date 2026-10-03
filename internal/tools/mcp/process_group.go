package mcp

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// reapProcessTree waits for the spawned server, then kills every process
// still sharing its group. A kill failure is reported, not returned: the only
// caller has already handed the process over.
func reapProcessTree(pid int, serverName string, cmd *exec.Cmd) {
	// A cancelled teardown kills the process, so a wait error is expected.
	_ = cmd.Wait()
	if err := killProcessGroup(pid); err != nil {
		ancli.Errf("mcp_%v: %v\n", serverName, err)
	}
}

// killProcessGroup SIGKILLs every process sharing pgid; a group that is
// already empty is the state the kill exists to reach, not an error.
func killProcessGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group %d: %w", pid, err)
	}
	return nil
}
