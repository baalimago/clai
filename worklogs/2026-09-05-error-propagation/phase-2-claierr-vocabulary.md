# Phase 2 — `pkg/claierr`, the shared vocabulary

**Status:** Complete
[Worklog README](./README.md)

## Goal

Ship the complete, closed error vocabulary — every row of the README's
vocabulary table, as sentinel + type + constructor — and absorb
`models.ErrRateLimit` behind an internal alias (D15) so the vocabulary is
closed in a single commit.

## Specification

New package `pkg/claierr`, importing the standard library only. Every name
comes from the README's vocabulary table ("The vocabulary table — single
source of names and owners"); this phase ships all of them, including the
evidence-gated ones whose wire mapping arrives later. No decoding logic
lives here — this package defines meanings, not mappings.

### Shape

Exactly the README's "Shape of the vocabulary":

- One exported sentinel per meaning (`errors.New` value), for `errors.Is`.
- One exported type per meaning, for `errors.As`. Each type that carries
  response facts embeds `*APIError` by pointer; its `Unwrap()` returns the
  meaning's sentinel; it implements `APIErrorer` via `API() *APIError`.
- `APIError` holds `StatusCode`, `ProviderCode`, `Body`. It is a facts
  struct, **not** an error — it has no `Error()` method, so `errors.As`
  against it cannot compile and facts flow only through a typed error or
  the `APIErrorer` interface.
- `APIErrorer` is `interface{ API() *APIError }`, so a logging path reads
  facts without knowing the meaning.
- The two non-API meanings follow the README table instead:
  `TransportError` and `McpServerStartupError` carry no `APIError`; each
  wraps its cause and its `Unwrap() []error` returns both its sentinel and
  the cause. `McpServerStartupError` additionally carries the server name
  and startup stage as fields.
- `RateLimitedError` carries `ResetAt`, `TokensRemaining`,
  `MaxInputTokens` alongside the embedded facts — the fields absorbed from
  `models.ErrRateLimit`, intact.

### Constructors, not literals

Every type gets an exported constructor (`NewRateLimited`,
`NewInsufficientCredits`, `NewAuthFailed`, `NewModelNotFound`,
`NewProviderUnavailable`, `NewTransport`,
`NewUnexpectedProviderResponse`, `NewContextLengthExceeded`,
`NewContentFiltered`, `NewMcpServerStartup`). The embedded `*APIError`
must never be nil (README constraint): a constructor handed a nil facts
pointer substitutes a zero-valued `APIError` rather than storing nil.
Decoders and repairs in later phases build errors only through these
constructors.

`Error()` strings are human-readable and include the status and provider
code where present. Their wording is not a contract (D17) and no test may
pin it beyond what this phase's own tests assert.

Composition is by `errors.Join` (D2); nothing in this package nests one
meaning inside another, and constructors return the typed value itself,
never a `fmt.Errorf` wrap of it.

### The absorption (D15)

`internal/models` keeps compiling and matching:

- `models.ErrRateLimit` becomes `type ErrRateLimit = claierr.RateLimitedError`
  (a type alias, so the existing `errors.As(err, &rateLimitErr)` with
  `*models.ErrRateLimit` in `internal/text` continues to compile and
  match with no edit).
- `models.NewRateLimitError` delegates to `claierr.NewRateLimited`,
  passing facts with `StatusCode` set to `http.StatusTooManyRequests`, so
  the never-nil invariant holds for every value it produces.
- `internal/models/errors_test.go` is amended to pin the alias: same
  field-parity assertions through the delegating constructor, plus
  `errors.Is(err, claierr.ErrRateLimited)` now matching. (Phase 5 deletes
  this file with the alias — see README, D15.)

### Invariants

| Invariant | Mechanism | Test |
| --- | --- | --- |
| Every vocabulary-table row has a sentinel, a type, and a constructor, and `errors.Is(New…(…), sentinel)` holds for each | table-driven over every row | `Test_Claierr_EveryTypeUnwrapsToItsSentinel` |
| A meaning never matches a sentinel it does not carry (a bare payment-required error matches `ErrLikelyInsufficientCredits`, not `ErrRateLimited`) | distinct sentinel values; `Join`, never nesting | `Test_Claierr_MeaningsDoNotCrossMatch` |
| The embedded `*APIError` is never nil on a constructor-built value | constructors substitute a zero-valued `APIError` | `Test_Claierr_ConstructorsNeverNilFacts` |
| `errors.Is` and `errors.As` survive `errors.Join` plus two layers of `%w` | stdlib unwrap semantics, verified here rather than assumed | `Test_Claierr_SurvivesJoinAndDoubleWrap` |
| Facts are readable without knowing the meaning | `APIErrorer` implemented by every facts-carrying type | `Test_Claierr_APIErrorerExposesFacts` |
| A joined error falls through a type switch (the documented consumer trap) | `errors.Join` returns its own concrete type | `Test_Claierr_JoinedErrorTypeSwitchFallsThrough` |
| The alias keeps old matching compiling and matching | `type ErrRateLimit = claierr.RateLimitedError` | amended `TestRateLimitError` |

