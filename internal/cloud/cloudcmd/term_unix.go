//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package cloudcmd

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal asks the terminal driver for f's settings, which only a
// terminal has: /dev/null is a character device too, but no terminal.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
