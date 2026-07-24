package utils

import (
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
	testboil.AssertStringContains(t, string(themeBytes), `"notificationBell": true`)

	if !NotificationBellEnabled() {
		t.Fatal("expected notification bell to remain enabled because zero-valued bools are backfilled from defaults")
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
