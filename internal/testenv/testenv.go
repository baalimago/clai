// Package testenv makes a test binary hermetic: a capped runtime, a process
// environment that owes nothing to the developer's shell or home, and a last
// word on its own budget before the -test.timeout alarm.
package testenv

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Procs caps a test binary: go test runs about one race binary per core at
// once, and full GOMAXPROCS per binary costs more CPU in scheduler spin and GC
// pauses than the tests themselves.
const Procs = 2

// GCPercent trades a little heap for far fewer collections. Under the race
// detector the collector, not the tests, dominated the e2e binary: 20.6 CPU-s
// at the default 100 against 9.0 at 400 for the same rows, peak memory 250 MB
// against 270 MB.
const GCPercent = 400

// PreviousProcs is the GOMAXPROCS setting Tune replaced.
type PreviousProcs int

// Tune caps the binary's processors and collector frequency, and its
// children's through the environment.
func Tune() PreviousProcs {
	_ = os.Setenv("GOMAXPROCS", strconv.Itoa(Procs))
	_ = os.Setenv("GOGC", strconv.Itoa(GCPercent))
	debug.SetGCPercent(GCPercent)
	return PreviousProcs(runtime.GOMAXPROCS(Procs))
}

// Setup is a package's own TestMain work around m.Run.
type Setup struct {
	// Before runs once the binary is hermetic; an error fails the run.
	Before func(Home) error
	// After runs when the tests are done.
	After func()
}

// Main is a whole TestMain for packages that need no other setup.
func Main(m *testing.M) { MainWith(m, Setup{}) }

// MainWith is Main around a package's own setup.
func MainWith(m *testing.M, s Setup) {
	Tune()
	os.Exit(run(m, s))
}

func run(m *testing.M, s Setup) int {
	stderr := os.Stderr
	if _, err := sweepStaleHomes(homeBase(), staleHome); err != nil {
		fmt.Fprintln(stderr, "testenv:", err)
	}
	home, err := Isolate()
	if home != "" {
		defer func() { _ = home.Remove() }()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if s.Before != nil {
		if err := s.Before(home); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if s.After != nil {
		defer s.After()
	}
	timeout, err := timeoutFlag()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	report := armDeadlineReport(stderr, time.Now(), timeout, reportMargin)
	defer report.stop()
	return m.Run()
}

// Home is the temporary directory standing in for the user's home.
type Home string

func (h Home) Remove() error { return os.RemoveAll(string(h)) }

// Isolate gives the binary a hermetic environment: the go command's
// directories pinned to what they are now, HOME, the XDG directories and
// TMPDIR in a new temporary directory, and every variable through which the
// developer's shell could reach a test removed or fixed.
func Isolate() (Home, error) {
	if err := pinGoDirs(); err != nil {
		return "", err
	}
	home, err := isolateHome()
	if err != nil {
		return home, err
	}
	return home, blankEnv()
}

func goDirNames() []string { return []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV"} }

// pinGoDirs keeps a go child warm after the home moves. The go command is the
// authority on its own directories (it honours the developer's go env file);
// without it on PATH the defaults are derived the way it derives them.
func pinGoDirs() error {
	values, err := goEnv(goDirNames())
	if err != nil {
		if values, err = derivedGoDirs(); err != nil {
			return err
		}
	}
	for _, name := range goDirNames() {
		if err := os.Setenv(name, values[name]); err != nil {
			return fmt.Errorf("pin %s: %w", name, err)
		}
	}
	return nil
}

func goEnv(names []string) (map[string]string, error) {
	out, err := exec.Command("go", append([]string{"env", "-json"}, names...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	var values map[string]string
	if err := json.Unmarshal(out, &values); err != nil {
		return nil, fmt.Errorf("decode go env: %w", err)
	}
	for _, name := range names {
		if values[name] == "" {
			return nil, fmt.Errorf("go env reports no %s", name)
		}
	}
	return values, nil
}

func derivedGoDirs() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home for GOPATH: %w", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolve cache dir for GOCACHE: %w", err)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve config dir for GOENV: %w", err)
	}
	gopath := cmp.Or(os.Getenv("GOPATH"), filepath.Join(home, "go"))
	return map[string]string{
		"GOPATH":     gopath,
		"GOMODCACHE": cmp.Or(os.Getenv("GOMODCACHE"), filepath.Join(gopath, "pkg", "mod")),
		"GOCACHE":    cmp.Or(os.Getenv("GOCACHE"), filepath.Join(cache, "go-build")),
		"GOENV":      cmp.Or(os.Getenv("GOENV"), filepath.Join(config, "go", "env")),
	}, nil
}

// Test homes live on Linux's RAM-backed scratch filesystem when it has
// ramDiskRoom to spare, else in the temp dir. On 2026-10-28 homes on an ext4
// /tmp made the file-heavy binaries two to three times slower than on tmpfs
// (vendors 26 s against 10 s, utils 13.5 s against 4.1 s) and the three
// largest binaries timed out; the same gate passed with the homes in RAM.
const (
	ramDisk     = "/dev/shm"
	ramDiskRoom = 1 << 30
	homePrefix  = "clai-test-home-"
	// staleHome is the age at which a leftover home may be removed by any
	// binary: a timed-out binary never reaches its own cleanup.
	staleHome = time.Hour
)

func homeBase() string {
	if room, err := roomAt(ramDisk); err == nil && room >= ramDiskRoom {
		return ramDisk
	}
	return os.TempDir()
}

// Swept counts the stale homes a sweep removed.
type Swept int

// sweepStaleHomes removes this user's homes under base older than olderThan.
func sweepStaleHomes(base string, olderThan time.Duration) (Swept, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0, fmt.Errorf("list %s for stale test homes: %w", base, err)
	}
	cutoff := time.Now().Add(-olderThan)
	var swept Swept
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), homePrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) || !ownedByUs(info) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove stale test home %s: %w", e.Name(), err))
			continue
		}
		swept++
	}
	return swept, errors.Join(errs...)
}

