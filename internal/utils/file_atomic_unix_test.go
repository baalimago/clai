//go:build unix

package utils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWriteFileAtomic_respectsUmask pins that perm is applied under the
// process umask, as os.WriteFile does (review 3, R3-09). Not parallel: the
// umask is process-wide.
func TestWriteFileAtomic_respectsUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	path := filepath.Join(t.TempDir(), "model.json")
	if err := WriteFileAtomic(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %o, want 0600 (0644 under umask 077)", got)
	}
}
