package generic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// sseServer serves the given lines as one streamed response, flushing each,
// then closes the connection.
func sseServer(t *testing.T, status int, lines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		fl, _ := w.(http.Flusher)
		for _, l := range lines {
			fmt.Fprintf(w, "%s\n", l)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
}

// streamFrom runs StreamCompletions against the server and fails the test on
// a setup error.
func streamFrom(t *testing.T, s *StreamCompleter, ts *httptest.Server) chan models.CompletionEvent {
	t.Helper()
	s.client = ts.Client()
	s.URL = ts.URL
	s.apiKey = "test-key"
	out, err := s.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamCompletions setup err: %v", err)
	}
	return out
}

// drainEvents reads the channel until it closes, failing the test on a stall.
func drainEvents(t *testing.T, ch chan models.CompletionEvent) []models.CompletionEvent {
	t.Helper()
	var evs []models.CompletionEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, open := <-ch:
			if !open {
				return evs
			}
			evs = append(evs, ev)
		case <-timeout:
			t.Fatal("timeout draining events")
		}
	}
}

// factsOf extracts the shared APIError facts, failing if the error is not
// typed.
func factsOf(t *testing.T, err error) *claierr.APIError {
	t.Helper()
	var apiErr claierr.APIErrorer
	if !errors.As(err, &apiErr) {
		t.Fatalf("error carries no APIError facts: %T %v", err, err)
	}
	return apiErr.API()
}

// nonOK runs StreamCompletions against a plain server answering the given
// status and body, returning the error.
func nonOK(t *testing.T, s *StreamCompleter, status int, body string) error {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	s.client = ts.Client()
	s.URL = ts.URL
	s.apiKey = "test-key"
	ch, err := s.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatalf("expected error for status %v, got nil (ch=%v)", status, ch)
	}
	return err
}

// Test_Generic_NonOK_NeverUntyped pins the catch-all: a non-OK response
// never yields nil or a bare formatted string, unmapped statuses included.
func Test_Generic_NonOK_NeverUntyped(t *testing.T) {
	t.Run("unmapped status through the real boundary", func(t *testing.T) {
		s := &StreamCompleter{}
		err := nonOK(t, s, http.StatusTeapot, "short and stout")
		if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
			t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
		}
		api := factsOf(t, err)
		if api.StatusCode != http.StatusTeapot || api.Body != "short and stout" {
			t.Fatalf("facts mismatch: %+v", api)
		}
	})
	t.Run("every non-OK status is typed, decoder silent or nil", func(t *testing.T) {
		silent := func(int, []byte) error { return nil }
		for status := 300; status < 600; status++ {
			for _, decode := range []func(int, []byte) error{nil, silent} {
				err := ResponseError(status, []byte("b"), decode)
				if err == nil {
					t.Fatalf("nil error for status %v", status)
				}
				var apiErr claierr.APIErrorer
				if !errors.As(err, &apiErr) {
					t.Fatalf("untyped error for status %v: %T %v", status, err, err)
				}
			}
		}
	})
}

// Test_Generic_BaselineTable pins the README baseline table row by row,
// through the real StreamCompletions boundary: what the status alone
// suggests, facts populated, no body parsing.
func Test_Generic_BaselineTable(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, claierr.ErrAuthFailed},
		{http.StatusForbidden, claierr.ErrAuthFailed},
		{http.StatusPaymentRequired, claierr.ErrLikelyInsufficientCredits},
		{http.StatusNotFound, claierr.ErrModelNotFound},
		{http.StatusTooManyRequests, claierr.ErrRateLimited},
		{http.StatusInternalServerError, claierr.ErrProviderUnavailable},
		{http.StatusServiceUnavailable, claierr.ErrProviderUnavailable},
	}
	sentinels := []error{
		claierr.ErrAuthFailed,
		claierr.ErrLikelyInsufficientCredits,
		claierr.ErrModelNotFound,
		claierr.ErrRateLimited,
		claierr.ErrProviderUnavailable,
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%v", tt.status), func(t *testing.T) {
			s := &StreamCompleter{}
			body := fmt.Sprintf("body-%v", tt.status)
			err := nonOK(t, s, tt.status, body)
			if !errors.Is(err, tt.want) {
				t.Fatalf("status %v: expected %v, got: %v", tt.status, tt.want, err)
			}
			// The baseline maps exactly one meaning per status.
			for _, other := range sentinels {
				if other != tt.want && errors.Is(err, other) {
					t.Fatalf("status %v: unexpected extra meaning %v in %v", tt.status, other, err)
				}
			}
			api := factsOf(t, err)
			if api.StatusCode != tt.status || api.Body != body {
				t.Fatalf("status %v: facts mismatch: %+v", tt.status, api)
			}
		})
	}
}

