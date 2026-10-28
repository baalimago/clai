package e2etest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
)

// ModuleRoot returns the repository root from this file's own location, so
// rows that read tracked files (architecture docs, examples) do not depend on
// the process working directory.
func ModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed, cannot locate the module root")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func SetupMainTestConfigDir(t *testing.T) string {
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
  "loopTitle": "",
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
	// setupTooling resolves the production schema cache through
	// utils.GetClaiCacheDir; without this, every e2e test that reaches
	// it resolves the developer's real cache directory the moment any of
	// them configures a non-empty mcpServers directory (R2-20).
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(confDir, "cache"))

	return confDir
}

// ChdirTemp moves the test into a fresh temp dir, restored on cleanup, and
// returns its canonical path.
func ChdirTemp(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	canon, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		canon = repoDir
	}
	return filepath.Clean(canon)
}
