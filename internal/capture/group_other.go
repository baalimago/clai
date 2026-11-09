//go:build !linux && !darwin

package capture

import (
	"os"
	"os/exec"
)

func ownProcessGroup(*exec.Cmd) {}

func interruptGroup(process *os.Process) error {
	return process.Signal(os.Interrupt)
}

func killGroup(process *os.Process) error {
	return process.Kill()
}
