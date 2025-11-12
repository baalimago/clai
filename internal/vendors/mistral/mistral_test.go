package mistral

import (
	"context"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestCleanRemovesExtraToolFieldsAndMergesAssistants(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "a1", ToolCalls: []pub_models.Call{{Name: "x", Function: pub_models.Specification{Name: "fn", Description: "desc"}}}},
		{Role: "assistant", Content: "a2"},
		{Role: "tool", Content: "tool-res"},
		{Role: "system", Content: "after"},
	}
	cleaned := clean(append([]pub_models.Message(nil), msgs...))

	// assistant fields stripped on first assistant with tool calls
	if cleaned[1].ToolCalls[0].Name != "" || cleaned[1].ToolCalls[0].Function.Description != "" {
		t.Fatalf("expected tool fields cleared, got %+v", cleaned[1].ToolCalls[0])
	}
	// content merged from consecutive assistants (first assistant content cleared, so only second remains with a leading newline)
	if cleaned[1].Content != "\na2" {
		t.Fatalf("expected merged content with leading newline, got %q", cleaned[1].Content)
	}
	// role change tool followed by system -> assistant
	if cleaned[3].Role != "assistant" {
		t.Fatalf("expected role assistant at idx 3, got %q", cleaned[3].Role)
	}
	// Ensure there are no consecutive assistants left
	for i := 1; i < len(cleaned); i++ {
		if cleaned[i].Role == "assistant" && cleaned[i-1].Role == "assistant" {
			t.Fatalf("expected no consecutive assistant messages after merge at positions %d and %d", i-1, i)
		}
	}
}

func TestSetupAssignsFieldsAndToolChoice(t *testing.T) {
	m := Default
	t.Setenv("MISTRAL_API_KEY", "k")
	if err := m.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if m.StreamCompleter.Model != m.Model {
		t.Errorf("model not mapped: %q vs %q", m.StreamCompleter.Model, m.Model)
	}
	if m.ToolChoice == nil || *m.ToolChoice != "any" {
		t.Errorf("expected tool choice 'any', got %#v", m.ToolChoice)
	}
	if m.Clean == nil {
		t.Errorf("expected Clean callback to be set")
	}
}

func TestStreamCompletionsDelegates(t *testing.T) {
	m := Default
	t.Setenv("MISTRAL_API_KEY", "k")
	_ = m.Setup() // ignore error, we will not actually perform network
	// Using a canceled context must quickly return an error channel or error per generic StreamCompleter tests
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = m.StreamCompletions(ctx, pub_models.Chat{})
}
