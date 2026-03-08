package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_QUERY_shell_context_flag_appends_wrapped_rendered_block(t *testing.T) {
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

	ctxJSON := `{
  "shell": "/bin/sh",
  "timeout_ms": 1000,
  "timed_out_value": "<timed out>",
  "error_value": "<error>",
  "template": "foo={{.foo}}\n",
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

	want := "<shell context>\nfoo=foo\n</shell context>\nhello\n"
	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, want)
}
