package claierr_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
)

// vocabularyRows returns one constructor-built error per row of the
// worklog README's vocabulary table, paired with its sentinel.
func vocabularyRows() []struct {
	name     string
	err      error
	sentinel error
} {
	api := &claierr.APIError{StatusCode: 402, ProviderCode: "fixture_code", Body: "fixture body"}
	return []struct {
		name     string
		err      error
		sentinel error
	}{
		{"auth_failed", claierr.NewAuthFailed(api), claierr.ErrAuthFailed},
		{"likely_insufficient_credits", claierr.NewInsufficientCredits(api), claierr.ErrLikelyInsufficientCredits},
		{"model_not_found", claierr.NewModelNotFound(api), claierr.ErrModelNotFound},
		{"rate_limited", claierr.NewRateLimited(api, time.Now(), 10, 100), claierr.ErrRateLimited},
		{"provider_unavailable", claierr.NewProviderUnavailable(api), claierr.ErrProviderUnavailable},
		{"transport", claierr.NewTransport(errors.New("connection refused")), claierr.ErrTransport},
		{"unexpected_provider_response", claierr.NewUnexpectedProviderResponse(418, []byte("teapot")), claierr.ErrUnexpectedProviderResponse},
		{"context_length_exceeded", claierr.NewContextLengthExceeded(api), claierr.ErrContextLengthExceeded},
		{"content_filtered", claierr.NewContentFiltered(api), claierr.ErrContentFiltered},
		{"mcp_server_startup", claierr.NewMcpServerStartup("fixture-server", "handshake", errors.New("boom")), claierr.ErrMcpServerStartup},
	}
}

func Test_Claierr_EveryTypeUnwrapsToItsSentinel(t *testing.T) {
	for _, row := range vocabularyRows() {
		t.Run(row.name, func(t *testing.T) {
			if !errors.Is(row.err, row.sentinel) {
				t.Fatalf("expected constructed %v error to match its sentinel", row.name)
			}
			if row.err.Error() == "" {
				t.Fatalf("expected a non-empty Error() string for %v", row.name)
			}
		})
	}
}

func Test_Claierr_MeaningsDoNotCrossMatch(t *testing.T) {
	rows := vocabularyRows()
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			for _, other := range rows {
				if other.sentinel == row.sentinel {
					continue
				}
				if errors.Is(row.err, other.sentinel) {
					t.Fatalf("%v error must not match the %v sentinel", row.name, other.name)
				}
			}
		})
	}
}

func Test_Claierr_ConstructorsNeverNilFacts(t *testing.T) {
	rows := []struct {
		name string
		err  claierr.APIErrorer
	}{
		{"auth_failed", claierr.NewAuthFailed(nil)},
		{"likely_insufficient_credits", claierr.NewInsufficientCredits(nil)},
		{"model_not_found", claierr.NewModelNotFound(nil)},
		{"rate_limited", claierr.NewRateLimited(nil, time.Time{}, 0, 0)},
		{"provider_unavailable", claierr.NewProviderUnavailable(nil)},
		{"unexpected_provider_response", claierr.NewUnexpectedProviderResponse(0, nil)},
		{"context_length_exceeded", claierr.NewContextLengthExceeded(nil)},
		{"content_filtered", claierr.NewContentFiltered(nil)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if row.err.API() == nil {
				t.Fatalf("expected non-nil facts on nil-constructed %v", row.name)
			}
			// Reading a promoted field must not panic on a
			// constructor-built value.
			if got := row.err.API().StatusCode; got != 0 {
				t.Fatalf("expected zero-valued facts, got status %v", got)
			}
			if asErr, ok := row.err.(error); !ok {
				t.Fatalf("%v does not implement error", row.name)
			} else if asErr.Error() == "" {
				t.Fatalf("expected non-empty Error() on nil-facts %v", row.name)
			}
		})
	}
}

func Test_Claierr_SurvivesJoinAndDoubleWrap(t *testing.T) {
	api := &claierr.APIError{StatusCode: 429, ProviderCode: "insufficient_quota", Body: "fixture body"}
	reset := time.Now().Add(time.Minute).Round(time.Second)
	joined := errors.Join(
		claierr.NewRateLimited(api, reset, 10, 100),
		claierr.NewInsufficientCredits(api),
	)
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", joined))

	if !errors.Is(wrapped, claierr.ErrRateLimited) {
		t.Fatal("expected ErrRateLimited to match through join plus double wrap")
	}
	if !errors.Is(wrapped, claierr.ErrLikelyInsufficientCredits) {
		t.Fatal("expected ErrLikelyInsufficientCredits to match through join plus double wrap")
	}
	if errors.Is(wrapped, claierr.ErrAuthFailed) {
		t.Fatal("a non-carried meaning must not match")
	}

	var rl *claierr.RateLimitedError
	if !errors.As(wrapped, &rl) {
		t.Fatal("expected errors.As to find *RateLimitedError through join plus double wrap")
	}
	if !rl.ResetAt.Equal(reset) {
		t.Fatalf("expected ResetAt %v, got %v", reset, rl.ResetAt)
	}
	var ic *claierr.InsufficientCreditsError
	if !errors.As(wrapped, &ic) {
		t.Fatal("expected errors.As to find *InsufficientCreditsError through join plus double wrap")
	}
	if rl.API() != ic.API() {
		t.Fatal("expected joined meanings to share one *APIError allocation")
	}
}