// Test_Generic_VendorDecoderOverridesBaseline pins D11: a recognizing
// vendor decoder's answer replaces the baseline entirely. This is the xAI
// shape — a 403 that means drained credits, not bad credentials.
func Test_Generic_VendorDecoderOverridesBaseline(t *testing.T) {
	var gotStatus int
	var gotBody []byte
	s := &StreamCompleter{}
	s.DecodeError = func(status int, body []byte) error {
		gotStatus = status
		gotBody = body
		return claierr.NewInsufficientCredits(&claierr.APIError{StatusCode: status, Body: string(body)})
	}
	err := nonOK(t, s, http.StatusForbidden, "used all available credits")
	if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
		t.Fatalf("expected ErrLikelyInsufficientCredits, got: %v", err)
	}
	if errors.Is(err, claierr.ErrAuthFailed) {
		t.Fatalf("baseline auth meaning survived alongside the decoder's answer: %v", err)
	}
	if errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("catch-all joined onto the decoder's answer: %v", err)
	}
	if gotStatus != http.StatusForbidden || string(gotBody) != "used all available credits" {
		t.Fatalf("decoder saw status %v body %q", gotStatus, gotBody)
	}
}

// Test_Generic_ErrorFrameAtOK_TypedOnChannel pins the frame decode point
// with a recognizing decoder: the decoder receives http.StatusOK and the
// whole raw frame, and its error rides the channel unchanged.
func Test_Generic_ErrorFrameAtOK_TypedOnChannel(t *testing.T) {
	frame := `{"error":{"message":"quota exhausted"}}`
	var gotStatus int
	var gotBody []byte
	decoded := claierr.NewInsufficientCredits(&claierr.APIError{StatusCode: http.StatusOK, Body: frame})
	s := &StreamCompleter{}
	s.DecodeError = func(status int, body []byte) error {
		gotStatus = status
		gotBody = append([]byte(nil), body...)
		return decoded
	}
	ts := sseServer(t, http.StatusOK, "data: "+frame)
	defer ts.Close()

	evs := drainEvents(t, streamFrom(t, s, ts))
	if len(evs) != 1 {
		t.Fatalf("expected exactly the error event, got %d events: %v", len(evs), evs)
	}
	err, ok := evs[0].(error)
	if !ok {
		t.Fatalf("expected an error event, got: %T %v", evs[0], evs[0])
	}
	if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
		t.Fatalf("expected the decoder's error, got: %v", err)
	}
	if err != error(decoded) {
		t.Fatalf("decoder's answer altered on the way to the channel: %v", err)
	}
	if errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("catch-all joined onto the decoder's answer: %v", err)
	}
	if gotStatus != http.StatusOK {
		t.Fatalf("decoder saw status %v, want %v", gotStatus, http.StatusOK)
	}
	if string(gotBody) != frame {
		t.Fatalf("decoder saw body %q, want the whole raw frame %q", gotBody, frame)
	}
}

// Test_Generic_ErrorFrame_SilentDecoder_Unexpected pins the silent-path
// fallback: an error frame at HTTP 200 with a nil or silent decoder still
// terminates as a typed ErrUnexpectedProviderResponse, never a NoopEvent,
// never a normal empty end.
func Test_Generic_ErrorFrame_SilentDecoder_Unexpected(t *testing.T) {
	frame := `{"error":{"message":"quota exhausted"}}`
	tests := []struct {
		name   string
		decode func(int, []byte) error
	}{
		{"nil hook", nil},
		{"silent decoder", func(int, []byte) error { return nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &StreamCompleter{}
			s.DecodeError = tt.decode
			ts := sseServer(t, http.StatusOK, "data: "+frame)
			defer ts.Close()

			evs := drainEvents(t, streamFrom(t, s, ts))
			if len(evs) != 1 {
				t.Fatalf("expected exactly the error event, got %d events: %v", len(evs), evs)
			}
			if _, isNoop := evs[0].(models.NoopEvent); isNoop {
				t.Fatal("error frame degraded to NoopEvent — the silent path is open")
			}
			err, ok := evs[0].(error)
			if !ok {
				t.Fatalf("expected an error event, got: %T %v", evs[0], evs[0])
			}
			if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
				t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
			}
			api := factsOf(t, err)
			if api.StatusCode != http.StatusOK {
				t.Fatalf("expected frame facts at status %v, got: %+v", http.StatusOK, api)
			}
			if !strings.Contains(api.Body, "quota exhausted") {
				t.Fatalf("expected frame body in facts, got: %+v", api)
			}
		})
	}
}

