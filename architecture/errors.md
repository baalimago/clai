# Typed errors across the module boundary

This document explains how clai returns **typed, specific errors for every
terminal error state** to its library consumers (`pkg/agent`), instead of
formatted strings that a consumer must match on. It is the companion to
[`streaming.md`](./streaming.md): that note describes how vendor streams are
normalized into one event stream; this one describes how the stream's
terminal states and every other provider failure are decoded into one shared
vocabulary before they cross the module boundary.

The contract is the package surface. A consumer writes
`errors.Is(err, claierr.ErrRateLimited)` or
`errors.As(err, &rl)` once and it matches every vendor that means the same
thing. The CLI is deliberately out of scope: upstream `cmd.Run` prints
`err.Error()` and collapses every failure to exit 1 before clai can map it
(worklog `2026-09-05-error-propagation`, D17). Wording improvements a CLI
user sees are a byproduct of the vocabulary's `Error()` strings, not a
contract.

The motivating consumer is sakfråga's LLM spend breaker: it must distinguish
"the account is empty" from "the API is pushing back". It used to match
substrings such as `"402"` in formatted messages — a coupling that breaks on
any upstream wording change and that cannot see a provider error frame
received at HTTP 200 at all.

## Files to read

| File | Purpose |
| --- | --- |
| `pkg/claierr/claierr.go` | The shared vocabulary: sentinels, types, constructors, `APIError` facts |
| `internal/text/generic/response_error.go` | The decode chain: vendor first, baseline fallback, catch-all (`ResponseError`) |
| `internal/text/generic/stream_completer_models.go` | The `DecodeError` hook field and the `chatCompletionChunk.Error` frame field |
| `internal/text/generic/stream_completer.go` | The two generic decode points (non-OK response, mid-stream frame) |
| `internal/vendors/<vendor>/decode_error.go` | Per-vendor decoders; each `testdata/` carries the journal-recorded wire fixture |
| `internal/models/models.go` | `CompletionEvent` and the channel contract |
| `internal/text/session_runner.go` | The runner's terminal handling of channel errors |
| `internal/text/querier_setup_tools.go`, `internal/text/conf.go` | The MCP `Setup` split: strict (explicit) versus warn-and-degrade (ambient) |
| `pkg/agent/agent.go` | The public surface: `WithMcpServers`, `Setup` |

## One vocabulary, many decoders

The load-bearing split runs opposite to the file layout.

**The error vocabulary is shared and closed. Decoding a vendor's wire format
into it is per-vendor and open.** A vendor never defines an error type; it
only answers "which shared errors does this response mean?" OpenAI saying
`insufficient_quota` and DeepSeek saying `402 Insufficient Balance` are
different sentences with the same meaning, and a consumer must be able to
write one `errors.Is(err, claierr.ErrLikelyInsufficientCredits)` that
catches both. If a vendor could mint its own type, that consumer would need
to know every vendor — the coupling this design exists to delete.

Each vendor registers one function on the generic completer it embeds
(`internal/vendors/openai/gpt.go:141`, `deepseek`, `xai`, `openrouter`):

```go
// Decodes a provider error payload into shared claierr values: the body of
// a non-OK response, or an error frame received mid-stream (status is then
// http.StatusOK). A nil field, or a nil return, means "no vendor knowledge"
// and the baseline (or the catch-all) stands alone.
DecodeError func(status int, body []byte) error
```

A plain func field, not an interface: it is one function, the struct
already carries its configuration this way, and there is no second
implementation to abstract over. A vendor whose response means two things
returns `errors.Join(a, b)` itself, so set semantics live in the value
rather than leaking into the signature.

Three properties follow:

- Adding a vendor adds one function — no change to the vocabulary, no change
  to any consumer.
- A vendor with no decoder still works: the status baseline covers it, so an
  unmapped vendor degrades to status-code accuracy rather than to a
  formatted string.
- The generic layer (`internal/text/generic/`) cannot contain vendor logic
  because it only holds a function field (`AGENTS.md`).

Adding an _error to the vocabulary_ is the deliberately harder change: it
touches `pkg/claierr` and every consumer that wants to match it. That
asymmetry is correct — vendors are expected to multiply, meanings are not.

