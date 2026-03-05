package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_QUERY_shell_context_flag_appends_rendered_block(t *testing.T) {
	// Goldenfile-ish CLI contract test for ASC (auto-append shell context).
	//
	// Expected behaviour from architecture/SHELL-CONTEXT.md:
	// - when -add-shell-context <name> is provided, clai loads <configDir>/shellContexts/<name>.json
	// - it executes the commands in vars, renders template, and appends it to the user prompt
	// - this must not interfere with token replacement / stdin handling (covered elsewhere)
	//
	// Note: until ASC is implemented, this test will fail.

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := t.TempDir()
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
		"shellContexts",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	// Minimal shell context definition, with deterministic commands.
	ctxJSON := `{
  "shell": "/bin/sh",
  "timeout_ms": 1000,
  "timed_out_value": "<timed out>",
  "error_value": "<error>",
  "template": "[Shell context]\nfoo={{.foo}}\n",
  "vars": {
    "foo": "printf foo"
  }
}`
	if err := os.WriteFile(filepath.Join(confDir, "shellContexts", "minimal.json"), []byte(ctxJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(shell context json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test -add-shell-context minimal q hello", " "))
	})

	// The test model echoes the final user prompt.
	want := "[Shell context]\nfoo=foo\nhello"
	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, want)
}
