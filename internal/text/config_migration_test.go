package text

import (
	"os"
	"path/filepath"
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
