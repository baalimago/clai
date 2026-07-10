package openai

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

// captureChatCompletionsBody drives ChatGPT through the legacy chat-completions
// opt-out path (explicit URL) and returns the decoded request body.
func captureChatCompletionsBody(t *testing.T, g *ChatGPT) map[string]any {
	t.Helper()

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	t.Setenv("OPENAI_API_KEY", "key")
	g.URL = srv.URL + "/v1/chat/completions"
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if g.useResponses {
		t.Fatalf("expected legacy chat-completions path for URL %q", g.URL)
	}

	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ch {
	}
	return got
}

func TestChatGPT_ChatCompletions_OmitsSamplingForReasoningModels(t *testing.T) {
	g := &ChatGPT{Model: "gpt-5.2", Temperature: 0.5, TopP: 0.8, FrequencyPenalty: 0.3, PresencePenalty: 0.2}
	body := captureChatCompletionsBody(t, g)

	for _, key := range []string{"temperature", "top_p", "frequency_penalty", "presence_penalty"} {
		if _, ok := body[key]; ok {
			t.Fatalf("reasoning model on legacy path must not send %q, got %#v", key, body[key])
		}
	}
}

func TestChatGPT_ChatCompletions_ForwardsSamplingForNonReasoningModels(t *testing.T) {
	g := &ChatGPT{Model: "gpt-4.1-mini", Temperature: 0.4, TopP: 0.7}
	body := captureChatCompletionsBody(t, g)

	if body["temperature"] != 0.4 {
		t.Fatalf("temperature: got %#v want 0.4", body["temperature"])
	}
	if body["top_p"] != 0.7 {
		t.Fatalf("top_p: got %#v want 0.7", body["top_p"])
	}
}

func TestChatGPT_ChatCompletions_ClearsStaleParametersWhenModelChanges(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	g := &ChatGPT{
		Model:           "gpt-4.1-mini",
		URL:             srv.URL + "/v1/chat/completions",
		Temperature:     0.4,
		TopP:            0.7,
		ReasoningEffort: "high",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	stream := func() {
		ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		for range ch {
		}
	}
	stream()
	g.Model = "gpt-5.2"
	stream()
	g.Model = "gpt-4.1-mini"
	stream()

	if len(bodies) != 3 {
		t.Fatalf("requests: got %d want 3", len(bodies))
	}
	if _, ok := bodies[1]["temperature"]; ok {
		t.Fatalf("reasoning request retained stale temperature: %#v", bodies[1])
	}
	if bodies[1]["reasoning_effort"] != "high" {
		t.Fatalf("reasoning effort: got %#v", bodies[1]["reasoning_effort"])
	}
	if _, ok := bodies[2]["reasoning_effort"]; ok {
		t.Fatalf("non-reasoning request retained stale reasoning_effort: %#v", bodies[2])
	}
}

// TestChatGPT_Responses_ForwardsStructuredOutput exercises the full public wiring
// (SetResponseFormat -> StreamCompletions -> responses text.format) rather than
// constructing a responsesStreamer literal.
func TestChatGPT_Responses_ForwardsStructuredOutput(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	// A custom host must name /v1/responses to select the Responses API (the
	// default flip is scoped to the canonical OpenAI host).
	g := &ChatGPT{Model: "gpt-5.2", URL: srv.URL + "/v1/responses"}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	g.SetResponseFormat(&generic.ResponseFormat{
		Type:       "json_schema",
		JSONSchema: &generic.JSONSchemaSpec{Name: "person", Strict: true, Schema: map[string]any{"type": "object"}},
	})

	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ch {
	}

	text, ok := got["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text object, got %#v", got["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("expected text.format object, got %#v", text["format"])
	}
	if format["type"] != "json_schema" || format["name"] != "person" || format["strict"] != true {
		t.Fatalf("unexpected format payload: %#v", format)
	}
}
