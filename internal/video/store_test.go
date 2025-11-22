package video

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveVideo(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "video_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create valid base64 data
	testContent := "Hello Video"
	b64Data := base64.StdEncoding.EncodeToString([]byte(testContent))

	container := "mp4"

	t.Run("success write to dir", func(t *testing.T) {
		out := Output{
			Dir:    tmpDir,
			Prefix: "test",
		}

		filePath, err := SaveVideo(out, b64Data, container)
		if err != nil {
			t.Fatalf("SaveVideo failed: %v", err)
		}

		// Verify file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("File was not created at %v", filePath)
		}

		// Verify content
		content, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("Failed to read created file: %v", err)
		}
		if string(content) != testContent {
			t.Errorf("File content mismatch. Got %s, want %s", string(content), testContent)
		}

		// Check filename format
		baseName := filepath.Base(filePath)
		if !strings.HasPrefix(baseName, "test_") || !strings.HasSuffix(baseName, "."+container) {
			t.Errorf("Filename format incorrect: %v", baseName)
		}
	})

	t.Run("invalid base64", func(t *testing.T) {
		out := Output{
			Dir:    tmpDir,
			Prefix: "test",
		}
		_, err := SaveVideo(out, "invalid-base64!!!!", container)
		if err == nil {
			t.Error("Expected error for invalid base64, got nil")
		}
	})

	t.Run("fallback to tmp", func(t *testing.T) {
		// Use a non-existent directory to force error (and thus fallback)
		// Or a directory we can"t write to.
		// Using a nested non-existent dir usually causes write error unless MkdirAll involves,
		// but WriteFile doesn"t create parent dirs.
		nonExistentDir := filepath.Join(tmpDir, "nonexistent")

		out := Output{
			Dir:    nonExistentDir,
			Prefix: "fallback",
		}

		filePath, err := SaveVideo(out, b64Data, container)
		if err != nil {
			t.Fatalf("SaveVideo failed during fallback test: %v", err)
		}

		// Check that it wrote to /tmp
		if !strings.HasPrefix(filePath, "/tmp/") {
			t.Errorf("Expected fallback to /tmp, got path: %v", filePath)
		}

		// Verify file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("File was not created at fallback location %v", filePath)
		}

		// Clean up the file in /tmp
		defer os.Remove(filePath)
	})
}