func Test_Claierr_APIErrorerExposesFacts(t *testing.T) {
	api := &claierr.APIError{StatusCode: 503, ProviderCode: "fixture_code", Body: "fixture body"}
	factsCarrying := []struct {
		name string
		err  error
	}{
		{"auth_failed", claierr.NewAuthFailed(api)},
		{"likely_insufficient_credits", claierr.NewInsufficientCredits(api)},
		{"model_not_found", claierr.NewModelNotFound(api)},
		{"rate_limited", claierr.NewRateLimited(api, time.Now(), 10, 100)},
		{"provider_unavailable", claierr.NewProviderUnavailable(api)},
		{"context_length_exceeded", claierr.NewContextLengthExceeded(api)},
		{"content_filtered", claierr.NewContentFiltered(api)},
	}
	for _, row := range factsCarrying {
		t.Run(row.name, func(t *testing.T) {
			var facts claierr.APIErrorer
			wrapped := fmt.Errorf("context: %w", row.err)
			if !errors.As(wrapped, &facts) {
				t.Fatalf("expected %v to expose facts via APIErrorer", row.name)
			}
			if facts.API() != api {
				t.Fatalf("expected the constructor-given facts pointer back, got %#v", facts.API())
			}
		})
	}
	t.Run("unexpected_provider_response", func(t *testing.T) {
		var facts claierr.APIErrorer
		err := claierr.NewUnexpectedProviderResponse(418, []byte("teapot"))
		if !errors.As(fmt.Errorf("context: %w", err), &facts) {
			t.Fatal("expected UnexpectedProviderResponseError to expose facts via APIErrorer")
		}
		if facts.API().StatusCode != 418 || facts.API().Body != "teapot" {
			t.Fatalf("unexpected facts: %#v", facts.API())
		}
	})
	noFacts := []struct {
		name string
		err  error
	}{
		{"transport", claierr.NewTransport(errors.New("connection refused"))},
		{"mcp_server_startup", claierr.NewMcpServerStartup("fixture-server", "handshake", errors.New("boom"))},
	}
	for _, row := range noFacts {
		t.Run(row.name, func(t *testing.T) {
			var facts claierr.APIErrorer
			if errors.As(row.err, &facts) {
				t.Fatalf("%v carries no APIError and must not satisfy APIErrorer", row.name)
			}
		})
	}
}

func Test_Claierr_JoinedErrorTypeSwitchFallsThrough(t *testing.T) {
	api := &claierr.APIError{StatusCode: 429}
	joined := errors.Join(
		claierr.NewRateLimited(api, time.Now(), 10, 100),
		claierr.NewInsufficientCredits(api),
	)
	switch joined.(type) {
	case *claierr.RateLimitedError, *claierr.InsufficientCreditsError:
		t.Fatal("a joined error must not satisfy a concrete type switch — this documents the consumer trap")
	default:
		// The documented behavior: discrimination is errors.Is/errors.As.
	}
	if !errors.Is(joined, claierr.ErrRateLimited) || !errors.Is(joined, claierr.ErrLikelyInsufficientCredits) {
		t.Fatal("errors.Is must remain the working discrimination on a joined error")
	}
}

func Test_Claierr_ErrorIsNilSafeOnLiteralBuiltValue(t *testing.T) {
	// Literal-built values violate the constructors-only rule, but a
	// logging path calling Error() on one must degrade to the bare
	// meaning rather than panic.
	if got := (&claierr.AuthFailedError{}).Error(); got == "" {
		t.Fatal("expected a non-empty Error() on a literal-built value")
	}
}

func Test_Claierr_TransportAndMcpUnwrapSentinelAndCause(t *testing.T) {
	t.Run("transport", func(t *testing.T) {
		cause := errors.New("connection refused")
		err := claierr.NewTransport(cause)
		if !errors.Is(err, claierr.ErrTransport) {
			t.Fatal("expected TransportError to match ErrTransport")
		}
		if !errors.Is(err, cause) {
			t.Fatal("expected TransportError to unwrap to its cause")
		}
	})
	t.Run("mcp_server_startup", func(t *testing.T) {
		cause := errors.New("boom")
		err := claierr.NewMcpServerStartup("fixture-server", "handshake", cause)
		if !errors.Is(err, claierr.ErrMcpServerStartup) {
			t.Fatal("expected McpServerStartupError to match ErrMcpServerStartup")
		}
		if !errors.Is(err, cause) {
			t.Fatal("expected McpServerStartupError to unwrap to its cause")
		}
		var mcpErr *claierr.McpServerStartupError
		if !errors.As(err, &mcpErr) {
			t.Fatal("expected errors.As to find *McpServerStartupError")
		}
		if mcpErr.ServerName != "fixture-server" || mcpErr.Stage != "handshake" {
			t.Fatalf("unexpected fields: %#v", mcpErr)
		}
	})
}
