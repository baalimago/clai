package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestFFProbeCall(t *testing.T) {
	// Check if ffprobe is available
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available, skipping test")
	}

	// Create a temporary test file (simple text file for testing)
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	tests := []struct {
		name        string
		input       pub_models.Input
		expectError bool
		contains    []string
	}{
		{
			name: "basic file analysis",
			input: pub_models.Input{
				"file": testFile,
			},
			expectError: false,
			contains:    []string{}, // ffprobe might fail on text file, but shouldn't crash
		},
		{
			name: "with json format",
			input: pub_models.Input{
				"file":   testFile,
				"format": "json",
			},
			expectError: false,
			contains:    []string{},
		},
		{
			name: "with show format",
			input: pub_models.Input{
				"file":       testFile,
				"showFormat": true,
			},
			expectError: false,
			contains:    []string{},
		},
		{
			name: "with show streams",
			input: pub_models.Input{
				"file":        testFile,
				"showStreams": true,
			},
			expectError: false,
			contains:    []string{},
		},
		{
			name: "invalid format",
			input: pub_models.Input{
				"file":   testFile,
				"format": "invalid",
			},
			expectError: true,
			contains:    []string{"unsupported format"},
		},
		{
			name: "invalid file type",
			input: pub_models.Input{
				"file": 123,
			},
			expectError: true,
			contains:    []string{"file must be a string"},
		},
		{
			name: "invalid showFormat type",
			input: pub_models.Input{
				"file":       testFile,
				"showFormat": "invalid",
			},
			expectError: true,
			contains:    []string{"showFormat must be a boolean"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FFProbe.Call(tt.input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				for _, contains := range tt.contains {
					if !strings.Contains(err.Error(), contains) {
						t.Errorf("expected error to contain %q, got: %v", contains, err.Error())
					}
				}
			} else {
				// Note: ffprobe might still return an error for non-media files,
				// but our tool should handle the call properly
				if err != nil {
					// Check if it's an ffprobe execution error (expected for text files)
					if !strings.Contains(err.Error(), "failed to run ffprobe") {
						t.Errorf("unexpected error type: %v", err)
					}
				}
				// If no error, check result contains expected strings
				for _, contains := range tt.contains {
					if !strings.Contains(result, contains) {
						t.Errorf("expected result to contain %q, got: %v", contains, result)
					}
				}
			}
		})
	}
}

func TestFFProbeSpecification(t *testing.T) {
	spec := FFProbe.Specification()

	if spec.Name != "ffprobe" {
		t.Errorf("expected name 'ffprobe', got %q", spec.Name)
	}

	if spec.Description == "" {
		t.Error("expected non-empty description")
	}

	if spec.Inputs == nil {
		t.Fatal("expected inputs to be defined")
	}

	if spec.Inputs.Type != "object" {
		t.Errorf("expected inputs type 'object', got %q", spec.Inputs.Type)
	}

	// Check required fields
	expectedRequired := []string{"file"}
	if len(spec.Inputs.Required) != len(expectedRequired) {
		t.Errorf("expected %d required fields, got %d", len(expectedRequired), len(spec.Inputs.Required))
	}

	for _, req := range expectedRequired {
		found := slices.Contains(spec.Inputs.Required, req)
		if !found {
			t.Errorf("expected required field %q not found", req)
		}
	}

	// Check that file parameter exists
	if _, exists := spec.Inputs.Properties["file"]; !exists {
		t.Error("expected 'file' parameter to exist")
	}
}
