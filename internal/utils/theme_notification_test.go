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

	if NotificationBellEnabled() {
		t.Fatal("expected notification bell to be disabled")
	}
}
