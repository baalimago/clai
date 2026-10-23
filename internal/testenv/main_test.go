package testenv

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { MainInHome(m) }

func TestMain_limitsProcsBeforeTestsRun(t *testing.T) {
	if got := runtime.GOMAXPROCS(0); got != Procs {
		t.Fatalf("GOMAXPROCS when tests start = %d, want %d", got, Procs)
	}
}

func TestMain_isolatesHomeBeforeTestsRun(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if !strings.HasPrefix(home, os.TempDir()) {
		t.Fatalf("HOME when tests start = %q, want a directory under %q", home, os.TempDir())
	}
}
