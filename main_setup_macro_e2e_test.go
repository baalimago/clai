package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stripANSI removes ANSI SGR escape sequences so assertions can match plain
// text regardless of colorization.
func stripANSI(s string) string {
	return ansiSGR.ReplaceAllString(s, "")
}

var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Test_e2e_setup_migrates_mode_configs_and_announces proves that `clai s`
// preloads the mode configs through LoadConfigFromFile: an existing
// textConfig.json that predates the stoploss schema is upgraded in place and
// the added fields are announced, so the wizard previews the current schema.
func Test_e2e_setup_migrates_mode_configs_and_announces_before_wizard(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	// Downgrade textConfig.json to the pre-stoploss schema.
	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{
		"model": "test",
	})

	stdout, status := runOne(t, confDir, "-n -cm test s 0 0 q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "added new field(s) to textConfig.json:") {
		t.Fatalf("expected the config upgrade announcement, got:\n%s", stdout)
	}
	// The wizard's TUI redraws by clearing its own frame plus one line above
	// the header (table ClearTermTo clears upTo+1 lines), so the announcement
	// block ends with a blank separator line that absorbs that overshoot and
	// the announcement survives interactive redraws. Pin that the announcement
	// appears before the wizard header with the blank line between them.
	annIdx := strings.Index(stdout, "added new field(s) to textConfig.json:")
	if annIdx < 0 || annIdx > strings.Index(stdout, "Setup categories") {
		t.Fatalf("expected the announcement before the wizard started, got:\n%s", stdout)
	}
	if !strings.Contains(stripANSI(stdout[annIdx:]), "\n\nSetup categories") {
		t.Fatalf("expected a blank separator line between the announcement and the wizard, got:\n%s", stdout)
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
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "profiles", "john.json"), map[string]any{
		"name":  "john",
		"model": "test",
	})

	stdout, status := runOne(t, confDir, "-n -cm test s 0 0 q")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	annIdx := strings.Index(stdout, "added new field(s) to john.json:")
	if annIdx < 0 {
		t.Fatalf("expected the profile upgrade announcement, got:\n%s", stdout)
	}
	if annIdx > strings.Index(stdout, "Setup categories") {
		t.Fatalf("expected the profile announcement before the wizard started, got:\n%s", stdout)
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
	confDir := setupMainTestConfigDir(t)

	// 0=select category 0 (general config) → config list shown → trailing q exits
	stdout, status := runOne(t, confDir, "-n -r -cm test s 0")
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
	confDir := setupMainTestConfigDir(t)

	// 0=category 0 (general config) → 0=config item → preview → b=back to list → trailing q exits
	stdout, status := runOne(t, confDir, "-n -r -cm test s 0 0 b")
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
	confDir := setupMainTestConfigDir(t)

	// 0=category 0 (general config) → 0=config item → preview → q=quit
	stdout, status := runOne(t, confDir, "-n -r -cm test s 0 0 q")
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
