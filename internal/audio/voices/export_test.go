package voices

import (
	"io/fs"
	"os"
)

// KeepFiles makes l fail to delete the clip files keep names, as a file
// system that refuses the deletion would.
func (l *Library) KeepFiles(keep func(path string) bool) {
	l.removeFile = func(path string) error {
		if keep(path) {
			return &os.PathError{Op: "remove", Path: path, Err: fs.ErrPermission}
		}
		return os.Remove(path)
	}
}
