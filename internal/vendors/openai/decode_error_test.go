package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// fixture reads a checked-in wire-shape reconstruction (worklog
// 2026-09-05-error-propagation, D14 — see testdata/README.md for
// provenance; these are reconstructions from provider docs, not raw
// captures).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %v: %v", name, err)
	}
	return b
}

// compactJSON flattens a pretty-printed fixture onto one line so it can ride
// a single SSE data: line.
func compactJSON(t *testing.T, in []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, in); err != nil {
		t.Fatalf("compact fixture: %v", err)
	}
	return buf.Bytes()
}

// chatCompletionsErr drives the real ChatGPT completer down the legacy
// chat-completions path against a server answering status/body, returning
// the error from StreamCompletions.
func chatCompletionsErr(t *testing.T, status int, body []byte) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("OPENAI_API_KEY", "key")
	g := &ChatGPT{Model: "gpt-4.1-mini", URL: srv.URL + "/v1/chat/completions"}
	if err := g.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if g.useResponses {
		t.Fatalf("expected legacy chat-completions path for URL %q", g.URL)
	}
	_, err := g.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatalf("expected error for status %v, got nil", status)
	}
	return err
}

// responsesErr drives the real responsesStreamer against a server answering
// status/body, returning the error from stream.
func responsesErr(t *testing.T, status int, body []byte) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{apiKey: "k", url: srv.URL + "/v1/responses", model: "gpt-test", client: srv.Client()}
	_, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatalf("expected error for status %v, got nil", status)
	}
	return err
}

// Test_OpenAIResponses_DoFailure_ErrTransport pins the transport row on the
// openai Responses boundary (worklog 2026-09-05-error-propagation, review 3,
// R3-02): a failed client.Do returns claierr.ErrTransport with the cause
// reachable, exactly as the generic boundary produces it.
func Test_OpenAIResponses_DoFailure_ErrTransport(t *testing.T) {
	t.Parallel()

	s := &responsesStreamer{
		apiKey: "k",
		url:    "https://example.invalid/v1/responses",
		model:  "gpt-test",
		client: newTestClient(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		})),
	}

	_, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, claierr.ErrTransport) {
		t.Fatalf("expected ErrTransport, got: %v", err)
	}
	var te *claierr.TransportError
	if !errors.As(err, &te) || te.Cause == nil {
		t.Fatalf("expected TransportError with a reachable cause, got: %v", err)
	}
	if !strings.Contains(te.Cause.Error(), "connection refused") {
		t.Fatalf("cause lost: %v", te.Cause)
	}
}

// Test_OpenAIResponses_ReadFailure_ErrTransport pins the openai Responses
// mid-stream read failure (worklog 2026-09-05-error-propagation, review 3,
// R3-02): a connection dropping mid-stream delivers ErrTransport on the
// channel with the cause reachable, exactly as the generic boundary
// produces it.
func Test_OpenAIResponses_ReadFailure_ErrTransport(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Promise more bytes than are sent, then return: the client's read
		// fails mid-stream with an unexpected EOF.
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{apiKey: "k", url: srv.URL + "/v1/responses", model: "gpt-test", client: srv.Client()}
	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var gotErr error
	for ev := range ch {
		if asErr, isErr := ev.(error); isErr {
			gotErr = asErr
		}
	}
	if gotErr == nil {
		t.Fatal("expected a trailing transport error, got a silent normal end")
	}
	if !errors.Is(gotErr, claierr.ErrTransport) {
		t.Fatalf("expected ErrTransport, got: %v", gotErr)
	}
	var te *claierr.TransportError
	if !errors.As(gotErr, &te) || te.Cause == nil {
		t.Fatalf("expected TransportError with a reachable cause, got: %v", gotErr)
	}
	if !errors.Is(gotErr, io.ErrUnexpectedEOF) {
		t.Fatalf("expected the underlying cause reachable via errors.Is, got: %v", gotErr)
	}
}

// Test_OpenAIDecode_InsufficientQuota_JoinsBothMeanings pins the D11
// obligation on openai's decoder: a 429 whose body says insufficient_quota
// means BOTH rate-limited and likely-out-of-credits, stated by the decoder
// itself with one shared facts allocation — the baseline backfills neither.
func Test_OpenAIDecode_InsufficientQuota_JoinsBothMeanings(t *testing.T) {
	body := fixture(t, "insufficient_quota_429.json")
	err := chatCompletionsErr(t, http.StatusTooManyRequests, body)

	if !errors.Is(err, claierr.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got: %v", err)
	}
	if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
		t.Fatalf("expected ErrLikelyInsufficientCredits, got: %v", err)
	}
	if errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("catch-all joined onto the decoder's answer: %v", err)
	}

	var rl *claierr.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *claierr.RateLimitedError, got: %v", err)
	}
	var ic *claierr.InsufficientCreditsError
	if !errors.As(err, &ic) {
		t.Fatalf("expected *claierr.InsufficientCreditsError, got: %v", err)
	}
	if rl.APIError != ic.APIError {
		t.Fatalf("joined meanings must share ONE facts allocation: %p vs %p", rl.APIError, ic.APIError)
	}
	if rl.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("facts status: got %v", rl.StatusCode)
	}
	if rl.ProviderCode != "insufficient_quota" {
		t.Fatalf("facts provider code: got %q", rl.ProviderCode)
	}
	if !strings.Contains(rl.Body, "You exceeded your current quota") {
		t.Fatalf("facts body lost: %q", rl.Body)
	}
}

