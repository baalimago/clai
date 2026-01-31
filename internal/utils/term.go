package utils

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// TermWidth returns the current terminal width.
//
// In CI / tests there is often no TTY attached, so ioctl(TIOCGWINSZ) returns
// "inappropriate ioctl for device". In that case we fall back to a sane default
// width (80) or the value from $COLUMNS if present.
func TermWidth() (int, error) {
	// Prefer explicit override when present.
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n, nil
		}
	}

	ws := &struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}{}

	retCode, _, _ := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(syscall.Stderr),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)

	if int(retCode) == -1 {
		// Non-tty (common in tests): fall back.
		return 80, nil
	}

	if ws.Col == 0 {
		return 80, nil
	}
	return int(ws.Col), nil
}
