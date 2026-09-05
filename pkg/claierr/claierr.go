// Package claierr is clai's shared error vocabulary: one sentinel and one
// type per terminal meaning. The vocabulary is closed — vendors decode
// their wire formats into these values, they never define their own.
//
// Each meaning ships as a pair: a sentinel for the cheap yes/no
// (errors.Is(err, claierr.ErrRateLimited)) and a type for the facts
// (errors.As(err, &rl) for rl.ResetAt). The type's Unwrap returns the
// sentinel, the stdlib pattern of fs.ErrNotExist alongside *fs.PathError.
//
// A response that carries several meanings is composed with errors.Join,
// never nesting: a 429 insufficient_quota is both rate-limited and
// likely-out-of-credits, but a bare 402 is only the latter. The joined
// meanings share one *APIError allocation.
//
// Consumer trap: a joined error cannot be type-switched — switch err.(type)
// sees errors.Join's own concrete type and falls to default. Discriminate
// with errors.Is or an errors.As ladder.
//
// Build values through the constructors, never struct literals: the
// embedded *APIError must never be nil, or reading a promoted field
// panics.
package claierr

import (
	"errors"
	"fmt"
	"time"
)

// The sentinels: one per meaning, for errors.Is.
var (
	ErrAuthFailed                 = errors.New("authentication failed")
	ErrLikelyInsufficientCredits  = errors.New("likely insufficient credits")
	ErrModelNotFound              = errors.New("model not found")
	ErrRateLimited                = errors.New("rate limited")
	ErrProviderUnavailable        = errors.New("provider unavailable")
	ErrTransport                  = errors.New("transport failure")
	ErrUnexpectedProviderResponse = errors.New("unexpected provider response")
	ErrContextLengthExceeded      = errors.New("context length exceeded")
	ErrContentFiltered            = errors.New("content filtered")
	ErrMcpServerStartup           = errors.New("mcp server startup failed")
)

// APIError holds the facts about one provider response, shared by every
// meaning that response carries. It is a facts struct, not an error: it
// has no Error method, so facts flow only through a typed error or the
// APIErrorer interface.
type APIError struct {
	StatusCode   int    // what the provider actually said
	ProviderCode string // e.g. "insufficient_quota", "rate_limit_exceeded"
	Body         string
}

// API returns the facts. Defined on *APIError so every embedding type
// satisfies APIErrorer by promotion.
func (a *APIError) API() *APIError { return a }

// describe renders the meaning plus whichever facts are present. Nil-safe
// so Error never panics, even on a literal-built value.
func (a *APIError) describe(meaning string) string {
	if a == nil {
		return meaning
	}
	if a.StatusCode != 0 {
		meaning = fmt.Sprintf("%v, status: %v", meaning, a.StatusCode)
	}
	if a.ProviderCode != "" {
		meaning = fmt.Sprintf("%v, provider code: %v", meaning, a.ProviderCode)
	}
	return meaning
}

// orZero enforces the never-nil invariant: a constructor handed a nil
// facts pointer substitutes a zero-valued APIError rather than storing
// nil.
func orZero(api *APIError) *APIError {
	if api == nil {
		return &APIError{}
	}
	return api
}

// APIErrorer reads the response facts without knowing the meaning.
// errors.As accepts an interface target, so a logging path can ask for
// this and stop there.
type APIErrorer interface{ API() *APIError }

// AuthFailedError means the provider rejected the credentials.
type AuthFailedError struct{ *APIError }

// NewAuthFailed builds an AuthFailedError. A nil api is replaced by a
// zero-valued APIError.
func NewAuthFailed(api *APIError) *AuthFailedError {
	return &AuthFailedError{APIError: orZero(api)}
}

func (e *AuthFailedError) Unwrap() error { return ErrAuthFailed }

func (e *AuthFailedError) Error() string { return e.describe("authentication failed") }

// InsufficientCreditsError means the response suggests the account is out
// of credits. "Likely" because some providers signal it ambiguously.
type InsufficientCreditsError struct{ *APIError }

// NewInsufficientCredits builds an InsufficientCreditsError. A nil api is
// replaced by a zero-valued APIError.
func NewInsufficientCredits(api *APIError) *InsufficientCreditsError {
	return &InsufficientCreditsError{APIError: orZero(api)}
}

func (e *InsufficientCreditsError) Unwrap() error { return ErrLikelyInsufficientCredits }

func (e *InsufficientCreditsError) Error() string { return e.describe("likely insufficient credits") }

// ModelNotFoundError means the provider does not recognize the requested
// model or route.
type ModelNotFoundError struct{ *APIError }

// NewModelNotFound builds a ModelNotFoundError. A nil api is replaced by a
// zero-valued APIError.
func NewModelNotFound(api *APIError) *ModelNotFoundError {
	return &ModelNotFoundError{APIError: orZero(api)}
}

func (e *ModelNotFoundError) Unwrap() error { return ErrModelNotFound }

func (e *ModelNotFoundError) Error() string { return e.describe("model not found") }

