package testenv

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
)

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
