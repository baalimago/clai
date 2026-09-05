# Phase 5 — vendor mappings

**Status:** Complete
[Worklog README](./README.md)

## Goal

Give each covered vendor its `decodeError` function (openai — Responses and
chat-completions —, anthropic, deepseek, xai, openrouter, per D14), rewire
the two non-generic boundaries onto `generic.ResponseError`, and delete the
`ErrRateLimit` alias (D15).

## Specification

Depends on phase 4 (the seam and the exported chain). Every decoder builds
through `pkg/claierr` constructors, returns nil for anything it does not
recognize (the baseline then stands, D11), and — the D11 obligation — when
it does recognize a payload, states the **complete** meaning set itself.

### Fixture policy (D14)

Decoder tests run against checked-in fixtures under each vendor package's
`testdata/`. Fixtures reconstructed from the journal-recorded live shapes
(journal, "Live vendor probe") carry a comment or sibling note labeling
them as reconstructions, not raw captures. No test may require a live
provider call (Validation policy).

### Evidence gate (D9)

The evidence-gated vocabulary rows — `ErrContextLengthExceeded`,
`ErrContentFiltered`, and model-not-found arriving as 400 — ship a mapping
**only** with a current provider-doc citation recorded in this phase's
Implementation notes. A signal that cannot be verified is left unmapped and
the baseline stands; that is a recorded outcome, not a failure of this
phase.

### Per-vendor work

