package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_HELP_prints_usage(t *testing.T) {
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
		gotStatusCode = run(strings.Split("help", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	if gotStdout == "" {
		t.Fatal("expected help output to be non-empty")
	}
	// The usage string is large; check for one stable snippet and that config dir was interpolated.
	testboil.AssertStringContains(t, gotStdout, "Usage:")
	testboil.AssertStringContains(t, gotStdout, confDir)
}

func Test_goldenFile_HELP_profile_prints_profile_help(t *testing.T) {
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
		gotStatusCode = run(strings.Split("help profile", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	want := internal.ProfileHelp + "\n"
	testboil.FailTestIfDiff(t, gotStdout, want)
}
