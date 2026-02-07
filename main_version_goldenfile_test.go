package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_VERSION_prints_version_and_exits_0(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := t.TempDir()
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("version", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	if gotStdout == "" {
		t.Fatal("expected version output to be non-empty")
	}
	// The exact version depends on build info / VCS state; assert stable prefix.
	testboil.AssertStringContains(t, gotStdout, "version: ")
}
