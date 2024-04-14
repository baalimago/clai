package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/photo"
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
	err = migrateOldChatConfig(tempDir)
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

func TestMigrateOldPhotoConfig(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create an old photoConfig.json file with test data
	oldPhotoConfigData := `{                                                                                                                                                                                                             
                "model": "test-model",                                                                                                                                                                                                       
                "photo-dir": "test-photo-dir",                                                                                                                                                                                               
                "photo-prefix": "test-photo-prefix",                                                                                                                                                                                         
                "prompt-format": "test-prompt-format"                                                                                                                                                                                        
        }`
	oldPhotoConfigPath := filepath.Join(tempDir, "photoConfig.json")
	err := os.WriteFile(oldPhotoConfigPath, []byte(oldPhotoConfigData), 0o644)
	if err != nil {
		t.Fatalf("Failed to create old photoConfig.json: %v", err)
	}

	// Call migrateOldPhotoConfig
	err = migrateOldPhotoConfig(tempDir)
	if err != nil {
		t.Fatalf("migrateOldPhotoConfig failed: %v", err)
	}

	// Check if the new photoConfig.json file was created
	newPhotoConfigPath := filepath.Join(tempDir, "photoConfig.json")
	if _, err := os.Stat(newPhotoConfigPath); os.IsNotExist(err) {
		t.Error("New photoConfig.json file was not created")
	}

	// Read the new photoConfig.json file and check its contents
	var newPhotoConfig photo.Configurations
	err = utils.ReadAndUnmarshal(newPhotoConfigPath, &newPhotoConfig)
	if err != nil {
		t.Fatalf("Failed to read new photoConfig.json: %v", err)
	}

	expectedPhotoConfig := photo.Configurations{
		Model:        "test-model",
		PromptFormat: "test-prompt-format",
		Output: photo.Output{
			Type:   photo.LOCAL,
			Dir:    "test-photo-dir",
			Prefix: "test-photo-prefix",
		},
	}

	if newPhotoConfig != expectedPhotoConfig {
		t.Errorf("Unexpected photo config.\nExpected: %+v\nGot: %+v", expectedPhotoConfig, newPhotoConfig)
	}
}
