package anthropic

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func sseEvent(event, data string) string {
	return "event: " + event + "\ndata: " + data + "\n\n"
}

const usageMessageStart = `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":20,"cache_creation_input_tokens":5,"cache_read_input_tokens":300,"output_tokens":1}}}`

// streamUntilEnd serves events and returns every event received up to and
// including the first Call or io.EOF, together with the usage the model
// reported at that moment, which is when the session runner reads it.
func streamUntilEnd(t *testing.T, events []string) ([]models.CompletionEvent, *pub_models.Usage) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			_, _ = io.WriteString(w, e)
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c := Claude{URL: srv.URL}
	t.Setenv("ANTHROPIC_API_KEY", "k")
	if err := c.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	out, err := c.StreamCompletions(ctx, pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	var got []models.CompletionEvent
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout; got %v", got)
		case ev, ok := <-out:
			if !ok {
				t.Fatalf("channel closed before a Call or io.EOF; got %v", got)
			}
			got = append(got, ev)
			if _, isCall := ev.(pub_models.Call); isCall {
				return got, c.TokenUsage()
			}
			if err, isErr := ev.(error); isErr {
				if errors.Is(err, io.EOF) {
					return got, c.TokenUsage()
				}
				t.Fatalf("stream error: %v", err)
			}
		}
	}
}

func TestStreamCompletions_reportsStreamedUsage(t *testing.T) {
	_, usage := streamUntilEnd(t, []string{
		sseEvent("message_start", usageMessageStart),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	})
	want := pub_models.Usage{PromptTokens: 325, CompletionTokens: 42, TotalTokens: 367}
	want.PromptTokensDetails.CachedTokens = 300
	if usage == nil || *usage != want {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
}

// TestStreamCompletions_toolCallCarriesItsOutputUsage pins that a tool step
// reads the message_delta usage, which Anthropic sends after the tool block.
func TestStreamCompletions_toolCallCarriesItsOutputUsage(t *testing.T) {
	got, usage := streamUntilEnd(t, []string{
		sseEvent("message_start", usageMessageStart),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"cat","input":{}}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file\":\"a\"}"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":17}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	})
	call, ok := got[len(got)-1].(pub_models.Call)
	if !ok || call.Name != "cat" {
		t.Fatalf("last event = %#v, want the cat call", got[len(got)-1])
	}
	if usage == nil || usage.CompletionTokens != 17 || usage.PromptTokens != 325 {
		t.Fatalf("usage at the call = %+v, want 325 in and 17 out", usage)
	}
}

func TestStreamCompletions_toolCallWithoutUsageStillArrives(t *testing.T) {
	got, _ := streamUntilEnd(t, []string{
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"cat","input":{}}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	})
	if call, ok := got[len(got)-1].(pub_models.Call); !ok || call.Name != "cat" {
		t.Fatalf("events = %#v, want the cat call last", got)
	}
}

func TestHandleToken_malformedUsageIsAnError(t *testing.T) {
	for _, event := range []string{"message_start", "message_delta"} {
		c := &Claude{}
		ev := c.handleToken(bufio.NewReader(strings.NewReader("data: {not json\n")), "event: "+event)
		if err, ok := ev.(error); !ok || !strings.Contains(err.Error(), event) {
			t.Fatalf("%s: event = %#v, want an error naming it", event, ev)
		}
	}
}
