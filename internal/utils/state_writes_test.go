package utils

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test_stateFilesUseAtomicWrites is the regression gate for the torn-read class:
// a state file that another clai process may read must be replaced by rename, not
// truncated in place. A plain os.WriteFile here reproduces "unexpected end of
// JSON input" under concurrent runs.
func Test_stateFilesUseAtomicWrites(t *testing.T) {
	stateDirs := []string{".", "../skills", "../chat", "../setup"}
	for _, dir := range stateDirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if strings.Contains(string(b), "os.WriteFile(") {
				t.Errorf("%s writes state with os.WriteFile; use WriteFileAtomic", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %q: %v", dir, err)
		}
	}
}
