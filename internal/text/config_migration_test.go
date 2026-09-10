package text

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestMigrateOldChatConfig(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatalf("failed to create temp dirr: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create an old chat config file
	oldChatConfig := oldChatConfig{
		Model:            "gpt-3.5-turbo",
		SystemPrompt:     "You are a helpful assistant.",
		FrequencyPenalty: 0.5,
		MaxTokens:        nil,
		PresencePenalty:  0.5,
		Temperature:      0.8,
		TopP:             1.0,
		URL:              "https://api.openai.com",
	}
	oldChatConfigPath := filepath.Join(tempDir, "chatConfig.json")
	err = utils.CreateFile(oldChatConfigPath, &oldChatConfig)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// Run the migration function
	err = MigrateOldChatConfig(tempDir)
	if err != nil {
		t.Fatalf("failed to migrate old chat config: %v", err)
	}

	// Check if the new text config file is created
	newTextConfigPath := filepath.Join(tempDir, "textConfig.json")
	_, err = os.Stat(newTextConfigPath)
	if err != nil {
		t.Fatalf("failed to find new config file: %v", err)
	}

	// Check if the old chat config file is removed
	_, err = os.Stat(oldChatConfigPath)
	if !os.IsNotExist(err) {
		t.Fatalf("failed to remove old chat config file: %v", err)
	}

	// Check if the new vendor-specific config file is created
	newVendorConfigPath := filepath.Join(tempDir, "openai_gpt_gpt-3.5-turbo.json")
	_, err = os.Stat(newVendorConfigPath)
	if err != nil {
		t.Fatalf("failed to create new config: %v", err)
	}
}

// TestTextConfigMigration_addsSummaryKeys pins that the presence-based
// loader announces summarize-conversations and summary-model on an upgraded
// file, fills the README default for the first, and keeps the empty
// summary-model key on disk (migrate tag, no omitempty).
func TestTextConfigMigration_addsSummaryKeys(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(confDir, "textConfig.json"), []byte(`{"model":"test","use-tools":false}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	conf, added, err := utils.LoadConfigFromFileCollect(confDir, "textConfig.json", MigrateOldChatConfig, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFileCollect: %v", err)
	}
	for _, key := range []string{"summarize-conversations", "summary-model"} {
		if !slices.Contains(added, key) {
			t.Fatalf("added = %v, want %q announced", added, key)
		}
	}
	if !conf.SummarizeConversations || conf.SummaryModel != "" {
		t.Fatalf("conf = summarize=%t model=%q, want the defaults true/\"\"", conf.SummarizeConversations, conf.SummaryModel)
	}
	raw, err := os.ReadFile(filepath.Join(confDir, "textConfig.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(present["summary-model"]) != `""` || string(present["summarize-conversations"]) != "true" {
		t.Fatalf("rewritten file = %s, want both keys persisted", raw)
	}
	_, again, err := utils.LoadConfigFromFileCollect(confDir, "textConfig.json", MigrateOldChatConfig, &Default)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second load added %v, want nothing", again)
	}
}
