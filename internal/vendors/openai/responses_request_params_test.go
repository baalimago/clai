package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// captureResponsesRequestBody runs the streamer against a server that records the
// decoded request body and immediately completes the stream.
func captureResponsesRequestBody(t *testing.T, s *responsesStreamer) map[string]any {
	t.Helper()

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	s.url = srv.URL
	s.client = srv.Client()
	if s.apiKey == "" {
		s.apiKey = "k"
	}
	if s.model == "" {
		s.model = "gpt-4.1-mini"
	}

	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ch {
	}
	return got
}

func responsesTextFormatFromBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text object in body, got %#v", body["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("expected text.format object, got %#v", text["format"])
	}
	return format
}

func TestResponsesStreamer_EnablesParallelToolCalls(t *testing.T) {
	t.Parallel()

	body := captureResponsesRequestBody(t, &responsesStreamer{tools: []responsesTool{{Type: "function", Name: "one"}}})
	if got := body["parallel_tool_calls"]; got != true {
		t.Fatalf("parallel_tool_calls: got %#v want true", got)
	}
}

func TestResponsesStreamer_StructuredOutput_JSONSchema(t *testing.T) {
	t.Parallel()

	s := &responsesStreamer{
		responseFormat: &generic.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &generic.JSONSchemaSpec{
				Name:   "person",
				Strict: true,
				Schema: map[string]any{"type": "object"},
			},
		},
	}
	body := captureResponsesRequestBody(t, s)
	format := responsesTextFormatFromBody(t, body)

	if format["type"] != "json_schema" {
		t.Fatalf("format type: got %#v want json_schema", format["type"])
	}
	if format["name"] != "person" {
		t.Fatalf("format name: got %#v want person", format["name"])
	}
	if format["strict"] != true {
		t.Fatalf("format strict: got %#v want true", format["strict"])
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Fatalf("format schema: got %#v want object", format["schema"])
	}
}

func TestResponsesStreamer_StructuredOutput_JSONObject(t *testing.T) {
	t.Parallel()

	s := &responsesStreamer{
		responseFormat: &generic.ResponseFormat{Type: "json_object"},
	}
	body := captureResponsesRequestBody(t, s)
	format := responsesTextFormatFromBody(t, body)

	if format["type"] != "json_object" {
		t.Fatalf("format type: got %#v want json_object", format["type"])
	}
	if _, ok := format["schema"]; ok {
		t.Fatalf("json_object should not carry a schema, got %#v", format["schema"])
	}
}

func TestResponsesStreamer_StructuredOutput_TextOmitsFormat(t *testing.T) {
	t.Parallel()

	for _, rf := range []*generic.ResponseFormat{nil, {Type: "text"}, {Type: ""}} {
		s := &responsesStreamer{responseFormat: rf}
		body := captureResponsesRequestBody(t, s)
		if _, ok := body["text"]; ok {
			t.Fatalf("expected no text field for %#v, got %#v", rf, body["text"])
		}
	}
}

func TestResponsesStreamer_MaxOutputTokensForwarded(t *testing.T) {
	t.Parallel()

	max := 4096
	s := &responsesStreamer{maxOutputTokens: &max}
	body := captureResponsesRequestBody(t, s)
	if body["max_output_tokens"] != float64(4096) {
		t.Fatalf("max_output_tokens: got %#v want 4096", body["max_output_tokens"])
	}
}

func TestResponsesStreamer_SamplingParamsForwardedWhenSet(t *testing.T) {
	t.Parallel()

	temp := 0.3
	topP := 0.9
	s := &responsesStreamer{temperature: &temp, topP: &topP}
	body := captureResponsesRequestBody(t, s)
	if body["temperature"] != 0.3 {
		t.Fatalf("temperature: got %#v want 0.3", body["temperature"])
	}
	if body["top_p"] != 0.9 {
		t.Fatalf("top_p: got %#v want 0.9", body["top_p"])
	}
}

func TestResponsesStreamer_SamplingParamsOmittedWhenNil(t *testing.T) {
	t.Parallel()

	body := captureResponsesRequestBody(t, &responsesStreamer{})
	if _, ok := body["temperature"]; ok {
		t.Fatalf("temperature should be omitted when nil, got %#v", body["temperature"])
	}
	if _, ok := body["top_p"]; ok {
		t.Fatalf("top_p should be omitted when nil, got %#v", body["top_p"])
	}
}

func TestChatGPT_Responses_OmitsSamplingForReasoningModels(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	g := &ChatGPT{Model: "gpt-5.2", URL: srv.URL + "/v1/responses", Temperature: 0.5, TopP: 0.8}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ch {
	}

	if _, ok := got["temperature"]; ok {
		t.Fatalf("reasoning model must not send temperature, got %#v", got["temperature"])
	}
	if _, ok := got["top_p"]; ok {
		t.Fatalf("reasoning model must not send top_p, got %#v", got["top_p"])
	}
}

func TestChatGPT_Responses_ForwardsSamplingForNonReasoningModels(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	g := &ChatGPT{Model: "gpt-4.1-mini", URL: srv.URL + "/v1/responses", Temperature: 0.4, TopP: 0.7}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range ch {
	}

	if got["temperature"] != 0.4 {
		t.Fatalf("temperature: got %#v want 0.4", got["temperature"])
	}
	if got["top_p"] != 0.7 {
		t.Fatalf("top_p: got %#v want 0.7", got["top_p"])
	}
}

func TestResponsesStreamer_ReasoningDeltasEmitReasoningEvents(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"think\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"ing\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{apiKey: "k", url: srv.URL, model: "gpt-5.2", client: srv.Client()}
	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var reasoning string
	var text string
	for ev := range ch {
		switch v := ev.(type) {
		case models.ReasoningEvent:
			reasoning += v.Content
		case string:
			text += v
		}
	}
	if reasoning != "thinking" {
		t.Fatalf("reasoning: got %q want %q", reasoning, "thinking")
	}
	if text != "answer" {
		t.Fatalf("text: got %q want %q", text, "answer")
	}
}

func TestResponsesStreamer_CompletedThenDoneEmitsSingleStop(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
		// A server that also emits [DONE] must not cause a second (blocking) stop.
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{apiKey: "k", url: srv.URL, model: "gpt-5.2", client: srv.Client()}
	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var stops int
	for ev := range ch {
		if _, ok := ev.(models.StopEvent); ok {
			stops++
		}
	}
	if stops != 1 {
		t.Fatalf("stop events: got %d want 1", stops)
	}
}