// Test_OpenAIDecode_UnrecognizedPayloads_Nil pins the decoder's silence: a
// payload it does not recognize returns nil so the baseline stands alone.
func Test_OpenAIDecode_UnrecognizedPayloads_Nil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"plain 429, no quota code", http.StatusTooManyRequests, `{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`},
		{"not json", http.StatusTooManyRequests, "boom"},
		{"empty body", http.StatusServiceUnavailable, ""},
		{"numeric code", http.StatusPaymentRequired, `{"error":{"message":"x","code":402}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := decodeError(tc.status, []byte(tc.body)); err != nil {
				t.Fatalf("expected nil for unrecognized payload, got: %v", err)
			}
		})
	}
}

// Test_OpenAIResponses_NonOK_DecodesVocabulary pins the Responses non-OK
// boundary onto the shared decode chain: the formatted string of
// responses_stream.go:229 (README "Motivation") is gone, and the same openai
// decoder speaks first.
func Test_OpenAIResponses_NonOK_DecodesVocabulary(t *testing.T) {
	t.Run("429 insufficient_quota joins both meanings", func(t *testing.T) {
		err := responsesErr(t, http.StatusTooManyRequests, fixture(t, "insufficient_quota_429.json"))
		if !errors.Is(err, claierr.ErrRateLimited) || !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
			t.Fatalf("expected both meanings, got: %v", err)
		}
	})
	t.Run("5xx falls to the baseline, typed", func(t *testing.T) {
		err := responsesErr(t, http.StatusInternalServerError, []byte(`{"error":{"message":"server melted","type":"server_error"}}`))
		if !errors.Is(err, claierr.ErrProviderUnavailable) {
			t.Fatalf("expected ErrProviderUnavailable, got: %v", err)
		}
		var apiErr claierr.APIErrorer
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected typed vocabulary error with facts, got: %T %v", err, err)
		}
		if apiErr.API().StatusCode != http.StatusInternalServerError || !strings.Contains(apiErr.API().Body, "server melted") {
			t.Fatalf("facts mismatch: %+v", apiErr.API())
		}
	})
}

// Test_OpenAIResponses_ErrorEvent_TypedOnChannel pins the D9-audited typed
// error events (`error`, `response.failed` — event names verified against
// current provider docs, citations in the phase's Implementation notes): a
// mid-stream failure event surfaces on the channel as a vocabulary error,
// never a bare formatted string.
func Test_OpenAIResponses_ErrorEvent_TypedOnChannel(t *testing.T) {
	streamEvents := func(t *testing.T, payloads ...[]byte) error {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, p := range payloads {
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(p)
				_, _ = w.Write([]byte("\n\n"))
			}
		}))
		t.Cleanup(srv.Close)

		s := &responsesStreamer{apiKey: "k", url: srv.URL + "/v1/responses", model: "gpt-test", client: srv.Client()}
		ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		var gotErr error
		for ev := range ch {
			switch v := ev.(type) {
			case error:
				gotErr = v
			case models.StopEvent:
				t.Fatal("a failed stream must not also emit a StopEvent")
			}
		}
		if gotErr == nil {
			t.Fatal("expected an error event on the channel")
		}
		return gotErr
	}

	t.Run("error event with insufficient_quota decodes to the vocabulary", func(t *testing.T) {
		// The fixture must be sent on one SSE line; strip the pretty-printing.
		err := streamEvents(t, compactJSON(t, fixture(t, "responses_error_event_insufficient_quota.json")))
		if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
			t.Fatalf("expected ErrLikelyInsufficientCredits, got: %v", err)
		}
		// A frame arrives at HTTP 200: no throttling evidence, so the quota
		// signal must not smuggle in a rate-limit meaning.
		if errors.Is(err, claierr.ErrRateLimited) {
			t.Fatalf("unexpected rate-limit meaning on a frame: %v", err)
		}
		var apiErr claierr.APIErrorer
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected facts, got: %T %v", err, err)
		}
		if apiErr.API().StatusCode != http.StatusOK {
			t.Fatalf("frame facts must carry status %v, got: %+v", http.StatusOK, apiErr.API())
		}
	})

	t.Run("response.failed with unrecognized code degrades typed", func(t *testing.T) {
		err := streamEvents(t, compactJSON(t, fixture(t, "responses_failed_event_server_error.json")))
		if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
			t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
		}
		var apiErr claierr.APIErrorer
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected facts, got: %T %v", err, err)
		}
		if apiErr.API().StatusCode != http.StatusOK || !strings.Contains(apiErr.API().Body, "server_error") {
			t.Fatalf("frame facts mismatch: %+v", apiErr.API())
		}
	})
}
