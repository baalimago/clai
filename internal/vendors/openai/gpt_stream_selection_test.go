package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestChatGPT_StreamCompletions_UsesChatCompletionsWhenURLOptsOut(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	// Explicitly configuring a chat/completions URL opts back into the legacy API.
	g := &ChatGPT{
		Model: "gpt-5.2",
		URL:   srv.URL + "/v1/chat/completions",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var gotText strings.Builder
	var gotStop bool
	for ev := range ch {
		switch v := ev.(type) {
		case string:
			gotText.WriteString(v)
		case models.StopEvent:
			gotStop = true
		}
	}

	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path: got %q want %q", gotPath, "/v1/chat/completions")
	}
	if gotText.String() != "hi" {
		t.Fatalf("text: got %q want %q", gotText.String(), "hi")
	}
	if !gotStop {
		t.Fatalf("expected StopEvent")
	}
}

func TestChatGPT_StreamCompletions_UsesResponsesByDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	// A URL naming /v1/responses selects the Responses API. (The bare-host default
	// flip is scoped to the canonical OpenAI host; see TestSelectOpenAIURL_*.)
	g := &ChatGPT{
		Model: "gpt-5.2",
		URL:   srv.URL + "/v1/responses",
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var gotText strings.Builder
	var gotStop bool
	for ev := range ch {
		switch v := ev.(type) {
		case string:
			gotText.WriteString(v)
		case models.StopEvent:
			gotStop = true
		}
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("path: got %q want %q", gotPath, "/v1/responses")
	}
	if gotText.String() != "hi" {
		t.Fatalf("text: got %q want %q", gotText.String(), "hi")
	}
	if !gotStop {
		t.Fatalf("expected StopEvent")
	}
}

func TestChatGPT_StreamCompletions_UsesResponsesForCodex(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	g := &ChatGPT{
		Model: "gpt-4.1-codex",
		URL:   srv.URL,
	}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ch, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var gotText strings.Builder
	var gotStop bool
	for ev := range ch {
		switch v := ev.(type) {
		case string:
			gotText.WriteString(v)
		case models.StopEvent:
			gotStop = true
		}
	}

	if gotPath != "/v1/responses" {
		t.Fatalf("path: got %q want %q", gotPath, "/v1/responses")
	}
	if gotText.String() != "hi" {
		t.Fatalf("text: got %q want %q", gotText.String(), "hi")
	}
	if !gotStop {
		t.Fatalf("expected StopEvent")
	}
}