## The vocabulary — `pkg/claierr`

### The shape: a pair per meaning

Each meaning ships as a pair:

- a **sentinel** for the cheap yes/no question, matched with `errors.Is`;
- a **type** for the facts, matched with `errors.As`.

The type's `Unwrap` returns the sentinel, which is what makes both APIs work
at once. This is the stdlib pattern of `fs.ErrNotExist` alongside
`*fs.PathError`.

Facts about one response live in one struct, `APIError`, embedded by
pointer, so a response that carries several meanings still allocates its
facts once:

```go
type APIError struct {
    StatusCode   int    // what the provider actually said
    ProviderCode string // e.g. "insufficient_quota", "rate_limit_exceeded"
    Body         string
}

// APIErrorer reads the facts without knowing the meaning. errors.As
// accepts an interface target, so a logging path can ask for this and stop.
type APIErrorer interface{ API() *APIError }
```

### The names

`go doc ./pkg/claierr` is the register; this table restates it so the doc
cannot silently drift. Every sentinel and type ships with a constructor;
values are built through constructors, never struct literals — the embedded
`*APIError` must never be nil, or reading a promoted field panics (the
constructors substitute a zero-valued `APIError` for nil input).

| Sentinel | Type | Constructor | Facts beyond `*APIError` |
| --- | --- | --- | --- |
| `ErrAuthFailed` | `AuthFailedError` | `NewAuthFailed(api)` | — |
| `ErrLikelyInsufficientCredits` | `InsufficientCreditsError` | `NewInsufficientCredits(api)` | — |
| `ErrModelNotFound` | `ModelNotFoundError` | `NewModelNotFound(api)` | — |
| `ErrRateLimited` | `RateLimitedError` | `NewRateLimited(api, resetAt, tokensRemaining, maxInputTokens)` | `ResetAt`, `TokensRemaining`, `MaxInputTokens` |
| `ErrProviderUnavailable` | `ProviderUnavailableError` | `NewProviderUnavailable(api)` | — |
| `ErrTransport` | `TransportError` | `NewTransport(cause)` | no `APIError`; wraps the cause |
| `ErrUnexpectedProviderResponse` | `UnexpectedProviderResponseError` | `NewUnexpectedProviderResponse(statusCode, body)` | — |
| `ErrContextLengthExceeded` | `ContextLengthExceededError` | `NewContextLengthExceeded(api)` | — |
| `ErrContentFiltered` | `ContentFilteredError` | `NewContentFiltered(api)` | — |
| `ErrMcpServerStartup` | `McpServerStartupError` | `NewMcpServerStartup(serverName, stage, cause)` | no `APIError`; `ServerName`, `Stage`, `Cause` |

`ErrTransport` and `ErrMcpServerStartup` carry no `APIError` — no provider
response is involved — and their `Unwrap` returns a slice, so `errors.Is`
matches the sentinel and the underlying cause alike:

```go
func (e *TransportError) Unwrap() []error { return []error{ErrTransport, e.Cause} }
```

### Consumer usage

The three patterns, from the cheapest to the most specific:

```go
import (
    "github.com/baalimago/clai/pkg/agent"
    "github.com/baalimago/clai/pkg/claierr"
)

a := agent.New(agent.WithModel("gpt-5"), agent.WithMcpServers(servers))
if err := a.Setup(ctx); err != nil {
    var mcpErr *claierr.McpServerStartupError
    if errors.As(err, &mcpErr) { // which explicit server failed, and at which stage
        return fmt.Errorf("cannot start %s: %w", mcpErr.ServerName, err)
    }
    return err
}

chat, err := a.Query(ctx, chat)
switch {
case errors.Is(err, claierr.ErrLikelyInsufficientCredits):
    // stop the spend: the account is (likely) empty — check every vendor
case errors.Is(err, claierr.ErrRateLimited):
    var rl *claierr.RateLimitedError
    errors.As(err, &rl) // facts: back off at least until rl.ResetAt
}

// A logging path that wants the facts but not the meaning:
var facts claierr.APIErrorer
if errors.As(err, &facts) {
    slog.Info("provider refused", "status", facts.API().StatusCode,
        "code", facts.API().ProviderCode)
}
```

