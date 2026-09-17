package main

import (
	"testing"

	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// countForeignIndexBuilds replaces the composition root's cache factory with a
// counting one for the duration of a test.
func countForeignIndexBuilds(t *testing.T) *int {
	t.Helper()
	built := 0
	previous := foreignCache
	t.Cleanup(func() { foreignCache = previous })
	foreignCache = func() vendors.SourceCache {
		built++
		return previous()
	}
	return &built
}

// TestCommands_buildConstructsNoForeignIndex: building the command map runs
// before the dispatcher knows which verb was typed, so nothing there may read
// and decode the foreign cache. A query, a photo command or a version print
// paid for a listing it never does (R1-02). Neither may a chat verb that
// never consults the cache: `dir` and `dirv2` run on shell-prompt hot paths,
// so a precmd hook decoded a corpus-sized index on every prompt render
// (R2-03, D27).
//
// The listing leg is the control: without it the test would pass just as well
// against a composition root that never wired the index at all.
func TestCommands_buildConstructsNoForeignIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAI_CACHE_DIR", t.TempDir())
	_ = setupMainTestConfigDir(t)
	built := countForeignIndexBuilds(t)

	if m := commands(); len(m) == 0 {
		t.Fatal("commands() built no commands")
	}
	if *built != 0 {
		t.Fatalf("building the command map constructed the foreign index %d times, want 0", *built)
	}

	var status int
	testboil.CaptureStdout(t, func(t *testing.T) {
		status = run([]string{"version"})
	})
	testboil.FailTestIfDiff(t, status, 0)
	if *built != 0 {
		t.Fatalf("a non-chat verb constructed the foreign index %d times, want 0", *built)
	}

	// A chat verb that reads no foreign row is the same case as a non-chat
	// one. `delete` fails with no such chat and must still build nothing.
	for _, argv := range [][]string{
		{"-r", "chat", "help"},
		{"-r", "chat", "dir"},
		{"-r", "chat", "dirv2"},
		{"-n", "-r", "chat", "delete", "no-such-chat"},
	} {
		testboil.CaptureStdout(t, func(t *testing.T) {
			_ = run(argv)
		})
		if *built != 0 {
			t.Fatalf("%v constructed the foreign index %d times; it never consults the cache", argv, *built)
		}
	}

	testboil.CaptureStdout(t, func(t *testing.T) {
		status = run([]string{"-n", "-r", "chat", "list", "q"})
	})
	testboil.FailTestIfDiff(t, status, 0)
	if *built != 1 {
		t.Fatalf("a consulting chat verb constructed the foreign index %d times, want exactly 1", *built)
	}
}
