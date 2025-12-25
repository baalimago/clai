package tools

import (
	"os"
	"path/filepath"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestWriteFileTool_Call(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		input   pub_models.Input
		wantErr bool
		check   func(t *testing.T, filePath string)
	}{
		{
			name: "write new file",
			input: pub_models.Input{
				"file_path": filepath.Join(tempDir, "test1.txt"),
				"content":   "Hello, World!",
			},
			wantErr: false,
			check: func(t *testing.T, filePath string) {
				content, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}
				if string(content) != "Hello, World!" {
					t.Errorf("Expected content 'Hello, World!', got '%s'", string(content))
				}
			},
		},
		{
			name: "overwrite existing file",
			input: pub_models.Input{
				"file_path": filepath.Join(tempDir, "test2.txt"),
				"content":   "New content",
			},
			wantErr: false,
			check: func(t *testing.T, filePath string) {
				content, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}
				if string(content) != "New content" {
					t.Errorf("Expected content 'New content', got '%s'", string(content))
				}
			},
		},
		{
			name: "append to existing file",
			input: pub_models.Input{
				"file_path": filepath.Join(tempDir, "test3.txt"),
				"content":   " Appended content",
				"append":    true,
			},
			wantErr: false,
			check: func(t *testing.T, filePath string) {
				content, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}
				if string(content) != "Initial content Appended content" {
					t.Errorf("Expected content 'Initial content Appended content', got '%s'", string(content))
				}
			},
		},
		{
			name: "missing file_path",
			input: pub_models.Input{
				"content": "Some content",
			},
			wantErr: true,
		},
		{
			name: "missing content",
			input: pub_models.Input{
				"file_path": filepath.Join(tempDir, "test4.txt"),
			},
			wantErr: true,
		},
		{
			name: "invalid append type",
			input: pub_models.Input{
				"file_path": filepath.Join(tempDir, "test5.txt"),
				"content":   "Some content",
				"append":    "true",
			},
			wantErr: true,
		},
	}

	writeTool := WriteFileTool{}

	// Set up file for append test
	if err := os.WriteFile(filepath.Join(tempDir, "test3.txt"), []byte("Initial content"), 0o644); err != nil {
		t.Fatalf("Failed to set up append test: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := writeTool.Call(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("WriteFileTool.Call() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if result == "" {
					t.Errorf("WriteFileTool.Call() returned empty result")
				}
				if tt.check != nil {
					tt.check(t, tt.input["file_path"].(string))
				}
			}
		})
	}
}

func TestWriteFileTool_Specification(t *testing.T) {
	writeTool := WriteFileTool{}
	userFunc := writeTool.Specification()

	if userFunc.Name != "write_file" {
		t.Errorf("Expected name 'write_file', got '%s'", userFunc.Name)
	}

	if userFunc.Description != "Write content to a file. Creates the file if it doesn't exist, or overwrites it if it does." {
		t.Errorf("Unexpected description: %s", userFunc.Description)
	}

	if len(userFunc.Inputs.Required) != 2 || (userFunc.Inputs.Required)[0] != "file_path" || (userFunc.Inputs.Required)[1] != "content" {
		t.Errorf("Unexpected required inputs: %v", userFunc.Inputs.Required)
	}

	if len(userFunc.Inputs.Properties) != 3 {
		t.Errorf("Expected 3 properties, got %d", len(userFunc.Inputs.Properties))
	}
}
