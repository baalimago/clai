# Phase 6 — the channel contract

**Status:** Complete
[Worklog README](./README.md)

## Goal

Pin the channel-borne error rule (D8) — `CompletionEvent` stays `any`, an
error on the channel is terminal unless `io.EOF` or `context.Canceled`, and
the runner's wrap preserves the vocabulary — as documentation plus tests,
with one invariant row per bound actor.

## Specification

Depends on phases 2 and 4. No signature changes anywhere (D8).

### The documented rule

A doc comment on `models.CompletionEvent` (`internal/models/models.go`)
states the contract from the README ("The channel contract is a rule, not a
type change"):

- `CompletionEvent` is `any`.
- An `error` value on the channel is terminal — the runner ends the step
  and returns it — unless it satisfies `errors.Is(err, io.EOF)` or
  `errors.Is(err, context.Canceled)`, which end the step normally.
- A producer that detects a provider error state mid-stream must send a
  vocabulary error, never a `NoopEvent`.
- The runner wraps the terminal error with `%w` and must not flatten it.

### The bound actors

The README names the four producer loops feeding the channel; each is a row
here, per the worklog's invariants-are-tables rule. Rows whose mechanism is
owned by another phase per the README seam table cite that ownership; the
evidence column still names a runnable test.

| Actor | Obligation | Mechanism | Evidence |
| --- | --- | --- | --- |
| runner (`internal/text/session_runner.go`) | a channel error ends the step and returns wrapped with `%w`; the vocabulary survives to the caller | the existing `case error:` wrap, now pinned | `Test_Runner_ChannelError_TerminalTypedSurvives` (this phase) |
| runner | `io.EOF` / `context.Canceled` on the channel end the step normally | the existing normal-end branch, now pinned | `Test_Runner_ChannelEOFCanceled_NormalEnd` (this phase) |
| `generic.StreamCompleter` | provider error frame → vocabulary error, never `NoopEvent`; transport failure → `ErrTransport` | the frame decode point and transport send (README seam table: owned by the generic-decoding phase) | the owning phase's acceptance suite — its frame-decode and transport criteria; green in `go test ./...` |
| anthropic `claude_stream` | non-OK and rate-limit states arrive on the channel or return path as vocabulary errors | its decode rewiring (README seam table: owned by the vendor-mappings phase) | the owning phase's anthropic acceptance criteria; green in `go test ./...` |
| openai `responses_stream` | read/parse/handle failures and any provider error event arrive typed | its rewiring onto `generic.ResponseError` (README seam table: owned by the vendor-mappings phase) | the owning phase's openai-Responses acceptance criteria; green in `go test ./...` |
| mock (`internal/vendors/mock.go`) | sends no error values; exempt from the producer obligation | inspection — it produces only strings, calls and stop events | source inspection recorded in Implementation notes |

### End-to-end pin

One integration test owned here proves the contract end to end without
duplicating producer tests: a fake completer injected into `sessionRunner`
sends a `claierr`-constructed error; the caller's returned error matches
the sentinel through the runner's wrap.

### Files

- `internal/models/models.go` — doc comment only
- `internal/text/session_runner_test.go` — the two runner tests

## Integration contract

| Trigger | Collaborators / fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Fake completer sends a `claierr` typed error on the channel | `sessionRunner` + fake completer | `Run` returns an error; `errors.Is` finds the sentinel and `errors.As` the type through the runner's `%w` wrap | step ends on the error event | flattening (sentinel unreachable); further events consumed after the error |
| Fake completer sends `io.EOF` | `sessionRunner` + fake completer | step ends normally, nil error, accumulated text preserved | none | an error returned to the caller |
| Fake completer sends an error satisfying `errors.Is(…, context.Canceled)` | `sessionRunner` + fake completer | step ends normally per the pinned rule | none | an error returned to the caller |

## Acceptance criteria

| Outcome | Evidence |
| --- | --- |
| A typed channel error survives to the caller intact | `Test_Runner_ChannelError_TerminalTypedSurvives` |
| The two non-terminal errors end the step normally | `Test_Runner_ChannelEOFCanceled_NormalEnd` |
| The rule is documented on `CompletionEvent` | doc comment present; cited by `architecture/errors.md` in the final phase |
| Every bound actor has a row with runnable evidence | the invariant table above; owning-phase suites green in `go test ./...` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Producer sends a terminal typed error | step ends; wrapped error to caller; vocabulary matchable | `Test_Runner_ChannelError_TerminalTypedSurvives` |
| Producer signals normal termination (`io.EOF`, canceled context) | normal end, no error | `Test_Runner_ChannelEOFCanceled_NormalEnd` |

## Implementation notes

- 2026-09-05 — phase-6 worker subagent (interrupted mid-run, then completed by the session supervisor). The worker added the two runner tests to `internal/text/session_runner_test.go` and set the phase `In Progress`, but was cut off before adding the `CompletionEvent` doc comment, running gates, or recording notes. The supervisor finished: added the doc comment, fixed the missing `fmt`/`io` imports the worker left behind, ran the gates, and closed the mock-inspection row.
- Mock inspection (`internal/vendors/mock.go`, `StreamCompletions`): the mock sends only strings, `pub_models.Call` values and `models.StopEvent{}` — no `error` values, so the mock is exempt from the producer obligation as the spec states.
- Gates run: `go vet ./...` exit 0; `go test ./internal/text/ -run 'Test_Runner_ChannelError_TerminalTypedSurvives|Test_Runner_ChannelEOFCanceled_NormalEnd' -race -count=3 -timeout=30s` ok; `go test $(go list ./... | grep -v '^github.com/baalimago/clai$') -race -cover -count=3 -timeout=30s` exit 0.
- Environmental gate issue (pre-existing, outside this phase's scope): the root-package e2e suite (`github.com/baalimago/clai`, files `main_*_e2e_test.go`) hangs under `-count=3` in this sandbox — `Test_e2e_setup_macro_select_category_quit` timed out with a live `net/http` h2 stream in the goroutine dump. The same package passes under `-count=1`, and none of the phases touched the root package; the hang is reproduced by `go test . -race -count=3 -timeout=30s` alone. Recorded, not fixed here, as it is not a phase-6 regression.
- Dupl: not re-measured here; phase 6 adds no production code (doc comment + tests only), so the clone count is unchanged from phase 5's 30.

## Review findings

### Review 4, 2026-09-05

Verified good: the runner's `case error:` wrap preserves the vocabulary
(`Test_Runner_ChannelError_TerminalTypedSurvives` is a real boundary test);
the generic producer stops after a terminal error send (D18); the openai
responses producer returns on every parse/handle/transport error path; the
mock sends no error values; the `CompletionEvent` doc comment now states the
producer-side stop rule (R4 closure).

- [x] **R4-01 (Medium) — the anthropic producer does not stop after a
  non-`io.EOF` terminal error send.** *Closed 2026-09-05, verified by
  review 5 — see below.* `internal/vendors/anthropic/claude_stream.go:151-160`
  returns after a channel error only when `errors.Is(asErr, io.EOF)` or the
  event is a `pub_models.Call`. `handleToken` returns errors for malformed
  event lines (`claude_stream.go:202, :207, :221, :228, :234`); those are
  emitted on the channel (`:148`) and the loop then continues, because the
  return condition does not match a plain parse error. The runner treats
  every channel error that is not `io.EOF`/`context.Canceled` as terminal and
  never reads the channel again, so the producer blocks on its next send and
  holds the response body open until context cancellation — the R3-01 leak
  class, in the anthropic producer. D18's "by error presence" rule and
  `architecture/errors.md`'s claim that all three real producers "stop
  reading after a terminal error send" are therefore not satisfied here.
  Fix: stop after any `error` send (change the `:153` condition from
  `isErr && errors.Is(asErr, io.EOF) || isFunctionCall` to
  `isErr || isFunctionCall`), and add a regression test that streams a
  malformed event line followed by more frames and asserts the channel
  closes after the error event, mirroring `Test_Generic_ErrorFrameThenDONE_ProducerStops`.

- [x] **R4-02 (Low, non-blocking, pre-existing) — anthropic's context-cancel
  event does not satisfy the pinned `context.Canceled` signal.** *Closed
  2026-09-05, verified by review 5 — see below.*
  `claude_stream.go:132` emits `errors.New("context cancelled")`; the phase-6
  contract pins `context.Canceled` (stdlib, "canceled") as the normal-end
  signal, and `errors.Is(errors.New("context cancelled"), context.Canceled)`
  is false, so a cancelled context can surface as a terminal error to the
  caller when the channel receive beats the runner's `<-ctx.Done()` case.
  Not touched by this worklog; either send `context.Canceled` or note the
  deviation in the contract.

### Review 5, 2026-09-05

Independent verification of the R4-01/R4-02 fix that landed in the working
tree after review 4 (`internal/vendors/anthropic/claude_stream.go`,
`claude_stream_leak_test.go`). The code was read and the gates re-run; the
fix session's own record was missing (R5-01 below), so this review supplies
the verification record.

**R4-01 — resolved.** The `:153` condition is now `isErr || isFunctionCall`
with no `io.EOF` carve-out, exactly as review 4 prescribed and as D18
requires. Regression test `TestClaudeStream_StopsAfterNonEOFErrorSend`
(`claude_stream_leak_test.go`) streams a malformed event line over a pipe
held open and asserts the channel closes right after the error event — the
consumer mirrors the runner (never reads again). The test was verified to
bite: with the fix reverted to the pre-fix condition it fails
(`channel not closed after the error event: producer goroutine leaked`,
2.0s timeout); with the fix restored it passes under `-race -count=3`.

**R4-02 — resolved, by removal rather than by either suggested option.**
The `ctx.Err()` branch (`claude_stream.go:131`) no longer emits
`errors.New("context cancelled")`; it returns silently and the deferred
close ends the channel. This satisfies the pinned contract without a
deviation note: the runner's `completion, ok := <-completionsChan` close
branch (`session_runner.go:164`) and its own `<-ctx.Done()` case both end
the step normally, and a channel-close is not an error event, so the
mismatched-signal surface is gone entirely. Verified that the session
runner is the only channel consumer (`gpt.go`/`mistral.go` forward the
channel; nothing else ranges it), so no other consumer depends on the old
emit. `TestClaudeStream_ClosesOnCtxCancelMidSend` still passes.

**Invariant re-traced through every producer branch** (the review-3
cross-cutting rule: stop reading after a terminal error send, every loop):

- anthropic `handleStreamResponse`: read-failure → `NewTransport` send →
  return; clean `io.EOF` (bare) → `io.EOF` send → return; `io.EOF` with
  trailing token → `handleFullResponse` (its unmarshal-failure emit is
  followed by an immediate return in both callee and caller) → return;
  `ctx.Err()` → return, no emit; `handleToken` error (all five parse sites
  and the `message_stop` `io.EOF`) → send → `isErr` → return; `Call` →
  send → return; failed `emitClaude` → return. **Every terminal send is
  followed by a return.**
- generic `handleStreamResponse`: `StopEvent` and `error` cases both
  return after the send (D18, unchanged since the R3-01 closure).
- openai `responses_stream`: transport, parse, and handle failures all
  return after the send; `doneFlag` returns (unchanged since review 4).
- mock: sends no errors (exempt, unchanged).

Parse-failure events on the anthropic path stay plain `fmt.Errorf` values,
not vocabulary errors — consistent with the README's "request-construction
and marshalling failures stay plain" rule and phase 5's recorded scope
(they are clai-side parse failures, not provider error states), so the
"vocabulary error, never a NoopEvent" obligation is not violated.

- [x] **R5-01 (Low) — the R4-01/R4-02 fix session recorded nothing in the
  worklog.** The fix and its regression test landed in the working tree,
  but the phase-6 findings stayed unchecked, the status board stayed
  `Reopened (review 4)`, and no session-journal entry recorded the fix,
  its test evidence, or its gates — the exact stale-completion-status
  hazard the worklog process exists to prevent (a later contributor would
  have re-fixed or mis-routed off the board). *Closed by this review: this
  section is the missing record, and the board/index/journal are updated
  in the same change.*

Verified good beyond the findings: `CompletionEvent`'s doc comment and
`architecture/errors.md`'s channel section state the producer-stop rule and
are now factually true for all three real producers; the runner's
`case error:` wrap and `io.EOF`/`context.Canceled` normal-end branches are
unchanged; anthropic package coverage rose to 75.6% with the new test.
Gates for this round are recorded in the README session journal (review 5).
