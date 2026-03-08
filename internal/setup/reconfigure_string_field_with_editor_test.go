package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text"
)

func TestUnescapedFieldEdit_ShellContext(t *testing.T) {
	dir := t.TempDir()
	profile := text.Profile{
		Model:           "m",
		UseTools:        true,
		Tools:           []string{"t1"},
		Prompt:          "keep\\nthis",
		ShellContext:    "line1\\nline2\\tX",
		SaveReplyAsConv: true,
	}
	b, err := json.MarshalIndent(profile, "", "\t")
	if err != nil {
		t.Fatalf("failed to marshal profile: %v", err)
	}
	fp := filepath.Join(dir, "prof.json")
	if err := os.WriteFile(fp, b, 0o644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	editor := filepath.Join(dir, "ed.sh")
	script := "#!/bin/sh\ncat > \"$1\" <<'EOF'\nctxA\nctxB\tY\nEOF\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write editor script: %v", err)
	}
	t.Setenv("EDITOR", editor)

	cfg := config{name: "prof.json", filePath: fp}
	if err := actionReconfigureStringFieldWithEditor(cfg, "shell-context"); err != nil {
		t.Fatalf("failed to edit shell-context: %v", err)
	}

	updatedBytes, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("failed to read updated profile: %v", err)
	}
	var updated map[string]any
	if err := json.Unmarshal(updatedBytes, &updated); err != nil {
		t.Fatalf("failed to unmarshal updated profile: %v", err)
	}

	if got := updated["shell-context"]; got != "ctxA\\nctxB\\tY" {
		t.Fatalf("unexpected shell-context: %v", got)
	}
	if got := updated["prompt"]; got != "keep\\nthis" {
		t.Fatalf("unexpected prompt mutation: %v", got)
	}
}

func TestUnescapedFieldEdit_MissingField(t *testing.T) {
	dir := t.TempDir()
	b, err := json.MarshalIndent(map[string]any{"model": "m"}, "", "\t")
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	fp := filepath.Join(dir, "prof.json")
	if err := os.WriteFile(fp, b, 0o644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	err = actionReconfigureStringFieldWithEditor(config{name: "prof.json", filePath: fp}, "shell-context")
	if err == nil || !strings.Contains(err.Error(), "missing string field") {
		t.Fatalf("expected missing field error, got %v", err)
	}
}

func TestUnescapedFieldEdit_NonStringField(t *testing.T) {
	dir := t.TempDir()
	b, err := json.MarshalIndent(map[string]any{"shell-context": true}, "", "\t")
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	fp := filepath.Join(dir, "prof.json")
	if err := os.WriteFile(fp, b, 0o644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	err = actionReconfigureStringFieldWithEditor(config{name: "prof.json", filePath: fp}, "shell-context")
	if err == nil || !strings.Contains(err.Error(), "not a string") {
		t.Fatalf("expected non-string error, got %v", err)
	}
}
