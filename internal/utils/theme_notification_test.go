package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestLoadTheme_AppendsNotificationBellTrueForExistingThemeWithoutField(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")

	err := os.WriteFile(themePath, []byte(strings.TrimSpace(`
{
  "primary": "p",
  "secondary": "s",
  "breadtext": "b",
  "roleSystem": "rs",
  "roleUser": "ru",
  "roleTool": "rt",
  "roleOther": "ro"
}
`)), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	err = LoadTheme(confDir)
	if err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}

	if !NotificationBellEnabled() {
		t.Fatal("expected notification bell to default to enabled for existing theme without field")
	}

	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	testboil.AssertStringContains(t, string(themeBytes), `"notificationBell": true`)
}

// TestLoadTheme_MalformedFileKeepsDefaults pins that a hand-edited broken
// theme.json surfaces an error without clobbering the in-memory defaults —
// the caller downgrades the error to a warning so the CLI stays usable.
func TestLoadTheme_MalformedFileKeepsDefaults(t *testing.T) {
	prev := globalTheme
	t.Cleanup(func() {
		globalTheme = prev
	})
	globalTheme = *defaultTheme()

	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"primary": "p",`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err == nil {
		t.Fatal("expected error for malformed theme.json")
	}
	if TableTheme().Items != defaultTheme().TableItems {
		t.Fatalf("expected default tableItems after failed load, got %d", TableTheme().Items)
	}
	if TableTheme().Primary != defaultTheme().Primary {
		t.Fatal("expected default primary color after failed load")
	}
}

// TestLoadTheme_NotificationBellCanBeDisabled pins the presence-based merge:
// a present notificationBell: false in the file is the user's choice and must
// survive the load — the old zero-value backfill clobbered it back to true
// on every load (config migration design, Q4).
func TestLoadTheme_NotificationBellCanBeDisabled(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")

	err := os.WriteFile(themePath, []byte(strings.TrimSpace(`
{
  "primary": "p",
  "secondary": "s",
  "breadtext": "b",
  "roleSystem": "rs",
  "roleUser": "ru",
  "roleTool": "rt",
  "roleOther": "ro",
  "notificationBell": false
}
`)), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	err = LoadTheme(confDir)
	if err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}

	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	testboil.AssertStringContains(t, string(themeBytes), `"notificationBell": false`)

	if NotificationBellEnabled() {
		t.Fatal("expected notification bell to stay disabled: a present false is never backfilled")
	}
}

func TestLoadTheme_AppendsTableItemsDefaultForExistingThemeWithoutField(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")

	err := os.WriteFile(themePath, []byte(strings.TrimSpace(`
{
  "primary": "p",
  "secondary": "s",
  "breadtext": "b",
  "roleSystem": "rs",
  "roleUser": "ru",
  "roleTool": "rt",
  "roleOther": "ro",
  "notificationBell": true
}
`)), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	err = LoadTheme(confDir)
	if err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}

	if got := TableTheme().Items; got != 10 {
		t.Fatalf("TableTheme().Items = %d, want 10", got)
	}

	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	testboil.AssertStringContains(t, string(themeBytes), `"tableItems": 10`)
}

func TestDefaultTheme_HasTableItemsSetTo10(t *testing.T) {
	th := defaultTheme()
	if th.TableItems != 10 {
		t.Fatalf("defaultTheme().TableItems = %d, want 10", th.TableItems)
	}
}

func TestDefaultTheme_RollingOutputDefaults(t *testing.T) {
	conf := defaultTheme().RollingOutput
	if !conf.Enabled {
		t.Fatal("defaultTheme().RollingOutput.Enabled = false, want true")
	}
	if conf.WindowCellHeight != 30 {
		t.Fatalf("defaultTheme().RollingOutput.WindowCellHeight = %d, want 30", conf.WindowCellHeight)
	}
}

func TestLoadTheme_AppendsToolOutputRowsDefaultForExistingThemeWithoutField(t *testing.T) {
	assertLoadThemeAppendsRowsDefault(
		t,
		`{"notificationBell": true, "tableItems": 10}`,
		"toolOutputRows",
		ToolOutputRows,
		6,
	)
}

func TestLoadTheme_AppendsRollingOutputDefaultsForExistingThemeWithoutField(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	initial := `{"notificationBell": true, "tableItems": 10, "toolOutputRows": 6}`
	if err := os.WriteFile(themePath, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	if !RollingOutputEnabled() {
		t.Fatal("RollingOutputEnabled() = false, want true")
	}
	if RollingOutputWindowCellHeight() != 30 {
		t.Fatalf("RollingOutputWindowCellHeight() = %d, want 30", RollingOutputWindowCellHeight())
	}
	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	for _, want := range []string{`"rollingOutput"`, `"enabled": true`, `"windowCellHeight": 30`} {
		testboil.AssertStringContains(t, string(themeBytes), want)
	}
}

func TestLoadTheme_FillsMissingWindowCellHeightInPartialRollingOutput(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"notificationBell":true,"rollingOutput":{"enabled":false}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	if RollingOutputEnabled() {
		t.Fatal("RollingOutputEnabled() = true, want false")
	}
	if RollingOutputWindowCellHeight() != 30 {
		t.Fatalf("RollingOutputWindowCellHeight() = %d, want 30", RollingOutputWindowCellHeight())
	}
	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	testboil.AssertStringContains(t, string(themeBytes), `"windowCellHeight": 30`)
}

func TestLoadTheme_MigratesKebabCaseRollingOutputKeys(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"notificationBell":true,"rolling-output":{"enabled":false,"window-cell-height":12}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	if RollingOutputEnabled() {
		t.Fatal("RollingOutputEnabled() = true, want false")
	}
	if got := RollingOutputWindowCellHeight(); got != 12 {
		t.Fatalf("RollingOutputWindowCellHeight() = %d, want 12", got)
	}
	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	content := string(themeBytes)
	for _, want := range []string{`"rollingOutput"`, `"enabled": false`, `"windowCellHeight": 12`} {
		testboil.AssertStringContains(t, content, want)
	}
	if strings.Contains(content, "rolling-output") || strings.Contains(content, "window-cell-height") {
		t.Fatalf("kebab-case keys survived migration: %s", content)
	}
}

func TestLoadTheme_PreservesDisabledRollingOutput(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"notificationBell":true,"rollingOutput":{"enabled":false}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	if RollingOutputEnabled() {
		t.Fatal("RollingOutputEnabled() = true, want false")
	}
}

func TestLoadTheme_NotificationBellMigrationPreservesDisabledRollingOutput(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"rolling-output":{"enabled":false}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	if !NotificationBellEnabled() {
		t.Fatal("notification bell must default to enabled after migration")
	}
	if RollingOutputEnabled() {
		t.Fatal("RollingOutputEnabled() = true, want false")
	}

	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	content := string(themeBytes)
	testboil.AssertStringContains(t, content, `"enabled": false`)
	testboil.AssertStringContains(t, content, `"rollingOutput"`)
	if strings.Contains(content, "rolling-output") {
		t.Fatalf("kebab-case rolling-output key survived migration: %s", content)
	}
}

func TestLoadTheme_IgnoresObsoleteFlatRollingKeys(t *testing.T) {
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"notificationBell":true,"rollingOutput":false,"activityOutputRows":12}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	if !RollingOutputEnabled() {
		t.Fatal("obsolete flat rollingOutput must not disable the rolling output")
	}
	if RollingOutputWindowCellHeight() != 30 {
		t.Fatalf("RollingOutputWindowCellHeight() = %d, want 30", RollingOutputWindowCellHeight())
	}
	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	content := string(themeBytes)
	testboil.AssertStringContains(t, content, `"rollingOutput": {`)
	testboil.AssertStringContains(t, content, `"windowCellHeight": 30`)
}

func assertLoadThemeAppendsRowsDefault(t *testing.T, initial, key string, get func() int, want int) {
	t.Helper()
	confDir := t.TempDir()
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}

	if err := LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	if got := get(); got != want {
		t.Fatalf("%s = %d, want %d", key, got, want)
	}
	themeBytes, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", themePath, err)
	}
	testboil.AssertStringContains(t, string(themeBytes), fmt.Sprintf("%q: %d", key, want))
}
