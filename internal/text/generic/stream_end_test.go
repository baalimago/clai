package generic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// streamEvents feeds body to a completer as an HTTP 200 response and returns
// every event up to the close of the channel.
func streamEvents(t *testing.T, body string) []models.CompletionEvent {
	t.Helper()
	s := &StreamCompleter{}
	res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	out, err := s.handleStreamResponse(context.Background(), res)
	if err != nil {
		t.Fatalf("handleStreamResponse: %v", err)
	}
	var events []models.CompletionEvent
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				return events
			}
			if _, noop := ev.(models.NoopEvent); !noop && ev != nil {
				events = append(events, ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the stream did not end")
		}
	}
}

func lastError(t *testing.T, events []models.CompletionEvent) error {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("the stream produced no event, want a terminal error")
	}
	err, ok := events[len(events)-1].(error)
	if !ok {
		t.Fatalf("the last event is %T %v, want a terminal error", events[len(events)-1], events[len(events)-1])
	}
	return err
}

const (
	textChunk   = `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}` + "\n\n"
	finishChunk = `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	doneChunk   = "data: [DONE]\n\n"
)

// TestStreamThatIsNoCompletionStream pins that an HTTP 200 whose body is not
// a completion stream is an unexpected provider response carrying the status
// and a bounded excerpt of the body, never a silent empty success.
func TestStreamThatIsNoCompletionStream(t *testing.T) {
	long := strings.Repeat("x", 4*bodyExcerptLimit)
	for name, body := range map[string]string{
		"html page":            "<html><body>Captive portal login</body></html>",
		"empty body":           "",
		"non-stream json":      `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"non-stream reply"},"finish_reason":"stop"}]}`,
		"non-stream json line": `{"id":"x","choices":[{"index":0,"message":{"content":"hi"},"finish_reason":"stop"}]}` + "\n",
		"unparseable sse line": "data: {not json at all\n\n" + finishChunk + doneChunk,
		"long body":            long,
	} {
		t.Run(name, func(t *testing.T) {
			err := lastError(t, streamEvents(t, body))
			unexpected, ok := errors.AsType[*claierr.UnexpectedProviderResponseError](err)
			if !ok {
				t.Fatalf("got %v, want an unexpected provider response", err)
			}
			if unexpected.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200", unexpected.StatusCode)
			}
			if !strings.Contains(unexpected.Body, "the provider returned no completion") {
				t.Fatalf("the error %q does not say that no completion came", unexpected.Body)
			}
			if len(unexpected.Body) > bodyExcerptLimit+200 {
				t.Fatalf("the body excerpt is %d bytes, want it bounded near %d", len(unexpected.Body), bodyExcerptLimit)
			}
			if shown := strings.TrimPrefix(strings.TrimSpace(body), "data: "); body != "" && body != long && !strings.Contains(unexpected.Body, shown[:12]) {
				t.Fatalf("the excerpt %q does not show the body", unexpected.Body)
			}
		})
	}
}

// TestStreamEndsWithoutItsFinishMarker pins that a stream which ends before
// finish_reason or [DONE] is a truncated reply: what arrived is delivered,
// then a transport error matching io.ErrUnexpectedEOF.
func TestStreamEndsWithoutItsFinishMarker(t *testing.T) {
	events := streamEvents(t, textChunk)
	if len(events) != 2 || events[0] != "Hello" {
		t.Fatalf("events %v, want the text and then the truncation error", events)
	}
	err := lastError(t, events)
	if !errors.Is(err, claierr.ErrTransport) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v, want a transport error matching io.ErrUnexpectedEOF", err)
	}
	for name, body := range map[string]string{
		"finish reason": textChunk + finishChunk,
		"done":          textChunk + doneChunk,
	} {
		t.Run(name+" completes the stream", func(t *testing.T) {
			events := streamEvents(t, body)
			if len(events) == 0 || events[0] != "Hello" {
				t.Fatalf("events %v, want the text", events)
			}
			for _, event := range events {
				if err, failed := event.(error); failed {
					t.Fatalf("a finished stream ends in %v", err)
				}
			}
		})
	}
}

// TestToolCallWithArgumentsThatAreNoObject pins that a tool call whose
// arguments are not a JSON object is delivered as a call carrying the
// problem, so the model gets a failed tool result it can correct.
func TestToolCallWithArgumentsThatAreNoObject(t *testing.T) {
	call := func(arguments string) string {
		return `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"pwd","arguments":` + arguments + `}}]}}]}` + "\n\n"
	}
	finishTools := `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"
	for name, arguments := range map[string]string{
		"broken json": `"{not json"`,
		"empty":       `""`,
		"array":       `"[1,2]"`,
		"string":      `"\"str\""`,
	} {
		t.Run(name, func(t *testing.T) {
			events := streamEvents(t, call(arguments)+finishTools+doneChunk)
			if len(events) != 2 {
				t.Fatalf("events %v, want the call and a StopEvent", events)
			}
			got, ok := events[0].(pub_models.Call)
			if !ok || got.Name != "pwd" || got.ID != "call_1" {
				t.Fatalf("first event %T %v, want the pwd call", events[0], events[0])
			}
			if got.ArgumentsErr == "" || !strings.Contains(got.ArgumentsErr, "JSON object") {
				t.Fatalf("ArgumentsErr %q, want the problem named", got.ArgumentsErr)
			}
			if got.Inputs == nil || len(*got.Inputs) != 0 || got.Function.Arguments != "{}" {
				t.Fatalf("the call replays inputs %v arguments %q, want an empty object", got.Inputs, got.Function.Arguments)
			}
		})
	}
	t.Run("a following call keeps its own arguments", func(t *testing.T) {
		second := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"ls","arguments":"{\"directory\":\".\"}"}}]}}]}` + "\n\n"
		events := streamEvents(t, call(`"[1,2]"`)+second+finishTools+doneChunk)
		if len(events) != 3 {
			t.Fatalf("events %v, want two calls and a StopEvent", events)
		}
		first, _ := events[0].(pub_models.Call)
		next, _ := events[1].(pub_models.Call)
		if first.ArgumentsErr == "" || next.ArgumentsErr != "" || next.Name != "ls" || (*next.Inputs)["directory"] != "." {
			t.Fatalf("calls %+v %+v, want the first failed and the second intact", first, next)
		}
	})
}
