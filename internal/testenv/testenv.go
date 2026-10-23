// Package testenv tunes test binaries for the repository's race gate.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Procs caps a heavy test binary: go test runs ~one race binary per core at
// once, and full GOMAXPROCS per binary costs more CPU in scheduler spin and GC
// pauses than the tests themselves.
const Procs = 2

// PreviousProcs is the GOMAXPROCS setting LimitProcs replaced.
type PreviousProcs int

func LimitProcs() PreviousProcs {
	return PreviousProcs(runtime.GOMAXPROCS(Procs))
}

// Main is a whole TestMain for packages that need no other setup.
func Main(m *testing.M) {
	LimitProcs()
	os.Exit(m.Run())
}

// Home is the temporary directory standing in for the user's home.
type Home string

// IsolateHome points HOME and the XDG config and cache directories into a new
// temporary directory, so no test reads the developer's own files.
func IsolateHome() (Home, error) {
	dir, err := os.MkdirTemp("", "clai-test-home-")
	if err != nil {
		return "", fmt.Errorf("create test home: %w", err)
	}
	for key, path := range map[string]string{
		"HOME":            dir,
		"XDG_CONFIG_HOME": filepath.Join(dir, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(dir, ".cache"),
	} {
		if err := os.Setenv(key, path); err != nil {
			return Home(dir), fmt.Errorf("set %s: %w", key, err)
		}
	}
	return Home(dir), nil
}

// MainInHome is Main for packages whose code reads the user's config or cache.
func MainInHome(m *testing.M) {
	os.Exit(runInHome(m))
}

func runInHome(m *testing.M) int {
	LimitProcs()
	home, err := IsolateHome()
	if home != "" {
		defer func() { _ = os.RemoveAll(string(home)) }()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}
