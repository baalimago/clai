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

func TestResponsesStreamer_ToolCallInputsPropagated(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"rg\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"pattern\\\":\\\"foo\\\",\\\"path\\\":\\\".\\\"}\"}\n\n")
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

	var got *pub_models.Input
	for ev := range ch {
		if c, ok := ev.(pub_models.Call); ok {
			got = c.Inputs
		}
	}

	if got == nil {
		t.Fatalf("expected inputs")
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal inputs: %v", err)
	}
	if m["pattern"] != "foo" || m["path"] != "." {
		t.Fatalf("inputs not propagated: got %s", string(b))
	}
}
