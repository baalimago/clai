package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestFakeBins_writesTheStubsAndPutsThemFirstOnPath(t *testing.T) {
	dir := FakeBins(t, "npx", "node")

	for _, name := range []string{"npx", "node"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("Stat(%s): %v", name, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s mode = %v, want it executable", name, info.Mode())
		}
	}
	if got := os.Getenv("PATH"); !strings.HasPrefix(got, dir+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q, want %q first", got, dir)
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Errorf("LookPath(npx): %v", err)
	}
}

func TestFakeBinsSetup_writesTheStubsBeforeTheTestsStart(t *testing.T) {
	t.Setenv("PATH", "")
	home := Home(t.TempDir())

	if err := FakeBinsSetup("npx").Before(home); err != nil {
		t.Fatalf("Before: %v", err)
	}

	if _, err := os.Stat(filepath.Join(string(home), "fakebins", "npx")); err != nil {
		t.Fatalf("Stat(npx): %v", err)
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Errorf("LookPath(npx): %v", err)
	}
}

func TestFakeBinsSetup_reportsADirectoryItCannotWrite(t *testing.T) {
	home := Home(filepath.Join(t.TempDir(), "missing", "..", "file"))
	if err := os.WriteFile(string(home), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := FakeBinsSetup("npx").Before(home)

	if err == nil {
		t.Fatal("Before() = nil, want an error for a home that is a file")
	}
}

func TestNoRaceExitSleep_turnsOffTheExitHookAndKeepsTheCallersOptions(t *testing.T) {
	t.Run("a child with no GORACE", func(t *testing.T) {
		got := NoRaceExitSleep([]string{"HOME=/tmp/home"})
		want := []string{"HOME=/tmp/home", "GORACE=atexit_sleep_ms=0"}
		if !slices.Equal(got, want) {
			t.Fatalf("NoRaceExitSleep = %v, want %v", got, want)
		}
	})
	t.Run("a child with options of its own", func(t *testing.T) {
		got := NoRaceExitSleep([]string{"GORACE=halt_on_error=1"})
		want := []string{"GORACE=halt_on_error=1", "GORACE=halt_on_error=1 atexit_sleep_ms=0"}
		if !slices.Equal(got, want) {
			t.Fatalf("NoRaceExitSleep = %v, want %v, whose last GORACE wins", got, want)
		}
	})
}

func TestRaceExitSleepOff_turnsOffTheExitHookAndKeepsTheCallersOptions(t *testing.T) {
	for _, row := range []struct {
		name   string
		gorace string
		want   string
	}{
		{name: "no options", gorace: "", want: "atexit_sleep_ms=0"},
		{name: "options of its own", gorace: "halt_on_error=1", want: "halt_on_error=1 atexit_sleep_ms=0"},
		{name: "a value with padding", gorace: " ", want: "atexit_sleep_ms=0"},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := RaceExitSleepOff(row.gorace); got != row.want {
				t.Fatalf("RaceExitSleepOff(%q) = %q, want %q", row.gorace, got, row.want)
			}
		})
	}
}

func TestTune_capsProcsAndCollectorAndReturnsPreviousProcs(t *testing.T) {
	before := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(before) })
	t.Setenv("GOMAXPROCS", "")
	t.Setenv("GOGC", "")
	gcBefore := debug.SetGCPercent(100)
	t.Cleanup(func() { debug.SetGCPercent(gcBefore) })

	got := Tune()

	if int(got) != before {
		t.Fatalf("Tune() = %d, want the previous setting %d", got, before)
	}
	if now := runtime.GOMAXPROCS(0); now != Procs {
		t.Fatalf("GOMAXPROCS after Tune = %d, want %d", now, Procs)
	}
	if env := os.Getenv("GOMAXPROCS"); env != strconv.Itoa(Procs) {
		t.Fatalf("GOMAXPROCS exported = %q, want %d so children inherit the cap", env, Procs)
	}
	if gc := debug.SetGCPercent(GCPercent); gc != GCPercent {
		t.Fatalf("GC percent after Tune = %d, want %d", gc, GCPercent)
	}
	if env := os.Getenv("GOGC"); env != strconv.Itoa(GCPercent) {
		t.Fatalf("GOGC exported = %q, want %d so children inherit it", env, GCPercent)
	}
}
