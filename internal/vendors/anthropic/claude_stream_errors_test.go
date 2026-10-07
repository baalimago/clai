package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// errorBody fails every Read, to drive the non-EOF stream read error branch.
type errorBody struct{}

func (errorBody) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }
func (errorBody) Close() error             { return nil }

func drainClaudeStream(t *testing.T, out chan models.CompletionEvent) []models.CompletionEvent {
	t.Helper()
	var events []models.CompletionEvent
	for evt := range out {
		events = append(events, evt)
	}
	return events
}

func TestClaudeStreamNonOKStatus(t *testing.T) {
	t.Run("plain error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server exploded"))
		}))
		t.Cleanup(srv.Close)

		c := &Claude{URL: srv.URL, client: srv.Client(), apiKey: "k"}
		if _, err := c.stream(context.Background(), mustClaudeRequest(t, srv.URL)); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})

	t.Run("rate limited with unparseable headers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("anthropic-ratelimit-tokens-reset", "not-a-timestamp")
			w.Header().Set("anthropic-ratelimit-input-tokens-limit", "not-a-number")
			w.Header().Set("anthropic-ratelimit-tokens-remaining", "not-a-number")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("slow down"))
		}))
		t.Cleanup(srv.Close)

		c := &Claude{URL: srv.URL, client: srv.Client(), apiKey: "k"}
		_, err := c.stream(context.Background(), mustClaudeRequest(t, srv.URL))
		if err == nil {
			t.Fatal("expected a rate-limit error even with unparseable headers")
		}
	})

	t.Run("rate limited with parseable headers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("anthropic-ratelimit-tokens-reset", "2026-01-02T03:04:05Z")
			w.Header().Set("anthropic-ratelimit-input-tokens-limit", "100")
			w.Header().Set("anthropic-ratelimit-tokens-remaining", "10")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		t.Cleanup(srv.Close)

		c := &Claude{URL: srv.URL, client: srv.Client(), apiKey: "k"}
		if _, err := c.stream(context.Background(), mustClaudeRequest(t, srv.URL)); err == nil {
			t.Fatal("expected a rate-limit error")
		}
	})
}

func mustClaudeRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

func TestClaudeHandleTokenErrors(t *testing.T) {
	c := &Claude{}
	tests := []struct {
		name  string
		token string
		rest  string
	}{
		{"missing event type", "event:", ""},
		{"wrong event prefix", "wrong: message_stop", ""},
		{"block start without body", "event: content_block_start", ""},
		{"block delta without body", "event: content_block_delta", ""},
		{"block stop without body", "event: content_block_stop", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := c.handleToken(bufio.NewReader(strings.NewReader(tc.rest)), tc.token)
			if _, ok := got.(error); !ok {
				t.Fatalf("handleToken(%q) = %#v, want an error", tc.token, got)
			}
		})
	}
}

func TestClaudeHandleTokenDebugAndNoop(t *testing.T) {
	c := &Claude{debug: true}
	got := c.handleToken(bufio.NewReader(strings.NewReader("\n")), "event: ping")
	if _, ok := got.(models.NoopEvent); !ok {
		t.Fatalf("handleToken(event: ping) = %#v, want NoopEvent", got)
	}
	if got := c.handleToken(bufio.NewReader(strings.NewReader("\n")), "event: message_stop"); !errors.Is(got.(error), io.EOF) {
		t.Fatalf("handleToken(event: message_stop) = %#v, want io.EOF", got)
	}
}