// Test_Generic_ErrorFrameThenDONE_ProducerStops is the R3-01 regression
// guard: a provider that streams an error frame and then the usual
// data: [DONE] frame, without closing the connection, must not strand the
// producer. The session runner returns on the error event and never reads
// the channel again, so the producer must stop after the send exactly as it
// does for StopEvent — otherwise it blocks forever on the [DONE] frame's
// StopEvent send and the deferred res.Body.Close() never runs.
func Test_Generic_ErrorFrameThenDONE_ProducerStops(t *testing.T) {
	frame := `{"error":{"message":"quota exhausted"}}`
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n", frame)
		if fl != nil {
			fl.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n")
		if fl != nil {
			fl.Flush()
		}
		// Keep the connection open: the leak only shows when the producer
		// must stop on its own rather than on the server closing first.
		<-release
	}))
	defer ts.Close()
	defer close(release)

	s := &StreamCompleter{}
	out := streamFrom(t, s, ts)

	// Consumer mirrors the session runner: it reads the error event and
	// returns without reading the channel again.
	select {
	case ev, ok := <-out:
		if !ok {
			t.Fatal("channel closed before the error event arrived")
		}
		err, isErr := ev.(error)
		if !isErr {
			t.Fatalf("expected an error event, got: %T %v", ev, ev)
		}
		if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
			t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the error event")
	}

	// The producer must terminate after the terminal error send and close
	// the channel. Pre-fix it blocks on the [DONE] frame's StopEvent send
	// and the channel stays open.
	select {
	case ev, ok := <-out:
		if ok {
			t.Fatalf("expected channel close after the error event, got event: %T", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after the error event: producer goroutine leaked")
	}
}

// Test_Generic_MalformedFrame_Noop pins keep-alive tolerance: a malformed
// frame is a NoopEvent regardless of debug flags, and a stream carrying one
// continues delivering valid frames.
func Test_Generic_MalformedFrame_Noop(t *testing.T) {
	t.Run("unconditional Noop, debug flag unset", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_CHAT", "")
		s := &StreamCompleter{}
		ev := s.handleStreamChunk([]byte("data: not-json{{\n"))
		if _, ok := ev.(models.NoopEvent); !ok {
			t.Fatalf("expected NoopEvent, got: %T %v", ev, ev)
		}
	})
	t.Run("unconditional Noop, debug flag set", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_CHAT", "1")
		s := &StreamCompleter{}
		ev := s.handleStreamChunk([]byte("data: not-json{{\n"))
		if _, ok := ev.(models.NoopEvent); !ok {
			t.Fatalf("expected NoopEvent, got: %T %v", ev, ev)
		}
	})
	t.Run("stream continues past malformed lines", func(t *testing.T) {
		ts := sseServer(t, http.StatusOK,
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			"data: not-json{{",
			`data: {"error":null,"choices":[{"delta":{"content":"World"}}]}`,
		)
		defer ts.Close()
		s := &StreamCompleter{}
		evs := drainEvents(t, streamFrom(t, s, ts))
		var strEvents []string
		for _, ev := range evs {
			if err, isErr := ev.(error); isErr {
				t.Fatalf("unexpected error event: %v", err)
			}
			if str, isStr := ev.(string); isStr {
				strEvents = append(strEvents, str)
			}
		}
		if len(strEvents) != 2 || strEvents[0] != "Hello" || strEvents[1] != "World" {
			t.Fatalf("expected valid frames delivered around the malformed one, got: %v", strEvents)
		}
	})
}

// Test_Generic_ReadFailure_ErrTransport pins the transport row: a
// connection dropping mid-stream delivers ErrTransport on the channel with
// the cause reachable.
func Test_Generic_ReadFailure_ErrTransport(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Promise more bytes than are sent, then return: the client's read
		// fails mid-stream with an unexpected EOF.
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n"))
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	defer ts.Close()

	s := &StreamCompleter{}
	evs := drainEvents(t, streamFrom(t, s, ts))
	if len(evs) == 0 {
		t.Fatal("expected events before the drop, got none — a silent normal end")
	}
	err, ok := evs[len(evs)-1].(error)
	if !ok {
		t.Fatalf("expected a trailing error event, got: %T %v — a silent normal end", evs[len(evs)-1], evs[len(evs)-1])
	}
	if !errors.Is(err, claierr.ErrTransport) {
		t.Fatalf("expected ErrTransport, got: %v", err)
	}
	var te *claierr.TransportError
	if !errors.As(err, &te) || te.Cause == nil {
		t.Fatalf("expected TransportError with a reachable cause, got: %v", err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected the underlying cause reachable via errors.Is, got: %v", err)
	}
}