`errors.As` unwraps through `fmt.Errorf("…: %w", err)` wraps, so callers can
add context without hiding the type. What stays true is only that the
`fmt.Errorf` result is never itself of your type — decoders must return the
typed value rather than a wrap of it.

### Composition: `errors.Join`, never nesting

A response satisfies a _set_ of meanings, not a hierarchy. A `429
insufficient_quota` is both rate-limited and likely-out-of-credits; a bare
`402` is only the latter. Nesting would assert "credits implies
rate-limited" and make callers back off against an empty account. Meaning
sets are composed with `errors.Join`, sharing one facts pointer:

```go
api := &claierr.APIError{StatusCode: 429, ProviderCode: "insufficient_quota", Body: body}
return errors.Join(
    claierr.NewRateLimited(api, resetAt, remaining, max),
    claierr.NewInsufficientCredits(api),
)
```

**The consumer trap — a joined error cannot be type-switched.** `switch
err.(type)` on the result of `errors.Join` sees `*errors.joinError` and
silently falls to `default`. This is the single most likely consumer
mistake:

```go
// WRONG: a 429 insufficient_quota arrives as errors.Join(...), so this
// switch sees *errors.joinError and both cases silently miss.
switch err := err.(type) {
case *claierr.RateLimitedError:
    // never reached for a joined error
case *claierr.InsufficientCreditsError:
    // never reached for a joined error
}

// RIGHT: discrimination is an errors.Is check or an errors.As ladder.
switch {
case errors.Is(err, claierr.ErrRateLimited):
case errors.Is(err, claierr.ErrLikelyInsufficientCredits):
}
```

## The decode chain — vendor first, baseline fallback, catch-all

`generic.ResponseError` (`internal/text/generic/response_error.go`) is the
single decode chain, exported because two of the three text boundaries do
not go through `generic`'s completer:

```go
func ResponseError(status int, body []byte, decode func(int, []byte) error) error {
    if decode != nil {
        if err := decode(status, body); err != nil {
            return err // vendor recognized it — its answer stands alone
        }
    }
    if err := baselineError(status, body); err != nil {
        return err // status-code default for unmapped vendors
    }
    return claierr.NewUnexpectedProviderResponse(status, body) // non-OK, no meaning found — never nil
}
```

The vendor decoder speaks first (D11). When it recognizes the payload its
answer is the whole answer; only a nil decode or a nil return falls back to
the **baseline** — what the HTTP status alone suggests, with no body
parsing — and when neither speaks, to the **catch-all**
(`ErrUnexpectedProviderResponse`): never a nil error, never a bare
formatted string.

Vendor-first is what lets a vendor quirk stay inside the vendor: xAI
answers 403 for a drained account, so its decoder returns
`ErrLikelyInsufficientCredits` alone and the baseline's `ErrAuthFailed`
guess never fires. A naive `errors.Join(baseline, decode(...))` could not
express that: Join adds meanings and cannot retract one.

### The status baseline

What the HTTP status alone suggests, therefore safe in the generic layer:

| Status | Meaning | Vocabulary |
| --- | --- | --- |
| 401, 403 | credentials rejected | `ErrAuthFailed` |
| 402 | payment required | `ErrLikelyInsufficientCredits` |
| 404 | route or model unknown | `ErrModelNotFound` |
| 429 | throttled | `ErrRateLimited` |
| 5xx | provider-side, retryable **by the caller** | `ErrProviderUnavailable` |

