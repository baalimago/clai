package claierr

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestModelUnavailable_EveryMeaning pins the one table the retry mechanism
// reads: which meanings mean "this model cannot answer" and which mean "the
// request or the network is the problem".
func TestModelUnavailable_EveryMeaning(t *testing.T) {
	api := &APIError{StatusCode: 429, Body: "slow down"}
	testCases := []struct {
		desc string
		err  error
		want bool
	}{
		{"model not found", NewModelNotFound(api), true},
		{"auth failed", NewAuthFailed(api), true},
		{"insufficient credits", NewInsufficientCredits(api), true},
		{"rate limited", NewRateLimited(api, time.Time{}, 0, 0), true},
		{"provider unavailable", NewProviderUnavailable(api), true},

		{"transport", NewTransport(errors.New("dial")), false},
		{"unexpected response", NewUnexpectedProviderResponse(200, []byte("{")), false},
		{"context length exceeded", NewContextLengthExceeded(api), false},
		{"content filtered", NewContentFiltered(api), false},
		{"mcp startup", NewMcpServerStartup("srv", "connect", errors.New("boom")), false},
		{"image unsupported", &ImageUnsupportedError{Vendor: "v", Model: "m", Reason: "route accepts no images"}, false},

		{"nil", nil, false},
		{"unknown", errors.New("something else"), false},
		{"wrapped unavailable", fmt.Errorf("query: %w", NewRateLimited(api, time.Time{}, 0, 0)), true},
		{"wrapped unexpected", fmt.Errorf("query: %w", NewUnexpectedProviderResponse(500, nil)), false},
		{"joined rate limit and quota", errors.Join(NewRateLimited(api, time.Time{}, 0, 0), NewInsufficientCredits(api)), true},
		{"joined content filtered and auth", errors.Join(NewContentFiltered(api), NewAuthFailed(api)), true},
		{"joined unavailable and transport", errors.Join(NewTransport(errors.New("dial")), NewProviderUnavailable(api)), true},
		{"joined only final meanings", errors.Join(NewTransport(errors.New("dial")), NewContentFiltered(api)), false},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			if got := ModelUnavailable(tc.err); got != tc.want {
				t.Fatalf("ModelUnavailable(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

// TestUnavailableMeaning_NamesTheReason pins the one phrase a notice prints: the
// meaning that made the model unavailable, never the provider prose.
func TestUnavailableMeaning_NamesTheReason(t *testing.T) {
	api := &APIError{StatusCode: 429, Body: "slow down"}
	testCases := []struct {
		desc string
		err  error
		want error
	}{
		{"model not found", NewModelNotFound(api), ErrModelNotFound},
		{"auth failed", NewAuthFailed(api), ErrAuthFailed},
		{"insufficient credits", NewInsufficientCredits(api), ErrLikelyInsufficientCredits},
		{"rate limited", NewRateLimited(api, time.Time{}, 0, 0), ErrRateLimited},
		{"provider unavailable", NewProviderUnavailable(api), ErrProviderUnavailable},
		{"wrapped", fmt.Errorf("query: %w", NewAuthFailed(api)), ErrAuthFailed},
		{"joined credits outranks the rate limit", errors.Join(NewRateLimited(api, time.Time{}, 0, 0), NewInsufficientCredits(api)), ErrLikelyInsufficientCredits},

		{"transport", NewTransport(errors.New("dial")), nil},
		{"unexpected response", NewUnexpectedProviderResponse(200, []byte("{")), nil},
		{"nil", nil, nil},
		{"unknown", errors.New("something else"), nil},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got := UnavailableMeaning(tc.err)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("UnavailableMeaning(%v) = %v, want nil", tc.err, got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("UnavailableMeaning(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
