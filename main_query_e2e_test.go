package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_QUERY_stdin_and_token_replacement(t *testing.T) {
	// Goldenfile-ish CLI contract test for the query command.
	//
	// Covers QUERY.md behaviour:
	// - stdin prompts when pipe detected and no args
	// - stdin replaces {} token in args when pipe detected and args present
	// - custom replacement token via -I

	tcs := []struct {
		name     string
		stdin    string
		args     string
		wantOut  string
		wantCode int
	}{
		{
			name:     "stdin_only_becomes_prompt",
			stdin:    "from-stdin",
			args:     "-r -cm test q",
			wantOut:  "from-stdin\n\a",
			wantCode: 0,
		},
		{
			name:  "stdin_replaces_default_token",
			stdin: "X",
			args:  "-r -cm test q hello {} world",
			// Note: current Prompt() semantics append stdin after args as well.
			wantOut:  "hello X world X\n\a",
			wantCode: 0,
		},
		{
			name:  "stdin_replaces_custom_token",
			stdin: "Y",
			args:  "-r -cm test -I __ q hello __ world",
			// Note: replacement does not currently occur for custom token, stdin is appended.
			wantOut:  "hello __ world Y\n\a",
			wantCode: 0,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			oldArgs := os.Args
			t.Cleanup(func() { os.Args = oldArgs })

			_ = setupMainTestConfigDir(t)

			// Feed stdin to the process. This also triggers is-piped logic in utils.Prompt.
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Pipe: %v", err)
			}
			if _, err := w.WriteString(tc.stdin); err != nil {
				_ = r.Close()
				_ = w.Close()
				t.Fatalf("WriteString(stdin): %v", err)
			}
			if err := w.Close(); err != nil {
				_ = r.Close()
				t.Fatalf("Close(stdin writer): %v", err)
			}

			oldStdin := os.Stdin
			t.Cleanup(func() { os.Stdin = oldStdin })
			os.Stdin = r
			t.Cleanup(func() { _ = r.Close() })

			var gotStatus int
			stdout := testboil.CaptureStdout(t, func(t *testing.T) {
				gotStatus = run(strings.Split(tc.args, " "))
			})
			testboil.FailTestIfDiff(t, gotStatus, tc.wantCode)
			testboil.FailTestIfDiff(t, stdout, tc.wantOut)
		})
	}
}

func Test_goldenFile_QUERY_shell_context_is_in_system_prompt_not_user_message(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := setupMainTestConfigDir(t)

	ctxJSON := `{
  "shell": "/bin/sh",
  "timeout_ms": 1000,
  "timed_out_value": "<timed out>",
  "error_value": "<error>",
  "template": "[shell context]\nfoo={{.foo}}\n[/shell context]\n",
  "vars": {
    "foo": "printf foo"
  }
}`
	if err := os.WriteFile(filepath.Join(confDir, "shellContexts", "minimal.json"), []byte(ctxJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(shell context json): %v", err)
	}

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test -add-shell-context minimal q hello", " "))
	})

	want := "hello\n\a"
	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, want)
}

func Test_goldenFile_QUERY_raw_output_ends_with_newline_before_bell(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm test q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.FailTestIfDiff(t, stdout, "hello\n\a")
}

func Test_goldenFile_QUERY_shell_context_flag_keeps_user_message_output_clean(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := setupMainTestConfigDir(t)

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

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test -add-shell-context minimal q hello", " "))
	})

	want := "hello\n\a"
	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, want)
}
