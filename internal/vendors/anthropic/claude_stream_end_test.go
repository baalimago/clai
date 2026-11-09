package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
)

func claudeEvents(t *testing.T, body string) []models.CompletionEvent {
	t.Helper()
	c := &Claude{}
	out, err := c.handleStreamResponse(context.Background(), &http.Response{Body: io.NopCloser(strings.NewReader(body))})
	if err != nil {
		t.Fatalf("handleStreamResponse: %v", err)
	}
	var events []models.CompletionEvent
	for _, ev := range drainClaudeStream(t, out) {
		if _, noop := ev.(models.NoopEvent); !noop {
			events = append(events, ev)
		}
	}
	return events
}

const claudeTextDelta = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n"

// TestClaudeStreamErrorEvent pins that an error event inside a 200 stream is
// decoded like the body of a non-OK response: the status anthropic documents
// for the error type gives it its meaning, and the type is its provider code.
func TestClaudeStreamErrorEvent(t *testing.T) {
	for _, tc := range []struct {
		errType string
		want    error
		status  int
	}{
		{"overloaded_error", claierr.ErrProviderUnavailable, 529},
		{"api_error", claierr.ErrProviderUnavailable, 500},
		{"rate_limit_error", claierr.ErrRateLimited, 429},
		{"authentication_error", claierr.ErrAuthFailed, 401},
		{"some_new_error", claierr.ErrUnexpectedProviderResponse, 200},
	} {
		t.Run(tc.errType, func(t *testing.T) {
			events := claudeEvents(t, claudeTextDelta+"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\""+tc.errType+"\",\"message\":\"Overloaded\"}}\n\n"+claudeTextDelta)
			if len(events) != 2 || events[0] != "Hello" {
				t.Fatalf("events %v, want the text and then the terminal error", events)
			}
			err := events[1].(error)
			var facts claierr.APIErrorer
			if !errors.Is(err, tc.want) || !errors.As(err, &facts) || facts.API().StatusCode != tc.status || facts.API().ProviderCode != tc.errType {
				t.Fatalf("got %v, want %v with status %d and provider code %s", err, tc.want, tc.status, tc.errType)
			}
		})
	}
	t.Run("an undecodable error event is still terminal", func(t *testing.T) {
		events := claudeEvents(t, "event: error\ndata: not json\n\n")
		if len(events) != 1 || !errors.Is(events[0].(error), claierr.ErrUnexpectedProviderResponse) {
			t.Fatalf("events %v, want one unexpected provider response", events)
		}
	})
}

// TestClaudeStreamEnd pins what the end of the body means: message_stop
// finishes a reply, a stream cut before it is truncated, and a full response
// with nothing in it is no completion.
func TestClaudeStreamEnd(t *testing.T) {
	t.Run("message_stop finishes the reply", func(t *testing.T) {
		events := claudeEvents(t, claudeTextDelta+"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		if len(events) != 2 || events[0] != "Hello" || !errors.Is(events[1].(error), io.EOF) {
			t.Fatalf("events %v, want the text and io.EOF", events)
		}
	})
	t.Run("a stream cut before message_stop is truncated", func(t *testing.T) {
		events := claudeEvents(t, claudeTextDelta)
		if len(events) != 2 || events[0] != "Hello" {
			t.Fatalf("events %v, want the text and the truncation error", events)
		}
		if err := events[1].(error); !errors.Is(err, claierr.ErrTransport) || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("got %v, want a transport error matching io.ErrUnexpectedEOF", err)
		}
	})
	t.Run("a full response without content is no completion", func(t *testing.T) {
		events := claudeEvents(t, `{"id":"x","object":"chat.completion","choices":[]}`)
		if len(events) != 1 || !errors.Is(events[0].(error), claierr.ErrUnexpectedProviderResponse) {
			t.Fatalf("events %v, want one unexpected provider response", events)
		}
	})
	t.Run("a full response with text is the reply", func(t *testing.T) {
		events := claudeEvents(t, `{"content":[{"type":"text","text":"whole"}]}`)
		if len(events) != 1 || events[0] != "whole" {
			t.Fatalf("events %v, want the text", events)
		}
	})
}
