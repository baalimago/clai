package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// roundTripFunc lets a test inject a failing RoundTrip without a live
// connection.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fixture reads a checked-in wire-shape reconstruction (worklog
// 2026-09-05-error-propagation, D14 — see testdata/README.md: these are
// reconstructions of the documented anthropic error envelope, not raw
// captures).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %v: %v", name, err)
	}
	return b
}

// claudeErr drives the real Claude completer against a server that answers
// via handler, returning the error from StreamCompletions. The custom URL
// keeps CountInputTokens on its heuristic path (no second request).
func claudeErr(t *testing.T, handler http.HandlerFunc) error {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := Default
	c.URL = srv.URL
	c.client = srv.Client()
	c.apiKey = "test-key"
	_, err := c.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	return err
}

// Test_Anthropic_DoFailure_ErrTransport pins the transport row on the
// anthropic boundary (worklog 2026-09-05-error-propagation, review 3,
// R3-02): a failed client.Do returns claierr.ErrTransport with the cause
// reachable, exactly as the generic boundary produces it.
func Test_Anthropic_DoFailure_ErrTransport(t *testing.T) {
	c := Default
	// A non-anthropic host keeps CountInputTokens on its heuristic path so
	// the injected failure below is the stream request's client.Do.
	c.URL = "https://example.invalid/v1/messages"
	c.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	c.apiKey = "test-key"

	_, err := c.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
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

// Test_Anthropic_CountTokensDoFailure_ErrTransport pins the second anthropic
// client.Do: CountInputTokens performs its own request on the anthropic.com
// host before the stream starts, and a connection failure there must surface
// typed too — a consumer matching ErrTransport cannot be left blind to it.
func Test_Anthropic_CountTokensDoFailure_ErrTransport(t *testing.T) {
	c := Default
	c.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	c.apiKey = "test-key"

	_, err := c.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
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
}

// Test_Anthropic_ReadFailure_ErrTransport pins the anthropic mid-stream read
// failure (worklog 2026-09-05-error-propagation, review 3, R3-02): a
// connection dropping mid-stream delivers ErrTransport on the channel with
// the cause reachable, exactly as the generic boundary produces it.
func Test_Anthropic_ReadFailure_ErrTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Promise more bytes than are sent, then return: the client's read
		// fails mid-stream with an unexpected EOF.
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	c := Default
	c.URL = srv.URL
	c.client = srv.Client()
	c.apiKey = "test-key"
	out, err := c.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var gotErr error
	for ev := range out {
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

// Test_AnthropicDecode_RateLimit_CarriesResetFacts pins the rewired 429
// branch: the header-parsing path constructs claierr.RateLimitedError
// directly, ResetAt/TokensRemaining/MaxInputTokens intact from the
// anthropic-ratelimit-* headers, with the response facts attached.
func Test_AnthropicDecode_RateLimit_CarriesResetFacts(t *testing.T) {
	resetAt := time.Now().Add(42 * time.Minute).UTC().Truncate(time.Second)
	body := fixture(t, "rate_limit_429.json")
	err := claudeErr(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("anthropic-ratelimit-tokens-reset", resetAt.Format(time.RFC3339))
		w.Header().Set("anthropic-ratelimit-input-tokens-limit", "80000")
		w.Header().Set("anthropic-ratelimit-tokens-remaining", "123")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write(body)
	})

	if !errors.Is(err, claierr.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got: %v", err)
	}
	var rl *claierr.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *claierr.RateLimitedError via errors.As, got: %v", err)
	}
	if !rl.ResetAt.Equal(resetAt) {
		t.Fatalf("ResetAt: got %v want %v", rl.ResetAt, resetAt)
	}
	if rl.MaxInputTokens != 80000 {
		t.Fatalf("MaxInputTokens: got %v want 80000", rl.MaxInputTokens)
	}
	if rl.TokensRemaining != 123 {
		t.Fatalf("TokensRemaining: got %v want 123", rl.TokensRemaining)
	}
	if rl.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("facts status: got %v", rl.StatusCode)
	}
	if !strings.Contains(rl.Body, "rate_limit_error") {
		t.Fatalf("facts body lost: %q", rl.Body)
	}
}

// Test_AnthropicDecode_NonOK_Baseline pins the end of the flattened error:
// every non-OK outside 429 rides generic.ResponseError, so the caller gets
// the baseline vocabulary meaning with facts instead of a formatted string.
// The anthropic decoder itself is nil until wire evidence exists (D9).
func Test_AnthropicDecode_NonOK_Baseline(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		fixture string
		want    error
	}{
		{"500 api_error is provider-unavailable", http.StatusInternalServerError, "api_error_500.json", claierr.ErrProviderUnavailable},
		{"404 not_found is model-not-found", http.StatusNotFound, "not_found_404.json", claierr.ErrModelNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fixture(t, tc.fixture)
			err := claudeErr(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write(body)
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got: %v", tc.want, err)
			}
			var apiErr claierr.APIErrorer
			if !errors.As(err, &apiErr) {
				t.Fatalf("flattened untyped error survived: %T %v", err, err)
			}
			api := apiErr.API()
			if api.StatusCode != tc.status || api.Body != string(body) {
				t.Fatalf("facts mismatch: %+v", api)
			}
		})
	}
}
