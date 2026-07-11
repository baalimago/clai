package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestCastPrimitive(t *testing.T) {
	tests := []struct {
		name string

		input any
		want  any
	}{
		{"String to int", "42", 42},
		{"String to float", "3.14", 3.14},
		{"String remains string", "hello", "hello"},
		{"Boolean true", "true", true},
		{"Boolean false", "false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := castPrimitive(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("castPrimitive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconfigureWithEditor(t *testing.T) {
	tests := []struct {
		name    string
		content string

		editor  string
		wantErr bool
	}{
		{
			name:    "No editor set",
			editor:  "",
			content: "",
			wantErr: true,
		},
		{
			name:    "Valid editor",
			editor:  "echo",
			content: "{\"test\": \"value\"}",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "config.json")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			oldEditor := os.Getenv("EDITOR")
			defer os.Setenv("EDITOR", oldEditor)
			os.Setenv("EDITOR", tt.editor)

			cfg := config{
				name:     "test",
				filePath: tmpFile,
			}
			err := actionReconfigureWithEditor(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("reconfigureWithEditor() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestActionCopy(t *testing.T) {
	t.Run("successful copy creates file and returns new config", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.json")
		content := `{"key": "value"}`
		if err := os.WriteFile(srcPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "mycopy", nil
		})
		defer restore()

		cfg := config{name: "source.json", filePath: srcPath}
		newCfg, err := actionCopy(cfg)
		if err != nil {
			t.Fatalf("actionCopy(): %v", err)
		}

		wantPath := filepath.Join(dir, "mycopy.json")
		if newCfg.filePath != wantPath {
			t.Fatalf("newCfg.filePath = %q, want %q", newCfg.filePath, wantPath)
		}
		if newCfg.name != "mycopy" {
			t.Fatalf("newCfg.name = %q, want %q", newCfg.name, "mycopy")
		}

		copied, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatalf("ReadFile(copy): %v", err)
		}
		if string(copied) != content {
			t.Fatalf("copy content = %q, want %q", string(copied), content)
		}
	})

	t.Run("empty name returns error", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.json")
		if err := os.WriteFile(srcPath, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "", nil
		})
		defer restore()

		_, err := actionCopy(config{name: "source.json", filePath: srcPath})
		if err == nil {
			t.Fatal("expected error for empty name, got nil")
		}
	})

	t.Run("target already exists returns error", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.json")
		if err := os.WriteFile(srcPath, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "existing.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile(existing): %v", err)
		}

		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "existing", nil
		})
		defer restore()

		_, err := actionCopy(config{name: "source.json", filePath: srcPath})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected 'already exists' error, got: %v", err)
		}
	})
}
