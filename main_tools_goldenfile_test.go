package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_TOOLS_lists_tools_and_footer(t *testing.T) {
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

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("tools", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)

	// We don't assert the entire listing because it changes as tools are added.
	// Instead, assert stable behaviors described in architecture/TOOLS.md.
	testboil.AssertStringContains(t, stdout, "Run 'clai tools <tool-name>' for more details.\n")
}

func Test_goldenFile_TOOLS_unknown_tool_errors(t *testing.T) {
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

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("tools definitely_not_a_tool", " "))
	})

	if gotStatus == 0 {
		t.Fatalf("expected non-zero status code")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got: %q", stdout)
	}
}