func TestClaudeStringFromDeltaTokenErrors(t *testing.T) {
	c := &Claude{}
	if _, err := c.stringFromDeltaToken("notdata value"); err == nil {
		t.Fatal("expected an error for a missing data: prefix")
	}
	if _, err := c.stringFromDeltaToken("data: {broken"); err == nil {
		t.Fatal("expected an error for undecodable delta JSON")
	}

	debug := &Claude{debug: true}
	delta, err := debug.stringFromDeltaToken(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`)
	if err != nil {
		t.Fatalf("stringFromDeltaToken: %v", err)
	}
	if delta.Text != "Hi" {
		t.Fatalf("delta text = %q, want Hi", delta.Text)
	}
}

func TestClaudeHandleFullResponse(t *testing.T) {
	c := &Claude{}
	ctx := context.Background()

	badOut := make(chan models.CompletionEvent, 1)
	c.handleFullResponse(ctx, "{not json", badOut)
	if _, ok := (<-badOut).(error); !ok {
		t.Fatal("expected an error event for undecodable full response")
	}

	out := make(chan models.CompletionEvent, 4)
	c.handleFullResponse(ctx, `{"content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"t","input":{"a":1}}]}`, out)
	close(out)

	var sawText, sawCall bool
	for evt := range out {
		switch cast := evt.(type) {
		case string:
			sawText = cast == "hi"
		case pub_models.Call:
			sawCall = cast.Name == "t"
		}
	}
	if !sawText || !sawCall {
		t.Fatalf("handleFullResponse emitted text=%v call=%v", sawText, sawCall)
	}
}

func TestClaudeConstructRequest(t *testing.T) {
	t.Run("bad url", func(t *testing.T) {
		c := &Claude{URL: "://bad"}
		if _, err := c.constructRequest(context.Background(), pub_models.Chat{}); err == nil {
			t.Fatal("expected an error for an unparseable URL")
		}
	})

	t.Run("debug branches and tools", func(t *testing.T) {
		t.Setenv("DEBUG", "1")
		c := &Claude{URL: "https://example.com", debug: true, tools: []pub_models.Specification{{Name: "t"}}, Temperature: 0.5}
		req, err := c.constructRequest(context.Background(), pub_models.Chat{
			Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("constructRequest: %v", err)
		}
		if req.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q", req.Header.Get("Content-Type"))
		}
	})
}

func TestClaudeCountInputTokensHTTP(t *testing.T) {
	const url = "https://anthropic.com/v1/messages"
	cases := []struct {
		name   string
		client *http.Client
		wantOK bool
	}{
		{"transport error", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		})}, false},
		{"non-200", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Status: "500", Body: io.NopCloser(strings.NewReader("nope")), Header: http.Header{}}, nil
		})}, false},
		{"decode error", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Status: "200", Body: io.NopCloser(strings.NewReader("not json")), Header: http.Header{}}, nil
		})}, false},
		{"ok", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Status: "200", Body: io.NopCloser(strings.NewReader(`{"input_tokens": 42}`)), Header: http.Header{}}, nil
		})}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Claude{Model: "m", URL: url, client: tc.client, PrintInputCount: true, AnthropicVersion: "2023-06-01"}
			count, err := c.CountInputTokens(context.Background(), pub_models.Chat{
				Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
			})
			if tc.wantOK && (err != nil || count != 42) {
				t.Fatalf("CountInputTokens = %d, %v; want 42, nil", count, err)
			}
			if !tc.wantOK && err == nil {
				t.Fatalf("expected an error from CountInputTokens")
			}
		})
	}
}

func TestClaudeStreamCompletionsRequestError(t *testing.T) {
	c := &Claude{URL: "://bad"}
	if _, err := c.StreamCompletions(context.Background(), pub_models.Chat{}); err == nil {
		t.Fatal("expected StreamCompletions to surface the construct-request error")
	}
}

func TestClaudeHandleStreamResponseEdgeEOF(t *testing.T) {
	t.Run("final JSON without newline", func(t *testing.T) {
		c := &Claude{}
		out, err := c.handleStreamResponse(context.Background(), &http.Response{
			Body: io.NopCloser(strings.NewReader(`{"content":[{"type":"text","text":"hi"}]}`)),
		})
		if err != nil {
			t.Fatalf("handleStreamResponse: %v", err)
		}
		events := drainClaudeStream(t, out)
		var sawText bool
		for _, evt := range events {
			if s, ok := evt.(string); ok && s == "hi" {
				sawText = true
			}
		}
		if !sawText {
			t.Fatalf("expected text event, got %#v", events)
		}
	})

	t.Run("clean EOF emits no second event", func(t *testing.T) {
		c := &Claude{}
		out, err := c.handleStreamResponse(context.Background(), &http.Response{
			Body: io.NopCloser(strings.NewReader("")),
		})
		if err != nil {
			t.Fatalf("handleStreamResponse: %v", err)
		}
		events := drainClaudeStream(t, out)
		if len(events) != 1 {
			t.Fatalf("expected exactly one EOF event, got %#v", events)
		}
		if !errors.Is(events[0].(error), io.EOF) {
			t.Fatalf("expected io.EOF, got %#v", events[0])
		}
	})

	t.Run("read error becomes a transport event", func(t *testing.T) {
		c := &Claude{}
		out, err := c.handleStreamResponse(context.Background(), &http.Response{Body: errorBody{}})
		if err != nil {
			t.Fatalf("handleStreamResponse: %v", err)
		}
		events := drainClaudeStream(t, out)
		if len(events) != 1 {
			t.Fatalf("expected one transport error event, got %#v", events)
		}
		if _, ok := events[0].(error); !ok {
			t.Fatalf("expected an error event, got %#v", events[0])
		}
	})

	t.Run("cancelled context stops the producer", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		t.Cleanup(srv.Close)

		c := &Claude{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out, err := c.handleStreamResponse(ctx, &http.Response{
			Body: io.NopCloser(strings.NewReader("event: ping\n")),
		})
		if err != nil {
			t.Fatalf("handleStreamResponse: %v", err)
		}
		if events := drainClaudeStream(t, out); len(events) != 0 {
			t.Fatalf("expected no events after cancellation, got %#v", events)
		}
	})
}

func TestClaudeStreamDebugEmitsFullMessage(t *testing.T) {
	c := &Claude{debug: true}
	out, err := c.handleStreamResponse(context.Background(), &http.Response{
		Body: io.NopCloser(strings.NewReader(
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n",
		)),
	})
	if err != nil {
		t.Fatalf("handleStreamResponse: %v", err)
	}
	events := drainClaudeStream(t, out)
	var sawText bool
	for _, evt := range events {
		if s, ok := evt.(string); ok && s == "Hi" {
			sawText = true
		}
	}
	if !sawText {
		t.Fatalf("expected debug stream to still emit text, got %#v", events)
	}
}
