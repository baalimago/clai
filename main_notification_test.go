package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func writeNotificationTestPriceFiles(t *testing.T, confDir string) {
	t.Helper()

	priceConfig := map[string]any{
		"price": map[string]any{
			"input_usd_per_token":        0.001,
			"input_cached_usd_per_token": 0.0005,
			"output_usd_per_token":       0.002,
		},
	}
	priceBytes, err := json.Marshal(priceConfig)
	if err != nil {
		t.Fatalf("Marshal(price config): %v", err)
	}
	for _, name := range []string{"mock_test_test.json", "mock_test_mock_test.json"} {
		if err := os.WriteFile(filepath.Join(confDir, name), priceBytes, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}
}

func Test_run_structured_output_prints_only_response(t *testing.T) {
	for _, rawFlag := range []string{"", "-r"} {
		t.Run("raw="+rawFlag, func(t *testing.T) {
			confDir := setupMainTestConfigDir(t)
			writeNotificationTestPriceFiles(t, confDir)
			writeSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: review\n---\nBody")
			writeSkillsConfigJSON(t, confDir, map[string]any{"enabled": true})
			responseFormatPath := filepath.Join("examples", "response-format-review.json")

			args := []string{"-cm", "test", "-lb", "-rf", responseFormatPath, "q", "hello"}
			if rawFlag != "" {
				args = append([]string{rawFlag}, args...)
			}
			var gotStatusCode int
			gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
				gotStatusCode = run(args)
			})

			testboil.FailTestIfDiff(t, gotStatusCode, 0)
			testboil.FailTestIfDiff(t, gotStdout, "hello\n")
		})
	}
}

func Test_run_redirected_completed_query_suppresses_bell(t *testing.T) {
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
  "roleReasoning": "",
  "roleOther": "",
  "notificationBell": true,
  "tableItems": 10,
  "toolOutputRows": 6,
  "rollingOutput": {
    "enabled": true,
    "windowCellHeight": 30
  }
}`), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)
	writeNotificationTestPriceFiles(t, confDir)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, "hello\n")
}

// Test_run_false_notification_bell_setting_stays_disabled pins the
// presence-based merge (Q4): a present notificationBell: false in theme.json
// is the user's choice and survives the load, so the run emits no bell. The
// old zero-value backfill clobbered it back to true on every load.
func Test_run_false_notification_bell_setting_stays_disabled(t *testing.T) {
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
  "roleReasoning": "",
  "roleOther": "",
  "notificationBell": false,
  "tableItems": 10,
  "toolOutputRows": 6,
  "rollingOutput": {
    "enabled": true,
    "windowCellHeight": 30
  }
}`), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)
	writeNotificationTestPriceFiles(t, confDir)

	var gotStatusCode int
	gotStdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatusCode = run(strings.Split("-r -cm test q hello", " "))
	})

	testboil.FailTestIfDiff(t, gotStatusCode, 0)
	testboil.FailTestIfDiff(t, gotStdout, "hello\n")

	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	testboil.AssertStringContains(t, string(themeBytes), `"notificationBell": false`)
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
	writeNotificationTestPriceFiles(t, confDir)

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
