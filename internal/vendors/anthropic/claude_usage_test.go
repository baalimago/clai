package anthropic

import (
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestClaude_TokenUsage_PopulatesPromptAndTotalFromCountedInput(t *testing.T) {
	c := &Claude{}
	c.amInputTokens = 123

	u := c.TokenUsage()
	if u == nil {
		t.Fatalf("expected non-nil usage")
	}
	if want, got := 123, u.PromptTokens; got != want {
		t.Fatalf("PromptTokens: want %d, got %d", want, got)
	}
	if want, got := 0, u.CompletionTokens; got != want {
		t.Fatalf("CompletionTokens: want %d, got %d", want, got)
	}
	if want, got := 123, u.TotalTokens; got != want {
		t.Fatalf("TotalTokens: want %d, got %d", want, got)
	}
}

func TestClaude_TokenUsage_NilReceiver(t *testing.T) {
	var c *Claude
	if got := c.TokenUsage(); got != nil {
		t.Fatalf("expected nil usage for nil receiver, got %+v", got)
	}
}

func TestClaude_TokenUsage_ZeroWhenNotCountedYet(t *testing.T) {
	c := &Claude{}
	u := c.TokenUsage()
	if u == nil {
		t.Fatalf("expected non-nil usage")
	}
	if *u != (pub_models.Usage{}) {
		t.Fatalf("expected zero usage, got %+v", *u)
	}
}
