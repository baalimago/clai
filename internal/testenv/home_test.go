package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// keepEnv restores the variables Isolate rewrites, so the values TestMain
// pinned survive the test.
func keepEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "TMPDIR", "GOPATH", "GOMODCACHE", "GOCACHE", "GOENV", "TTY", "LC_ALL", "PATH"} {
		t.Setenv(key, os.Getenv(key))
	}
}

func fakeDeveloperHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "/developer")
	t.Setenv("XDG_CONFIG_HOME", "/developer/.config")
	t.Setenv("XDG_CACHE_HOME", "/developer/.cache")
	t.Setenv("XDG_DATA_HOME", "/developer/.local/share")
}

func isolate(t *testing.T) Home {
	t.Helper()
	home, err := Isolate()
	if err != nil {
		t.Fatalf("Isolate: %v", err)
	}
	t.Cleanup(func() { _ = home.Remove() })
	return home
}

func TestIsolate_pointsUserDirsIntoANewDirectory(t *testing.T) {
	keepEnv(t)
	fakeDeveloperHome(t)

	home := isolate(t)

	for name, dir := range map[string]func() (string, error){
		"UserHomeDir":   os.UserHomeDir,
		"UserConfigDir": os.UserConfigDir,
		"UserCacheDir":  os.UserCacheDir,
	} {
		got, err := dir()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(got, string(home)+string(filepath.Separator)) && got != string(home) {
			t.Fatalf("%s = %q, want inside %q", name, got, home)
		}
	}
	if got := os.Getenv("XDG_DATA_HOME"); !strings.HasPrefix(got, string(home)) {
		t.Fatalf("XDG_DATA_HOME = %q, want inside %q", got, home)
	}
}

func TestIsolate_movesTemporaryFilesIntoTheHome(t *testing.T) {
	keepEnv(t)
	fakeDeveloperHome(t)

	home := isolate(t)

	if got := os.TempDir(); !strings.HasPrefix(got, string(home)+string(filepath.Separator)) {
		t.Fatalf("TempDir = %q, want inside %q", got, home)
	}
	probe, err := os.MkdirTemp("", "probe-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if !strings.HasPrefix(probe, string(home)) {
		t.Fatalf("a temp dir landed at %q, outside %q", probe, home)
	}
}

func TestIsolate_pinsTheGoDirsTheGoCommandReportsBeforeTheMove(t *testing.T) {
	keepEnv(t)
	fakeDeveloperHome(t)
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV"} {
		t.Setenv(key, "")
	}
	want := map[string]string{}
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV"} {
		out, err := exec.Command("go", "env", key).Output()
		if err != nil {
			t.Fatalf("go env %s: %v", key, err)
		}
		want[key] = strings.TrimSpace(string(out))
	}

	home := isolate(t)

	for key, w := range want {
		got := os.Getenv(key)
		if got != w {
			t.Fatalf("%s = %q after Isolate, want %q as go env reported before the move", key, got, w)
		}
		if strings.HasPrefix(got, string(home)) {
			t.Fatalf("%s = %q points into the test home; a go child would start cold", key, got)
		}
	}
}

func TestIsolate_keepsAGoDirSetInTheShell(t *testing.T) {
	keepEnv(t)
	fakeDeveloperHome(t)
	t.Setenv("GOCACHE", "/warm/go-build")

	isolate(t)

	if got := os.Getenv("GOCACHE"); got != "/warm/go-build" {
		t.Fatalf("GOCACHE = %q, want the shell's value kept", got)
	}
}

func TestIsolate_derivesTheGoDirsWithoutTheGoCommand(t *testing.T) {
	keepEnv(t)
	fakeDeveloperHome(t)
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV"} {
		t.Setenv(key, "")
	}
	t.Setenv("PATH", t.TempDir())

	isolate(t)

	for key, want := range map[string]string{
		"GOPATH":     "/developer/go",
		"GOMODCACHE": "/developer/go/pkg/mod",
		"GOCACHE":    "/developer/.cache/go-build",
		"GOENV":      "/developer/.config/go/env",
	} {
		if got := os.Getenv(key); got != want {
			t.Fatalf("%s = %q, want the go command's default %q", key, got, want)
		}
	}
}

func TestIsolate_blanksWhatTheDevelopersShellCouldFeedATest(t *testing.T) {
	keepEnv(t)
	fakeDeveloperHome(t)
	blanked := map[string]string{
		"OPENAI_API_KEY":   "sk-paid",
		"xai_api_key":      "lower",
		"CLAI_CONFIG_DIR":  "/developer/.config/.clai",
		"DEBUG_SUMMARY":    "1",
		"EDITOR":           "vim",
		"VISUAL":           "code",
		"HTTPS_PROXY":      "http://127.0.0.1:9",
		"no_proxy":         "localhost",
		"SANDBOX":          "1",
		"QWEN_HOME":        "/developer/.qwen",
		"GIT_DIR":          "/developer/.git",
		"NO_COLOR":         "1",
		"CLAI_CLOUD_TOKEN": "t",
	}
	for key, value := range blanked {
		t.Setenv(key, value)
	}
	t.Setenv("TERM", "xterm-test")

	isolate(t)

	for key := range blanked {
		if got, ok := os.LookupEnv(key); ok {
			t.Fatalf("%s = %q survived Isolate", key, got)
		}
	}
	if got := os.Getenv("TERM"); got != "xterm-test" {
		t.Fatalf("TERM = %q, want untouched", got)
	}
	if got := os.Getenv("TTY"); got != os.DevNull {
		t.Fatalf("TTY = %q, want %q", got, os.DevNull)
	}
	if got := os.Getenv("LC_ALL"); got != "C" {
		t.Fatalf("LC_ALL = %q, want C", got)
	}
}

func TestSweepStaleHomes_removesOnlyThisUsersOldHomes(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, homePrefix+"old")
	fresh := filepath.Join(base, homePrefix+"fresh")
	other := filepath.Join(base, "someone-elses-old-dir")
	for _, dir := range []string{old, fresh, other} {
		if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	for _, dir := range []string{old, other} {
		if err := os.Chtimes(dir, twoHoursAgo, twoHoursAgo); err != nil {
			t.Fatal(err)
		}
	}

	swept, err := sweepStaleHomes(base, time.Hour)
	if err != nil {
		t.Fatalf("sweepStaleHomes: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept %d homes, want 1", swept)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("the stale home is still there: %v", err)
	}
	for _, dir := range []string{fresh, other} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s should have been kept: %v", filepath.Base(dir), err)
		}
	}
	if _, err := sweepStaleHomes(filepath.Join(base, "missing"), time.Hour); err == nil || !strings.Contains(err.Error(), "stale test homes") {
		t.Fatalf("sweepStaleHomes(missing base) error = %v, want the listing failure", err)
	}
}

func TestHomeBase_isTheRAMDiskWhenItHasRoom(t *testing.T) {
	room, err := roomAt(ramDisk)
	if err != nil || room < ramDiskRoom {
		t.Skipf("no RAM disk with room here: %v, %d bytes", err, room)
	}
	if got := homeBase(); got != ramDisk {
		t.Fatalf("homeBase() = %q, want %q", got, ramDisk)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(home, ramDisk+"/"+homePrefix) {
		t.Fatalf("this binary's home %q is not on the RAM disk", home)
	}
}
