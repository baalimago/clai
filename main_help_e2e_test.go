package main

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/profiles"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Test_goldenFile_usage_on_no_args pins the dispatcher usage: bare clai
// prints the full usage — prerequisites, the generated command table, the
// per-command -h pointer, config/cache dirs and examples — and exits 1.
// The help command is gone; per-command help lives on -h.
func Test_goldenFile_usage_on_no_args(t *testing.T) {
	confDir := setupMainTestConfigDir(t)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(nil)
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 1)
	testboil.AssertStringContains(t, gotStdout, "Usage:")
	for _, command := range []string{
		"query|q", "chat|c", "photo|p", "video|v", "audio|a",
		"setup|s", "version", "replay|re", "dir-replay|dre",
		"tools|t", "profiles", "confdir", "completion",
	} {
		testboil.AssertStringContains(t, gotStdout, command)
	}
	for _, forbidden := range []string{"__complete", "help|h", "glob|g"} {
		if strings.Contains(gotStdout, forbidden) {
			t.Fatalf("usage must not list %q, got:\n%s", forbidden, gotStdout)
		}
	}
	testboil.AssertStringContains(t, gotStdout, "clai <command> -h")
	testboil.AssertStringContains(t, gotStdout, "clai -asc minimal q \"what changed in this repo?\"")
	testboil.AssertStringContains(t, gotStdout, confDir)
}

// Test_goldenFile_help_command_removed pins that the old help command is an
// unknown command now: it errors and falls back to the usage listing.
func Test_goldenFile_help_command_removed(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	for _, args := range []string{"help", "h"} {
		t.Run(args, func(t *testing.T) {
			var gotStatusCode int
			var gotStdout string
			gotStderr := testboil.CaptureStderr(t, func(t *testing.T) {
				gotStdout = testboil.CaptureStdout(t, func(t *testing.T) {
					gotStatusCode = run([]string{args})
				})
			})
			testboil.FailTestIfDiff(t, gotStatusCode, 1)
			if !strings.Contains(gotStderr, args) {
				t.Fatalf("expected error naming %q, got: %q", args, gotStderr)
			}
			testboil.AssertStringContains(t, gotStdout, "query|q")
		})
	}
}

// Test_goldenFile_profiles_help_carries_profile_docs pins the new home of
// ProfileHelp: 'clai profiles -h'.
func Test_goldenFile_profiles_help_carries_profile_docs(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run([]string{"profiles", "-h"})
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.AssertStringContains(t, gotStdout, profiles.Help)
	testboil.AssertStringContains(t, gotStdout, "Examples:")
}
