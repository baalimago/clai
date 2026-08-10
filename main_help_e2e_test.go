package main

import (
	"os"
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

	confDir := setupMainTestConfigDir(t)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("help", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	if gotStdout == "" {
		t.Fatal("expected help output to be non-empty")
	}
	// The usage string is large; check for a few stable snippets and that config dir was interpolated.
	testboil.AssertStringContains(t, gotStdout, "Usage:")
	testboil.AssertStringContains(t, gotStdout, "-s, -skills string")
	testboil.AssertStringContains(t, gotStdout, "-asc, -append-shell-context str")
	testboil.AssertStringContains(t, gotStdout, "-mt, -max-tokens int")
	testboil.AssertStringContains(t, gotStdout, "-mtc, -max-tool-calls int")
	testboil.AssertStringContains(t, gotStdout, "-max-tool-calls-after-handover int")
	testboil.AssertStringContains(t, gotStdout, "clai -asc minimal q \"what changed in this repo?\"")
	testboil.AssertStringContains(t, gotStdout, confDir)
	assertFlagDescriptionsAligned(t, gotStdout)
}

// assertFlagDescriptionsAligned fails when the flag descriptions in the usage
// text do not all start at the same column. Keeping the column aligned is a
// manual padding exercise today; this guard turns a misalignment into a test
// failure instead of a silent cosmetic regression. The structured replacement
// (generating the flag block from a table of {flag, type, description}) is a
// future upgrade.
func assertFlagDescriptionsAligned(t *testing.T, usageOut string) {
	t.Helper()
	var descCol int
	for line := range strings.SplitSeq(usageOut, "\n") {
		if !strings.HasPrefix(line, "  -") {
			continue
		}
		// The description is the text after the first run of 2+ spaces that
		// follows the flag spec.
		rest := line[2:]
		runStart := -1
		for i := 0; i+1 < len(rest); i++ {
			if rest[i] == ' ' && rest[i+1] == ' ' {
				runStart = i
				break
			}
		}
		if runStart < 0 {
			continue
		}
		j := runStart
		for j < len(rest) && rest[j] == ' ' {
			j++
		}
		if j == len(rest) {
			continue
		}
		col := 2 + j
		if descCol == 0 {
			descCol = col
			continue
		}
		if col != descCol {
			t.Fatalf("flag description column mismatch: %q starts at column %d, want %d", line, col+1, descCol+1)
		}
	}
	if descCol == 0 {
		t.Fatal("expected at least one flag line in the usage output")
	}
}

func Test_goldenFile_HELP_profile_prints_profile_help(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = setupMainTestConfigDir(t)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("help profile", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	want := internal.ProfileHelp + "\n"
	testboil.FailTestIfDiff(t, gotStdout, want)
}
