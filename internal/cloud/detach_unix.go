//go:build !windows

package cloud

import (
	"os/exec"
	"syscall"
)

// detach lets the background sync outlive the command that started it and
// keeps it out of the terminal's process group, so ^C never reaches it.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
