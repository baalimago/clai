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
	"strings"
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
	ErrMcpConnClosed              = errors.New("mcp connection closed")
	ErrMcpFrameUndecodable        = errors.New("mcp frame could not be parsed as JSON-RPC")
	ErrMcpAuthChallenge           = errors.New("mcp endpoint demands authorization")
	ErrMcpTransport               = errors.New("mcp transport failure")
	ErrMcpHttpStatus              = errors.New("mcp endpoint returned a failing status")
	ErrMcpUnsupportedContentType  = errors.New("mcp endpoint returned an unsupported content type")
	ErrMcpRPCError                = errors.New("mcp server returned a JSON-RPC error")
)

// APIError holds the facts about one provider response, shared by every
// meaning that response carries. It is a facts struct, not an error: it
// has no Error method, so facts flow only through a typed error or the
// APIErrorer interface.
type APIError struct {
	StatusCode   int    // what the provider actually said
	ProviderCode string // e.g. "insufficient_quota", "rate_limit_exceeded"
	Message      string // the provider's human-readable explanation, when a decoder extracted one
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
	switch {
	case a.Message != "":
		meaning = fmt.Sprintf("%v, message: %v", meaning, a.Message)
	case strings.TrimSpace(a.Body) != "":
		meaning = fmt.Sprintf("%v, body: %v", meaning, strings.TrimSpace(a.Body))
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
	return NewUnexpectedProviderResponseWithAPI(&APIError{
		StatusCode: statusCode,
		Body:       string(body),
	})
}

// NewUnexpectedProviderResponseWithAPI builds an UnexpectedProviderResponseError
// from decoded provider facts. A nil api is replaced by a zero-valued APIError.
func NewUnexpectedProviderResponseWithAPI(api *APIError) *UnexpectedProviderResponseError {
	return &UnexpectedProviderResponseError{APIError: orZero(api)}
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

// McpConnClosedError means a call was attempted on an MCP connection that is
// closed, or was closed while the call was still pending.
type McpConnClosedError struct {
	ServerName string
}

// NewMcpConnClosed builds a McpConnClosedError for the named server.
func NewMcpConnClosed(serverName string) *McpConnClosedError {
	return &McpConnClosedError{ServerName: serverName}
}

func (e *McpConnClosedError) Unwrap() error { return ErrMcpConnClosed }

func (e *McpConnClosedError) Error() string {
	return fmt.Sprintf("mcp connection to %q is closed", e.ServerName)
}

// McpFrameUndecodableError means a line received from an MCP server could
// not be routed to a waiting call: either it cannot be parsed as a
// JSON-RPC object (invalid JSON, or valid JSON without a jsonrpc member), or
// it exceeded the per-message read bound before it could be read in full.
// Every call pending on the connection fails with this error, since the
// frame carries no usable id.
type McpFrameUndecodableError struct {
	ServerName string
	Cause      error
}

// NewMcpFrameUndecodable builds a McpFrameUndecodableError for the named
// server, wrapping the cause.
func NewMcpFrameUndecodable(serverName string, cause error) *McpFrameUndecodableError {
	return &McpFrameUndecodableError{ServerName: serverName, Cause: cause}
}

// Unwrap returns both the sentinel and the cause, so errors.Is matches
// ErrMcpFrameUndecodable and the underlying reason alike.
func (e *McpFrameUndecodableError) Unwrap() []error { return []error{ErrMcpFrameUndecodable, e.Cause} }

func (e *McpFrameUndecodableError) Error() string {
	return fmt.Sprintf("mcp server %q sent a frame that could not be parsed as JSON-RPC: %v", e.ServerName, e.Cause)
}

// AuthChallengeError reports that an endpoint demands authorization.
// Challenge is the verbatim WWW-Authenticate header value; ResourceMetadata
// is the URL parsed out of its resource_metadata parameter, empty when
// absent. The transport phase returns it; the authorization phase consumes
// it and parses nothing further (worklog 2026-10-02-mcp-connection-cost,
// README shared interfaces).
type AuthChallengeError struct {
	ServerName       string
	Challenge        string
	ResourceMetadata string
}

// NewAuthChallenge builds an AuthChallengeError for the named server.
func NewAuthChallenge(serverName, challenge, resourceMetadata string) *AuthChallengeError {
	return &AuthChallengeError{ServerName: serverName, Challenge: challenge, ResourceMetadata: resourceMetadata}
}

func (e *AuthChallengeError) Unwrap() error { return ErrMcpAuthChallenge }

func (e *AuthChallengeError) Error() string {
	return fmt.Sprintf("mcp endpoint %q demands authorization: %v", e.ServerName, e.Challenge)
}

// BlockedOutsideRun reports that resolving the connection cannot proceed
// until a human acts, outside the run itself. mcp.Connector's single-flight
// resolution reads this (via its own outsideRunBlocker interface) to avoid
// memoising the challenge as a terminal failure, so a later call retries
// instead of replaying a cached error (worklog 2026-10-02-mcp-connection-cost,
// phase 6, D20).
func (e *AuthChallengeError) BlockedOutsideRun() bool { return true }

// McpTransportError means a request to an MCP endpoint never got an answer:
// a failed dial, TLS handshake or mid-request network failure. It carries
// no APIError — there is no response to carry facts of.
type McpTransportError struct {
	ServerName string
	Endpoint   string
	Cause      error
}

// NewMcpTransport builds a McpTransportError naming the server and the
// endpoint it could not reach, wrapping the underlying cause.
func NewMcpTransport(serverName, endpoint string, cause error) *McpTransportError {
	return &McpTransportError{ServerName: serverName, Endpoint: endpoint, Cause: cause}
}

func (e *McpTransportError) Unwrap() []error { return []error{ErrMcpTransport, e.Cause} }

func (e *McpTransportError) Error() string {
	return fmt.Sprintf("mcp server %q: could not reach endpoint %q: %v", e.ServerName, e.Endpoint, e.Cause)
}

// McpHttpStatusError means an MCP endpoint answered with a status this
// transport treats as a failure: anything other than a success or an
// accepted-with-no-body outcome.
type McpHttpStatusError struct {
	ServerName string
	StatusCode int
	Message    string
}

// NewMcpHttpStatus builds a McpHttpStatusError carrying the server's status
// and its response message.
func NewMcpHttpStatus(serverName string, statusCode int, message string) *McpHttpStatusError {
	return &McpHttpStatusError{ServerName: serverName, StatusCode: statusCode, Message: message}
}

func (e *McpHttpStatusError) Unwrap() error { return ErrMcpHttpStatus }

func (e *McpHttpStatusError) Error() string {
	return fmt.Sprintf("mcp server %q: status %d: %v", e.ServerName, e.StatusCode, e.Message)
}

// McpUnsupportedContentTypeError means an MCP endpoint's response carried a
// content type that is neither JSON nor an event stream.
type McpUnsupportedContentTypeError struct {
	ServerName  string
	ContentType string
}

// NewMcpUnsupportedContentType builds a McpUnsupportedContentTypeError
// naming the server and the content type it could not handle.
func NewMcpUnsupportedContentType(serverName, contentType string) *McpUnsupportedContentTypeError {
	return &McpUnsupportedContentTypeError{ServerName: serverName, ContentType: contentType}
}

func (e *McpUnsupportedContentTypeError) Unwrap() error { return ErrMcpUnsupportedContentType }

func (e *McpUnsupportedContentTypeError) Error() string {
	return fmt.Sprintf("mcp server %q: unsupported response content type %q", e.ServerName, e.ContentType)
}

// McpRPCError reports a JSON-RPC-level error an MCP server returned for a
// call, carrying its code and message so a caller (the endpoint schema
// cache's unknown-tool signal) can classify it without parsing the rendered
// string. Both the stdio and the streamable-HTTP transport build this one
// type for one meaning (worklog 2026-10-02-mcp-connection-cost, R2-16):
// previously only the HTTP transport did, so the signal could never fire
// for a stdio server.
type McpRPCError struct {
	ServerName string
	Code       int
	Message    string
}

// NewMcpRPCError builds a McpRPCError for the named server, carrying the
// JSON-RPC error's code and message.
func NewMcpRPCError(serverName string, code int, message string) *McpRPCError {
	return &McpRPCError{ServerName: serverName, Code: code, Message: message}
}

func (e *McpRPCError) Unwrap() error { return ErrMcpRPCError }

func (e *McpRPCError) Error() string {
	return fmt.Sprintf("mcp server %q: JSON-RPC error %d: %s", e.ServerName, e.Code, e.Message)
}
