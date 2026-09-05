# Phase 4 — `generic.StreamCompleter` decoding

**Status:** Complete (R3-01 closed 2026-09-05)
[Worklog README](./README.md)

## Goal

Implement the two decode points behind the one `DecodeError` hook (D7, D12)
— the non-OK response and the streamed error frame — so every terminal
provider state leaving `generic` is a vocabulary error and the silent
HTTP-200 path is closed.

## Specification

Depends on phase 2 (`pkg/claierr`). All decoding here is vendor-agnostic;
`generic` gains only the hook, the baseline, and the frame envelope — never
a vendor rule (`AGENTS.md`).

### The seam

Per the README seam table, this phase introduces:

- `StreamCompleter.DecodeError` — the func field exactly as the README's
  "One vocabulary, many decoders" declares it: set by the vendor, decodes a
  provider error payload into shared error values, nil field or nil return
  means "no vendor knowledge".
- `chatCompletionChunk.Error` — a raw-payload field
  (`internal/text/generic/stream_completer_models.go`) tagged `error`, so
  the OpenAI-compat `{"error": …}` envelope is *seen* rather than ignored.
  Presence (non-empty raw value) is the trigger; `generic` never parses the
  envelope's contents.
- The decode chain, **exported** as `generic.ResponseError(status, body,
  decode)` per the README seam table, implementing the README's
  `responseError` pseudocode verbatim: vendor first (D11), then the
  baseline, then `claierr.NewUnexpectedProviderResponse` — never nil for a
  non-OK status. The export exists for the two non-generic boundaries
  (anthropic, openai responses), which phase 5 rewires onto it.
- `baselineError(status, body)` — unexported; exactly the README's baseline
  table, populating `claierr.APIError` facts from the status and body. No
  body parsing beyond carrying it as a fact (README, Finding 8: the
  baseline is what the status *alone* suggests).

### Decode point one — the non-OK response

The status check in `StreamCompletions`
(`internal/text/generic/stream_completer.go`) currently returns a formatted
string. It becomes `generic.ResponseError(res.StatusCode, body, s.DecodeError)`.

### Decode point two — the streamed frame

In the frame handler (`handleStreamChunk` path):

1. If the unmarshalled chunk's `Error` field is present, hand the **whole
   raw frame** to `DecodeError(http.StatusOK, frame)` (D12 — the OK status
   tells a caring decoder the payload arrived as a frame).
2. A nil hook or nil return degrades to
   `claierr.NewUnexpectedProviderResponse` carrying `http.StatusOK` and the
   frame body — still terminal, still typed, merely meaning-less (README,
   "The silent path").
3. The resulting error is sent as the completion event. It must never
   become a `NoopEvent`.

The error-frame branch runs before the `len(chunk.Choices)` check, since an
error frame has no choices.

### The mis-nested return

The unmarshal-failure branch currently returns `NoopEvent` only under the
debug flag and otherwise falls through onto a zero-valued chunk (README,
"The silent path"). Fix: an unmarshal failure returns `NoopEvent`
unconditionally (keep-alive tolerance is the intent); the warning stays
behind the debug flag.

### Transport

The mid-stream read-failure send (`failed to read line` in the scanner
loop) becomes `claierr.NewTransport(cause)`, per the vocabulary table's
`ErrTransport` row. A failed `client.Do` in `StreamCompletions` likewise.

### Invariants

| Invariant | Mechanism | Test |
| --- | --- | --- |
| A non-OK response never yields nil or a bare formatted string | `ResponseError` ends in the catch-all constructor | `Test_Generic_NonOK_NeverUntyped` |
| The baseline maps exactly the README baseline table | table-driven `baselineError` | `Test_Generic_BaselineTable` |
| A recognizing vendor decoder's answer replaces the baseline entirely (D11) | early return in `ResponseError` | `Test_Generic_VendorDecoderOverridesBaseline` |
| An error frame at HTTP 200 is a typed terminal event, never `NoopEvent` | error-frame branch precedes the choices check | `Test_Generic_ErrorFrameAtOK_TypedOnChannel` |
| A silent or nil decoder on an error frame still terminates typed | frame fallback to `NewUnexpectedProviderResponse` | `Test_Generic_ErrorFrame_SilentDecoder_Unexpected` |
| A malformed frame is a `NoopEvent` regardless of debug flags | unconditional return on unmarshal failure | `Test_Generic_MalformedFrame_Noop` |
| A mid-stream read failure carries `ErrTransport` wrapping its cause | `NewTransport` at the send site | `Test_Generic_ReadFailure_ErrTransport` |

