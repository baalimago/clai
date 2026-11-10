//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package term

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal asks the terminal driver for f's settings, which only a
// terminal has: /dev/null is a character device too, but no terminal.
func IsTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
