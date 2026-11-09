//go:build linux || darwin

package capture

import (
	"os"
	"os/exec"
	"syscall"
)

func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptGroup(process *os.Process) error {
	return signalGroup(process, syscall.SIGINT)
}

func killGroup(process *os.Process) error {
	return signalGroup(process, syscall.SIGKILL)
}

// signalGroup signals the group the process leads, and the process alone when
// it leads none.
func signalGroup(process *os.Process, sig syscall.Signal) error {
	if err := syscall.Kill(-process.Pid, sig); err != nil {
		return process.Signal(sig)
	}
	return nil
}
