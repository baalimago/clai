package utils

import (
	"syscall"
	"unsafe"
)

func TermWidth() (int, error) {
	ws := &struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}{}

	retCode, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(syscall.Stderr),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)

	if int(retCode) == -1 {
		return 0, errno
	}

	return int(ws.Col), nil
}
