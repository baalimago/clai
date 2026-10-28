//go:build !unix

package testenv

import (
	"errors"
	"io/fs"
)

func roomAt(string) (int64, error) {
	return 0, errors.New("filesystem room is not measured on this platform")
}

func ownedByUs(fs.FileInfo) bool { return false }
