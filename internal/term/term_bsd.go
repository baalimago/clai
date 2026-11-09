//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package term

import "syscall"

const ioctlGetTermios = syscall.TIOCGETA
