package generic

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestStreamContentIsTraceOnly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n", `{"choices":[{"delta":{"content":"REPLY-MARKER"}}]}`)
		fmt.Fprintf(w, "data: %s\n", `{"choices":[{"delta":{"tool_calls":[{"id":"c1","function":{"name":"ls","arguments":"{\"dir\":\"ARGS-MARKER\"}"}}]}}]}`)
		fmt.Fprintf(w, "data: %s\n", `{not json BROKEN-MARKER`)
		fmt.Fprint(w, "data: [DONE]\n")
	}))
	defer ts.Close()
	run := func(level diagnostic.Level) string {
		var out bytes.Buffer
		s := &StreamCompleter{client: ts.Client(), apiKey: "k", URL: ts.URL, Model: "m", subsystem: runenv.SubsystemVendorOpenAI}
		events, err := s.StreamCompletions(procenv.Diagnostics(context.Background(), &out, level),
			pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "PROMPT-MARKER"}}})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		for range events {
		}
		return out.String()
	}
	debug := run(diagnostic.LevelDebug)
	if strings.Contains(debug, "MARKER") {
		t.Errorf("debug output carries conversation content:\n%s", debug)
	}
	for _, want := range []string{"generic streamcompleter request url=" + ts.URL + " model=m messages=1 tools=0", "response events=", " tokens_in=0 tokens_out=0 took=", " types=", "tool call arguments bytes=", "failed to unmarshal token of "} {
		if !strings.Contains(debug, want) {
			t.Errorf("debug output lacks %q:\n%s", want, debug)
		}
	}
	trace := run(diagnostic.LevelTrace)
	for _, want := range []string{"REPLY-MARKER", "ARGS-MARKER", "BROKEN-MARKER", "PROMPT-MARKER", "generic streamcompleter request (tools redacted):"} {
		if !strings.Contains(trace, want) {
			t.Errorf("trace output lacks %q:\n%s", want, trace)
		}
	}
}
