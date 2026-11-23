package openai

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// The reader may end the run on the last event, so the record must be out by then.
func TestResponseSummaryIsWrittenBeforeTheLastEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n")
	}))
	t.Cleanup(srv.Close)
	var out bytes.Buffer
	s := &responsesStreamer{apiKey: "k", url: srv.URL, model: "gpt-test", client: srv.Client()}
	ch, err := s.stream(procenv.Diagnostics(context.Background(), &out, diagnostic.LevelDebug), pub_models.Chat{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for evt := range ch {
		if _, last := evt.(models.StopEvent); last && !strings.Contains(out.String(), "response events=1 bytes=95 tokens_in=7 tokens_out=3 took=") {
			t.Errorf("the last event arrived before the response's record:\n%s", out.String())
		}
	}
}

func TestResponsesStreamContentIsTraceOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"REASON-MARKER\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"REPLY-MARKER\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\",\"encrypted_content\":\"SEALED-MARKER\"}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	run := func(level diagnostic.Level) string {
		var out bytes.Buffer
		s := &responsesStreamer{apiKey: "k", url: srv.URL, model: "gpt-test", client: srv.Client()}
		ch, err := s.stream(procenv.Diagnostics(context.Background(), &out, level),
			pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "PROMPT-MARKER"}}})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		for range ch {
		}
		return out.String()
	}
	debug := run(diagnostic.LevelDebug)
	if strings.Contains(debug, "MARKER") {
		t.Errorf("debug output carries conversation content:\n%s", debug)
	}
	for _, want := range []string{
		"openai responses request url=" + srv.URL + " model=gpt-test input_items=1 tools=0 bytes=",
		"debug [vendor.openai] response events=4 bytes=282 tokens_in=0 tokens_out=0 took=",
		" types=response.completed:1,response.output_item.done:1,response.output_text.delta:1,response.reasoning_summary_text.delta:1\n",
	} {
		if !strings.Contains(debug, want) {
			t.Errorf("debug output lacks %q:\n%s", want, debug)
		}
	}
	if lines := strings.Count(debug, "\n"); lines != 2 {
		t.Errorf("debug output is %d lines, want the request and one line for the response:\n%s", lines, debug)
	}
	trace := run(diagnostic.LevelTrace)
	for _, want := range []string{"REPLY-MARKER", "REASON-MARKER", "SEALED-MARKER", "PROMPT-MARKER", "openai responses request (tools redacted):"} {
		if !strings.Contains(trace, want) {
			t.Errorf("trace output lacks %q:\n%s", want, trace)
		}
	}
}