### Files

- `pkg/claierr/claierr.go` — the package (may be split across files within
  the package at the executor's discretion).
- `pkg/claierr/claierr_test.go` — every test above except the alias pin.
- `internal/models/models.go` — alias and delegating constructor.
- `internal/models/errors_test.go` — amended alias pin.

## Integration contract

`unit-test-only`. This is a pure value package; its cross-package behavior
(alias compatibility) is proven by compilation of the unchanged
`internal/text` matching sites plus the amended alias pin.

## Acceptance criteria

| Outcome | Evidence |
| --- | --- |
| Every vocabulary-table row exists and unwraps to its sentinel | `Test_Claierr_EveryTypeUnwrapsToItsSentinel` |
| Joined meanings each match, non-carried meanings do not | `Test_Claierr_MeaningsDoNotCrossMatch` |
| Facts reachable via concrete type and via `APIErrorer` | `Test_Claierr_APIErrorerExposesFacts` |
| Matching survives join-plus-double-wrap | `Test_Claierr_SurvivesJoinAndDoubleWrap` |
| `models.NewRateLimitError` values still satisfy the existing `errors.As` site and now also `errors.Is(…, claierr.ErrRateLimited)` | amended `TestRateLimitError`; `go build ./...` with `internal/text` unedited |
| `pkg/claierr` imports the standard library only | `go list -f '{{ join .Imports "\n" }}' ./pkg/claierr` shows no module-internal or third-party path |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Constructor handed a nil `*APIError` | non-nil zero-valued facts; reading a promoted field does not panic | `Test_Claierr_ConstructorsNeverNilFacts` |
| Consumer type-switches a joined error | falls to `default`; `errors.Is`/`errors.As` remain the documented discrimination | `Test_Claierr_JoinedErrorTypeSwitchFallsThrough` |
| Typed error wrapped in context by a caller (`fmt.Errorf("…: %w", err)`) | sentinel and type still match through the wrap | `Test_Claierr_SurvivesJoinAndDoubleWrap` |

## Implementation notes

phase-2 worker subagent, 2026-09-05. Deltas from the specification only:

- `API() *APIError` is defined once on `*APIError` and promoted into every
  facts-carrying type, instead of eight identical per-type methods. The
  spec's contract ("implements `APIErrorer` via `API() *APIError`") holds
  for every type; `APIError` itself incidentally satisfies `APIErrorer`,
  which is harmless since it is not an error.
- `NewUnexpectedProviderResponse` takes `(statusCode int, body []byte)`
  per the README's `responseError` chain, not a `*APIError` — it builds
  its own facts, so its never-nil invariant holds by construction and it
  is exercised in `Test_Claierr_ConstructorsNeverNilFacts` with a nil
  body.
- `NewRateLimited` parameter order follows the README example
  (`api, resetAt, remaining, max`); `models.NewRateLimitError` keeps its
  historical `(resetAt, maxInputTokens, tokensRemaining)` order and
  reorders when delegating — the anthropic call site compiles unedited.
- `Error()` is nil-receiver-safe (`describe` guards a nil `*APIError`), so
  a literal-built value degrades to the bare meaning instead of panicking.
  Needed in practice: `internal/text/session_runner_test.go` and
  `querier_test.go` build `models.ErrRateLimit` literals with no facts
  pointer, and they must keep passing unedited. Pinned by
  `Test_Claierr_ErrorIsNilSafeOnLiteralBuiltValue` (a test beyond the
  spec's table, added for this reason).
- The old `TestRateLimitError` pinned two `Error()` substrings; the
  amended pin asserts non-empty only, per this phase's "wording is not a
  contract" rule.
- `Test_Claierr_TransportAndMcpUnwrapSentinelAndCause` (also beyond the
  spec's table) pins the `Unwrap() []error{sentinel, cause}` shape for
  the two non-API meanings, since no invariant row covered the
  cause-matching half.

Verification (all run 2026-09-05, all pass):

- `go run mvdan.cc/gofumpt@latest -w -l .` — no output (nothing to
  reformat)
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — clean
- `go vet ./...` — clean
- `go test ./... -race -cover -count=3 -timeout=30s` — ok, 45 packages;
  `pkg/claierr` coverage 100.0%, `internal/models` 100.0%,
  `internal/text` 81.4% (unedited)
- `go fix ./...` — clean
- `go run github.com/mibk/dupl@latest -t 80 .` — 28 clone groups
  (baseline: 28)
- `go list -f '{{ join .Imports "\n" }}' ./pkg/claierr` — `errors`,
  `fmt`, `time` only
- `git status --porcelain` — only `internal/models/{models.go,
  errors_test.go}` modified plus new `pkg/claierr/`; `internal/text`
  untouched, proving the alias-compatibility contract by compilation.

## Review findings

None.
