package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_CMD_quit_does_not_execute(t *testing.T) {
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

	// handleCmdMode reads from a TTY path; provide a temp file containing our choice.
	tty := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(tty, []byte("q\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(tty): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("TTY", tty)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		// test model echoes input; cmd mode will then ask for execute/quit.
		gotStatus = run(strings.Split("-r -cm test cmd echo hi", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)

	// Expected behavior:
	// - model output (echoed input)
	// - newline injected by cmd-mode
	// - prompt
	want := "echo hi\n\nDo you want to [e]xecute cmd, [q]uit?: "
	testboil.FailTestIfDiff(t, stdout, want)
}
