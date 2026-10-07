package skills

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Test_writeJSONFile_replacesWithoutTruncating pins the invariant that broke
// under concurrent runs: a reader that already has the file open must keep
// seeing the old file. A truncating write makes that handle observe the new,
// partly written file instead.
func Test_writeJSONFile_replacesWithoutTruncating(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	old := Config{Enabled: true, ProjectSkillDirs: []string{"old"}}
	if err := writeJSONFile(path, old); err != nil {
		t.Fatalf("writeJSONFile(old): %v", err)
	}
	oldBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(old): %v", err)
	}

	held, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(held): %v", err)
	}
	defer held.Close()

	if err := writeJSONFile(path, Config{Enabled: false, ProjectSkillDirs: []string{"new"}}); err != nil {
		t.Fatalf("writeJSONFile(new): %v", err)
	}

	got := make([]byte, len(oldBytes)+64)
	n, err := held.ReadAt(got, 0)
	if err != nil && err.Error() != "EOF" {
		t.Fatalf("held.ReadAt: %v", err)
	}
	if string(got[:n]) != string(oldBytes) {
		t.Errorf("an open handle saw the replacement, so the write truncated in place:\ngot:  %q\nwant: %q", string(got[:n]), string(oldBytes))
	}
}

// Test_LoadConfig_survivesConcurrentLoads reproduces the failure seen when five
// clai processes start at once: one of them read a torn skills config and died
// with "load skills config: unexpected end of JSON input".
func Test_LoadConfig_survivesConcurrentLoads(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadConfig(dir); err != nil {
		t.Fatalf("seed LoadConfig: %v", err)
	}
	path := filepath.Join(dir, configFileName)

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 4 {
		wg.Go(func() {
			for range 25 {
				if _, err := LoadConfig(dir); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	for range 2 {
		wg.Go(func() {
			for i := range 50 {
				cfg := Config{Enabled: i%2 == 0, ProjectSkillDirs: []string{"p"}}
				if err := writeJSONFile(path, cfg); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent load failed: %v", err)
	}
}
