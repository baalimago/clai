package mistral

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/baalimago/clai/internal/text/generic"
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

func TestStreamCompletions_JSONSchemaForcesAutoToolChoice(t *testing.T) {
	m := Default
	t.Setenv("MISTRAL_API_KEY", "k")
	if err := m.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Register a tool so ToolChoice is included in the request.
	m.InternalRegisterTool(mockTool{})

	// Set json_schema response format.
	m.ResponseFormat = &generic.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &generic.JSONSchemaSpec{
			Name:   "test",
			Strict: true,
			Schema: map[string]any{"type": "object"},
		},
	}

	var capturedToolChoice string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		if tc, ok := body["tool_choice"].(string); ok {
			capturedToolChoice = tc
		}
		// Return empty SSE to avoid parse errors.
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer ts.Close()

	m.StreamCompleter.URL = ts.URL

	ctx := context.Background()
	ch, err := m.StreamCompletions(ctx, pub_models.Chat{
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletions err: %v", err)
	}
	// Drain the channel.
	for range ch {
	}

	if capturedToolChoice != "auto" {
		t.Fatalf("expected tool_choice='auto' when json_schema is set, got: %q", capturedToolChoice)
	}

	// Verify we restored the original ToolChoice after the call.
	if m.ToolChoice == nil || *m.ToolChoice != "any" {
		t.Fatalf("expected ToolChoice to be restored to 'any', got: %v", m.ToolChoice)
	}
}

type mockTool struct{}

func (mockTool) Call(pub_models.Input) (string, error) { return "ok", nil }
func (mockTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "test",
		Description: "a test tool",
		Inputs:      &pub_models.InputSchema{Type: "object"},
	}
}
