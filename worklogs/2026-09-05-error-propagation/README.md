# Error propagation — typed terminal errors out of clai

clai should return a **typed, specific error for every terminal error state**
to its library consumers (`pkg/agent`). Today it returns formatted strings,
so every consumer reconstructs the state by matching on message text.

The package surface is the whole scope (D17): `pkg/agent` callers get
the typed values (`errors.Is`/`errors.As`). The CLI is out of scope —
upstream `cmd.Run` prints `err.Error()` and collapses every failure to exit
1 before clai can map it, and no phase performs CLI-facing surfacing work.
Whatever improvement CLI users see in stderr wording is an incidental
byproduct of the vocabulary's `Error()` strings, not a contract.

## Status board

| #   | Phase                                                                                        | Status      | Summary                                                                                                                                     |
| --- | -------------------------------------------------------------------------------------------- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | [Wave-two survey](./phase-1-wave-two-survey.md)                                              | Complete    | 31 chain-breaking sites (not 55), 51 swallowed errors (not 29), Finding 3 (silent HTTP-200 provider error), exact retry blast radius, D4–D6 |
| 2   | [`pkg/claierr` — the shared vocabulary](./phase-2-claierr-vocabulary.md)                     | Complete    | All ten vocabulary rows shipped, stdlib-only, 100% covered; `ErrRateLimit` aliased per D15 with `internal/text` compiling unedited          |
| 3   | [Remove the retry loop](./phase-3-remove-retry-loop.md)                                      | Complete    | All 7 symbols + call site removed; `InputTokenCounter` untouched; rate limit surfaces typed on the first call, unretried                    |
| 4   | [`generic.StreamCompleter` decoding](./phase-4-generic-decoding.md)                          | Complete    | Both decode points live behind the one `DecodeError` hook; silent HTTP-200 path closed; transport typed; R3-01 closed: the producer stops after a terminal error event (D18)      |
| 5   | [Vendor mappings](./phase-5-vendor-mappings.md)                                              | Complete    | All five vendors decode per the table; one `errors.Is` matches every credits spelling; alias deleted (D15); R3-02 closed 2026-09-05: `client.Do` and mid-stream read failures on anthropic/openai-responses ride `claierr.NewTransport` (plus anthropic's pre-flight count request — scope note in the phase file) |
| 6   | [Channel-borne errors carry the vocabulary](./phase-6-channel-contract.md)                   | Complete    | `CompletionEvent` stays `any`; terminal-unless-`io.EOF`/`context.Canceled` pinned; R4-01 closed 2026-09-05 (anthropic stops on any error send, `TestClaudeStream_StopsAfterNonEOFErrorSend`); R4-02 closed (cancel path emits nothing; channel close ends the step) — verified by review 5                                             |
| 7   | ~~CLI surfacing~~                                                                            | Removed     | Dropped at sign-off (D17): the worklog concerns the package surface only. Number retired in place so phase references stay stable           |
| 8   | [Repair the 31 chain-breaking sites and the swallowed terminal states](./phase-8-repairs.md) | Complete    | Tiers A–E repaired `%v`→`%w`; S2–S11 executed per the repair table (D16); explicit/ambient MCP `Setup` split typed per D13; substring matcher → `errors.Is`; every acceptance test shipped; dupl 33            |
| 9   | [`architecture/errors.md` + quality-gate sweep](./phase-9-errors-doc-and-gates.md) | Complete    | `architecture/errors.md` publishes the vocabulary (name register, consumer patterns, `errors.Join` trap, decode chain, channel + Setup contracts); indexed in `architecture/README.md`; sweep green: AST recount zero chain-breaks, no production substring matchers, dupl 33 (delta zero), `pkg/claierr` 100% covered |

**Phase order.** Phase 1 is done and gates everything. Phases 2 and 3 are
independent of each other and of the rest — either can run next. Phase 4
depends on 2. Phase 5 depends on 4. Phase 6 depends on 2 and 4. Phase 7 is
removed (D17). Phase 8 depends on 1 and 2 — its repairs speak the
vocabulary (typed MCP startup errors, `errors.Is` conversions) — and can run
in parallel with 3–6. Phase 9 runs last.

D4–D6 and the design-review decisions D11–D16 are settled, so no phase is
blocked on an open decision.

**Next eligible work:** none. Review 4's two findings are closed — the
R4-01/R4-02 fix landed after review 4 and review 5 (2026-09-05) verified it
independently, closing phase 6 (details in the phase file's Review findings).
Review 5 found one new Low (R5-01, the fix session's missing worklog record),
closed in place by review 5's own recording. No open finding of any severity
remains; every status-board row is Complete or Removed.

## Motivation

sakfråga needs to distinguish "the account is empty" from "the API is pushing
back", to stop an estate-wide LLM spend when credits run out. Its design
(`architecture/llm-breaker-v1.md` in that repo) currently identifies these
with `strings.Contains(err.Error(), "402")` and friends, because nothing else
crosses the module boundary.

That works **by accident**. `openai/responses_stream.go:229` formats
`fmt.Errorf("unexpected status code %v, body: %s", res.Status, string(body))`,
so `res.Status` renders as `"429 Too Many Requests"` and the body carries
`insufficient_quota` — the substrings a downstream matcher happens to look
for. A wording change upstream silently breaks a spend control downstream.
That is the whole argument.

The consumer requirement is small:
`errors.Is(err, clai.ErrLikelyInsufficientCredits)` and
`errors.Is(err, clai.ErrRateLimited)`, each carrying the response facts. The
work here is larger, because it is worth doing properly once.

Phase 1 found a second, sharper argument the wave-one survey missed: for
OpenAI-compatible providers that answer `200 OK` and then emit an error
frame, **nothing crosses the boundary at all** — the run is reported as
successful with an empty answer. sakfråga's substring breaker cannot catch
that case even by accident. See Strategy → "The silent path".

## Survey baseline

Measured at `v1.10.23-18-g59999b9`, excluding tests. Phase 1 recounted every
wave-one figure; where the two disagree, phase 1's method is stated in its
Specification section.

| Measure                                                 | Wave one | Phase 1 | Note                                                        |
| ------------------------------------------------------- | -------- | ------- | ----------------------------------------------------------- |
| Non-test Go files                                       | 214      | 216     | branch moved                                                |
| Error-returning sites                                   | 901      | —       | not re-measured; not load-bearing                           |
| Sites wrapping with `%w`                                | 642      | —       | as above                                                    |
| `%v` verbs inside `fmt.Errorf`                          | —        | 114     | of which 33 take an error argument                          |
| **Sites that break the error chain**                    | **55**   | **31**  | wave one counted `%v` verbs, not error-valued ones          |
| …of those, in `pkg/tools`                               | 14       | **0**   | every `pkg/tools` site is `%w`-paired or formats non-errors |
| Errors swallowed (logged, execution continues)          | 29       | 51      | wave one appears to have counted `ancli.Warnf` alone        |
| …of those, hiding a terminal or capability-losing state | —        | 11      | phase 1 bucket 2, S1–S11                                    |
| **Exported error sentinels in the entire module**       | 1        | 1       | `ErrNoMIMEType`, `internal/chat/image_builder.go:17`        |
| dupl clone groups                                       | —        | 28      | this worklog's baseline — not reproducible with the configured command at its own measurement point (journal 2026-09-05, phase 3: 31 there); phase-9 close reads 33 by phase 4's exclusion method (delta zero), production files alone 5 |

The wrapping discipline is broadly good, so this is not a rewrite. It is:
decode at the boundaries, export the vocabulary, and repair the 31 places
that break the chain.

## Strategy

### One vocabulary, many decoders

This is the load-bearing split, and it runs the opposite way to the file
layout.

**The error vocabulary is shared and closed. Decoding a vendor's wire format
into it is per-vendor and open.**

A vendor never defines an error type. It only answers _"which shared errors
does this response mean?"_ OpenAI saying `insufficient_quota` and DeepSeek
saying `402 Insufficient Balance` are different sentences with the same
meaning, and a consumer must be able to write one
`errors.Is(err, ErrLikelyInsufficientCredits)` that catches both. If a vendor
could mint its own type, that consumer would need to know every vendor —
which is the coupling this work exists to delete.

The vocabulary is _error_ naming, not AI naming. "Classify" is already spoken
for in this domain — it is what a model does to content — so nothing here
uses it. A vendor **decodes** an error response, exactly as it already
decodes a success response.

```go
// On generic.StreamCompleter, set by the vendor that embeds it.
// Decodes a provider error payload into shared error values: the body of a
// non-OK response, or an error frame received mid-stream (status is then
// http.StatusOK). A nil field, or a nil return, means "no vendor
// knowledge" and the baseline (or the catch-all) stands alone.
DecodeError func(status int, body []byte) error
```

A plain func field, not an interface: it is one function, the struct already
carries its configuration this way, and there is no second implementation to
abstract over. It returns a single `error` — a vendor whose response means
two things returns `errors.Join(a, b)` itself, so the set semantics live in
the value rather than leaking into the signature.

**The vendor decoder speaks first; the baseline is a fallback, not a
co-author (D11).** If the decoder recognizes the payload, its answer is the
whole answer. Only when the vendor is silent does the completer fall back to
the **baseline** — what the HTTP status alone suggests, with no body
parsing — and when neither speaks, to the catch-all:

```go
func responseError(status int, body []byte, decode func(int, []byte) error) error {
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

The chain is exported (`generic.ResponseError` — seam table below) because
two of the three text boundaries do not go through `generic`'s completer:
anthropic and openai's Responses reader reuse the same chain rather than
reimplementing it.

Vendor-first is what lets a vendor quirk stay inside the vendor: xAI answers
**403 for a drained account** (live probe, journal 2026-09-05), so its
decoder returns `ErrLikelyInsufficientCredits` alone and the baseline's
`ErrAuthFailed` guess never fires. The wave-one composition —
`errors.Join(baseline, decode(...))` — could not express that: Join adds
meanings and cannot retract one, so a breaker would have paged about
credentials while spend continued.

The obligation that buys this: **a decoder that recognizes a payload states
the complete meaning set itself.** OpenAI's `429` + `insufficient_quota`
decoder returns `errors.Join(NewRateLimited(…), NewInsufficientCredits(…))` —
the baseline no longer backfills `ErrRateLimited` for it. Each vendor's
decoder is thereby the single readable truth for that vendor's wire format,
and its fixture tests catch an omitted meaning.

Three properties follow:

- **Adding a vendor adds one function.** No change to `generic`, no change to
  the vocabulary, no change to any consumer.
- **A vendor with no decoder still works.** The baseline covers it, so an
  unmapped vendor degrades to status-code accuracy rather than to a formatted
  string.
- **`AGENTS.md` is satisfied structurally**, not by discipline: `generic`
  cannot contain vendor logic because it only holds a function field.

Adding an _error to the vocabulary_ is the deliberately harder change — it
touches the shared package and every consumer that wants to match it. That
asymmetry is correct: vendors are expected to multiply, meanings are not.

### The silent path — two decode points, not one

Phase 1, Finding 3. `generic.StreamCompleter` decodes each SSE frame into
`chatCompletionChunk` (`internal/text/generic/stream_completer_models.go:64`),
which has **no `error` field**. An OpenAI-compatible provider that answers
`200 OK` and then emits `{"error": {...}}` as a frame produces:

unmarshal succeeds (unknown keys ignored) → zero-valued chunk → `len(Choices) == 0`
→ `models.NoopEvent{}` → the runner's empty `case models.NoopEvent:` → stream
closes → `EndedNormally = true`, `AssistantText == ""` → `Run` returns `nil`.

**A quota exhaustion mid-stream is reported to the caller as a successful run
with an empty answer.**

The wave-one architecture decodes errors from the **non-OK HTTP response**
only, and therefore does not cover this at all. Phase 4 must implement two
decode points:

1. the non-OK status response (the `responseError` chain above), and
2. each **streamed frame**, so an error frame at HTTP 200 becomes a
   vocabulary error on the channel instead of a `NoopEvent`.

Both points feed the **same** `DecodeError` hook (D12). Generic itself
learns to _see_ the frame — `chatCompletionChunk` gains an `Error` field,
because the `{"error": …}` envelope is the OpenAI-compat convention rather
than vendor noise — and hands the raw frame to
`DecodeError(http.StatusOK, frame)`; a decoder that cares whether the
payload arrived as a body or a frame branches on the status. A nil or
silent decoder degrades to `ErrUnexpectedProviderResponse` carrying the
frame body: still terminal, still typed, merely meaning-less.

(The live probe could not reach this path — every drained account failed at
the status line — so it is a real but undemonstrated defect, not a proven
live money leak; journal 2026-09-05.)

The same site has a mis-nested `return` — `stream_completer.go:179–185`
returns `NoopEvent` only when `DEBUG_CHAT` is set, so an unparseable frame
otherwise falls through onto the zero-valued chunk. Phase 4 fixes both.

`openai/responses_stream.go` reads typed SSE events and dispatches on event
type, so it is not exposed the same way, but phase 5 must audit it for a
`response.failed` / `error` event.

### Shape of the vocabulary

Three parts: a **sentinel** per meaning for the yes/no question, a **type**
per meaning for the facts, and **one shared facts struct** embedded by
pointer so a response that carries two meanings still allocates its facts
once.

```go
// The cheap yes/no. A breaker that only needs to decide matches on these.
var (
    ErrRateLimited               = errors.New("rate limited")
    ErrLikelyInsufficientCredits = errors.New("likely insufficient credits")
    // … one per row of the baseline table below
)

// Facts about ONE response, shared by every meaning that response carries.
type APIError struct {
    StatusCode   int      // what the provider actually said
    ProviderCode string   // "insufficient_quota", "rate_limit_exceeded"
    Body         string
}

// Read the facts without knowing the meaning. errors.As accepts an
// interface target, so a logging path can ask for this and stop there.
type APIErrorer interface{ API() *APIError }

// One type per meaning. Facts are promoted from the embedded pointer;
// meaning-specific fields exist only where they are meaningful.
type RateLimitedError struct {
    *APIError
    ResetAt         time.Time   // absorbed from models.ErrRateLimit
    TokensRemaining int
    MaxInputTokens  int
}

func (e *RateLimitedError) Unwrap() error  { return ErrRateLimited }
func (e *RateLimitedError) API() *APIError { return e.APIError }

type InsufficientCreditsError struct{ *APIError }   // no ResetAt: meaningless here
```

`Unwrap` returning the sentinel is what makes both APIs work at once:
`errors.Is(err, claierr.ErrRateLimited)` for the decision,
`errors.As(err, &rl)` for `rl.ResetAt`. This is the stdlib pattern —
`fs.ErrNotExist` alongside `*fs.PathError`.

Composed with **`errors.Join`, not nesting** — a response satisfies a _set_
of meanings, not a hierarchy. A `429 insufficient_quota` is both rate-limited
and likely-out-of-credits; a `402 Insufficient Balance` is only the latter.
Nesting would assert "credits implies rate-limited" and make callers back off
against an empty account. The facts pointer is shared across the joined
values, so `api` is allocated once:

```go
api := &claierr.APIError{StatusCode: 429, ProviderCode: "insufficient_quota", Body: body}
return errors.Join(
    claierr.NewRateLimited(api, resetAt, remaining, max),
    claierr.NewInsufficientCredits(api),
)
```

Four properties, all verified against Go 1.26 (`errors.Is`/`As` through
`errors.Join` and two layers of `%w`):

- `errors.Is` finds every joined meaning; a bare `402` matches
  `ErrLikelyInsufficientCredits` and **not** `ErrRateLimited`.
- `errors.As` finds each concrete type, and `errors.As(err, &apiErrorer)`
  against the `APIErrorer` interface target returns common facts without the
  caller knowing the meaning.
- Wrapping is safe. `fmt.Errorf("context: %w", typed)` does **not** hide the
  type — `errors.As` unwraps through it. What is true is only that the
  `fmt.Errorf` result is never itself of your type, so a decoder must return
  the typed value rather than a wrap of it.
- `errors.As(err, &apiErr)` against a bare `APIError` target does not work:
  `APIError` is not an error, and `errors.As` matches concrete types, not
  embedded fields. Facts come back through a typed error or through
  `APIErrorer`.

**Constraint — a joined error cannot be type-switched.** `switch err.(type)`
on the result of `errors.Join` sees `*errors.joinError`, so a consumer that
writes the obvious type switch silently falls to `default`. Discrimination is
an `errors.Is` check or an `errors.As` ladder. Phase 9 documents this in
`architecture/errors.md`; it is the single most likely consumer mistake.

**Constraint — the embedded pointer must never be nil.** `rl.StatusCode` on a
zero-valued `RateLimitedError` panics on the promoted field. `pkg/claierr`
therefore exports constructors (`NewRateLimited`, `NewInsufficientCredits`, …)
and vendor decoders build through them, never through struct literals.

### The vocabulary table — single source of names and owners

Every sentinel and type in `pkg/claierr`, in one place. Phase files refer to
these rows by name and never restate them. **Every name ships in phase 2** —
including the evidence-gated ones, whose _wire mapping_ waits for phase 5 —
so the vocabulary is closed in a single commit and "adding a meaning" stays
the deliberately hard change.

| Sentinel                        | Type                              | Facts                                                                                                | Wire mapping (owner phase)                                                                        |
| ------------------------------- | --------------------------------- | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `ErrAuthFailed`                 | `AuthFailedError`                 | `*APIError`                                                                                          | baseline 401/403 (4)                                                                              |
| `ErrLikelyInsufficientCredits`  | `InsufficientCreditsError`        | `*APIError`                                                                                          | baseline 402 (4); deepseek 402, openrouter 402, xai 403, openai 429+`insufficient_quota` (5)      |
| `ErrModelNotFound`              | `ModelNotFoundError`              | `*APIError`                                                                                          | baseline 404 (4); 400-shaped variants per vendor, evidence-gated D9 (5)                           |
| `ErrRateLimited`                | `RateLimitedError`                | `*APIError` + `ResetAt`, `TokensRemaining`, `MaxInputTokens`                                         | baseline 429 (4); anthropic reset headers (5). Absorbs `models.ErrRateLimit`, fields intact (D15) |
| `ErrProviderUnavailable`        | `ProviderUnavailableError`        | `*APIError`                                                                                          | baseline 5xx (4)                                                                                  |
| `ErrTransport`                  | `TransportError`                  | **no** `APIError`; wraps the cause: `Unwrap() []error{ErrTransport, cause}`                          | failed `client.Do`, mid-stream read failure (4)                                                   |
| `ErrUnexpectedProviderResponse` | `UnexpectedProviderResponseError` | `*APIError` (status is 200 when it arrived as a frame)                                               | fallback at both decode points when vendor and baseline are silent (4)                            |
| `ErrContextLengthExceeded`      | `ContextLengthExceededError`      | `*APIError`                                                                                          | per-vendor, evidence-gated D9 (5)                                                                 |
| `ErrContentFiltered`            | `ContentFilteredError`            | `*APIError`                                                                                          | per-vendor, evidence-gated D9 (5)                                                                 |
| `ErrMcpServerStartup`           | `McpServerStartupError`           | server name, startup stage, cause: `Unwrap() []error{ErrMcpServerStartup, cause}`; **no** `APIError` | explicit-server `Setup` failures, D13 (8)                                                         |

Injectable seams introduced by this worklog, each with one owner:

| Seam                                                                                                                          | Introduced by |
| ----------------------------------------------------------------------------------------------------------------------------- | ------------- |
| `StreamCompleter.DecodeError` field                                                                                           | phase 4       |
| `chatCompletionChunk.Error` field                                                                                             | phase 4       |
| `generic.ResponseError` — the decode chain, exported so the two non-generic boundaries (anthropic, openai responses) reuse it | phase 4       |
| per-vendor `decodeError` functions                                                                                            | phase 5       |

### The baseline table

What the HTTP status alone suggests, therefore safe in `generic` (phase 1,
Finding 8):

| Status  | Meaning                                    | Vocabulary                     |
| ------- | ------------------------------------------ | ------------------------------ |
| 401,403 | credentials rejected                       | `ErrAuthFailed`                |
| 402     | payment required                           | `ErrLikelyInsufficientCredits` |
| 404     | route or model unknown                     | `ErrModelNotFound`             |
| 429     | throttled                                  | `ErrRateLimited`               |
| 5xx     | provider-side, retryable **by the caller** | `ErrProviderUnavailable`       |

Two probe corrections (journal 2026-09-05) qualify this table. It is a
**fallback, not ground truth**: xAI answers 403 for a drained account, and
model-not-found arrives as 400 from huggingface and inception — both are
cases where the vendor decoder speaks first (D11) and the baseline's guess
never fires. Any non-OK status with no row and no vendor meaning becomes
`ErrUnexpectedProviderResponse` — never a nil error, never a bare formatted
string.

Plus one with no status involved: `ErrTransport`, for a failed `client.Do` or
a mid-stream read failure, wrapping the underlying `net`/`url` error.

Body-decoded states — quota exhausted at 429, context length exceeded,
content filtered — are per-vendor `DecodeError` work in phase 5. Beyond the
shapes the live probe recorded (D14), wire signals must be verified against
current provider docs before implementation, not asserted from memory (D9).

Request-construction and marshalling failures stay plain wrapped errors:
they are clai bugs, not provider states, and a consumer has no distinct
action for them.

### The retry loop is a relic and gets removed

`runStepWithRetry` (`internal/text/session_runner.go:129`) catches
`models.ErrRateLimit`, sleeps until `ResetAt`, and retries up to
`RateLimitRetries = 3`. It is reachable from `pkg/agent`
(`Agent.Query` → `Querier.TextQuery` → `runStepWithRetry`), so every library
consumer already runs inside it.

Only the Anthropic vendor constructs `ErrRateLimit`, so for every other
vendor **the loop never fires**. Decoding at the other two boundaries would
silently switch it on estate-wide: calls that fail fast today would block for
up to 3 × time-to-`ResetAt` (20s minimum), holding the caller's worker, and —
because a blocked call emits nothing — producing exactly the "stuck with no
event" shape sakfråga is trying to eliminate.

Retry policy belongs to the caller, which has the context to choose it;
clai's job is to report accurately. Exact blast radius in phase 1, Finding 6.

**The trap:** do _not_ remove `models.InputTokenCounter` / `CountInputTokens`
with it. `internal/text/stoploss.go:163` asserts that interface and the
anthropic vendor calls it unconditionally
(`internal/vendors/anthropic/claude_stream.go:30`). A naive
dependency-following removal breaks token stoploss (worklog
`2026-08-04-token-stoploss`).

### `ErrRateLimit` is absorbed, not duplicated

It already exists, is already consumed, and carries `ResetAt`,
`TokensRemaining` and `MaxInputTokens`. The new vocabulary must **be** this
type — moved to `pkg/`, renamed, fields intact — not a sibling beside it. Two
rate-limit errors in one module is the failure mode this work exists to
prevent.

Once the retry loop goes, nothing inside clai consumes `ResetAt` any more —
but consumers will, so the field stays.

The move is mandatory rather than cosmetic: `ErrRateLimit` lives in
`internal/models`, so `errors.As(err, &models.ErrRateLimit{})` **does not
compile** from outside the module. Today no external consumer can match
clai's one typed error even though it exists.

Transition (D15): phase 2 moves the type — `claierr.RateLimitedError`,
fields intact — and leaves `internal/models` a thin alias
(`type ErrRateLimit = claierr.RateLimitedError`, with `NewRateLimitError`
delegating to the `claierr` constructor) so phase 3 (which deletes code
matching it) and phase 5 (anthropic, which constructs it) stay independent
of each other, as the phase order promises. Phase 5 rewires anthropic to the
`claierr` constructor and deletes the alias along with
`internal/models/errors_test.go`'s pin.

### The channel contract is a rule, not a type change

`CompletionEvent` is `any`, and every vendor stream feeds it through **four
producer loops**: `generic.StreamCompleter`, anthropic's `claude_stream`,
openai's `responses_stream`, and the mock. Changing that type is a breaking
change across all of them for no gain: the runner's `case error:` at
`session_runner.go:242` already wraps with `%w`, so a typed error placed on
the channel survives to the caller intact. The gap is production, not
transport.

The four producers are the bound actors of phase 6's contract — its
invariant table carries one row per producer, not a prose "vendors must".
Three of them already send `error` values today (a correction to phase 1,
Finding 4, which counted only one site): generic's transport read failure
(`stream_completer.go:132`), openai responses' read/parse/handle failures
(`responses_stream.go:250,259,270`), and anthropic, which forwards error
events from its frame handler through `emitClaude`. The mock does not.

Phase 6 pins:

- `CompletionEvent` stays `any`. No signature change.
- An `error` value on the channel is **terminal** — the runner ends the step
  and returns it — **unless** it satisfies `errors.Is(err, io.EOF)` or
  `errors.Is(err, context.Canceled)`. That existing behaviour
  (`session_runner.go:243`) becomes the documented rule.
- A vendor that detects a provider error frame mid-stream **must** send a
  vocabulary error rather than a `NoopEvent`.
- The runner must not flatten; the `%w` at `:250` becomes a pinned invariant
  with a test.
- The producer that sends a terminal `error` **must** stop reading after the
  send, on every producer loop — the producer-side half of "error is
  terminal" that the runner rule alone does not cover (review 3, R3-01).

### The three text boundaries

13 vendor directories (`pi` among them is a conversation-source reader, not
an LLM vendor), but the text path funnels tightly:

| Site                                              | Covers                                                                                                                                                                                 |
| ------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/vendors/openai/responses_stream.go:229` | the Responses API — `gpt-5*`, sakfråga's live path                                                                                                                                     |
| `internal/text/generic/stream_completer.go:49`    | 11 vendors riding `generic.StreamCompleter` — deepseek, mistral, xai, openrouter, novita, ollama, gemini, huggingface, berget, inception embed it; openai (chat-completions) holds one |
| `internal/vendors/anthropic/claude_stream.go:42`  | anthropic — already decodes 429 into `models.ErrRateLimit`, flattens everything else                                                                                                   |

Non-text boundaries exist and are out of scope: `openai/dalle.go:152`,
`openai/sora.go:271`, `gemini/image.go:60`, `openrouter/catalog_fetcher.go:49`.

### Swallowed-state repairs — S1–S11, one owner each (D16)

Phase 1's bucket 2, resolved. A row here is the whole decision; phase 8
executes its rows without re-deriving them.

| Sites   | Outcome                                                                                                                                                                                               | Owner                        |
| ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| S1      | Dissolved by the frame decode point                                                                                                                                                                   | phase 4                      |
| S2–S4   | Explicit servers (`WithMcpServers`): failures collected and joined into a typed `Setup` error (D13). Ambient servers (config-dir `*.json`): warn-and-degrade stays                                    | phase 8                      |
| S5–S6   | A malformed MCP response is delivered as an error to the request waiting on it, not logged and dropped                                                                                                | phase 8                      |
| S7      | A tool-response unmarshal failure returns an error result to the model instead of an empty success                                                                                                    | phase 8                      |
| S8      | A failed reply persist joins the run's returned error instead of warning — a later `-re`/`-dre` must not silently read stale state                                                                    | phase 8                      |
| S9      | Stays a warning: cost accounting is ambient capability, not something the caller named                                                                                                                | none — documented in phase 8 |
| S10–S11 | Stays as documented: recorders never abort the run (`pkg/agent` contract, metrics worklog). The consumer owns the recorder and observes its own failures. Overrides phase 1's "low cost to propagate" | none — documented in phase 8 |
| —       | The mistral delete-range warn (phase 1, bucket 3's delete-on-sight note): reworded to a plain warning, behavior unchanged                                                                             | phase 8                      |

### clai is its own bad consumer

`internal/chat/handler.go:219` does
`strings.Contains(err.Error(), "failed to list chats")` — the exact
anti-pattern this worklog deletes, inside clai, one module boundary short of
sakfråga's. Phase 8 converts it; it is a free proof that the vocabulary
works.

## Decisions

| #   | Decision                                                                                                                                                                                                                                                                                                                                                                                        | Rationale                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | The vocabulary is shared and closed; decoding is per-vendor and open, via a `DecodeError func(int, []byte) error` field on `generic.StreamCompleter`                                                                                                                                                                                                                                            | A consumer must write one `errors.Is` that catches every vendor's spelling of the same meaning. `AGENTS.md`'s vendor/generic rule is then satisfied structurally, not by discipline (wave one)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| D2  | Composition by `errors.Join`, never nesting; the typed error is the outer value, never wrapped by `fmt.Errorf`                                                                                                                                                                                                                                                                                  | A response satisfies a set of meanings, not a hierarchy — nesting would assert "credits implies rate-limited" and make callers back off against an empty account. `fmt.Errorf("%w…")` returns `*fmt.wrapError` and loses the outer type (wave one, verified)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| D3  | Remove `runStepWithRetry` and its machinery; retry policy belongs to the caller                                                                                                                                                                                                                                                                                                                 | The loop fires only for anthropic today; decoding the other boundaries would switch it on estate-wide, blocking callers for up to 3 × `ResetAt` with no events emitted — the exact shape sakfråga is eliminating (wave one)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| D4  | The vocabulary lives in a dedicated **`pkg/claierr`**                                                                                                                                                                                                                                                                                                                                           | User decision, 2026-09-05. The package name says where the error came from at every call site (`claierr.ErrRateLimited`), and it keeps consumers out of a `models` namespace collision — clai's own code already has to alias (`pub_models "…/pkg/text/models"`), so putting errors there would force that same dance on every consumer. Cycle-free: `pkg/claierr` is stdlib-only and `internal/models` imports it, matching the existing internal→pkg direction                                                                                                                                                                                                                                                                                                                                                                                |
| D5  | CLI exit codes stay at 0/1 in this worklog                                                                                                                                                                                                                                                                                                                                                      | User decision, 2026-09-05. Phase 1, Finding 5: `cmd.Run` is upstream (`go_away_boilerplate@v1.33.12`) and collapses every error to exit 1 before clai sees it, so exit-code mapping needs an upstream release. sakfråga is a _library_ consumer, so the motivating requirement is fully met by phases 2–6. Coupling a library fix to an upstream release buys nothing here. An upstream `ExitCoder` is a separate, later effort                                                                                                                                                                                                                                                                                                                                                                                                                 |
| D6  | An MCP server that fails to start keeps warning on the CLI path, and the degraded startup becomes readable by a `pkg/agent` caller                                                                                                                                                                                                                                                              | User decision, 2026-09-05. Phase 1, S2–S4: the run stays alive, because a missing tool is not by itself terminal. But a caller that passed `WithMcpServers` and whose task depends on those tools must be able to see that they are absent, rather than inferring it from a bad answer. **Mechanism settled by D13**                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| D7  | Phase 4 implements **two** decode points: the non-OK response and each streamed frame                                                                                                                                                                                                                                                                                                           | Phase 1, Finding 3: the wave-one architecture decodes only non-OK responses, so a provider error frame at HTTP 200 stays invisible and the run reports success with an empty answer                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| D8  | `CompletionEvent` stays `any`; the channel contract is a documented rule plus vendor obligation, not a type change                                                                                                                                                                                                                                                                              | Phase 1, Finding 4: every vendor stream feeds it through the four producer loops, and the runner's existing `%w` already preserves a typed error end to end. The gap is that producers do not _produce_ typed errors                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| D9  | Body-decoded vendor states (quota-at-429, context length, content filter) are named but their wire signals are confirmed against live responses or current provider docs at implementation time, not asserted from memory                                                                                                                                                                       | Phase 1, Finding 8: the wave-one README names `insufficient_quota` and `402 Insufficient Balance`; neither was verified by the survey, and a wrong constant reintroduces exactly the fragility this worklog removes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| D10 | Each meaning gets **both** a sentinel (`ErrRateLimited`, for `errors.Is`) and a type (`RateLimitedError`, for `errors.As`), with the type's `Unwrap()` returning the sentinel. Facts live in one `*APIError` embedded by pointer and shared across joined meanings; an `APIErrorer` interface exposes them without knowing the meaning; `pkg/claierr` exports constructors, not struct literals | User decision, 2026-09-05, superseding the wave-one shape. That shape declared only struct _types_ while the motivation promised `errors.Is` — which does not compile (`ErrLikelyInsufficientCredits (type) is not an expression`), because `errors.Is` takes a value. A fact-carrying struct gives `errors.As` for free and `errors.Is` not at all: `errors.Is` is value equality, and returns false even against a target with identical fields. The sentinel supplies the stable value. Facts are a property of the _response_, not of each meaning, so embedding by pointer stops a 429-plus-quota response storing its status and body twice. Type-per-meaning is kept over a single `ProviderError` because a consumer must know _which_ provider error it has in order to react, and because `ResetAt` is meaningless on an auth failure |
| D11 | **Vendor-first decoding.** A recognizing decoder's answer replaces the baseline entirely; the baseline fires only when the vendor is silent; `ErrUnexpectedProviderResponse` when both are                                                                                                                                                                                                      | User decision, 2026-09-05 design review (F1, F2). `errors.Join(baseline, decode(…))` is additive and cannot retract a wrong status guess: the probe's xAI 403-for-drained-credits would have joined `ErrAuthFailed` onto the truth, paging the wrong team while spend continued. Vendor quirks stay in vendor decoders, which in exchange must state complete meaning sets — pinned by fixture tests                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| D12 | **One `DecodeError` hook serves both decode points.** Generic detects the `{"error": …}` frame envelope itself (`chatCompletionChunk.Error`) and calls `DecodeError(http.StatusOK, frame)`                                                                                                                                                                                                      | User decision, 2026-09-05 design review (F5). One hook per vendor instead of two that share parsing; a vendor setting only one of two hooks would silently keep the empty-success bug — the defect class this worklog closes. The 200 status tells a caring decoder the payload arrived as a frame                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| D13 | **Explicit/ambient MCP split.** Servers passed via `WithMcpServers` are load-bearing: any startup failure fails `Setup` with a joined, typed, per-server `McpServerStartupError`. Config-dir servers keep warn-and-degrade. `Setup` returning nil means every requested server is running                                                                                                       | User decision, 2026-09-05 design review (F9), settling D6's mechanism. MCP setup already blocks inside `Setup` (`querier_setup_tools.go` WaitGroup), so the report is complete at return — a plain error beats a new hook or report method. Strict-for-ambient would brick every library `Setup` sharing a config dir with one stale json                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| D14 | Phase 5 covers **xai and openrouter** alongside openai, anthropic, deepseek. Decoder fixtures are reconstructed from the journal-recorded live shapes and labeled as such                                                                                                                                                                                                                       | User decision, 2026-09-05 design review (F4, F11). xai is the vendor whose absence actively misdecodes under the baseline (D11). The probe's raw bodies were not preserved and the drained-account state that produced them is ephemeral; the same-day journal transcription is the best remaining evidence and beats memory (D9)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| D15 | Phase 2 absorbs `models.ErrRateLimit` behind an internal alias; phase 5 rewires anthropic and deletes the alias                                                                                                                                                                                                                                                                                 | Design review, 2026-09-05 (F3). The status board and phase 1's Finding 6 disagreed on the owning phase; the alias gives the move one owner while keeping phases 2, 3 and 5 mutually independent, as the phase order promises                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| D16 | Swallowed-state outcomes per the repair table: S1→phase 4; S2–S4→D13; S5–S7 propagate to their waiting requester; S8 joins the run error; S9 stays a warning; S10–S11 keep the recorders' documented never-abort contract                                                                                                                                                                       | Design review, 2026-09-05 (F10). S10/S11's swallowing is a documented `pkg/agent` contract from the metrics worklog and the consumer owns the recorder — overriding phase 1's "low cost to propagate" note                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| D17 | The CLI is out of scope entirely: phase 7 is removed, its number retired in place, and no phase performs CLI-facing surfacing work. The worklog's contract is the package surface — `pkg/agent` and `pkg/claierr`                                                                                                                                                                               | User decision, 2026-09-05 sign-off round. Extends D5: with exit codes already collapsed upstream, stderr wording was the only remaining CLI deliverable, and it improves incidentally through the typed errors' `Error()` strings anyway. Scoping to the package keeps the worklog aligned with its motivating consumer, a library caller                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |

| D18 | **Producer-side terminal-error stop (R3-01 closure).** A producer that sends a terminal `error` event stops reading right after the send, exactly as it stops after `StopEvent`, with **no** `io.EOF`/`context.Canceled` carve-out on the producer side. The check is by error presence, not vocabulary membership | Implementer decision (clai worker), 2026-09-05, closing review 3's R3-01 in `generic.StreamCompleter`. The runner's normal-end branches for `io.EOF` and `context.Canceled` errors return as well and never read the channel again (`session_runner.go:194-201`), so **every** channel error is the last event a consumer reads; a producer that continued would block on its next send. Carving out the two errors would protect against an emission that cannot occur from `handleStreamChunk` (its errors are decode, tool-argument and transport values) and would still leak once the runner's normal-end branch returned. Presence-of-`error` is therefore both safe and simpler than a vocabulary test |

| D19 | **R3-02 scope: anthropic's pre-flight `CountInputTokens` `client.Do` rides the transport fix.** Beyond R3-02's four enumerated sites, the `CountInputTokens` request's connection failure — a third `client.Do` in `claude_stream.go`, reached from `StreamCompletions` before the stream starts — also returns `claierr.NewTransport(cause)` | Implementer decision (clai worker), 2026-09-05, closing review 3's R3-02. It is a failed `client.Do` crossing the module boundary on a file the finding names, so leaving it plain would keep a known gap in the very promise the finding restates ("a consumer matching `ErrTransport` silently misses anthropic"); no wrong-meaning risk, since `ErrTransport` truthfully describes a connection failure. Pinned by `Test_Anthropic_CountTokensDoFailure_ErrTransport`; parse/handle and the non-OK count-response path stay untouched as out of scope |

## Rejected alternatives

| Idea                                                                            | Reason rejected                                                                                                                                                                                                      |
| ------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Each vendor defines its own error types                                         | A consumer would need to know every vendor — the coupling this work exists to delete (D1)                                                                                                                            |
| `DecodeError` as an interface rather than a func field                          | One function, no second implementation to abstract over, and the struct already carries its configuration as fields (D1)                                                                                             |
| `DecodeError` returning `[]error` or a set type                                 | A vendor whose response means two things returns `errors.Join(a, b)` itself; set semantics belong in the value, not the signature (D1)                                                                               |
| Nesting the error types (`ErrLikelyInsufficientCredits` wraps `ErrRateLimited`) | Asserts "credits implies rate-limited"; a caller would back off against an empty account (D2)                                                                                                                        |
| Keep the retry loop and make it vendor-aware                                    | Retry policy needs the caller's context (deadline, budget, fallback model), which clai does not have (D3)                                                                                                            |
| A new `ErrRateLimited` beside `models.ErrRateLimit`                             | Two rate-limit errors in one module is the failure mode this work exists to prevent                                                                                                                                  |
| Change `CompletionEvent` from `any` to a closed interface                       | Breaking change across every producer loop and vendor; the runner's existing `%w` already preserves typed errors, so it buys nothing (D8)                                                                            |
| Bypass `cmd.Run` in clai's `run()` to control exit codes                        | Duplicates dispatch, help and completion (phase 1, Finding 5)                                                                                                                                                        |
| Put the vocabulary in `pkg/text/models`                                         | Cycle-free and adds no new import, but forces every consumer into the `models` namespace clai itself already has to alias around (`pub_models`). `pkg/claierr` names its origin at the call site (D4)                |
| Make a failed MCP server start terminal _for runs and ambient servers_          | A missing tool is not by itself a terminal state, and it would break runs that degrade gracefully today. Explicitly requested servers now fail `Setup` instead (D13) — which is a different thing than killing a run |
| Join the baseline with the vendor decode unconditionally                        | Additive-only: cannot retract a wrong status guess. xAI's 403-for-drained-credits would carry `ErrAuthFailed` alongside the truth (D11)                                                                              |
| A second `DecodeErrorFrame` hook for mid-stream frames                          | Two hooks per vendor that mostly share parsing; a vendor forgetting the second silently keeps the empty-success bug (D12)                                                                                            |
| A recorder hook or report method for degraded MCP startup                       | `Setup` already blocks until every server resolves, so a plain error covers it: nil means all requested servers running. No new public surface (D13)                                                                 |
| Repair all 114 `%v` verbs                                                       | 81 of them format file paths, model names, status lines and command output. Only the 31 that discard an error break the chain (phase 1, Finding 1)                                                                   |

## Out of scope

- Non-text boundaries: `openai/dalle.go`, `openai/sora.go`, `gemini/image.go`,
  `openrouter/catalog_fetcher.go`. Their chain-breaking `%v` sites are still
  repaired in phase 8; their _vocabulary decoding_ is not.
- `internal/audio` decoding (`generic/transcriber.go:118` is its own
  boundary).
- Retry, backoff, or fallback policy of any kind — deleted, not relocated.
- Changing `CompletionEvent`'s type.
- Any CLI-facing error surfacing — wording and exit codes alike (D17,
  subsuming D5's upstream-`pkg/cmd` exclusion). The CLI keeps printing
  `err.Error()` through upstream `cmd.Run`; improved wording is a
  byproduct, not a deliverable.
- sakfråga's own `internal/breaker` rewrite — tracked in that repo.

## Consumer note

sakfråga's `internal/breaker` substring predicates become `errors.Is` once
phase 5 lands, and its three LLM routines currently match with two different
rules — worth unifying on that side at the same time. That work is tracked in
sakfråga, not here.

Phase 1, Finding 3 is worth relaying to that repo now, ahead of any code
here: for OpenAI-compatible providers the breaker currently has a blind spot
no substring can cover, because a mid-stream quota error arrives as a
successful empty response.

## Definition of success

Each line is provable, and names its proof:

1. A consumer outside the module writes
   `errors.Is(err, claierr.ErrLikelyInsufficientCredits)` **once** and it
   matches deepseek's 402, openrouter's 402, xai's 403, and openai's
   429+`insufficient_quota` alike — phase 5's fixture tests, one per vendor.
2. The silent path is closed: an `{"error": …}` frame at HTTP 200 surfaces
   as a typed terminal error on the channel, never as a successful empty
   run — phase 4's acceptance test on the frame decode point.
3. `Agent.Setup` returning nil proves every `WithMcpServers` server is
   running; a failed explicit server yields a joined `McpServerStartupError`
   naming it — phase 8's acceptance tests.
4. No formatted-string matching remains in clai:
   `internal/chat/handler.go:219` uses `errors.Is`, and the phase 1 AST
   recount re-run in phase 9 reports zero chain-breaking sites.
5. The retry loop is gone: a rate limit surfaces on the first call, unretried
   — phase 3's inverted iteration test. That test asserts against
   `models.ErrRateLimit`, the name that is stable whichever of phases 2 and 3
   runs first; once phase 2's alias lands (D15) the same assertion is
   `claierr.ErrRateLimited`, with no edit to the test.
6. `make qa` passes unedited, with the dupl baseline at or below phase 1's
   recorded count. (Review 3 note: in this sandbox
   `go test ./... -race -count=3 -timeout=30s` times out in the root
   package's pty e2e suite under `-count=3` — it passes under `-count=1`;
   all 44 non-root packages are green under `-count=3`. See R3-03.)

## Validation policy

The repository's gates, run exactly as `CLAUDE.md` specifies (`make qa`):
gofumpt, staticcheck, `go vet`, `go test ./... -race -cover -count=3
-timeout=30s`, `go fix`, dupl. No timeout, count, race or skip
modifications. New implementation phases target the repository's coverage
bar (70+% required, 90+% preferred).

One standing exception: live vendor confirmation (D9/D14) is manual and
paid, and the drained-account condition that produced the probe evidence
cannot be reproduced on demand. Vendor decoder tests therefore run against
checked-in fixtures reconstructed from the journal-recorded shapes; no
phase may require a live provider call to pass its gates. Phase 9 owns the
final sweep.

## Readiness checklist

Run by the author before requesting validation; outcome recorded in the
session journal.

1. No numerals in phase files outside integration-contract oracle rows
   (wire fixtures, status codes in oracle tables):
   `grep -nE '(^|[^0-9])[0-9]+ ?(s|ms|%|retries|tokens)\b' phase-*.md`
2. Every test name is declared in exactly one phase and one file list.
3. Every config field, flag, and injectable seam has one owner — the
   vocabulary table and its injectable-seams table are the register.
4. Every invariant and limit is a table with a test per row (the channel
   contract in phase 6, the decode chain in phase 4, the `Setup` contract
   in phase 8).
5. Any phase mentioning listening, manual, or paid steps has a
   `Human required` subsection. (Target: none — see Validation policy.)
6. No phase references text scheduled for deletion; retry-loop symbols
   appear only in phase 3's removal tables.
7. New conventions do not contradict existing code conventions — checked
   against `pkg/agent/agent.go` (functional options, recorder never-abort
   comments) and `internal/text/generic/` (vendor-agnostic rule,
   `AGENTS.md`).

## Session journal

### 2026-09-05 — First-wave survey (imago + clai)

Scoping pass. Established the two error paths (return value, stream channel),
the three text HTTP boundaries, the retry loop's reachability from
`pkg/agent`, and the `ErrRateLimit`-must-be-absorbed constraint. Designed the
one-vocabulary/many-decoders split and the `errors.Join` composition, and
verified the two `errors.As` gotchas. Left six items for wave two and the
vocabulary's home open. Nothing implemented.

### 2026-09-05 — Phase 1, wave-two survey (imago + clai)

Phase 1 is Complete. Survey only; no production code changed. Recounted every
wave-one figure with an AST pass over all 216 non-test files, then read each
site.

Corrections: the 55 `%v` sites are **31** (wave one counted `%v` verbs, not
error-valued ones), and `pkg/tools` — wave one's recommended starting point at
14 sites — has **zero**. The 29 swallowed errors are **51**, of which 11 hide
a terminal or capability-losing state.

New and load-bearing: **Finding 3**. `chatCompletionChunk` has no `error`
field, so an OpenAI-compatible provider that answers `200 OK` and then emits
an error frame produces `NoopEvent` → stream close → `EndedNormally` → `nil`.
A mid-stream quota exhaustion is reported as a successful empty run. The
wave-one architecture does not cover it, because it decodes only non-OK
responses. Phase 4 grows a second decode point (D7). The same site has a
mis-nested `return` that only fires under `DEBUG_CHAT`.

Also settled: the vocabulary's home is decidable — both candidates are
cycle-free, `pkg/text/models` recommended (D4); the channel contract is a
rule, not a type change (D8); the retry blast radius gains `sleepContext`
(wave one missed it) and one keep-and-amend test rather than a delete.

Escalated to the user: D4 (vocabulary home), D5 (CLI exit codes — blocked
upstream), D6 (MCP startup failure severity).

Baseline: `go build ./...` ✓; `go vet ./...` ✓; dupl → 28 clone groups.

### 2026-09-05 — Decisions D4–D6 settled (imago)

D4: the vocabulary gets its own package, **`pkg/claierr`** — rejecting the
survey's `pkg/text/models` recommendation. The reasoning is the consumer's
import line, not the dependency graph: clai already aliases its own public
models package (`pub_models "…/pkg/text/models"`), and putting errors there
would push that same namespace collision onto every downstream user.
`claierr.ErrRateLimited` says where the error came from at the call site.

D5: CLI exit codes stay 0/1; the upstream `ExitCoder` is a separate effort.
Phase 7 shrinks to stderr wording.

D6: an MCP server that fails to start keeps warning and the run continues,
but the degraded startup becomes readable by a `pkg/agent` caller. Folded
into phase 8 rather than given its own phase.

No phase is blocked. Next eligible: phase 2 (`pkg/claierr`), phase 3, or
phase 8 tiers A–C.

### 2026-09-05 — Live vendor probe, and two dispatcher bugs it exposed (imago + clai)

The D9 evidence gate ran as a live capture rather than a docs read: every
provider account was already drained, so `probe/` (own `go.mod`, excluded
from `go build ./...`) sent one minimal request per vendor using each vendor
package's own URL, auth shape and body.

**Error evidence captured.** deepseek `402` (`message: "Insufficient
Balance"`, but `code: "invalid_request_error"` and `type: "unknown_error"` —
both useless for decoding); openrouter `402` (`metadata.limit_source:
"openrouter_credits"`, `code` a _number_); xai **`403`** ("used all available
credits"). openai still had credit, so its `429` + `insufficient_quota`
shape comes from provider docs.

**This corrects the baseline table.** The draft mapped `401, 403 →
ErrAuthFailed`. xAI answers **403 for an empty account**, so that row would
decode a drained xAI key as "credentials rejected" — a breaker would page
someone about API keys while spend continued. Also: model-not-found arrives
as **both** 400 (huggingface, inception) and 404 (anthropic, gemini,
berget), so a lone `404 → ErrModelNotFound` row is incomplete.

**Finding 3 downgraded.** No vendor answered `200 OK` with an error frame;
every drained account failed at the status line. The code path is real and
phase 4 still fixes it, but it is an unreached path, not a demonstrated live
money leak. The earlier stronger claim is withdrawn.

**Two production bugs found and fixed on this branch** (incidental to the
worklog, discovered because the probe copied the vendor defaults):

- `internal/text/querier_setup.go` — `vendorType` opened with
  `strings.Contains(fromModel, "test")` → mock, capturing every model whose
  name contains "test", which is the whole `-latest` convention.
  `mistral-large-latest` (mistral's own default), `gpt-4o-latest`,
  `claude-3-latest` all resolved to the mock vendor and returned fabricated
  output with no error. Changed to `HasPrefix`.
- `internal/vendors/inception/inception.go` — default model `murcury`, a typo
  for `mercury`. `vendorType` matches the substring `mercury`, so the default
  routed nowhere (`failed to find vendor for: murcury`).

New `internal/text/vendor_defaults_test.go` pins both: every vendor default
must reach its own vendor, and mock selection must not capture real models.
It asserts no specific model id, so defaults stay free to change.
`querier_setup_test.go` and `create_querier_test.go` swapped live model ids
for synthetic `fixture` names — the parser tests a naming _shape_ and never
consults a catalogue, so live ids there only rot and mislead. (They were
never load-bearing: replacing three vendor defaults with a placeholder left
the full suite green.)

`internal/vendors/gemini` default moved to `gemini-3.6-flash`, the successor
the API named in its own 404 body. Still stale, with no evidence for a
replacement and therefore deliberately untouched: `anthropic`
(`claude-sonnet-4`), `berget` (`gemma-4-31B-it`), `huggingface`
(`meta-llama/Meta-Llama-3.1-8B-Instruct`). These route correctly; they are
retired upstream.

Gates after the changes: `go build ./...` ✓; `go vet ./...` ✓;
`go test ./... -race -cover -count=3 -timeout=30s` ✓ exit 0, 43 packages;
gofumpt ✓; staticcheck ✓; `go fix ./...` ✓; dupl 28 clone groups (baseline
unchanged).

### 2026-09-05 — Design review before phase authoring (imago + clai)

Full README inspection against the code before writing phases 2–9. Twelve
findings (F1–F12; feedback index below). The load-bearing ones: the
composition rule was additive-only and could not express xAI's
403-for-drained-credits, which the probe had already journaled but the
Strategy never absorbed (F1, F2 → D11, vendor-first decoding); the frame
decode point had no defined seam (F5 → D12, one `DecodeError` hook for both
points); D6's "readable degraded startup" had no interface, and reading
`querier_setup_tools.go` showed MCP setup already blocks inside `Setup`, so
the mechanism became the explicit/ambient split with no new public surface
(F9 → D13); phase 5 grew xai and openrouter plus a fixture policy, since the
probe's raw bodies were not preserved (F4, F11 → D14); the `ErrRateLimit`
move got a single owner via an internal alias (F3 → D15); and the
swallowed-state repairs got one outcome each (F10 → D16).

Sections the worklog format requires were added: the vocabulary table
(single source of names and owners), swallowed-state repair table,
definition of success, validation policy, readiness checklist. Phase 8's
dependency corrected to phases 1 and 2.

Awaiting README sign-off; no phase files written yet.

### 2026-09-05 — Sign-off round (imago + clai)

Scope ruling during README review: the goal's "and the CLI" was challenged —
upstream `cmd.Run` consumes the error value before clai can map it — and
rather than keep the stderr-wording remnant, the CLI was dropped from scope
entirely (D17). Phase 7 is removed, its number retired in place so phase
references in the completed phase 1 stay valid. The worklog now concerns
the package surface only. Sign-off still pending on the amended README.

### 2026-09-05 — Pre-sign-off consistency audit (clai)

Every claim in the README re-verified against the code at
`v1.10.23-19-g7b57af5` (one commit past phase 1's measurement point — the
probe session's vendor-defaults commit). All cited line anchors in the
README hold at HEAD. Phase 1 is untouched as a historical record pinned at
`v1.10.23-18`; its `querier_setup.go:278` (S9) anchor is 283 at HEAD,
expected drift from that commit.

Five corrections, G1–G5 (feedback index below). Load-bearing ones: the
vendor counts were wrong in three places — 13 vendor directories, not 17;
11 packages on `generic.StreamCompleter`, not 12; and `CompletionEvent` is
fed by four producer loops, not "17 vendors" (G1). Phase 1's Finding 4
claimed only one production site sends an `error` into a channel; there are
three — generic, openai responses (three sites), and anthropic's forwarded
error events — now named in Strategy as the bound actors of phase 6's
invariant table (G2).

Readiness checklist not yet run — it applies once phase files exist.

### 2026-09-05 — README signed off; phases authored (imago + clai)

**Sign-off recorded**: imago approved the README as amended by the
consistency audit, including the `McpServerStartupError` naming (G3), and
directed phase authoring to proceed.

Phases 2–6, 8 and 9 written from the approved README; all `Not Started`.
Three README amendments made during authoring, all mechanical promotions
rather than new decisions:

- `generic.ResponseError` added to the seam table with a Strategy note
  (owner: phase 4): the decode chain must be exported because the
  anthropic and openai-responses boundaries do not go through `generic`'s
  completer and phase 5 rewires them onto it.
- The mistral delete-range warn (phase 1, bucket 3's delete-on-sight note)
  was owned by no phase; added to the repair table, owner phase 8, as a
  rewording with behavior unchanged.
- Status board rows 2–9 gained their phase-file links.

**Readiness checklist run** (author, this session):

1. Numerals-with-units grep over `phase-*.md`: clean for phases 2–9. The
   only hits are in phase 1, a completed historical survey whose oracle
   figures and quoted gate commands predate this checklist; accepted.
2. Test names: every declared test name is owned by exactly one of phases
   2–9 with a file list; verified by a per-file distribution check over
   the phase files. Phase 1 mentions the phase-3 test dispositions as
   survey evidence; phase 3 is the single owner of those actions. Phase
   6's foreign-owned invariant rows cite "the owning phase's acceptance
   criteria" without naming tests, so no name spans two phases.
3. Seam owners: all four seam-table rows have one owner (phase 4 × three,
   phase 5 × one); no new config fields or flags are introduced anywhere.
4. Invariants and limits: tables with a test (or named evidence) per row in
   phases 2, 4 and 6; the `Setup` contract rows live in phase 8's
   integration contract and error coverage.
5. No phase mentions listening or paid steps; phase 5's evidence gate is
   doc-verification with citations, no live calls (Validation policy).
   Target of none: met.
6. No phase references text scheduled for deletion: retry-loop symbols
   appear only in phase 3's removal tables; the alias appears only in its
   owner phases (2 introduces, 5 deletes, per D15).
7. Conventions checked against `pkg/agent/agent.go` (functional options,
   recorder never-abort comments) and `internal/text/generic/`
   (vendor-agnostic rule): the explicit/ambient split rides the existing
   `WithMcpServers` option; `generic` gains only a func field, an envelope
   field, and status-only logic. No contradictions.

Handoff: ready for worklog validation; target verdict `Conditionally
ready`.

### 2026-09-05 — Phase 2 executed (phase-2 worker subagent)

Phase 2 is Complete. `pkg/claierr` ships every vocabulary-table row —
ten sentinels, ten types, ten constructors, `APIError`, `APIErrorer` —
stdlib-only (`errors`, `fmt`, `time`), 100% statement coverage.
`models.ErrRateLimit` is now the D15 alias with `NewRateLimitError`
delegating at `http.StatusTooManyRequests`; `internal/text` compiles and
passes unedited. One shape note for later phases: `Error()` is
nil-receiver-safe on the facts pointer, because existing `internal/text`
tests build the alias as a literal without facts. All gates pass; dupl
holds at the 28-group baseline. Details in the phase's Implementation
notes.

### 2026-09-05 — Phase 3 executed (phase-3 worker subagent)

Phase 3 is Complete. The retry loop is gone: all seven production symbols
and the `Run` call site removed per the removal table, `Run` now calls
`executeModelStep` directly, and a rate limit surfaces typed
(`errors.As` → `*models.ErrRateLimit`) on the first call —
`Test_sessionRunner_Run_RateLimitSurfacesOnFirstCall` pins it with a
far-future `ResetAt`, so any surviving sleep path would blow the suite
timeout. The keep-table trap held: `models.InputTokenCounter` /
`CountInputTokens` untouched, stoploss and anthropic suites green. The
acceptance grep over `internal/` and `pkg/` returns nothing. All gates
pass (`make qa` exit 0, 45 packages). One recording defect surfaced:
phase 1's dupl baseline of 28 is not reproducible — the configured
command reports **31** groups at the baseline commit `59999b9` itself
and identically before/after this phase (delta zero); details in the
phase's Implementation notes. Later phases should compare against 31.

### 2026-09-05 — Phase 4 executed (phase-4 worker subagent)

Phase 4 is Complete. Both decode points ship behind the one `DecodeError`
hook: the non-OK response goes through the exported
`generic.ResponseError` chain (vendor first, baseline fallback, catch-all
— never nil), and an `{"error": …}` frame at HTTP 200 reuses the same
chain at `http.StatusOK`, so the silent path is closed — the frame
surfaces typed on the channel, never as `NoopEvent`. The mis-nested
unmarshal return is fixed (Noop unconditionally, warning stays behind
the debug flag), and failed `client.Do` plus mid-stream read failures
carry `claierr.ErrTransport` with the cause reachable. All seven
invariant tests pass at the real `httptest` boundary; the 11 embedding
vendors compile and pass unedited. One interpretation recorded: a
literal `"error": null` envelope does not trigger the frame decode.
All gates pass; dupl reads 31 excluding the stray `.claude/worktrees/`
copy that inflates naive runs (phase 3's recount stands). Details in
the phase's Implementation notes.

### 2026-09-05 — Phase 5 executed (phase-5 worker subagent)

Phase 5 is Complete. Every vendor in D14's set decodes per the spec table:
openai's 429+`insufficient_quota` joins both meanings through one shared
facts allocation (chat and Responses boundaries alike, via the one
`decodeError`); the Responses failure events (`response.failed`, top-level
`error` — both verified against the current streaming-events reference,
D9) now ride `generic.ResponseError` at `http.StatusOK` and surface typed
on the channel; anthropic's 429 header branch constructs
`claierr.NewRateLimited` directly and every other non-OK rides the chain
(decoder nil until evidence); deepseek 402, xai 403-drained (credits
ALONE, no auth page-out) and openrouter 402 decode from journal-shape
fixtures (D14, provenance notes beside each `testdata/`).
`Test_AllVendors_InsufficientCredits_OneMatcher` proves the worklog's
first definition-of-success item at the real httptest boundary. The D15
alias is deleted and the acceptance grep is clean. Evidence-gated rows:
quota mapped with citations; context-length, content-filter and 400-shaped
model-not-found left unmapped — no current-doc evidence (recorded per row
in the phase's Implementation notes). One structural deviation:
`shared_test.go` became `package vendors_test` to break an import cycle.
All gates pass (`make qa` exit 0, 44 packages); dupl 30 groups by phase
4's method, −1 from its 31.

### 2026-09-05 — Phases 2–6 executed; phase-6 completion notes (clai supervisor)

Phases 2–5 were executed by one worker each, reviewed by the session
supervisor, and accepted (see each phase's Implementation notes). The
phase-6 worker was interrupted by a session-limit error after adding its
two runner tests but before the `CompletionEvent` doc comment and gates;
the supervisor completed the phase: added the doc comment, repaired the
missing `fmt`/`io` imports, ran the phase tests and the non-root package
sweep (exit 0), and closed the mock-inspection row. One environmental
gate issue was recorded rather than fixed: the root-package e2e suite
hangs under `-count=3` in this sandbox (`Test_e2e_setup_macro_select_category_quit`
timed out with a live `net/http` h2 stream) while passing under
`-count=1`; none of the phases touched the root package, so it is a
pre-existing environment flake, not a phase regression. Remaining work:
phase 8 (repairs) and phase 9 (doc + quality-gate sweep).

### 2026-09-05 — Phase 8 executed (phase-8 continuation worker, session 3; clai)

Phase 8 is Complete. Two earlier phase-8 sessions had shipped the mcp
package repairs (S4–S7 + Tier E), the explicit/ambient MCP split (D13),
S8's finalizer-returns-error join, the S9–S11 comments, the chat-handler
`errListChats` sentinel, and the tier C/D/E `%v`→`%w` sites; this session
finished the remaining acceptance evidence and the gates. Added:
`Test_Finalizer_PersistFailure_JoinsRunError`; `Test_PkgAgent_SetupWrapsCause`;
`Test_AgentSetup_ExplicitMcpFailure_JoinedTyped` and
`Test_AgentSetup_AmbientMcpFailure_Degrades` at the real public surface
(`WithModel("mock_test")` path); D13 internal/text pins for the strict spawn,
strict handshake (manager report channel) and strict-keeps-ambient-degrade
rows. Every acceptance-table and error-coverage row now has its named test
(one audio-row name shipped as `TestSplitter_…` in session 2 — recorded in
the phase file).

Gates at HEAD of this session: gofumpt clean; `go vet ./...` ✓;
staticcheck ✓; `go fix ./...` ✓;
`go test ./... -race -cover -count=3 -timeout=30s` ✓ for all 44 non-root
packages (internal/text 81.8%, pkg/agent 94.2% statement coverage). The
root-package e2e suite still times out under `-count=3` on the recorded
pre-existing flake (`Test_e2e_setup_announcement_survives_interactive_wizard`)
and passes under `-count=1` (exit 0) — phase 8 touched no root-package file.
dupl reads 33 clone groups by phase 4's exclusion method (30 at phase 5
close; +3 all from phase-8 acceptance-test additions across its three
sessions, +1 of them the cross-package startup-error walker pair — test-only
and structurally forced, see the phase file). Remaining work: phase 9.

### 2026-09-05 — Phase 9 executed (phase-9 worker)

Phase 9 is Complete, and with it the worklog: every status-board row is
Complete or Removed, and no next-eligible phase remains. `architecture/errors.md`
publishes the vocabulary — the name register (all 33 `pkg/claierr`
exports, cross-checked against `go doc -all`), the three consumer patterns
(`errors.Is`, `errors.As`, `APIErrorer`), the `errors.Join` type-switch
trap with a worked example, the vendor-first/baseline/catch-all decode
chain with its two decode points, the channel rule, the explicit/ambient
MCP `Setup` contract, and what stays untyped — and
`architecture/README.md` indexes it in Core concepts. The sweep is green:
gofumpt/staticcheck/vet/fix clean; `-count=3` green for all 44 non-root
packages (root pty-e2e flake under `-count=3` unchanged, passes
`-count=1`); AST recount over 222 non-test files reports **zero**
chain-breaking sites (34 remaining no-`%w` `%v` sites all format non-error
data, incl. the deliberate `sora.go` any-assert and `chatUsage` renders);
the substring-matcher grep returns nothing over non-test files (test-only
hits are assertions, recorded as an interpretation in the phase file);
dupl 33 by phase 4's method, delta zero (production files alone: 5);
`pkg/claierr` 100% covered, touched surfaces at 62–94%. Phase 9 changed no
production code. Details in the phase file's Implementation notes.

### 2026-09-05 — Holistic review, follow-up pass (imago worker, post-phase-9)

No next-eligible phase exists, so this session re-ran the closing review
against the working tree at HEAD-of-branch plus uncommitted phase-8/9 tail.
Independent re-verification of the phase-9 sweep, reproduced exactly:
`go test ./... -race -cover -count=3 -timeout=30s` green for all 44
non-root packages with the recorded coverage (pkg/claierr 100.0%, pkg/agent
94.2%, internal/text 81.8%); the root package times out under `-count=3` on
the recorded pre-existing pty-e2e flake (`Test_e2e_setup_announcement_survives_interactive_wizard`)
and passes `-count=1` exit 0; gofumpt/staticcheck/vet/fix clean; dupl 33
clone groups by phase 4's method (5 production-only); the substring-matcher
grep returns nothing over non-test files; a fresh AST recount (throwaway
tool, phase-1 method) over all 222 non-test files reports 34
`fmt.Errorf`-with-`%v`-no-`%w` sites, every argument read as non-error data
— zero chain-breaking sites. Acceptance-test names from the phase 8 table
spot-checked present; the mcp manager report channel, D13 split, S8
finalizer join and B7 branch read against the code and match their records.
Two Low findings from this pass, both closed — details in the phase-9
Review findings and the Findings index below (R1, R2). R2 amended
`architecture/query.md` and `architecture/streaming.md`, which still
described the phase-3-removed retry loop by its deleted `models.ErrRateLimit`
name; no production code changed. One decision recorded: retry-policy
documentation now points at `architecture/errors.md` rather than restating
phase-3 removal history in the flow docs.

### 2026-09-05 — Review 3 (independent implementation review; clai)

Independent verification against the working tree: re-ran the gates and read
the code rather than the notes.

Commands re-run and results:

- `go run mvdan.cc/gofumpt@latest -l .` — clean (no output).
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0.
- `go vet ./...` — exit 0.
- `go fix ./...` — exit 0.
- `go test ./... -race -cover -count=3 -timeout=30s` — all 44 non-root
  packages ok; the root package times out under `-count=3` (this run:
  `Test_e2e_replay_loads_theme/dir-replay`) and passes under `-count=1`
  (`go test . -race -cover -count=1 -timeout=120s`, exit 0). Reproduces the
  recorded pre-existing pty-e2e flake; no root-package file is modified by
  this worklog.
- `go run github.com/mibk/dupl@latest -t 80 .` with the phase-4 exclusion
  method (`find . -name '*.go' -not -path './.claude/*' | go run
  github.com/mibk/dupl@latest -t 80 -files`) — 33 clone groups; production
  files alone (`… -not -name '*_test.go'`) — 5. Matches the phase-9 close.

Verified good by reading the code: `pkg/claierr` (all ten
sentinel/type/constructor pairs, `APIError`/`APIErrorer`, never-nil facts via
`orZero`); `generic.ResponseError` implements vendor-first/baseline/catch-all
and never returns nil for a non-OK status; both generic decode points feed the
one `DecodeError` hook; the four vendor decoders key on their documented
signals and build through constructors; the runner's `case error:` wrap
preserves the vocabulary (`Test_Runner_ChannelError_TerminalTypedSurvives` is
a real boundary test); the explicit/ambient MCP `Setup` split and the S5–S8
repairs match their contracts; `architecture/errors.md` covers every
`pkg/claierr` export and the three consumer patterns.

Verdict: not ready to sign off. The vocabulary and decode chain are correct
and lean, but two Medium findings (R3-01, R3-02) leave the terminal-error
invariant incompletely closed at the producer and boundary level, and one Low
finding (R3-03) records that the `make qa`-passes-unescaped criterion is not
reproducible in this environment. Phases 4 and 5 are reopened; the fixes are
small and local.

Cross-cutting invariant promoted to Strategy: a terminal error event must not
just reach the channel — the producer that sends it must stop reading after
the send, on every producer loop, exactly as it already does for `StopEvent`.
The phase-6 channel contract pins the runner side (return on error); the
producer side (stop after send) is the missing half (R3-01).

### 2026-09-05 — R3-01 closure, phase 4 (clai worker, reopen session)

Selected phase 4's R3-01 as the next eligible reopened phase (phase 5's
R3-02 remains open). Implemented the reviewed fix in
generic's `handleStreamResponse` producer loop: after the send, the loop
returns on any chunk-handler `error` exactly as it returns on `StopEvent`
(type switch, no `io.EOF`/`context.Canceled` carve-out — rationale in D18).

Test-first: added `Test_Generic_ErrorFrameThenDONE_ProducerStops`
(`internal/text/generic/decode_error_test.go`) — an `httptest` SSE server
writes an error frame then `data: [DONE]` and holds the connection open;
the consumer mirrors the session runner (stops after the error event) and
asserts the channel closes promptly. Pre-fix the test failed (`expected
channel close after the error event, got event: models.StopEvent`); post-
fix it passes under `-race -count=3`.

Gates (all 2026-09-05, repo root): `go test ./internal/text/generic/
-race -cover -count=3 -timeout=60s` exit 0 (76.6%); non-root full suite
`go test $(go list ./... | grep -v '^github.com/baalimago/clai$') -race
-cover -count=3 -timeout=30s` exit 0 (root-package pty e2e excluded per
the recorded R3-03 flake); gofumpt clean; `go vet ./...` exit 0;
`go run honnef.co/go/tools/cmd/staticcheck@latest ./...` exit 0;
`go fix ./...` exit 0; dupl (phase-4 exclusion method) 33 full file set /
5 production-only — unchanged. Phase 4 status board row flipped back to
Complete; D18 records the producer-stop decision.

### 2026-09-05 — R3-02 closure, phase 5 (clai worker, reopen session)

Selected phase 5's R3-02 as the next eligible reopened phase (the remaining
Medium from review 3; R3-03 is Low and non-blocking). Implemented the reviewed
fix at both non-generic boundaries: the `client.Do` and mid-stream read
failures in `internal/vendors/anthropic/claude_stream.go` and
`internal/vendors/openai/responses_stream.go` now return/send
`claierr.NewTransport(cause)` instead of context-only `fmt.Errorf` wraps, so a
consumer's `errors.Is(err, claierr.ErrTransport)` matches anthropic and
openai-responses exactly as it already matched the 11 generic-riding vendors.
Parse/handle failures stay plain, as the finding allows.

Test-first: five boundary tests were added before the fix and failed on the
recorded plain wraps; each drives the real completer and asserts
`errors.Is(err, claierr.ErrTransport)`, `errors.As` to
`*claierr.TransportError` with a reachable cause, and the cause via
`errors.Is` — `Test_Anthropic_DoFailure_ErrTransport`,
`Test_Anthropic_CountTokensDoFailure_ErrTransport`,
`Test_Anthropic_ReadFailure_ErrTransport`,
`Test_OpenAIResponses_DoFailure_ErrTransport` and
`Test_OpenAIResponses_ReadFailure_ErrTransport`. The read-failure tests drop
the connection mid-stream (httptest `Content-Length` larger than the body) so
the cause is `io.ErrUnexpectedEOF`, mirroring phase 4's generic-boundary test.

One scope note recorded in the phase file: anthropic's pre-flight
`CountInputTokens` request performs its own `client.Do` on the real
anthropic host, so its connection failure was a third untyped `client.Do`
crossing the boundary (beside the finding's two); it rides the same fix
rather than remaining a known gap.

Gates (all 2026-09-05, repo root): `go test ./internal/vendors/anthropic/
./internal/vendors/openai/ -race -cover -count=3 -timeout=60s` exit 0
(anthropic 75.0%, openai 73.8%); dependents `go test ./internal/text/...
./pkg/... -race -count=1 -timeout=120s` exit 0; non-root full suite
`go test $(go list ./... | grep -v '^github.com/baalimago/clai$') -race
-cover -count=3 -timeout=30s` exit 0 (root-package pty e2e excluded per
the recorded R3-03 flake); gofumpt clean; `go vet ./...` exit 0;
`go run honnef.co/go/tools/cmd/staticcheck@latest ./...` exit 0;
`go fix ./...` exit 0; dupl (phase-4 exclusion method) unchanged. Phase 5
status board row flipped back to Complete; the R3-02 finding row records the
closure. No open Medium/High/Blocker finding remains — the worklog's next
eligible work is none (R3-03 wording stays Low, applied when phase 9 is next
touched).

### 2026-09-05 — Holistic review, final consistency pass (imago worker, session after R3-02)

No next-eligible phase exists, so this session re-ran the closing review
against the final working tree and closed the worklog's last open items.

Commands re-run and results (repo root, 2026-09-05):

- `go build ./...` — exit 0.
- `go run mvdan.cc/gofumpt@latest -l .` — clean (no output).
- `go vet ./...` — exit 0.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0.
- `go fix ./...` — exit 0.
- `go test ./internal/... ./pkg/... -race -cover -count=3 -timeout=30s` —
  exit 0 for all 44 non-root packages; coverage matches the recorded close
  (pkg/claierr 100.0%, pkg/agent 94.2%, internal/text 81.8%, internal/text/
  generic 76.6%).
- Root package `go test . -race -cover -count=1 -timeout=180s` — exit 0,
  21.4s, 55.2%.
- Root package `go test . -race -count=3 -timeout=30s` — FAIL at the 30s
  timeout with the recorded pty-e2e goroutine dump (this run hung inside the
  e2e suite's repeated runs; earlier sessions recorded different hung tests
  — `Test_e2e_setup_macro_select_category_quit`,
  `Test_e2e_setup_announcement_survives_interactive_wizard`,
  `Test_e2e_replay_loads_theme/dir-replay`). Reproduces R3-03 exactly; no
  root-package file is modified by this worklog.
- dupl, phase-4 exclusion method — 33 clone groups full file set, 5
  production-only (matches the phase-9 close).

Read the code rather than the notes: `pkg/claierr` (ten sentinel/type/
constructor pairs, `APIError`/`APIErrorer`, never-nil facts via `orZero`);
`generic.ResponseError` vendor-first/baseline/catch-all plus both decode
points on the one `DecodeError` hook; the producer loop returns after a
transport send, a `StopEvent`, and any terminal `error` event (D18); the
runner's `case error:` `%w` wrap with the `io.EOF`/`context.Canceled`
normal-end branches; the four vendor decoders and the anthropic 429-header
`NewRateLimited` branch; the openai Responses failure-event dispatch;
`client.Do` and mid-stream read failures returning/sending
`claierr.NewTransport` on anthropic and openai-responses (R3-02); the
retry-loop removal (grep over `internal/` and `pkg/` returns nothing for any
removed symbol); the D15 alias deletion (`models.ErrRateLimit` appears only
in docs); the MCP explicit/ambient `Setup` split and the manager report
channel; the chat-handler `errors.Is` conversion; the acceptance-test names
from the phase 4/5/6/8 tables spot-checked present; the production
substring-matcher grep returns nothing (test-only hits are assertion
helpers, recorded interpretation H1).

Three Low findings from this pass, all closed by applying — details in the
phase-9 Review findings:

- R4: `internal/models/models.go`'s `CompletionEvent` doc comment and
  `architecture/errors.md`'s channel-contract section predate D18 and
  omitted the producer-side stop rule; both now state it. No production code
  changed.
- R5: `architecture/errors.md`'s Reference cited the decision register as
  "D1–D17"; it now reads D1–D19 (the decision log holds D18 and D19). No
  production code changed.
- R3-03 closure: the phase-9 acceptance-criterion row now carries the
  qualified gate wording (44 non-root packages green under `-count=3`; root
  package passes under `-count=1`); README definition-of-success item 6
  already carried it.

No Blocker, High, or Medium finding remains; every status-board row stays
Complete or Removed, and the worklog's next eligible work is none.

### 2026-09-05 — Review 4 (independent implementation review; clai)

Independent verification against the working tree: re-ran the gates and read
the code rather than the notes.

Commands re-run and results (repo root, 2026-09-05):

- `go build ./...` — exit 0.
- `go run mvdan.cc/gofumpt@latest -l .` — clean (no output).
- `go vet ./...` — exit 0.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0.
- `go fix ./...` — exit 0.
- `go test ./internal/... ./pkg/... -race -cover -count=3 -timeout=30s` —
  exit 0 for all 44 non-root packages; coverage matches the recorded close
  (pkg/claierr 100.0%, pkg/agent 94.2%, internal/text 81.8%, internal/text/
  generic 76.6%).
- Root package `go test . -race -count=1 -timeout=180s` — exit 0 (21.3s),
  reproducing the R3-03 root-package `-count=3` qualification.
- dupl, phase-4 exclusion method — production files alone: 5 clone groups
  (internal/photo vs internal/video; two internal/setup pairs; a pkg/tools
  pair; anthropic vs pi source_reader pairs).

Read the code rather than the notes: `pkg/claierr` (ten sentinel/type/
constructor pairs, `APIError`/`APIErrorer`, never-nil facts via `orZero`,
`TransportError`/`McpServerStartupError` slice-`Unwrap`);
`generic.ResponseError` vendor-first/baseline/catch-all plus both decode
points on the one `DecodeError` hook; the generic producer loop returns after
`StopEvent`, any chunk-handler `error`, and a transport send; the four vendor
decoders key on their documented signals and build through constructors; the
openai Responses producer returns on every parse/handle/transport error path;
the runner's `case error:` `%w` wrap with the `io.EOF`/`context.Canceled`
normal-end branches; the D15 alias deletion (`models.ErrRateLimit` appears
only in docs); the MCP explicit/ambient `Setup` split; the S8 finalizer join;
the chat-handler `errors.Is` conversion; `architecture/errors.md` covers every
`pkg/claierr` export and cites D1–D19 (R5 closure).

Verdict: not ready to sign off. The vocabulary, decode chain and vendor
mappings are correct and lean, but one Medium finding (R4-01) leaves the
producer-side terminal-error stop rule unmet in the anthropic producer loop —
the R3-01 leak class in a producer the review-3 cross-cutting invariant and
`architecture/errors.md` claim to cover — and one Low finding (R4-02) records
a pre-existing context-cancel signal mismatch. Phase 6 is reopened; the fix is
small and local.

### 2026-09-05 — Review 5 (independent implementation review; clai)

Reviewed the R4-01/R4-02 fix found in the working tree (it landed after
review 4 with no worklog record — R5-01). Re-ran the gates and read the code
rather than the notes.

Commands re-run and results (repo root, 2026-09-05):

- `go build ./...` — exit 0.
- `go vet ./...` — exit 0.
- `go run mvdan.cc/gofumpt@latest -l .` — clean (no output).
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0.
- `go fix ./...` — exit 0.
- `go test ./internal/... ./pkg/... -race -cover -count=3 -timeout=30s` —
  exit 0 for all 44 non-root packages; coverage matches the recorded close
  (pkg/claierr 100.0%, pkg/agent 94.2%, internal/text 81.8%, internal/text/
  generic 76.6%; internal/vendors/anthropic rose to 75.6% with the new
  regression test).
- Root package `go test . -race -cover -count=1 -timeout=180s` — exit 0
  (8.7s, 55.2%), per the R3-03 qualification.
- dupl, phase-4 exclusion method — 33 clone groups full file set, 5
  production-only (matches the phase-9 close; the fix added no clones).

Verification beyond the gates: reverted the R4-01 condition in place and
confirmed `TestClaudeStream_StopsAfterNonEOFErrorSend` fails with the exact
leak (`channel not closed after the error event: producer goroutine
leaked`), then restored the fix and confirmed it passes — the test bites at
the real boundary. Re-traced the producer-stop invariant through every
branch of all four producer loops (enumeration in the phase-6 Review
findings): every terminal error send is followed by a return in anthropic,
generic, and openai-responses; the mock sends no errors. Confirmed the
R4-02 resolution is sound on every path: the session runner is the only
consumer of a completions channel, and both its channel-close branch and
its `<-ctx.Done()` case end the step normally, so removing the cancel-path
emit leaves no consumer behind. `CompletionEvent`'s doc comment and
`architecture/errors.md`'s "every producer loop" claim are now factually
true.

Verdict: **ready**. Both review-4 findings are resolved and verified; the
producer-side terminal-error stop rule (D18) now holds on every producer
loop. One new Low finding (R5-01: the fix session's missing worklog record)
is closed by this review's own recording. No open finding of any severity
remains; every status-board row is Complete or Removed, and the worklog's
next eligible work is none. The standing gate qualification (R3-03: root
pty-e2e flake under `-count=3`, passes `-count=1`) is environmental and
unchanged.

## Review feedback

### Severity taxonomy

| Severity | Meaning                                                                        |
| -------- | ------------------------------------------------------------------------------ |
| Blocker  | Contract cannot be implemented as written; execution must not start            |
| High     | Implemented literally, produces failing acceptance criteria or unsafe behavior |
| Medium   | Incorrect rationale or unaddressed edge that bites in realistic use            |
| Low      | Documentation/consistency nit; non-blocking                                    |

Reopen rule: Blocker, High and Medium must be resolved before their phase can
be marked Complete; Low findings are non-blocking.

### Feedback index

Design review, 2026-09-05 (pre-phase inspection; author-side findings, so
severities are not assigned — every item was closed before phase authoring):

| Finding | What it was                                                         | Closed by                                                                |
| ------- | ------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| F1      | `errors.Join` composition additive-only; cannot retract xAI's 403   | D11; Strategy "One vocabulary, many decoders" rewrite                    |
| F2      | Baseline table contradicted the probe journal                       | Baseline-table correction paragraph; D11                                 |
| F3      | Phases 2 and 5 both claimed the `ErrRateLimit` move                 | D15; transition paragraph; status board rows 2 and 5                     |
| F4      | Probe raw bodies not preserved                                      | D14 fixture policy; Validation policy exception                          |
| F5      | Frame-decode seam undefined                                         | D12; "The silent path" mechanics paragraph                               |
| F6      | Unmapped non-OK status with silent decoder returned nothing defined | `ErrUnexpectedProviderResponse` row; `responseError` chain in Strategy   |
| F7      | `ErrTransport` did not fit D10's `*APIError` shape                  | Vocabulary table row: no `APIError`, `Unwrap() []error{sentinel, cause}` |
| F8      | Vocabulary scattered across three places, no single owner register  | The vocabulary table — all names ship in phase 2                         |
| F9      | D6's mechanism undesigned                                           | D13                                                                      |
| F10     | Phase 8's S-site repairs were per-site behavior changes, no policy  | D16; swallowed-state repair table                                        |
| F11     | Phase 5 ignored the probe's xai/openrouter evidence                 | D14; status board row 5                                                  |
| F12     | Missing skill sections; status board row 2 mis-cited D4             | Definition of success, Validation policy, Readiness checklist; row 2     |

Consistency audit, 2026-09-05 (pre-sign-off; author-side, all closed in
place):

| Finding | What it was                                                                                                                                                             | Closed by                                                                                            |
| ------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| G1      | Vendor counts wrong against the code: "17 vendor directories" (13), "12 embedding generic" (11), "17 vendors send into `CompletionEvent`" (four producer loops)         | "The three text boundaries", "The channel contract" rewrite; D8 rationale; rejected-alternatives row |
| G2      | Phase 1 Finding 4: "only one production site sends an error into a channel" — three producers do (generic `:132`; responses `:250,259,270`; anthropic via `emitClaude`) | Producer set named in Strategy as phase 6's bound actors; phase 1 left as historical record          |
| G3      | Type `McpStartupError` broke the sentinel/type pairing pattern (`ErrMcpServerStartup`)                                                                                  | Renamed `McpServerStartupError` — vocabulary table, D13, definition of success item 3                |
| G4      | Definition of success item 5 asserted `claierr.ErrRateLimited` while phases 2 and 3 are order-independent                                                               | Rewritten against the alias-stable `models.ErrRateLimit` name (D15)                                  |
| G5      | Phase 9 created `architecture/errors.md` without the `architecture/README.md` index row the repo's doc convention requires                                              | Status board row 9                                                                                   |

### Findings index

Close-out review, 2026-09-05 (phase-9 holistic pass; two Low findings,
both closed — details in the phase-9 Review findings):

| Finding | What it was                                                                                | Closed by                                     |
| --- | --- | --- |
| H1 | Acceptance grep over `strings.Contains(err.Error(), …)` matches `_test.go` assertion helpers by design | Recorded interpretation: the criterion targets production control-flow matching (zero there) |
| H2 | Survey-baseline dupl row (28) unreproducible at its own measurement point; register had drifted (31/30/33) | README table row annotated with corrected readings (phase-9 close: 33 full file set, 5 production files) |

Follow-up holistic pass, 2026-09-05 (post-phase-9 worker session; two Low
findings, both closed — details in the phase-9 Review findings):

| Finding | What it was                                                                                | Severity | Closed by                                     |
| --- | --- | --- | --- |
| R1 | Staged/unstaged index drift: the index copy of `internal/vendors/openai/sora.go` still holds the pre-B7 gofmt-dirty branch while the worktree is clean (several `MM` files) | Low | Recorded; refresh the index (`git add -A`) before the eventual commit — QA gates read the worktree, which is clean |
| R2 | `architecture/query.md` and `architecture/streaming.md` still described the phase-3-removed retry loop by its deleted `models.ErrRateLimit` name | Low | Applied: both passages now state the typed-terminal contract and point at `architecture/errors.md` |

Independent review, 2026-09-05 (review 3; two Medium reopen phases 4 and 5,
one Low non-blocking — details in the owning phase files):

| Finding | What it was | Severity | Tracked in |
| --- | --- | --- | --- |
| R3-01 | The generic producer loop stops after `StopEvent` but not after a terminal `error` event, so an error frame followed by `[DONE]` (or any further data) strands the producer goroutine and holds the response body open until context cancellation | Medium | [phase 4](./phase-4-generic-decoding.md), reopened → **closed 2026-09-05** (D18): the generic producer stops on any chunk-handler `error` |
| R3-02 | `claierr.ErrTransport` is produced only at the generic boundary; anthropic and openai-responses keep plain `fmt.Errorf` wraps for `client.Do` and mid-stream read failures, so a consumer matching `ErrTransport` misses those boundaries | Medium | [phase 5](./phase-5-vendor-mappings.md), reopened → **closed 2026-09-05**: `client.Do` and mid-stream read failures on both boundaries return/send `claierr.NewTransport(cause)`; five boundary tests pin `errors.Is`/`errors.As` (scope note: anthropic's pre-flight `CountInputTokens` `client.Do` rides the same fix) |
| R3-03 | `make qa` / `go test ./... -race -count=3 -timeout=30s` does not exit zero: the root package's pty e2e suite times out under `-count=3` (passes `-count=1`); phase 9's "`make qa` exits zero" criterion and README definition-of-success item 6 are not reproducible as written | Low | [phase 9](./phase-9-errors-doc-and-gates.md), closed 2026-09-05 (holistic final pass): acceptance-criterion row 1 amended to the qualified wording; README item 6 already carried it |

Holistic final pass, 2026-09-05 (post-R3-02 worker session; three Low
findings, all closed by applying — details in the phase-9 Review findings):

| Finding | What it was                                                                                | Severity | Closed by                                     |
| --- | --- | --- | --- |
| R4 | `internal/models/models.go`'s `CompletionEvent` doc comment and `architecture/errors.md`'s channel-contract section predate D18 and omit the producer-side stop rule (a producer that sends a terminal error must stop reading right after the send) | Low | Applied: both documents now state the rule; no production code changed |
| R5 | `architecture/errors.md`'s Reference cited the decision register as "D1–D17" while the worklog's decision log had grown to D1–D19 | Low | Applied: the reference now reads D1–D19; no production code changed |

Review 4, 2026-09-05 (independent implementation review; one Medium reopens
phase 6, one Low non-blocking — details in the phase-6 Review findings):

| Finding | What it was | Severity | Tracked in |
| --- | --- | --- | --- |
| R4-01 | The anthropic producer loop stops after `io.EOF` and function calls but not after other terminal `error` sends (`handleToken` parse errors), so a malformed event line followed by more frames strands the producer goroutine and holds the response body open until context cancellation — the R3-01 leak class, contradicting D18's "by error presence" rule and `architecture/errors.md`'s "every producer loop" claim | Medium | [phase 6](./phase-6-channel-contract.md), reopened → **closed 2026-09-05, verified by review 5**: the producer stops on any `error` send (`isErr \|\| isFunctionCall`); `TestClaudeStream_StopsAfterNonEOFErrorSend` pins it and was verified to fail against the pre-fix condition |
| R4-02 | Anthropic's context-cancel event (`errors.New("context cancelled")`) does not satisfy the pinned `context.Canceled` signal, so a cancelled context can surface as a terminal error to the caller (pre-existing, not touched by this worklog) | Low | [phase 6](./phase-6-channel-contract.md), non-blocking → **closed 2026-09-05, verified by review 5**: the cancel branch emits nothing and returns; the channel close ends the step normally (the runner is the sole channel consumer) |

Review 5, 2026-09-05 (independent implementation review of the R4-01/R4-02
fix; one Low, closed in place — details in the phase-6 Review findings):

| Finding | What it was | Severity | Closed by |
| --- | --- | --- | --- |
| R5-01 | The R4-01/R4-02 fix session landed code and tests but recorded nothing in the worklog: phase-6 findings unchecked, status board still `Reopened (review 4)`, no journal entry — a stale completion status in reverse (stale *reopened* status), which would have sent the next contributor to re-fix closed findings | Low | Review 5's own recording: phase-6 Review findings section, board row 6, this index, and the journal entry below |
