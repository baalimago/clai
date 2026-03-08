package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellContextSetupCategory_LoadsAndCreatesDefaultContext(t *testing.T) {
	dir := t.TempDir()
	shellContextsDir := filepath.Join(dir, "shellContexts")
	if err := os.MkdirAll(shellContextsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(shellContexts): %v", err)
	}

	category := shellContextSetupCategory()

	cfgs, err := category.load(dir)
	if err != nil {
		t.Fatalf("load shell context configs: %v", err)
	}

	if len(cfgs) != 1 {
		t.Fatalf("expected 1 shell context config, got %d: %+v", len(cfgs), cfgs)
	}

	gotPath := filepath.Join(dir, "shellContexts", "default.json")
	if cfgs[0].filePath != gotPath {
		t.Fatalf("unexpected config path: got %q want %q", cfgs[0].filePath, gotPath)
	}

	b, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("ReadFile(default shell context): %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal(default shell context): %v", err)
	}

	if got["template"] == "" {
		t.Fatalf("expected default shell context template to be populated")
	}
}

func TestValidateEditedStringField_ValidatesShellContextTemplate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "shellContexts", "ctx.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(shellContexts): %v", err)
	}

	err := validateEditedStringField(
		config{name: "ctx.json", filePath: cfgPath},
		"template",
		"{{ if }}",
	)
	if err == nil {
		t.Fatalf("expected template validation error, got nil")
	}
	if !strings.Contains(err.Error(), "validate shell context template") {
		t.Fatalf("unexpected error: %v", err)
	}
}
