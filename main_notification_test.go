package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_run_emits_terminal_bell_on_completed_query_when_theme_enables_it(t *testing.T) {
	confDir := t.TempDir()
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	err := os.WriteFile(filepath.Join(confDir, "theme.json"), []byte(`{
  "primary": "",
  "secondary": "",
  "breadtext": "",
  "roleSystem": "",
  "roleUser": "",
  "roleTool": "",
  "roleOther": "",
  "notificationBell": true
}`), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, "hello\n\a")
}

func Test_run_false_notification_bell_setting_is_backfilled_to_true(t *testing.T) {
	confDir := t.TempDir()
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	themePath := filepath.Join(confDir, "theme.json")
	err := os.WriteFile(themePath, []byte(`{
  "primary": "",
  "secondary": "",
  "breadtext": "",
  "roleSystem": "",
  "roleUser": "",
  "roleTool": "",
  "roleOther": "",
  "notificationBell": false
}`), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, "hello\n\a")

	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	testboil.AssertStringContains(t, string(themeBytes), `"notificationBell": true`)
}

func Test_run_appends_notification_bell_true_to_existing_theme_json(t *testing.T) {
	confDir := t.TempDir()
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	themePath := filepath.Join(confDir, "theme.json")
	err := os.WriteFile(themePath, []byte(`{
  "primary": "",
  "secondary": "",
  "breadtext": "",
  "roleSystem": "",
  "roleUser": "",
  "roleTool": "",
  "roleOther": ""
}`), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)

	var gotStatusCode int
	_ = testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)

	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	gotTheme := string(themeBytes)
	testboil.AssertStringContains(t, gotTheme, `"notificationBell": true`)
}
