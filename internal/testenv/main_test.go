package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

func TestMain_tunesTheRuntimeBeforeTestsRun(t *testing.T) {
	if got := runtime.GOMAXPROCS(0); got != Procs {
		t.Fatalf("GOMAXPROCS when tests start = %d, want %d", got, Procs)
	}
	if got := os.Getenv("GOMAXPROCS"); got != strconv.Itoa(Procs) {
		t.Fatalf("GOMAXPROCS in the environment = %q, want %d for child processes", got, Procs)
	}
	if got := debug.SetGCPercent(GCPercent); got != GCPercent {
		t.Fatalf("GC percent when tests start = %d, want %d", got, GCPercent)
	}
	if got := os.Getenv("GOGC"); got != strconv.Itoa(GCPercent) {
		t.Fatalf("GOGC in the environment = %q, want %d for child processes", got, GCPercent)
	}
}

func TestMain_isolatesHomeBeforeTestsRun(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(home), "clai-test-home-") {
		t.Fatalf("HOME when tests start = %q, want a fresh test home", home)
	}
	if tmp := os.TempDir(); !strings.HasPrefix(tmp, home) {
		t.Fatalf("TempDir when tests start = %q, want inside %q", tmp, home)
	}
}

func TestMain_keepsTheGoCommandWarmOutsideTheTestHome(t *testing.T) {
	out, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatalf("go env GOCACHE: %v", err)
	}
	got := strings.TrimSpace(string(out))
	home, _ := os.UserHomeDir()
	if got != os.Getenv("GOCACHE") || strings.HasPrefix(got, home) {
		t.Fatalf("a go child would use GOCACHE %q (env %q, test home %q)", got, os.Getenv("GOCACHE"), home)
	}
}

func TestMain_blanksVendorKeysBeforeTestsRun(t *testing.T) {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasSuffix(strings.ToUpper(name), "_API_KEY") {
			t.Fatalf("%s reached the tests", name)
		}
	}
	if got := os.Getenv("TTY"); got != os.DevNull {
		t.Fatalf("TTY = %q, want %q", got, os.DevNull)
	}
}