### Files

- `internal/text/generic/stream_completer.go`
- `internal/text/generic/stream_completer_models.go`
- `internal/text/generic/stream_completer_test.go` (or a new test file in
  the package)

## Integration contract

Scenarios run against an `httptest` server speaking SSE, through
`StreamCompleter.StreamCompletions` and its channel.

| Trigger | Collaborators / fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Server answers 200, streams `{"error": {"message": "quota exhausted"}}` as a frame, closes; no vendor decoder set | httptest SSE server | channel delivers an error satisfying `errors.Is(err, claierr.ErrUnexpectedProviderResponse)`, facts carry status 200 and the frame body | stream ends after the error event | `NoopEvent` for the frame; a normal stream end with empty text |
| Same frame, vendor decoder set and recognizing | httptest SSE server + stub decoder | channel delivers the decoder's error unchanged | decoder receives status 200 and the whole raw frame | baseline or catch-all meanings joined onto the decoder's answer |
| Server answers 402 with a body; no decoder | httptest server | `StreamCompletions` returns an error matching `claierr.ErrLikelyInsufficientCredits`; facts carry status and body | none | a formatted-string-only error |
| Server answers 403; stub decoder returns insufficient-credits | httptest server + stub decoder | returned error matches `ErrLikelyInsufficientCredits` and **not** `ErrAuthFailed` (the xAI shape, D11) | none | baseline auth meaning surviving alongside |
| Server answers an unmapped non-OK status (e.g. 418); no decoder | httptest server | returned error matches `ErrUnexpectedProviderResponse` | none | nil error |
| Server streams a malformed non-JSON line between valid frames | httptest SSE server | stream continues; valid frames still delivered | none | stream termination; an error event |
| Connection drops mid-stream | httptest server closing early | channel delivers an error matching `claierr.ErrTransport`, cause reachable via `errors.Is`/`errors.As` | stream ends | a silent normal end |

## Acceptance criteria

| Outcome | Evidence |
| --- | --- |
| The silent path is closed: an error frame at HTTP 200 surfaces as a typed terminal error, never a successful empty run (Definition of success, item on the silent path) | `Test_Generic_ErrorFrameAtOK_TypedOnChannel`, `Test_Generic_ErrorFrame_SilentDecoder_Unexpected` |
| Every baseline-table row maps to its sentinel | `Test_Generic_BaselineTable` |
| Vendor-first decoding holds (D11) | `Test_Generic_VendorDecoderOverridesBaseline` |
| Non-OK never untyped, unmapped statuses included | `Test_Generic_NonOK_NeverUntyped` |
| Keep-alive tolerance preserved; the mis-nested return is gone | `Test_Generic_MalformedFrame_Noop` |
| Transport failures carry the vocabulary | `Test_Generic_ReadFailure_ErrTransport` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Provider error frame at HTTP 200, no decoder | `ErrUnexpectedProviderResponse`, terminal | `Test_Generic_ErrorFrame_SilentDecoder_Unexpected` |
| Provider error frame at HTTP 200, decoder recognizes | decoder's vocabulary error, terminal | `Test_Generic_ErrorFrameAtOK_TypedOnChannel` |
| Non-OK status in the baseline table | the table's sentinel with facts | `Test_Generic_BaselineTable` |
| Non-OK status outside the table, decoder silent | `ErrUnexpectedProviderResponse` | `Test_Generic_NonOK_NeverUntyped` |
| Malformed frame (keep-alive, partial JSON) | `NoopEvent`; stream continues | `Test_Generic_MalformedFrame_Noop` |
| Read failure mid-stream | `ErrTransport` wrapping the cause | `Test_Generic_ReadFailure_ErrTransport` |

## Implementation notes

phase-4 worker subagent, 2026-09-05. Deltas from the specification only.

