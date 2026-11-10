package openai

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
)

func responsesEvents(t *testing.T, body string) []models.CompletionEvent {
	t.Helper()
	out := make(chan models.CompletionEvent)
	s := &responsesStreamer{}
	go s.readResponsesStream(context.Background(), io.NopCloser(strings.NewReader(body)), out)
	var events []models.CompletionEvent
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				return events
			}
			if _, noop := ev.(models.NoopEvent); !noop {
				events = append(events, ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the stream did not end")
		}
	}
}

const responsesTextDelta = `data: {"type":"response.output_text.delta","delta":"Hello"}` + "\n\n"

// TestResponsesStreamEnd pins what the end of a Responses body means: the
// protocol guarantees a terminal event, so a body without any event is no
// completion, and a stream that ends before its terminal event is truncated.
func TestResponsesStreamEnd(t *testing.T) {
	for name, body := range map[string]string{
		"html page":       "<html><body>Captive portal login</body></html>",
		"empty body":      "",
		"non-stream json": `{"id":"x","object":"chat.completion"}`,
	} {
		t.Run(name+" is no completion", func(t *testing.T) {
			events := responsesEvents(t, body)
			if len(events) != 1 {
				t.Fatalf("events %v, want one terminal error", events)
			}
			unexpected, ok := errors.AsType[*claierr.UnexpectedProviderResponseError](events[0].(error))
			if !ok || unexpected.StatusCode != 200 || !strings.Contains(unexpected.Body, "the provider returned no completion") {
				t.Fatalf("got %v, want an unexpected provider response saying no completion came", events[0])
			}
		})
	}
	t.Run("a stream cut before its terminal event is truncated", func(t *testing.T) {
		events := responsesEvents(t, responsesTextDelta)
		if len(events) != 2 || events[0] != "Hello" {
			t.Fatalf("events %v, want the text and the truncation error", events)
		}
		if err := events[1].(error); !errors.Is(err, claierr.ErrTransport) || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("got %v, want a transport error matching io.ErrUnexpectedEOF", err)
		}
	})
	for _, terminal := range []string{
		`data: {"type":"response.completed","response":{"id":"r"}}`,
		`data: {"type":"response.incomplete","response":{"id":"r","incomplete_details":{"reason":"max_output_tokens"}}}`,
	} {
		t.Run("a terminal event ends the stream", func(t *testing.T) {
			events := responsesEvents(t, responsesTextDelta+terminal+"\n\n")
			if _, stopped := events[len(events)-1].(models.StopEvent); !stopped || len(events) != 2 {
				t.Fatalf("events %v, want the text and a StopEvent", events)
			}
		})
	}
	if got := (responsesStreamEvent{}).incompleteReason(); got != "not given" {
		t.Fatalf("reason %q for an event without details", got)
	}
}
