package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
)

// setupProfileTest isolates a profiling run: its own working directory (the
// profile is written relative to it), its own config and cache dirs, and no
// inherited debug switches.
func setupProfileTest(t *testing.T) string {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	blankDebugAndVendorKeys(t)
	return chdirTemp(t)
}

func TestRunProfiled_writesParseableProfile(t *testing.T) {
	wd := setupProfileTest(t)
	t.Setenv("DEBUG_CPU", "1")

	var status int
	_, stderr := captureStdoutStderr(t, func() {
		status = runProfiled([]string{"version"})
	})
	if status != 0 {
		t.Fatalf("status = %d stderr = %q, want 0", status, stderr)
	}

	path := filepath.Join(wd, cpuProfileFileName)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip.NewReader(%q): %v", path, err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("ReadAll(%q): %v", path, err)
	}
	if len(raw) == 0 {
		t.Fatalf("profile %q decoded to zero bytes, want a profile body", path)
	}
}

func TestRunProfiled_exitCodeUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{name: "success", args: []string{"version"}, want: 0},
		{name: "failure", args: []string{"this-command-does-not-exist"}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupProfileTest(t)

			var plain int
			plainOut, _ := captureStdoutStderr(t, func() { plain = run(tc.args) })

			t.Setenv("DEBUG_CPU", "1")
			var profiled int
			profiledOut, _ := captureStdoutStderr(t, func() { profiled = runProfiled(tc.args) })

			if plain != tc.want || profiled != tc.want {
				t.Fatalf("status plain = %d profiled = %d, want %d", plain, profiled, tc.want)
			}
			if plainOut != profiledOut {
				t.Fatalf("profiling changed stdout:\nplain    = %q\nprofiled = %q", plainOut, profiledOut)
			}
		})
	}
}

func TestRunProfiled_disabledWritesNothing(t *testing.T) {
	wd := setupProfileTest(t)

	var status int
	_, stderr := captureStdoutStderr(t, func() {
		status = runProfiled([]string{"version"})
	})
	if status != 0 {
		t.Fatalf("status = %d stderr = %q, want 0", status, stderr)
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", wd, err)
	}
	if len(entries) != 0 {
		t.Fatalf("working dir = %v, want no profile file when DEBUG_CPU is unset", entries)
	}
}

func TestRunProfiled_createFailureStillRuns(t *testing.T) {
	wd := setupProfileTest(t)
	t.Setenv("DEBUG_CPU", "1")
	// A directory occupying the profile's name is the portable way to make
	// os.Create fail: a working directory cannot itself be a file.
	if err := os.Mkdir(filepath.Join(wd, cpuProfileFileName), 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", cpuProfileFileName, err)
	}

	var status int
	stdout, stderr := captureStdoutStderr(t, func() {
		status = runProfiled([]string{"version"})
	})
	if status != 0 {
		t.Fatalf("status = %d, want 0: a profiling failure must not stop the command", status)
	}
	if !strings.Contains(stdout, "version: ") {
		t.Fatalf("stdout = %q, want the command's own output", stdout)
	}
	if !strings.Contains(stderr, cpuProfileFileName) {
		t.Fatalf("stderr = %q, want a notice naming %q", stderr, cpuProfileFileName)
	}
}

func TestRunProfiled_startFailureStillRuns(t *testing.T) {
	wd := setupProfileTest(t)
	t.Setenv("DEBUG_CPU", "1")

	// Holding a profile open is the only way StartCPUProfile fails.
	held, err := os.Create(filepath.Join(t.TempDir(), "held.prof"))
	if err != nil {
		t.Fatalf("Create(held.prof): %v", err)
	}
	defer held.Close()
	if err := pprof.StartCPUProfile(held); err != nil {
		t.Fatalf("StartCPUProfile(held.prof): %v", err)
	}
	defer pprof.StopCPUProfile()

	var status int
	stdout, stderr := captureStdoutStderr(t, func() {
		status = runProfiled([]string{"version"})
	})
	if status != 0 {
		t.Fatalf("status = %d, want 0: a profiling failure must not stop the command", status)
	}
	if !strings.Contains(stdout, "version: ") {
		t.Fatalf("stdout = %q, want the command's own output", stdout)
	}
	if !strings.Contains(stderr, "profile") {
		t.Fatalf("stderr = %q, want a notice about the profiler", stderr)
	}
	if _, err := os.Stat(filepath.Join(wd, cpuProfileFileName)); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) = %v, want no partial file left behind", cpuProfileFileName, err)
	}
}
