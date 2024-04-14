package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReturnNonDefault(t *testing.T) {
	tests := []struct {
		name       string
		a          interface{}
		b          interface{}
		defaultVal interface{}
		want       interface{}
		wantErr    bool
	}{
		{
			name:       "Both defaults",
			a:          "default",
			b:          "default",
			defaultVal: "default",
			want:       "default",
			wantErr:    false,
		},
		{
			name:       "A non-default",
			a:          "non-default",
			b:          "default",
			defaultVal: "default",
			want:       "non-default",
			wantErr:    false,
		},
		{
			name:       "B non-default",
			a:          "default",
			b:          "non-default",
			defaultVal: "default",
			want:       "non-default",
			wantErr:    false,
		},
		{
			name:       "Both non-default",
			a:          "non-default-a",
			b:          "non-default-b",
			defaultVal: "default",
			want:       "default",
			wantErr:    true,
		},
		{
			name:       "Both non-default same value",
			a:          "non-default",
			b:          "non-default",
			defaultVal: "default",
			want:       "default",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReturnNonDefault(tt.a, tt.b, tt.defaultVal)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReturnNonDefault() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ReturnNonDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunMigrationCallback(t *testing.T) {
	// Create a test migration callback
	var migrationCalled bool
	migrationCb := func(configDirPath string) error {
		migrationCalled = true
		return nil
	}

	// Test running the migration callback
	configDirPath := "/path/to/config"
	err := runMigrationCallback(migrationCb, configDirPath)
	if err != nil {
		t.Errorf("Unexpected error running migration callback: %v", err)
	}
	if !migrationCalled {
		t.Error("Expected migration callback to be called")
	}

	// Test running the migration callback with nil callback
	migrationCalled = false
	err = runMigrationCallback(nil, configDirPath)
	if err != nil {
		t.Errorf("Unexpected error running nil migration callback: %v", err)
	}
	if migrationCalled {
		t.Error("Expected migration callback not to be called")
	}
}

func TestCreateConfigDir(t *testing.T) {
	// Create a temporary directory for testing
	configDirPath := filepath.Join(t.TempDir(), ".clai")

	// Test creating a new config directory
	err := createConfigDir(configDirPath)
	if err != nil {
		t.Errorf("Unexpected error creating config directory: %v", err)
	}
	if _, err := os.Stat(configDirPath); os.IsNotExist(err) {
		t.Error("Expected config directory to exist")
	}

	// Test creating an existing config directory
	err = createConfigDir(configDirPath)
	if err != nil {
		t.Errorf("Unexpected error creating existing config directory: %v", err)
	}
}

func TestCreateDefaultConfigFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	os.MkdirAll(filepath.Join(tempDir, ".clai"), 0o755)

	configDirPath := filepath.Join(tempDir, ".clai")
	configFileName := "config.json"

	// Test creating a new default config file
	dflt := &struct {
		Name string `json:"name"`
	}{Name: "John"}
	err := createDefaultConfigFile(configDirPath, configFileName, dflt)
	if err != nil {
		t.Errorf("Unexpected error creating default config file: %v", err)
	}
	configFilePath := filepath.Join(configDirPath, configFileName)
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		t.Error("Expected default config file to exist")
	}

	// Test creating an existing default config file
	err = createDefaultConfigFile(configDirPath, configFileName, dflt)
	if err != nil {
		t.Errorf("Unexpected error creating existing default config file: %v", err)
	}
}
