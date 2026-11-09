//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package term

import "os"

// IsTerminal reports whether f is a character device: without a terminal
// driver to ask, that is the best this platform can tell.
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
