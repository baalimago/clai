package e2e

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/profiles"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Test_goldenFile_usage_on_no_args pins the dispatcher usage: bare clai
// prints the full usage — prerequisites, the generated command table, the
// per-command -h pointer, config/cache dirs and examples — and exits 1.
// The help command is gone; per-command help lives on -h.
func Test_goldenFile_usage_on_no_args(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = harness.Run(nil)
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 1)
	testboil.AssertStringContains(t, gotStdout, "Usage:")
	for _, command := range []string{
		"query|q", "chat|c", "photo|p", "video|v", "audio|a",
		"setup|s", "version", "replay|re", "dir-replay|dre",
		"tools|t", "profiles", "skills", "confdir", "completion",
	} {
		testboil.AssertStringContains(t, gotStdout, command)
	}
	for _, forbidden := range []string{"__complete", "help|h", "glob|g"} {
		if strings.Contains(gotStdout, forbidden) {
			t.Fatalf("usage must not list %q, got:\n%s", forbidden, gotStdout)
		}
	}
	testboil.AssertStringContains(t, gotStdout, "clai <command> -h")
	testboil.AssertStringContains(t, gotStdout, "clai -add-shell-context minimal -query-summary query \"what changed in this repo?\"")
	testboil.AssertStringContains(t, gotStdout, confDir)
}

// Test_goldenFile_help_prints_usage pins how a person or an agent asks
// for the usage: `clai help`, `clai -h` and `clai --help` print it to
// stdout, with nothing on stderr, and exit 0.
func Test_goldenFile_help_prints_usage(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)

	for _, args := range []string{"help", "-h", "--help"} {
		t.Run(args, func(t *testing.T) {
			var gotStatusCode int
			var gotStdout string
			gotStderr := testboil.CaptureStderr(t, func(t *testing.T) {
				gotStdout = testboil.CaptureStdout(t, func(t *testing.T) {
					gotStatusCode = harness.Run([]string{args})
				})
			})
			testboil.FailTestIfDiff(t, gotStatusCode, 0)
			testboil.FailTestIfDiff(t, gotStderr, "")
			testboil.AssertStringContains(t, gotStdout, "query|q")
			testboil.AssertStringContains(t, gotStdout, "clai help <command>")
		})
	}
}

// Test_goldenFile_help_short_alias_unknown pins that "h" is no command: it
// errors and falls back to the usage listing.
func Test_goldenFile_help_short_alias_unknown(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	var gotStatusCode int
	var gotStdout string
	gotStderr := testboil.CaptureStderr(t, func(t *testing.T) {
		gotStdout = testboil.CaptureStdout(t, func(t *testing.T) {
			gotStatusCode = harness.Run([]string{"h"})
		})
	})
	testboil.FailTestIfDiff(t, gotStatusCode, 1)
	if !strings.Contains(gotStderr, "'h'") {
		t.Fatalf("expected error naming %q, got: %q", "h", gotStderr)
	}
	testboil.AssertStringContains(t, gotStdout, "query|q")
}

// Test_goldenFile_profiles_help_carries_profile_docs pins the new home of
// ProfileHelp: 'clai profiles -h'.
func Test_goldenFile_profiles_help_carries_profile_docs(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = harness.Run([]string{"profiles", "-h"})
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.AssertStringContains(t, gotStdout, profiles.Help)
	testboil.AssertStringContains(t, gotStdout, "Examples:")
}
