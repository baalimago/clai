package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
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
		"skills",
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
	priceFiles := []string{
		"mock_test_test.json",
		"mock_test_mock_test.json",
	}
	for _, name := range priceFiles {
		if err := os.WriteFile(filepath.Join(confDir, name), priceBytes, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &text.Default); err != nil {
		t.Fatalf("CreateFile(textConfig.json): %v", err)
	}
	if err := utils.CreateFile(filepath.Join(confDir, "skills.json"), &skills.Config{
		Enabled:            false,
		GlobalSkillDirs:    []string{},
		ProjectSkillDirs:   []string{"./agents/skills", ".claude/skills"},
		TrustAllSkills:     false,
		MaxActivatedSkills: 10,
	}); err != nil {
		t.Fatalf("CreateFile(skills.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)

	return confDir
}
