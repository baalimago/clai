//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package cloudcmd

import "syscall"

const ioctlGetTermios = syscall.TIOCGETA
