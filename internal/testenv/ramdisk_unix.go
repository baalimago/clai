//go:build unix

package testenv

import (
	"io/fs"
	"os"
	"syscall"

	"github.com/baalimago/clai/internal/diag"
)

// roomAt is the space this user may still fill at path, in bytes.
func roomAt(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, diag.Errorf("statfs %s: %w", path, err)
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func ownedByUs(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
