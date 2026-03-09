package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setupMainTestConfigDir(t *testing.T) string {
	t.Helper()

	confDir := t.TempDir()
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
		"shellContexts",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	themeContent := `{
  "primary": "",
  "secondary": "",
  "breadtext": "",
  "roleSystem": "",
  "roleUser": "",
  "roleTool": "",
  "roleOther": "",
  "notificationBell": false
}`
	if err := os.WriteFile(filepath.Join(confDir, "theme.json"), []byte(themeContent), 0o644); err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)

	return confDir
}
