package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestResponsesStreamer_CreateRequest_ToolChoiceNotSentWhenNoTools(t *testing.T) {
	t.Parallel()

	var got map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n"))
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{
		apiKey: "k",
		url:    srv.URL,
		model:  "m",
		client: srv.Client(),
	}

	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}

	_, err := s.stream(context.Background(), chat)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	if _, ok := got["tool_choice"]; ok {
		t.Fatalf("expected tool_choice to be omitted when tools are not provided, got: %#v", got["tool_choice"])
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("expected tools to be omitted when not provided, got: %#v", got["tools"])
	}
}
