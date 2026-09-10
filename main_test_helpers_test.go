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
	// A developer's key would make every query run fetch the live OpenRouter
	// catalog; keyless CI never pays that, so neither should local runs.
	t.Setenv("OPENROUTER_API_KEY", "")

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
  "roleReasoning": "",
  "roleOther": "",
  "notificationBell": true,
  "tableItems": 10,
  "toolOutputRows": 6,
  "rollingOutput": {
    "enabled": true,
    "windowCellHeight": 30
  }
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

	// The shared fixture runs with the in-flight summarizer off so the
	// pre-existing e2e rows cost what they cost on main; the summary rows
	// opt in through setupSummaryE2E (worklog
	// 2026-09-09-conversation-summaries, phase 8 notes).
	textConf := text.Default
	textConf.SummarizeConversations = false
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &textConf); err != nil {
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

// blankDebugAndVendorKeys keeps a fixture host-insensitive: no debug
// chatter on the captured streams and no developer key that could select a
// paid vendor.
func blankDebugAndVendorKeys(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DEBUG", "DEBUG_SUMMARY", "DEBUG_CHAT", "DEBUG_STOPLOSS", "OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
}
