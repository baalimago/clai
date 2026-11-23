package generic

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"

	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSetupPrompts(t *testing.T) {
	t.Run("formats positional args into the prompt", func(t *testing.T) {
		c := Configurations{PromptFormat: "draw: '%v'"}
		if err := c.SetupPrompts(procenv.Context(context.Background()), []string{"photo", "a", "cat"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "draw: 'a cat'") {
			t.Fatalf("expected formatted prompt, got %q", c.Prompt)
		}
	})

	t.Run("reply mode prepends the previous conversation", func(t *testing.T) {
		confDir := t.TempDir()
		t.Setenv("CLAI_CONFIG_DIR", confDir)
		prev := pub_models.Chat{ID: "previous-chat", Messages: []pub_models.Message{
			{Role: "user", Content: "previous prompt"},
		}}
		if err := chat.SaveAsPreviousQuery(context.Background(), confDir, prev); err != nil {
			t.Fatalf("SaveAsPreviousQuery: %v", err)
		}

		c := Configurations{PromptFormat: "%v", ReplyMode: true}
		if err := c.SetupPrompts(procenv.Context(context.Background()), []string{"photo", "again"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "previous prompt") || !strings.Contains(c.Prompt, "again") {
			t.Fatalf("expected reply context + new prompt, got %q", c.Prompt)
		}
	})

	t.Run("reply mode without previous query degrades to a plain prompt", func(t *testing.T) {
		t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
		c := Configurations{PromptFormat: "%v", ReplyMode: true}
		if err := c.SetupPrompts(procenv.Context(context.Background()), []string{"photo", "more"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "more") {
			t.Fatalf("expected prompt from args, got %q", c.Prompt)
		}
	})
}
