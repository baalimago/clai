package main

import (
	"strings"
	"testing"
)

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