**Decisions made while implementing:**

- Decode point two reuses `ResponseError(http.StatusOK, token, s.DecodeError)`
  directly instead of a parallel frame-only branch: the baseline has no
  row for an OK status, so the chain degrades to exactly the specified
  `NewUnexpectedProviderResponse(http.StatusOK, frame)` on a nil hook or
  nil return, and D12's one-chain property holds by construction.
- The frame handed to `DecodeError` is the SSE data payload — the token
  after the `data: ` prefix and whitespace trim. The transport framing
  prefix is not part of "the whole raw frame"; the decoder sees the same
  JSON the unmarshaller saw. Pinned by
  `Test_Generic_ErrorFrameAtOK_TypedOnChannel`'s byte-equality assertion.
- The presence trigger excludes a literal JSON `null` envelope
  (`len(chunk.Error) > 0 && string(chunk.Error) != "null"`). A strict
  "non-empty raw value" reading would terminate every stream from a
  provider that emits `"error": null` on ordinary chunks; a null literal
  is no envelope. This is a byte comparison against the null literal,
  not envelope parsing. Exercised in `Test_Generic_MalformedFrame_Noop`
  (an `"error":null` frame still delivers its content event).
- `DecodeError` carries a `json:"-"` tag: `StreamCompleter` is embedded
  in vendor structs that are JSON-marshaled into config files, and an
  untagged func field would fail `json.Marshal` on config save.
- Two existing tests pinned the exact formatted strings this phase
  replaces and were updated to the typed contract:
  `TestStreamCompletions_DoError` now asserts
  `errors.Is(err, claierr.ErrTransport)` with the cause in the message;
  `TestStreamCompletions_Non200_And_CleanDoesNotMutateOriginal` asserts
  `errors.Is(err, claierr.ErrProviderUnavailable)` for its 500.

**Surprises:**

- The recorded dupl baseline is inflated by a stray
  `.claude/worktrees/` repo copy in a naive run (35 groups). Excluding
  it (`find . -name '*.go' -not -path './.claude/*' | dupl -t 80
  -files`) reports **31**, identical to the branch-point measurement:
  delta zero for this phase.
- `errors.Is(err, io.ErrUnexpectedEOF)` reaches the transport cause
  through `TransportError.Unwrap() []error` as designed — the
  mid-stream-drop integration test asserts it against a real truncated
  `Content-Length` response.

**Verification (all run 2026-09-05, repo root):**

- `go run mvdan.cc/gofumpt@latest -w -l .` — one alignment reformat in
  `stream_completer_models.go`, then clean.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0.
- `go vet ./...` — exit 0.
- `go test ./... -race -cover -count=3 -timeout=30s` — exit 0, 45
  packages; `internal/text/generic` 76.5% statements. New code:
  `ResponseError` 100%, `baselineError` 100%; modified
  `StreamCompletions` 85.0%, `handleStreamResponse` 90.9%,
  `handleStreamChunk` 88.2%.
- `go fix ./...` — exit 0.
- dupl (excluding the stray worktree copy, above) — 31 clone groups,
  baseline unchanged.

All seven invariant-table tests exist under their exact names in
`internal/text/generic/decode_error_test.go` and pass under
`-race -count=3`. The integration-contract scenarios run against real
`httptest` servers through `StreamCompletions` and its channel: the
baseline table row by row, the 418 catch-all, the 403-decoder override,
both silent-decoder frame variants, the recognizing-decoder frame, the
malformed-line stream continuation, and the mid-stream connection drop.

## Review findings (review 3, 2026-09-05)

### R3-01 — Medium — the generic producer does not stop after a terminal error event

`internal/text/generic/stream_completer.go:142-154` sends whatever
`handleStreamChunk` returns and then stops the loop only when the event is a
`models.StopEvent`. The frame decode point (`:195`) and the tool-argument
unmarshal failure (`:288`) both return `error` values, which the runner
(`session_runner.go:194-202`) treats as terminal — it returns and never reads
the channel again. The producer, however, keeps reading after sending an
`error`, so a provider that emits an `{"error": …}` frame and then the usual
`data: [DONE]` (or any further frame) leaves the producer blocked on the next
`outChan <- evt` send until the context is cancelled; the deferred
`res.Body.Close()` never runs, so the connection stays open too.

