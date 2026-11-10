package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"

	"github.com/baalimago/clai/e2e/e2etest"
)

// Test_e2e_setup_migrates_mode_configs_and_announces proves that `clai s`
// preloads the mode configs through LoadConfigFromFile: an existing
// textConfig.json that predates the stoploss schema is upgraded in place and
// the added fields are announced, so the wizard previews the current schema.
func Test_e2e_setup_migrates_mode_configs_and_announces_before_wizard(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	// Downgrade textConfig.json to the pre-stoploss schema.
	e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{
		"model": "test",
	})

	stdout, stderr, status := runSetupMacro(t, confDir, "-n s 0 0 q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	// The announcement is a diagnostic on stderr (F1); its block ends with a
	// blank separator line that absorbs the wizard TUI's one-line clear
	// overshoot (table ClearTermTo clears upTo+1 lines), so it survives the
	// first redraw.
	found := strings.Contains(stderr, "added new field(s) to textConfig.json:")
	if !found {
		t.Fatalf("expected the config upgrade announcement on stderr, got:\n%s", stderr)
	}
	if !strings.HasSuffix(stderr, "\n\n") {
		t.Fatalf("expected a blank separator line after the announcement, got:\n%q", stderr)
	}
	if !strings.Contains(stdout, "Setup categories") {
		t.Fatalf("expected the wizard on stdout, got:\n%s", stdout)
	}
	regenerated, err := os.ReadFile(filepath.Join(confDir, "textConfig.json"))
	if err != nil {
		t.Fatalf("ReadFile(textConfig.json): %v", err)
	}
	if !strings.Contains(string(regenerated), `"stoploss"`) {
		t.Fatalf("expected stoploss appended to textConfig.json:\n%s", regenerated)
	}
}

// Test_e2e_setup_migrates_profiles_and_announces_before_wizard proves the
// united config migration covers profiles during setup too: a profile that
// predates the current schema is upgraded before the wizard runs, and its
// announcement is printed before the wizard starts (the announcement block's
// trailing blank line absorbs the TUI's one-line clear overshoot, so the
// announcement survives redraws).
func Test_e2e_setup_migrates_profiles_and_announces_before_wizard(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "profiles", "john.json"), map[string]any{
		"name":  "john",
		"model": "test",
	})

	stdout, stderr, status := runSetupMacro(t, confDir, "-n s 0 0 q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stderr, "added new field(s) to john.json:") {
		t.Fatalf("expected the profile upgrade announcement on stderr, got:\n%s", stderr)
	}
	regenerated, err := os.ReadFile(filepath.Join(confDir, "profiles", "john.json"))
	if err != nil {
		t.Fatalf("ReadFile(john.json): %v", err)
	}
	if !strings.Contains(string(regenerated), `"use_tools"`) {
		t.Fatalf("expected use_tools appended to john.json:\n%s", regenerated)
	}
}

// ============================================================
// Phase 7: Expanded e2e macro regression suite — setup macro tests
// ============================================================

// Test_e2e_setup_macro_select_category_quit verifies that selecting a setup
// category via macro input and then auto-quitting works correctly.
func Test_e2e_setup_macro_select_category_quit(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)

	// 0=select category 0 (general config) → config list shown → trailing q exits
	stdout, status := harness.RunOne(t, confDir, "-n s 0")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	// Verify the general config category was entered.
	if !strings.Contains(stdout, "general config") {
		t.Fatalf("expected 'general config' category, got:\n%s", stdout)
	}
}

// Test_e2e_setup_macro_select_config_and_back verifies selecting a category,
// picking a config item, previewing, then backing out to the config list.
func Test_e2e_setup_macro_select_config_and_back(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)

	// 0=category 0 (general config) → 0=config item → preview → b=back to list → trailing q exits
	stdout, status := harness.RunOne(t, confDir, "-n s 0 0 b")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	// Should see the category name.
	if !strings.Contains(stdout, "general config") {
		t.Fatalf("expected 'general config' category, got:\n%s", stdout)
	}
	// Should see config item preview content.
	if !strings.Contains(stdout, "textConfig.json") {
		t.Fatalf("expected 'textConfig.json' in output, got:\n%s", stdout)
	}
}

// Test_e2e_setup_macro_select_config_and_quit verifies selecting a category
// and config item, then quitting from the preview action prompt.
func Test_e2e_setup_macro_select_config_and_quit(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)

	// 0=category 0 (general config) → 0=config item → preview → q=quit
	stdout, status := harness.RunOne(t, confDir, "-n s 0 0 q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	// Should see the category name.
	if !strings.Contains(stdout, "general config") {
		t.Fatalf("expected 'general config' category, got:\n%s", stdout)
	}
	// Should see config item preview content.
	if !strings.Contains(stdout, "textConfig.json") {
		t.Fatalf("expected 'textConfig.json' in output, got:\n%s", stdout)
	}
}

func runSetupMacro(t *testing.T, cwd, args string) (string, string, int) {
	t.Helper()
	t.Chdir(cwd)
	t.Cleanup(func() {
		utils.Live = true
		utils.ReadonlyConfig = false
		utils.NoCreateConfig = false
	})
	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split(args, " "))
	})
	return stdout, stderr, status
}