| Vendor | Boundary | Mapping |
| --- | --- | --- |
| openai (chat-completions) | `DecodeError` set where `internal/vendors/openai/gpt.go` builds its `generic.StreamCompleter` | 429 with provider code `insufficient_quota` → `errors.Join(NewRateLimited(…), NewInsufficientCredits(…))`, facts shared (the README composition example). Source: provider docs (D9), cited in Implementation notes |
| openai (Responses) | `internal/vendors/openai/responses_stream.go` | the non-OK path (today a formatted string, README "Motivation") routes through `generic.ResponseError` with the same openai decoder; **audit** the typed-event dispatch for an error / `response.failed` event and, if the API defines one, decode it to a vocabulary error on the channel (event names verified against current docs, D9) |
| anthropic | `internal/vendors/anthropic/claude_stream.go` | the 429 header-parsing branch constructs via `claierr.NewRateLimited` directly, facts populated from status and headers (`ResetAt`, `TokensRemaining`, `MaxInputTokens` intact); every other non-OK falls to `generic.ResponseError` with an anthropic decoder (nil until evidence per D9) instead of the current flattened error |
| deepseek | `DecodeError` on its embedded completer | 402 → `NewInsufficientCredits`, `ProviderCode`/`Body` facts from the body (journal: the body's `code` and `type` fields are useless for decoding; the decoder keys on status within the vendor and preserves the body as facts) |
| xai | `DecodeError` on its embedded completer | 403 whose body is the drained-credits shape (journal) → `NewInsufficientCredits` **alone** — the baseline's `ErrAuthFailed` guess must not fire (D11). A 403 with any other body → nil, so the baseline's auth meaning stands |
| openrouter | `DecodeError` on its embedded completer | 402 (journal: `metadata.limit_source` credits shape, numeric `code`) → `NewInsufficientCredits` with facts |

### The alias deletion (D15)

With anthropic rewired, nothing constructs `models.ErrRateLimit`:

- delete the alias and delegating constructor from
  `internal/models/models.go`;
- delete `internal/models/errors_test.go`;
- rewrite the remaining `*models.ErrRateLimit` match sites (the
  `internal/text` `errors.As` sites left after phase 3) to
  `*claierr.RateLimitedError`.

### Files

- `internal/vendors/openai/` — decoder (new file at executor's discretion),
  `gpt.go`, `responses_stream.go`, tests, `testdata/`
- `internal/vendors/anthropic/claude_stream.go`, tests, `testdata/`
- `internal/vendors/deepseek/`, `internal/vendors/xai/`,
  `internal/vendors/openrouter/` — decoder, tests, `testdata/` each
- `internal/models/models.go`, `internal/models/errors_test.go` (deletions)
- `internal/text/` — the rewritten match sites
- `internal/vendors/shared_test.go` — the cross-vendor matcher test

## Integration contract

Each scenario drives the vendor's real completer against an `httptest`
server returning a fixture, and asserts on the returned or channel-borne
error.

| Trigger | Collaborators / fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| openai chat fixture: 429, body with `insufficient_quota` | httptest + openai completer | error matches **both** `ErrRateLimited` and `ErrLikelyInsufficientCredits`; one shared facts allocation | none | baseline backfilling either meaning |
| openai Responses fixture: non-OK status | httptest + responses reader | typed vocabulary error, not the formatted string | none | string-matching-dependent behavior |
| anthropic fixture: 429 with rate-limit headers | httptest + Claude completer | error matches `ErrRateLimited`; `errors.As` yields `ResetAt`, `TokensRemaining`, `MaxInputTokens` from the headers | none | the retry loop reappearing in any form |
| anthropic fixture: non-OK outside 429 | httptest + Claude completer | baseline vocabulary error per the README baseline table | none | a flattened untyped error |
| deepseek fixture: 402 drained-balance body | httptest + deepseek completer | `ErrLikelyInsufficientCredits` with body facts | none | — |
| xai fixture: 403 drained-credits body | httptest + xai completer | `ErrLikelyInsufficientCredits` and **not** `ErrAuthFailed` | none | any auth meaning on the drained fixture |
| xai fixture: 403 unrecognized body | httptest + xai completer | `ErrAuthFailed` via the baseline | none | credits meaning |
| openrouter fixture: 402 credits body | httptest + openrouter completer | `ErrLikelyInsufficientCredits` with facts | none | — |
| every credits fixture above, one matcher | the four vendor completers | a single `errors.Is(err, claierr.ErrLikelyInsufficientCredits)` matches all four (Definition of success, first item) | none | per-vendor matching knowledge |

## Acceptance criteria

| Outcome | Evidence |
| --- | --- |
| One `errors.Is` catches every vendor's spelling of insufficient credits | `Test_AllVendors_InsufficientCredits_OneMatcher` (`internal/vendors/shared_test.go`) |
| openai 429-plus-quota joins both meanings, decoder states the complete set | `Test_OpenAIDecode_InsufficientQuota_JoinsBothMeanings` |
| openai Responses non-OK path speaks the vocabulary; error-event audit recorded | `Test_OpenAIResponses_NonOK_DecodesVocabulary`; audit outcome + doc citations in Implementation notes (and `Test_OpenAIResponses_ErrorEvent_TypedOnChannel` if the API defines the event) |
| anthropic rate limit carries its facts through `claierr` | `Test_AnthropicDecode_RateLimit_CarriesResetFacts` |
| anthropic non-429 failures stop being flattened | `Test_AnthropicDecode_NonOK_Baseline` |
| deepseek, xai, openrouter fixtures decode per the table | `Test_DeepseekDecode_InsufficientBalance`, `Test_XaiDecode_DrainedCredits_NotAuthFailed`, `Test_OpenrouterDecode_CreditsExhausted` |
| The alias is gone | `grep -rn "models.ErrRateLimit\|NewRateLimitError" --include='*.go' internal/ pkg/` returns nothing |
| Evidence-gated rows mapped only with citations, else recorded unmapped | Implementation notes carry the citation or the "left unmapped" record per row |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Vendor body malformed or unrecognized | decoder returns nil; baseline stands | `Test_XaiDecode_DrainedCredits_NotAuthFailed` (unrecognized-body case), per-vendor nil-return cases in the decode tests |
| Drained xai account (403) | credits meaning alone, no auth page-out | `Test_XaiDecode_DrainedCredits_NotAuthFailed` |
| Provider signal exists but docs unverifiable (D9) | mapping omitted; baseline covers; outcome recorded | Implementation notes record, per gated row |

## Implementation notes

**Session:** phase-5 worker subagent, 2026-09-05. Deltas from the
specification only; the spec itself is not restated.

### D9 audit outcomes, per gated signal

- **`insufficient_quota` (the required openai mapping) — mapped.**
  OpenAI's current error-codes guide
  (developers.openai.com/api/docs/guides/error-codes) confirms the code
  in current docs: "For billing-related errors, inspect `error.code` to
  identify the specific cause. The broader `error.type` can still be
  `insufficient_quota`." The 429 pairing and the
  `{"error": {message, type, code}}` envelope with both `type` and
  `code` set to `insufficient_quota` are corroborated by OpenAI's
  developer-community records of the wire shape (e.g.
  community.openai.com/t/429-error-insufficient-quota/492350). The
  decoder therefore recognizes the code wherever it appears and keys
  the rate-limit half of the join on the 429 status.
- **Responses failure events (`response.failed`, `error`) — audit
  complete, both defined, both decoded.** `response.failed` is defined
  in the current Responses streaming-events API reference
  (platform.openai.com/docs/api-reference/responses-streaming/response/failed):
  emitted when a response fails, error nested under `response.error`
  with `code`/`message` (documented example code `server_error`). The
  top-level `error` event is likewise defined
  (developers.openai.com/api/reference/resources/responses/streaming-events):
  type always `error`, fields `code`/`message`/`param`/
  `sequence_number` at the top level — with a community-documented
  discrepancy that the wire sometimes nests an `error` object instead
  (community.openai.com/t/responses-error-event-shape-is-wrong-between-documentation-and-sdks-and-reality/1358301),
  so the decoder reads both spellings. Both events now ride
  `generic.ResponseError(http.StatusOK, rawFrame, decodeError)`;
  `Test_OpenAIResponses_ErrorEvent_TypedOnChannel` pins the channel
  contract. `response.error` was kept in the dispatch case as a
  defensive alias (it predates this phase); it is not doc-defined and
  nothing is claimed for it.
- **`ErrContextLengthExceeded` — left unmapped.** The current official
  error-codes guide does not document `context_length_exceeded`; only
  community and third-party sources describe the 400-shaped signal.
  Fails the D9 gate; the baseline stands (a 400 degrades to the typed
  catch-all). No in-scope vendor doc verified either.
- **`ErrContentFiltered` — left unmapped.** No current official doc for
  an in-scope vendor defines a content-filter *error status* on these
  boundaries; OpenAI signals filtering via `finish_reason` /
  `response.incomplete` (a successful, truncated stream), which is not
  a terminal error state. Fails the D9 gate; nothing to map.
- **400-shaped model-not-found — left unmapped.** The journal
  attributes the 400 spelling to huggingface and inception, both
  outside D14's vendor set; no current provider doc was found tying a
  phase-5 vendor to a 400-shaped model-not-found. Fails the D9 gate;
  the 404 baseline row covers the in-scope vendors.

### Deviations and decisions made while implementing

- `internal/vendors/shared_test.go` moved from `package vendors` to the
  external `package vendors_test`: importing the openai package from an
  internal test file is an import cycle (openai → internal/photo/generic
  → internal/chat → internal/vendors). Same file, test names unchanged;
  the pre-existing tests now qualify `vendors.NormalizeToolCallSequence`.
- `responsesStreamEvent` gained an unexported `raw []byte` (set by
  `parseResponsesLine`) so failure events can hand the whole frame to
  the decode chain as facts — the Responses-boundary mirror of D12's
  frame semantics.
- A body-read failure on a non-OK Responses response now returns
  `claierr.NewTransport(err)` (previously a formatted string); there is
  no body to decode, and the read failure is transport.
- Interpretation: `insufficient_quota` arriving at any status other
  than 429 — in practice an error frame at `http.StatusOK` — decodes to
  `InsufficientCredits` alone. A frame carries no throttling evidence,
  and D11 requires the stated meaning set to be complete, not padded.
- Existing openai tests asserting the deleted formatted strings were
  rewritten to the typed contract (the provider message now travels in
  `APIError.Body` facts, not `Error()` wording):
  `TestResponsesStreamer_Non200Response`,
  `TestValidateResponsesHTTPResponse_Non200IsTypedWithBodyFacts` (was
  `…IncludesBody`), `TestHandleResponsesStreamEvent_FailedReturnsTypedError`
  (was `…ReturnsErrorMessage`),
  `TestHandleResponsesStreamEvent_FailedDecodesNestedResponseError`
  (was `…ReadsNestedResponseError`; now pins the nested
  `insufficient_quota` shape),
  `TestHandleResponsesStreamEvent_TopLevelErrorSurfacesTyped` (was
  `…SurfacesMessage`), and
  `TestResponsesStreamer_TopLevelErrorEventSurfaced` (assertions only).
- The anthropic decoder seat is a package-level
  `var decodeAnthropicError func(int, []byte) error` left nil, per the
  spec's nil-until-evidence wording, documented at the declaration.
- One comment-only edit outside the phase's file list:
  `pkg/claierr/claierr.go`'s absorbed-from note reworded so the
  acceptance grep (`models.ErrRateLimit|NewRateLimitError`) is clean —
  it matched a prose mention, not code.
- Fixture provenance notes live in a `testdata/README.md` per vendor
  (D14): every fixture is labeled a reconstruction — deepseek, xai,
  openrouter from the journal-recorded probe shapes; openai and
  anthropic from current provider docs (the probe never drained those
  accounts).

### Verification

- `go run mvdan.cc/gofumpt@latest -w -l .` — no output, exit 0.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0.
- `go vet ./...` — exit 0.
- `go fix ./...` — exit 0.
- `go test ./... -race -cover -count=3 -timeout=30s` — exit 0,
  44 packages ok, unedited flags.
- `make qa` — exit 0.
- Acceptance grep
  `grep -rn "models.ErrRateLimit\|NewRateLimitError" --include='*.go' internal/ pkg/`
  — returns nothing (exit 1).
- All acceptance-criteria tests pass by name:
  `Test_AllVendors_InsufficientCredits_OneMatcher` (all four vendor
  subtests), `Test_OpenAIDecode_InsufficientQuota_JoinsBothMeanings`,
  `Test_OpenAIResponses_NonOK_DecodesVocabulary`,
  `Test_OpenAIResponses_ErrorEvent_TypedOnChannel`,
  `Test_AnthropicDecode_RateLimit_CarriesResetFacts`,
  `Test_AnthropicDecode_NonOK_Baseline`,
  `Test_DeepseekDecode_InsufficientBalance`,
  `Test_XaiDecode_DrainedCredits_NotAuthFailed`,
  `Test_OpenrouterDecode_CreditsExhausted`.
- dupl, configured command over the working tree with the stray
  `.claude/worktrees/` copy excluded: **30 clone groups** (phase 4's
  same-method reading was 31 → delta −1). A naive `dupl -t 80 .`
  including the stray copy reads 50 and is not comparable. A clean
  worktree of HEAD (pre-worklog) reads 28, which reproduces phase 1's
  original record; the 31/28 discrepancy phase 3 reported appears to be
  tree-content, not command, drift.
- Coverage on new code: `decodeError` 100% in all four decoder
  packages; `providerErrorCode` 83.3%;
  `validateResponsesHTTPResponse` 87.5%. Touched packages: openai
  73.5%, anthropic 72.2%, deepseek 90.9%, xai 89.5%, openrouter 62.0%
  (package figure — openrouter's untested catalog/transcribe code
  predates this phase; its new decoder file is 100%).

## Review findings (review 3, 2026-09-05)

### R3-02 — Medium — `ErrTransport` is not produced on the two non-generic boundaries

The vocabulary table defines `ErrTransport` as "failed `client.Do`, mid-stream
read failure", and phase 4 wires it at the generic boundary
(`stream_completer.go:46` and `:133`). The two boundaries phase 5 rewired onto
`generic.ResponseError` only route *status* decoding through the shared chain;
their transport failures remain plain `fmt.Errorf` wraps:

- anthropic `claude_stream.go:49` — `fmt.Errorf("failed to do request: %w", err)`
- anthropic `claude_stream.go:125` — `fmt.Errorf("failed to read line: %w", err)`
- openai responses `responses_stream.go:208` — `fmt.Errorf("openai responses: do request: %w", err)`
- openai responses `responses_stream.go:254` — `fmt.Errorf("openai responses: read stream line: %w", err)`

A consumer writing `errors.Is(err, claierr.ErrTransport)` to detect a
connection failure therefore catches it for the 11 generic-riding vendors but
silently misses anthropic and openai-responses. That is a real gap in the
worklog's "typed, specific error for every terminal error state" promise, and
it contradicts `architecture/errors.md`'s "What stays untyped" list, which
does not name transport.

Fix:

- [x] Route the `client.Do` and mid-stream read failures at
      `internal/vendors/anthropic/claude_stream.go` and
      `internal/vendors/openai/responses_stream.go` through
      `claierr.NewTransport(cause)` (parse/handle errors can stay plain — they
      are clai-side, not provider transport).
- [x] Add/extend boundary tests asserting `errors.Is(err, claierr.ErrTransport)`
      on each of those four sites.
- [x] No site stays intentionally untyped, so `architecture/errors.md`'s
      "What stays untyped" list needs no transport entry.

### R3-02 closure (clai worker, 2026-09-05, reopen session)

Implemented per the checklist. The `client.Do` and mid-stream read failures at
both boundaries now return/send `claierr.NewTransport(cause)` directly, so the
consumer's `errors.Is(err, claierr.ErrTransport)` sees every boundary that
phase 4 wired at `generic` — no context-only `fmt.Errorf` between the cause
and the vocabulary. Parse/handle failures (unmarshal, tool-argument buffer,
event dispatch) stay plain wrapped errors, as the finding allows.

One scope note beyond the four enumerated sites: anthropic's
`CountInputTokens` performs its own `client.Do` against the count-tokens
endpoint on the real anthropic host before the stream starts
(`claude_stream.go`, reached from `StreamCompletions`), so its connection
failure also crosses the module boundary and was equally invisible to an
`ErrTransport` matcher. It is a `client.Do` failure within the same file the
finding names, so it rides the same fix rather than remaining a known gap.

Boundary tests (each drives the real completer, asserting `errors.Is(err,
claierr.ErrTransport)`, `errors.As` to `*claierr.TransportError` with a
reachable cause, and the cause via `errors.Is`):

- `Test_Anthropic_DoFailure_ErrTransport` — injected failing RoundTrip on the
  stream request's `client.Do`.
- `Test_Anthropic_CountTokensDoFailure_ErrTransport` — the same injection on
  the pre-flight count request (scope note above).
- `Test_Anthropic_ReadFailure_ErrTransport` — an `httptest` server declaring a
  `Content-Length` larger than its body, so the client read fails mid-stream
  with `io.ErrUnexpectedEOF`.
- `Test_OpenAIResponses_DoFailure_ErrTransport` — failing RoundTrip on
  `responsesStreamer.stream`'s `client.Do`.
- `Test_OpenAIResponses_ReadFailure_ErrTransport` — the `Content-Length` drop
  mid-stream on `readResponsesStream`.

All five failed pre-fix on the recorded plain wraps and pass post-fix under
`-race -count=3`. Full gates in the README journal entry for this session.

Verified good in this phase: the four vendor decoders key on their documented
signals and build through constructors; openai's 429-plus-quota joins both
meanings through one shared facts allocation; the D15 alias is deleted;
`Test_AllVendors_InsufficientCredits_OneMatcher` drives the real completers
against httptest fixtures and proves the one-matcher contract.
