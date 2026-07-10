package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func reasoningObj(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	obj, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning object, got %#v", body["reasoning"])
	}
	return obj
}

func TestResponsesStreamer_ReasoningRequestedWithEffortForReasoningModels(t *testing.T) {
	t.Parallel()

	s := &responsesStreamer{model: "gpt-5.2", reasoningEffort: "high"}
	body := captureResponsesRequestBody(t, s)
	obj := reasoningObj(t, body)
	if obj["summary"] != "auto" {
		t.Fatalf("summary: got %#v want auto", obj["summary"])
	}
	if obj["effort"] != "high" {
		t.Fatalf("effort: got %#v want high", obj["effort"])
	}
}

func TestResponsesStreamer_ReasoningSummaryRequestedWhenEffortUnset(t *testing.T) {
	t.Parallel()

	// Even with no configured effort, summary must be requested so the [thinking]
	// deltas actually stream; effort is omitted so the API default applies.
	s := &responsesStreamer{model: "o3-mini"}
	body := captureResponsesRequestBody(t, s)
	obj := reasoningObj(t, body)
	if obj["summary"] != "auto" {
		t.Fatalf("summary: got %#v want auto", obj["summary"])
	}
	if _, ok := obj["effort"]; ok {
		t.Fatalf("effort should be omitted when unset, got %#v", obj["effort"])
	}
}

func TestResponsesStreamer_ReasoningOmittedForNonReasoningModels(t *testing.T) {
	t.Parallel()

	// A configured effort must not leak onto a non-reasoning model, which rejects it.
	s := &responsesStreamer{model: "gpt-4.1-mini", reasoningEffort: "high"}
	body := captureResponsesRequestBody(t, s)
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("reasoning must be omitted for non-reasoning models, got %#v", body["reasoning"])
	}
}

func TestResponsesStreamer_StoreAlwaysFalse(t *testing.T) {
	t.Parallel()

	body := captureResponsesRequestBody(t, &responsesStreamer{model: "gpt-4.1-mini"})
	store, ok := body["store"]
	if !ok {
		t.Fatalf("store must be sent")
	}
	if store != false {
		t.Fatalf("store: got %#v want false", store)
	}
}

func TestChatGPT_Responses_ForwardsReasoningEffort(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	g := &ChatGPT{Model: "gpt-5.2", URL: srv.URL + "/v1/responses", ReasoningEffort: "high"}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ch {
	}

	obj := reasoningObj(t, got)
	if obj["summary"] != "auto" || obj["effort"] != "high" {
		t.Fatalf("unexpected reasoning payload: %#v", obj)
	}
}

func TestChatGPT_ChatCompletions_ForwardsReasoningEffortForReasoningModels(t *testing.T) {
	g := &ChatGPT{Model: "gpt-5.2", ReasoningEffort: "low"}
	body := captureChatCompletionsBody(t, g)
	if body["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort: got %#v want low", body["reasoning_effort"])
	}
}

func TestChatGPT_ChatCompletions_OmitsReasoningEffortForNonReasoningModels(t *testing.T) {
	g := &ChatGPT{Model: "gpt-4.1-mini", ReasoningEffort: "low"}
	body := captureChatCompletionsBody(t, g)
	if _, ok := body["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort must be omitted for non-reasoning models, got %#v", body["reasoning_effort"])
	}
}