// RateLimitedError means the provider is throttling. ResetAt,
// TokensRemaining and MaxInputTokens are absorbed from the former
// internal/models rate-limit error (D15), fields intact; they are zero when
// the provider did not say.
type RateLimitedError struct {
	*APIError
	ResetAt         time.Time
	TokensRemaining int
	MaxInputTokens  int
}

// NewRateLimited builds a RateLimitedError. A nil api is replaced by a
// zero-valued APIError.
func NewRateLimited(api *APIError, resetAt time.Time, tokensRemaining, maxInputTokens int) *RateLimitedError {
	return &RateLimitedError{
		APIError:        orZero(api),
		ResetAt:         resetAt,
		TokensRemaining: tokensRemaining,
		MaxInputTokens:  maxInputTokens,
	}
}

func (e *RateLimitedError) Unwrap() error { return ErrRateLimited }

func (e *RateLimitedError) Error() string {
	msg := e.describe("rate limited")
	if !e.ResetAt.IsZero() {
		msg = fmt.Sprintf("%v, reset at: %v", msg, e.ResetAt)
	}
	return msg
}

// ProviderUnavailableError means a provider-side failure, retryable by the
// caller.
type ProviderUnavailableError struct{ *APIError }

// NewProviderUnavailable builds a ProviderUnavailableError. A nil api is
// replaced by a zero-valued APIError.
func NewProviderUnavailable(api *APIError) *ProviderUnavailableError {
	return &ProviderUnavailableError{APIError: orZero(api)}
}

func (e *ProviderUnavailableError) Unwrap() error { return ErrProviderUnavailable }

func (e *ProviderUnavailableError) Error() string { return e.describe("provider unavailable") }

// TransportError means the request never got a provider answer: a failed
// client.Do or a mid-stream read failure. It carries no APIError — there
// is no response to carry facts of — and wraps the underlying net/url
// cause.
type TransportError struct {
	Cause error
}

// NewTransport builds a TransportError wrapping the underlying cause.
func NewTransport(cause error) *TransportError {
	return &TransportError{Cause: cause}
}

// Unwrap returns both the sentinel and the cause, so errors.Is matches
// ErrTransport and the underlying error alike.
func (e *TransportError) Unwrap() []error { return []error{ErrTransport, e.Cause} }

func (e *TransportError) Error() string {
	return fmt.Sprintf("transport failure: %v", e.Cause)
}

// UnexpectedProviderResponseError is the catch-all when neither the vendor
// decoder nor the status baseline found a meaning. StatusCode is 200 when
// the payload arrived as a mid-stream frame.
type UnexpectedProviderResponseError struct{ *APIError }

// NewUnexpectedProviderResponse builds an UnexpectedProviderResponseError
// carrying the raw status and body.
func NewUnexpectedProviderResponse(statusCode int, body []byte) *UnexpectedProviderResponseError {
	return &UnexpectedProviderResponseError{APIError: &APIError{
		StatusCode: statusCode,
		Body:       string(body),
	}}
}

func (e *UnexpectedProviderResponseError) Unwrap() error { return ErrUnexpectedProviderResponse }

func (e *UnexpectedProviderResponseError) Error() string {
	return e.describe("unexpected provider response")
}

// ContextLengthExceededError means the request exceeded the model's
// context window.
type ContextLengthExceededError struct{ *APIError }

// NewContextLengthExceeded builds a ContextLengthExceededError. A nil api
// is replaced by a zero-valued APIError.
func NewContextLengthExceeded(api *APIError) *ContextLengthExceededError {
	return &ContextLengthExceededError{APIError: orZero(api)}
}

func (e *ContextLengthExceededError) Unwrap() error { return ErrContextLengthExceeded }

func (e *ContextLengthExceededError) Error() string { return e.describe("context length exceeded") }

// ContentFilteredError means the provider refused the content by policy.
type ContentFilteredError struct{ *APIError }

// NewContentFiltered builds a ContentFilteredError. A nil api is replaced
// by a zero-valued APIError.
func NewContentFiltered(api *APIError) *ContentFilteredError {
	return &ContentFilteredError{APIError: orZero(api)}
}

func (e *ContentFilteredError) Unwrap() error { return ErrContentFiltered }

func (e *ContentFilteredError) Error() string { return e.describe("content filtered") }

// McpServerStartupError means an explicitly requested MCP server failed to
// start. It carries no APIError — no provider response is involved — and
// wraps the startup cause.
type McpServerStartupError struct {
	ServerName string
	Stage      string
	Cause      error
}

// NewMcpServerStartup builds a McpServerStartupError for the named server,
// failing at the given startup stage, wrapping the cause.
func NewMcpServerStartup(serverName, stage string, cause error) *McpServerStartupError {
	return &McpServerStartupError{
		ServerName: serverName,
		Stage:      stage,
		Cause:      cause,
	}
}

// Unwrap returns both the sentinel and the cause, so errors.Is matches
// ErrMcpServerStartup and the underlying error alike.
func (e *McpServerStartupError) Unwrap() []error { return []error{ErrMcpServerStartup, e.Cause} }

func (e *McpServerStartupError) Error() string {
	return fmt.Sprintf("mcp server '%v' failed to start at stage '%v': %v", e.ServerName, e.Stage, e.Cause)
}