The baseline is a fallback, not ground truth (xAI's 403-for-drained-account
and 400-shaped model-not-found from some providers are cases where the
vendor decoder speaks first and the baseline's guess never fires). Any
non-OK status with no row and no vendor meaning becomes
`ErrUnexpectedProviderResponse`.

### Two decode points, not one

Both the non-OK response **and** each streamed frame feed the same
`DecodeError` hook (D12):

1. the non-OK status response — the `ResponseError` chain above;
2. each streamed frame, so an `{"error": …}` frame at HTTP 200 becomes a
   vocabulary error on the channel instead of a silent empty success.

Generic itself learns to _see_ the frame — `chatCompletionChunk` carries the
raw `Error json.RawMessage` field, because the `{"error": …}` envelope is
the OpenAI-compat convention rather than vendor noise — and hands the frame
to `DecodeError(http.StatusOK, frame)`. A decoder that cares whether the
payload arrived as a body or a frame branches on the status. A silent
decoder degrades to `ErrUnexpectedProviderResponse` carrying the frame body:
still terminal, still typed, merely meaning-less. Before this design, such a
frame produced a zero-valued chunk, a `NoopEvent`, and a **successful run
with an empty answer** — a quota exhaustion mid-stream that no substring
matcher downstream could catch even by accident.

The same chain serves the two non-generic boundaries: anthropic's
`claude_stream.go` (which constructs `claierr.NewRateLimited` itself for its
429 header facts and rides the chain for everything else) and openai's
Responses reader, which dispatches its terminal failure events
(`response.failed`, top-level `error`, `response.error`) through
`ResponseError` at `http.StatusOK`.

## The channel contract

`CompletionEvent` is deliberately `any` — changing it would be a breaking
change across every producer loop for no gain, because the runner's existing
`%w` wrap already preserves a typed error end to end. The contract is a
pinned rule, documented on `internal/models/models.go`:

- An `error` value on the channel is **terminal**: the runner ends the step
  and returns it, unless it satisfies `errors.Is(err, io.EOF)` or
  `errors.Is(err, context.Canceled)`, which end the step normally
  (`internal/text/session_runner.go:194`).
- A producer that detects a provider error state mid-stream must send a
  vocabulary error, never a `NoopEvent`.
- A producer that sends a terminal error must stop reading right after the
  send, on every producer loop (D18): the runner ends the step on every
  channel error and never reads the channel again, so a producer that
  continued would block on its next send. This is the producer-side half of
  the rule; without it, an error frame followed by `data: [DONE]` strands the
  producer goroutine and holds the response body open until cancellation.
- The runner wraps the terminal error with `%w` and must not flatten it, so
  `errors.Is`/`errors.As` keep matching the vocabulary through the wrap.

Four producers feed the channel: `generic.StreamCompleter`, anthropic's
`claude_stream`, openai's `responses_stream`, and the mock. The mock sends
no errors; the three real producers send vocabulary values at their decode
points and stop reading after a terminal error send.

## The `Setup` MCP contract — explicit versus ambient

`Agent.Setup` returning nil proves that every server registered through
`WithMcpServers` is running (D13). The split is explicit versus ambient:

- **Explicit** — servers passed via `WithMcpServers` are load-bearing. Any
  startup failure fails `Setup` with an `errors.Join` of typed, per-server
  `*claierr.McpServerStartupError` values (spawn, `initialize`, or
  `tools/list` stage, each naming the server). `pkg/agent` enables
  `AgentSettings.StrictMcpStartup` exactly when the explicit list is
  non-empty.
- **Ambient** — servers discovered from the config directory
  (`<configDir>/mcpServers/*.json`) keep warn-and-degrade: a missing tool is
  not by itself a terminal state, and the CLI path must keep running with
  whatever servers did start.

A nil `Setup` error therefore carries meaning: nothing the caller asked for
is missing. This is the public surface contract; profile-sourced servers
ride the CLI path and stay ambient.

## What stays untyped

- **Request-construction and marshalling failures** stay plain wrapped
  errors: they are clai bugs, not provider states, and a consumer has no
  distinct action for them.
- **Non-text boundaries** (`openai/dalle.go`, `openai/sora.go`,
  `gemini/image.go`, `openrouter/catalog_fetcher.go`) do not decode into the
  vocabulary — their chain-breaking `%v` sites are repaired, their error
  wording is not a contract.
- **Recorder and cost-accounting failures** never abort the run: they are
  ambient capability, the consumer owns the recorder and observes its own
  failures.

## Reference

Design and survey: worklog `worklogs/2026-09-05-error-propagation/` — the
vocabulary table (names and owners), the baseline table, the channel
contract, and the phase-by-phase decisions (D1–D19) live there. This
document is the architecture-level restatement; the worklog is the record.
