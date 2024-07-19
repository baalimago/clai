package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGetConfigs(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "test_configs")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test files
	testFiles := []string{
		"config1.json",
		"config2.json",
		"textConfig.json",
		"photoConfig.json",
		"otherFile.txt",
	}
	for _, file := range testFiles {
		_, err := os.Create(filepath.Join(tempDir, file))
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", file, err)
		}
	}

	tests := []struct {
		name            string
		includeGlob     string
		excludeContains []string
		want            []config
	}{
		{
			name:            "All JSON files",
			includeGlob:     filepath.Join(tempDir, "*.json"),
			excludeContains: []string{},
			want: []config{
				{name: "config1.json", filePath: filepath.Join(tempDir, "config1.json")},
				{name: "config2.json", filePath: filepath.Join(tempDir, "config2.json")},
				{name: "photoConfig.json", filePath: filepath.Join(tempDir, "photoConfig.json")},
				{name: "textConfig.json", filePath: filepath.Join(tempDir, "textConfig.json")},
			},
		},
		{
			name:            "Exclude text and photo configs",
			includeGlob:     filepath.Join(tempDir, "*.json"),
			excludeContains: []string{"textConfig", "photoConfig"},
			want: []config{
				{name: "config1.json", filePath: filepath.Join(tempDir, "config1.json")},
				{name: "config2.json", filePath: filepath.Join(tempDir, "config2.json")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getConfigs(tt.includeGlob, tt.excludeContains)
			if err != nil {
				t.Errorf("getConfigs() error = %v", err)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getConfigs() = %v, want %v", got, tt.want)
			}
		})
	}
}