This is the exact leak class the loop already fixes for `StopEvent` at
`:148-154` ("Stop here so a trailing blank line or a second [DONE] cannot
block forever"), but the terminal-`error` path was not given the same guard.
The phase-6 acceptance test (`Test_Runner_ChannelError_TerminalTypedSurvives`)
pins the runner side with a *buffered* mock channel, so it cannot see this
unbuffered-producer leak; the frame-decode tests
(`Test_Generic_ErrorFrameAtOK_TypedOnChannel`,
`Test_Generic_ErrorFrame_SilentDecoder_Unexpected`) close the stream
immediately after the error frame, so the producer hits `io.EOF` before any
second send.

Failure scenario: an OpenAI-compatible provider answers `200 OK`, streams an
`{"error":{"message":"quota exhausted"}}` frame, then `data: [DONE]` and
closes. The runner returns the typed error, but the producer goroutine blocks
on the `StopEvent` send (or the next frame), holding the response body and
goroutine until the run context is cancelled. With a long-lived shared context
this is a persistent goroutine + connection leak per occurrence.

Fix (both items closed 2026-09-05, session journal “R3-01 closure”):

- [x] After `handleStreamChunk` returns, stop the loop on a terminal `error`
      event exactly as it stops on `StopEvent` (any `error` from the chunk
      handler is terminal, because the runner ends the step on every
      `case error:`).
- [x] Add a regression test: an SSE server that writes an error frame followed
      by `data: [DONE]` (without closing first), and assert the channel still
      closes promptly — the producer returns after the error send.

Verified good in this phase: `ResponseError` never returns nil for a non-OK
status and implements vendor-first/baseline/catch-all exactly; both decode
points feed the one `DecodeError` hook; the mis-nested unmarshal return is
fixed; `Test_Generic_BaselineTable` and
`Test_Generic_VendorDecoderOverridesBaseline` exercise the real boundary.

### R3-01 closure — implementation note (clai worker, 2026-09-05)

The production change is one type switch at
`internal/text/generic/stream_completer.go` (the `handleStreamResponse`
producer loop): after the send, the loop returns not only on `StopEvent`
but on any `error` event. No `io.EOF`/`context.Canceled` carve-out is
needed on the producer side: the runner's normal-end branches for those two
errors return as well and never read the channel again, so any error send
is the last event a consumer reads — recorded as decision D18.

The regression test `Test_Generic_ErrorFrameThenDONE_ProducerStops`
(`internal/text/generic/decode_error_test.go`) drives the real
`StreamCompletions` boundary against an `httptest` SSE server that writes
an error frame, then `data: [DONE]`, then holds the connection open until
the test releases it. The consumer mirrors the session runner (read the
error event, never read again); pre-fix the producer kept going and either
delivered the trailing `StopEvent` or blocked on its send — the test failed
with `expected channel close after the error event, got event:
models.StopEvent`. Post-fix the producer returns right after the error send
and closes the channel.

Verification (all run 2026-09-05, repo root):

- `go test ./internal/text/generic/ -race -cover -count=3 -timeout=60s` —
  exit 0; coverage 76.6% (was 76.5% in the original phase run — the added
  branch and test net +0.1).
- New regression test alone, `-race -count=3` — PASS x3.
- `go run mvdan.cc/gofumpt@latest -l internal/text/generic/` — clean.
- `go vet ./internal/text/generic/` — exit 0.
- Full non-root suite
  `go test $(go list ./... | grep -v '^github.com/baalimago/clai$')
  -race -cover -count=3 -timeout=30s` — exit 0 (root-package pty e2e
  excluded per the recorded R3-03 environment flake; unchanged by this
  phase).
- dupl, phase-4 exclusion method (`find . -name '*.go' -not -path
  './.claude/*' | dupl -t 80 -files`) — unchanged at 33 full file set
  (5 production-only) after the added test.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0;
  `go vet ./...` — exit 0; `go fix ./...` — exit 0.

Accepted production change is limited to `stream_completer.go`; the only
other delta is the added test file section. No vendor file changed.
