package testserverbin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProducesAnExecutable(t *testing.T) {
	path, err := builtServer()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%q is not executable: %v", path, info.Mode())
	}
}

func TestBuildFailureLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	_, err := build(dir, "github.com/baalimago/clai/internal/tools/mcp/testserverbin/no-such-package")
	if err == nil || !strings.Contains(err.Error(), "go build") {
		t.Fatalf("err = %v, want a go build error", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("the failed build left %d files behind, want none", len(entries))
	}
}

func TestBuildFailsWhenTheTempDirIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if _, err := build(dir, testServerPkg); err == nil || !strings.Contains(err.Error(), "create temp file") {
		t.Fatalf("err = %v, want a create temp file error", err)
	}
}
