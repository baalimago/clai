package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Test_e2e_chat_list_alias_equivalence pins that both alias forms of the
// nested chat list resolve to the same subcommand, without any config
// writes.
func Test_e2e_chat_list_alias_equivalence(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	writeNotificationTestPriceFiles(t, confDir)
	if status := run(strings.Split("-cm test -r q seed for listing", " ")); status != 0 {
		t.Fatal("failed to seed conversation")
	}

	var outputs []string
	for _, args := range []string{"-n -r c l 0 b", "-n -r chat list 0 b"} {
		stdout, status := runOne(t, confDir, args)
		if status != 0 {
			t.Fatalf("%q: expected zero status, got %d. stdout=%q", args, status, stdout)
		}
		outputs = append(outputs, stdout)
	}
	testboil.FailTestIfDiff(t, outputs[0], outputs[1])
}

// Test_e2e_chat_list_on_readonly_config_dir proves the read-only chat subs
// are structurally read-only: listing against a chmod-read-only config dir
// succeeds without writes.
func Test_e2e_chat_list_on_readonly_config_dir(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())
	var restore []string
	err := filepath.Walk(confDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			restore = append(restore, path)
			return os.Chmod(path, 0o555)
		}
		return err
	})
	if err != nil {
		t.Fatalf("chmod read-only: %v", err)
	}
	t.Cleanup(func() {
		for _, path := range restore {
			_ = os.Chmod(path, 0o755)
		}
	})

	stdout, status := runOne(t, t.TempDir(), "-n c l 0")
	if status != 0 {
		t.Fatalf("expected zero status against read-only config dir, got %d. stdout=%q", status, stdout)
	}
}

// Test_e2e_sub_flag_before_command_is_forwarded pins the placement
// convenience: a flag owned by a deeper level, written before the command,
// reaches the level that defines it instead of failing. All three of these
// are audio-transcribe flags written one or two levels too shallow.
func Test_e2e_sub_flag_before_command_is_forwarded(t *testing.T) {
	testCases := []struct {
		desc string
		args string
	}{
		{desc: "before the top-level command", args: "-parallelism 2 -am test -af text a t "},
		{desc: "between the command and its verb", args: "a -am test -af text t "},
		{desc: "split across levels", args: "-am test a -af text t "},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_ = setupMainTestConfigDir(t)
			audioFile := writeE2EAudioFile(t)

			var status int
			var stdout string
			stderr := testboil.CaptureStderr(t, func(t *testing.T) {
				stdout = testboil.CaptureStdout(t, func(t *testing.T) {
					status = run(strings.Split(tc.args+audioFile, " "))
				})
			})

			if status != 0 {
				t.Fatalf("expected the forwarded flags to be accepted, got status %v: %v", status, stderr)
			}
			// -af text proves the forwarded value landed, not just parsed.
			testboil.FailTestIfDiff(t, stdout, wantMockText)
		})
	}
}

// Test_e2e_parent_flag_after_sub_is_forwarded pins the other direction: a
// flag only the parent defines, written after the subcommand, reaches the
// parent instead of failing. 'chat help' registers no flags of its own; -p
// lives on the chat parent.
func Test_e2e_parent_flag_after_sub_is_forwarded(t *testing.T) {
	confDir := setupMainTestConfigDir(t)

	stdout, status := runOne(t, confDir, "chat help -p gopher")
	if status != 0 {
		t.Fatalf("expected the forwarded parent flag to be accepted, got status %v: %q", status, stdout)
	}
}

// Test_e2e_off_path_flag_still_hints pins the limit of the convenience: a
// flag whose owning command this run never reaches is an error naming that
// owner, since a flag configuring nothing must not pass silently.
func Test_e2e_off_path_flag_still_hints(t *testing.T) {
	testCases := []struct {
		desc     string
		args     string
		wantHint []string
	}{
		{
			desc:     "audio flag on a query",
			args:     "-parallelism 2 q hello",
			wantHint: []string{"-parallelism", "audio transcribe"},
		},
		{
			desc:     "photo flag on the audio tree",
			args:     "-pd /tmp a t f.wav",
			wantHint: []string{"-pd", "photo"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_ = setupMainTestConfigDir(t)

			var status int
			stderr := testboil.CaptureStderr(t, func(t *testing.T) {
				_ = testboil.CaptureStdout(t, func(t *testing.T) {
					status = run(strings.Split(tc.args, " "))
				})
			})

			testboil.FailTestIfDiff(t, status, 1)
			for _, want := range tc.wantHint {
				if !strings.Contains(stderr, want) {
					t.Fatalf("expected %q in the error, got: %q", want, stderr)
				}
			}
		})
	}
}

// Test_e2e_nested_help pins per-level help: the parent lists its sub table,
// the sub prints its own help.
func Test_e2e_nested_help(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	t.Run("chat -h lists sub table", func(t *testing.T) {
		var status int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			status = run([]string{"chat", "-h"})
		})
		testboil.FailTestIfDiff(t, status, 0)
		for _, want := range []string{"continue|c", "delete|d", "list|l", "dir", "dirv2"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("expected chat help to list %q, got: %q", want, stdout)
			}
		}
	})

	t.Run("chat list -h shows list help", func(t *testing.T) {
		var status int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			status = run([]string{"chat", "list", "-h"})
		})
		testboil.FailTestIfDiff(t, status, 0)
		if !strings.Contains(stdout, "List all existing chats") {
			t.Fatalf("expected list-specific help, got: %q", stdout)
		}
		if strings.Contains(stdout, "delete|d") {
			t.Fatalf("list help must not print the parent's sub table, got: %q", stdout)
		}
	})
}

// Test_e2e_unknown_chat_sub_falls_through pins contract row 10: an unmatched
// positional stays with the chat parent and yields today's chat handler
// error, not a dispatcher ArgNotFoundError.
func Test_e2e_unknown_chat_sub_falls_through(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeNotificationTestPriceFiles(t, confDir)

	var status int
	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			status = run(strings.Split("-r chat banana", " "))
		})
	})
	testboil.FailTestIfDiff(t, status, 1)
	combined := stdout + stderr
	if !strings.Contains(combined, "unknown subcommand: 'banana'") {
		t.Fatalf("expected chat handler's unknown-subcommand error, got: %q", combined)
	}
	if strings.Contains(combined, "is not a valid argument") {
		t.Fatalf("dispatcher must not reject the positional, got: %q", combined)
	}
}