func isolateHome() (Home, error) {
	dir, err := os.MkdirTemp(homeBase(), homePrefix)
	if err != nil {
		return "", fmt.Errorf("create test home: %w", err)
	}
	// Temporary files land inside the home too: one entry in the developer's
	// temp dir per binary, however many entries that dir already holds, and
	// nothing left behind there.
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return Home(dir), fmt.Errorf("create test tmp dir: %w", err)
	}
	for key, path := range map[string]string{
		"HOME":            dir,
		"XDG_CONFIG_HOME": filepath.Join(dir, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(dir, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(dir, ".local", "share"),
		"TMPDIR":          tmp,
	} {
		if err := os.Setenv(key, path); err != nil {
			return Home(dir), fmt.Errorf("set %s: %w", key, err)
		}
	}
	return Home(dir), nil
}

// blankEnv removes what the developer's shell could feed a test and fixes the
// rest: vendor keys select models and spend money, CLAI_* is clai's own
// configuration, DEBUG* adds output, an editor or a proxy reaches outside the
// test, the locale changes child output, and no terminal is ever attached.
func blankEnv() error {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !blanked(name) {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("unset %s: %w", name, err)
		}
	}
	for name, value := range map[string]string{"TTY": os.DevNull, "LC_ALL": "C"} {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}
	return nil
}

func blanked(name string) bool {
	switch name {
	case "EDITOR", "VISUAL", "SANDBOX", "NO_COLOR", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE":
		return true
	}
	upper := strings.ToUpper(name)
	switch upper {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
		return true
	}
	return strings.HasSuffix(upper, "_API_KEY") ||
		strings.HasPrefix(upper, "CLAI_") ||
		strings.HasPrefix(upper, "DEBUG") ||
		strings.HasPrefix(upper, "QWEN_")
}
