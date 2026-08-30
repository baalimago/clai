package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/utils"
)

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
