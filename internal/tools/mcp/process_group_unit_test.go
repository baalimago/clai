package mcp

import (
	"os/exec"
	"testing"
)

// TestKillProcessGroupEmptyGroupIsNil: a group that is already gone is not an
// error.
func TestKillProcessGroupEmptyGroupIsNil(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if err := killProcessGroup(cmd.Process.Pid); err != nil {
		t.Errorf("killProcessGroup on a dead pid: %v", err)
	}
}
