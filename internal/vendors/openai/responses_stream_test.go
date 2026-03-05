package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestResponsesMapper_MessageRoleBasedContentTypes(t *testing.T) {
	t.Parallel()

	userParts, err := mapMessageToResponsesContent(pub_models.Message{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatalf("map user: %v", err)
	}
	if len(userParts) != 1 || userParts[0].Type != "input_text" {
		t.Fatalf("user parts: got %#v", userParts)
	}
}

func TestResponsesMapper_ToolRole_IsMappedToFunctionCallOutputItem(t *testing.T) {
	t.Parallel()

	items, err := mapMessageToResponsesInputItems(pub_models.Message{Role: "tool", ToolCallID: "call_1", Content: "ok"})
	if err != nil {
		t.Fatalf("map tool msg: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Type != "function_call_output" {
		t.Fatalf("type: got %q want %q", item.Type, "function_call_output")
	}
	if item.CallID != "call_1" {
		t.Fatalf("call_id: got %q want %q", item.CallID, "call_1")
	}
	if s, ok := item.Output.(string); !ok || s != "ok" {
		t.Fatalf("output: got %#v want %q", item.Output, "ok")
	}
}

func TestResponsesMapper_ToolRole_MissingToolCallID_ReturnsError(t *testing.T) {
	t.Parallel()

	_, err := mapMessageToResponsesInputItems(pub_models.Message{Role: "tool", Content: "ok"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "tool_call_id") {
		t.Fatalf("expected context about tool_call_id, got %v", err)
	}
}

func TestResponsesMapper_MapChatToResponsesInput_IncludesAssistantMessages(t *testing.T) {
	t.Parallel()

	input, err := mapChatToResponsesInput(pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "system", Content: "s"},
			{Role: "user", Content: "u"},
			{Role: "assistant", Content: "a"},
			{Role: "tool", ToolCallID: "call_1", Content: "ok"},
		},
	})
	if err != nil {
		t.Fatalf("mapChatToResponsesInput: %v", err)
	}
	if len(input) != 4 {
		t.Fatalf("len: got %d want 4, input=%#v", len(input), input)
	}

	var gotAssistant bool
	for _, it := range input {
		if it.Type == "message" && it.Role == "assistant" {
			gotAssistant = true
		}
	}
	if !gotAssistant {
		t.Fatalf("expected assistant message to be included, input=%#v", input)
	}
}

func TestResponsesStreamer_TextStreaming(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{
		apiKey: "k",
		url:    srv.URL + "/v1/responses",
		model:  "gpt-test",
		client: srv.Client(),
	}

	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("path: got %q want %q", gotPath, "/v1/responses")
	}

	var deltas []string
	var gotStop bool
	for ev := range ch {
		switch v := ev.(type) {
		case string:
			deltas = append(deltas, v)
		case models.StopEvent:
			gotStop = true
		}
	}

	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("deltas: got %q want %q", strings.Join(deltas, ""), "hello")
	}
	if !gotStop {
		t.Fatalf("expected StopEvent")
	}
}

func TestResponsesStreamer_FunctionCallStreaming_EmitsOnDoneBoundary(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		tools.Registry.Set("rg", fakeTool{spec: pub_models.Specification{Name: "rg"}})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"rg\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"pattern\\\":\\\"foo\\\"\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\",\\\"path\\\":\\\".\\\"\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"}\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.done\"}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}))
		t.Cleanup(srv.Close)

		s := &responsesStreamer{
			apiKey: "k",
			url:    srv.URL + "/v1/responses",
			model:  "gpt-test",
			client: srv.Client(),
		}

		ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}

		var calls []pub_models.Call
		for ev := range ch {
			if c, ok := ev.(pub_models.Call); ok {
				calls = append(calls, c)
			}
		}

		if len(calls) != 1 {
			t.Fatalf("calls: got %d want 1", len(calls))
		}
		if calls[0].ID != "call_1" {
			t.Fatalf("call id: got %q want %q", calls[0].ID, "call_1")
		}
		if calls[0].Name != "rg" {
			t.Fatalf("call name: got %q want %q", calls[0].Name, "rg")
		}
		if calls[0].Inputs == nil {
			t.Fatalf("expected Inputs")
		}
		b, err := json.Marshal(calls[0].Inputs)
		if err != nil {
			t.Fatalf("marshal inputs: %v", err)
		}
		var m map[string]any
		if uErr := json.Unmarshal(b, &m); uErr != nil {
			t.Fatalf("unmarshal inputs json: %v", uErr)
		}
		if m["path"] != "." || m["pattern"] != "foo" {
			t.Fatalf("inputs: got %s", string(b))
		}
	})
}

func TestResponsesStreamer_Non200Response(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "nope")
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{
		apiKey: "k",
		url:    srv.URL + "/v1/responses",
		model:  "gpt-test",
		client: srv.Client(),
	}

	_, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "openai responses") {
		t.Fatalf("expected context prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected status, got %v", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected body, got %v", err)
	}
}

func TestResponsesStreamer_MalformedSSEJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {nope}\n\n")
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{
		apiKey: "k",
		url:    srv.URL + "/v1/responses",
		model:  "gpt-test",
		client: srv.Client(),
	}

	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var gotErr error
	for ev := range ch {
		if e, ok := ev.(error); ok {
			gotErr = e
			break
		}
	}
	if gotErr == nil {
		t.Fatalf("expected error event")
	}
	if !strings.Contains(gotErr.Error(), "openai responses: parse stream event") {
		t.Fatalf("expected context in error, got %v", gotErr)
	}
}

func TestResponsesStreamer_UsageCapturedOnCompleted(t *testing.T) {
	t.Parallel()

	capturedCh := make(chan *pub_models.Usage, 1)
	usageSetter := func(u *pub_models.Usage) error {
		capturedCh <- u
		return nil
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":123,\"output_tokens\":34,\"total_tokens\":157,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens_details\":{\"reasoning_tokens\":0}}}}\n\n")
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{
		apiKey:      "k",
		url:         srv.URL + "/v1/responses",
		model:       "gpt-test",
		client:      srv.Client(),
		usageSetter: usageSetter,
	}

	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	for range ch {
	}

	var captured *pub_models.Usage
	select {
	case captured = <-capturedCh:
	default:
		captured = nil
	}

	if captured == nil {
		t.Fatalf("expected usage to be captured")
	}
	if captured.PromptTokens != 123 || captured.CompletionTokens != 34 || captured.TotalTokens != 157 {
		t.Fatalf("usage: got %+v", *captured)
	}
	if captured.PromptTokensDetails.CachedTokens != 0 {
		t.Fatalf("prompt cached: got %d", captured.PromptTokensDetails.CachedTokens)
	}
	if captured.CompletionTokensDetails.ReasoningTokens != 0 {
		t.Fatalf("completion reasoning: got %d", captured.CompletionTokensDetails.ReasoningTokens)
	}
}
