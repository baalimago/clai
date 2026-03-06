package setup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestCastPrimitive(t *testing.T) {
	tests := []struct {
		name  string
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
		editor  string
		content string
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
				kind:     configKindNormal,
			}

			err := reconfigureWithEditor(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("reconfigureWithEditor() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestQueryForAction_Back(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "b", nil
	})
	defer restore()

	_, err := queryForAction([]action{conf})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, utils.ErrBack) {
		t.Fatalf("expected ErrBack, got %v", err)
	}
}

func TestActOnConfigItem_BackFromActions(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "b", nil
	})
	defer restore()

	err := actOnConfigItem(setupCategory{actions: []action{conf}}, config{name: "cfg", filePath: "cfg.json", kind: configKindNormal})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, utils.ErrBack) {
		t.Fatalf("expected ErrBack, got %v", err)
	}
}
