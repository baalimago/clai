package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// themeRoleColor differs from the built-in default role colors, so its
// presence in the output proves theme.json was actually loaded.
const themeRoleColor = "\x1b[35m"

func writeReplayTheme(t *testing.T, confDir string, bell bool) {
	t.Helper()
	theme := map[string]any{
		"primary":          "",
		"secondary":        "",
		"breadtext":        "",
		"roleSystem":       themeRoleColor,
		"roleUser":         themeRoleColor,
		"roleTool":         themeRoleColor,
		"roleReasoning":    themeRoleColor,
		"roleOther":        themeRoleColor,
		"notificationBell": bell,
		"tableItems":       10,
		"toolOutputRows":   6,
	}
	b, err := json.Marshal(theme)
	if err != nil {
		t.Fatalf("Marshal(theme): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "theme.json"), b, 0o644); err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}
}

// replayFreshProcess runs one clai command in its own process. Theme state is
// a process global, so an in-process run would inherit the theme loaded by the
// seeding query and hide exactly the defect these tests pin.
func replayFreshProcess(t *testing.T, bin, arg string) string {
	t.Helper()
	stdout, err := exec.Command(bin, arg).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("clai %v failed: %v\nstderr: %s", arg, err, exitErr.Stderr)
		}
		t.Fatalf("clai %v failed: %v", arg, err)
	}
	return string(stdout)
}

// Test_e2e_replay_loads_theme pins that replay honors theme.json: both
// commands render conversation content, so they must run the theme prep even
// though they skip config migration.
func Test_e2e_replay_loads_theme(t *testing.T) {
	for _, tc := range []struct {
		name string
		verb string
	}{
		{name: "replay", verb: "re"},
		{name: "dir-replay", verb: "dre"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Built before chdir: the build runs in the current directory.
			bin := filepath.Join(builtClaiDir(t), "clai")
			t.Setenv("NO_COLOR", "")
			confDir := setupMainTestConfigDir(t)
			t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
			chdirTemp(t)
			writeReplayTheme(t, confDir, false)

			if status := run(strings.Split("-r -cm mock_test q hello", " ")); status != 0 {
				t.Fatalf("seed query status %d", status)
			}

			testboil.AssertStringContains(t, replayFreshProcess(t, bin, tc.verb), themeRoleColor)
		})
	}
}

// Test_e2e_dirscope_replay_honors_notification_bell pins the bell against
// theme.json: dir-replay runs through the adapter's default Run, which rings
// the completion bell, so an unloaded theme would ring against the user's
// explicit opt-out.
func Test_e2e_dirscope_replay_honors_notification_bell(t *testing.T) {
	for _, tc := range []struct {
		name string
		bell bool
	}{
		{name: "bell disabled in theme", bell: false},
		{name: "bell enabled in theme", bell: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := filepath.Join(builtClaiDir(t), "clai")
			confDir := setupMainTestConfigDir(t)
			t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
			chdirTemp(t)
			writeReplayTheme(t, confDir, tc.bell)

			if status := run(strings.Split("-r -cm mock_test q hello", " ")); status != 0 {
				t.Fatalf("seed query status %d", status)
			}

			stdout := replayFreshProcess(t, bin, "dre")

			if got := strings.Contains(stdout, "\a"); got != tc.bell {
				t.Fatalf("bell rung: %v, want %v, stdout: %q", got, tc.bell, stdout)
			}
		})
	}
}
