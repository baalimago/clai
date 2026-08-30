package generic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSetupPrompts(t *testing.T) {
	t.Run("formats positional args into the prompt", func(t *testing.T) {
		c := Configurations{PromptFormat: "draw: '%v'"}
		if err := c.SetupPrompts([]string{"photo", "a", "cat"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "draw: 'a cat'") {
			t.Fatalf("expected formatted prompt, got %q", c.Prompt)
		}
	})

	t.Run("reply mode prepends the previous conversation", func(t *testing.T) {
		confDir := t.TempDir()
		t.Setenv("CLAI_CONFIG_DIR", confDir)
		convDir := filepath.Join(confDir, "conversations")
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		prev := pub_models.Chat{ID: "globalScope", Messages: []pub_models.Message{
			{Role: "user", Content: "previous prompt"},
		}}
		b, err := json.Marshal(prev)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(convDir, "globalScope.json"), b, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		c := Configurations{PromptFormat: "%v", ReplyMode: true}
		if err := c.SetupPrompts([]string{"photo", "again"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "previous prompt") || !strings.Contains(c.Prompt, "again") {
			t.Fatalf("expected reply context + new prompt, got %q", c.Prompt)
		}
	})

	t.Run("reply mode without previous query degrades to a plain prompt", func(t *testing.T) {
		t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
		c := Configurations{PromptFormat: "%v", ReplyMode: true}
		if err := c.SetupPrompts([]string{"photo", "more"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "more") {
			t.Fatalf("expected prompt from args, got %q", c.Prompt)
		}
	})
}
