//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package cloudcmd

import "os"

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
