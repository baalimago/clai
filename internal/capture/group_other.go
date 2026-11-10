//go:build (!linux && !darwin) || wasix

package capture

import (
	"os"
	"os/exec"
)

func ownProcessGroup(*exec.Cmd) {}

func interruptGroup(process *os.Process) error {
	return interruptProcess(process)
}

func killGroup(process *os.Process) error {
	return killProcess(process)
}
