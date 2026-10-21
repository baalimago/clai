# Phase 2 — LoopRunner

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Drive multi-iteration loop execution across fresh chat sessions until
`CompletionRecorder` records achievement, a block, caller cancellation, or the
iteration limit.

## Interface

Written to `internal/text/loop.go`.

```go
package text

import (
	"context"
	"errors"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ErrLoopBlocked and ErrMaxIterations are the terminal sentinels returned by
// LoopRunner.Query when a loop stops without achieving its goal. (F3)
var (
	ErrLoopBlocked   = errors.New("loop task blocked")
	ErrMaxIterations = errors.New("loop max iterations reached")
)

// LoopBlockedError reports that the model called complete_task with blocked status. (F3)
type LoopBlockedError struct {
	Iteration int
	Summary   string
}

// Error formats the blocked iteration and summary; Unwrap returns ErrLoopBlocked. (LoopBlockedError.1, F3)
func (e *LoopBlockedError) Error() string
func (e *LoopBlockedError) Unwrap() error

// MaxIterationsError reports that the loop exhausted MaxIterations without achieving the goal. (F3)
type MaxIterationsError struct {
	MaxIterations int
	LastHandover  string
}

// Error formats the exhausted limit; Unwrap returns ErrMaxIterations. (MaxIterationsError.1, F3)
func (e *MaxIterationsError) Error() string
func (e *MaxIterationsError) Unwrap() error

// LoopConfig holds the seed chat and iteration bounds for one LoopRunner. (F3, F5, F7, L1)
type LoopConfig struct {
	SeedChat      pub_models.Chat
	GoalPrompt    string
	MaxIterations int
}

// LoopQuerier is the querier a LoopRunner drives: a ChatQuerier that also
// accepts one additional tool beside the user's selected catalog. *Querier[C]
// satisfies it.
type LoopQuerier interface {
	models.ChatQuerier

	// RegisterExtraTool makes tool callable on every following TextQuery,
	// beside every tool already selected, whether -t is unset, a glob, or an
	// explicit list; it never removes or narrows an existing tool.
	// (RegisterExtraTool.1, F6)
	RegisterExtraTool(tool pub_models.LLMTool)
}

// RegisterExtraTool adds tool to the querier's dispatch and selectable
// catalogs and to the model's tool box, so a following TextQuery offers it
// beside every already-selected tool. It is called between queries and never
// during one. (Querier.RegisterExtraTool.1, RegisterExtraTool.1, F6)
func (q *Querier[C]) RegisterExtraTool(tool pub_models.LLMTool)

// LoopRunner executes outer-loop iterations over a LoopQuerier and a
// CompletionRecorder. (F3, F4, F5, F7, L1)
type LoopRunner interface {
	models.Querier

	// Query runs iterations starting at 1 until the goal is achieved, blocked,
	// cancelled, or MaxIterations is exhausted.
	//
	// - MaxIterations follows F7: negative returns an error whose message
	//   contains "max-iterations" before any TextQuery runs; 0 means
	//   unlimited. (Query.1, F7)
	// - Before each iteration, calls recorder.Reset() and derives an
	//   iteration-scoped child context with its own cancel stored under
	//   utils.ContextCancelKey; a StopEvent inside TextQuery cancels only the
	//   iteration context, whereas cancellation of ctx aborts the loop and
	//   returns ctx.Err(). (Query.2, F4)
	// - Every iteration runs a chat whose system message is SeedChat's system
	//   message with L1's loop instructions appended. Iteration 1 runs the seed
	//   chat otherwise unchanged; each iteration k > 1 allocates a fresh chat
	//   ID via chatid.New(), preserves SeedChat's messages through its first
	//   user message, and appends the prior iteration's assistant handover
	//   plus iteration k's continuation user message containing GoalPrompt.
	//   (Query.3, F5, L1)
	// - After each TextQuery, inspects recorder.Submission(): achieved returns
	//   nil; blocked returns *LoopBlockedError; no submission advances to the
	//   next iteration or returns *MaxIterationsError when k == MaxIterations.
	//   (Query.4, F3)
	Query(ctx context.Context) error
}

// NewLoopRunner constructs a LoopRunner over cq and recorder with cfg.
//
//   - Registers recorder on cq through RegisterExtraTool exactly once, before
//     any iteration, so every iteration can call the completion tool F1
//     names. (NewLoopRunner.1, F1, F6)
func NewLoopRunner(cq LoopQuerier, recorder CompletionRecorder, cfg LoopConfig) LoopRunner
```

Consumes: `internal/text:CompletionRecorder`, `internal/text:LoopSubmission`,
`internal/text:LoopStatus`, `internal/text:LoopStatusAchieved`,
`internal/text:LoopStatusBlocked` (phase 1); `internal/models:Querier`,
`internal/models:ChatQuerier`, `internal/models:ToolBox`,
`internal/models:StreamCompleter` (import); `internal/text:Querier` (import);
`internal/chatid:New` (import); `internal/utils:ContextCancelKey` (import);
`pkg/text/models:Chat`, `pkg/text/models:Message`, `pkg/text/models:LLMTool`
(import).

## Acceptance criteria

| #   | Criterion                                                                                                   | Proves                                            | Proof                                          |
| --- | ----------------------------------------------------------------------------------------------------------- | ------------------------------------------------- | ---------------------------------------------- |
| 1   | Single-iteration achievement returns `nil` after one `TextQuery` call                                       | `Query.4`, `F3`                                   | `TestLoopRunner_AchievedFirstIteration`        |
| 2   | Handover across iterations preserves the first user message (`GroupKey` invariant) and isolates `StopEvent` | `Query.2`, `Query.3`, `Query.4`, `F4`, `F5`, `L1` | `TestLoopRunner_HandoverAndStopEventIsolation` |
| 3   | Blocked submission stops immediately and returns `*LoopBlockedError` matching `ErrLoopBlocked`              | `Query.4`, `LoopBlockedError.1`, `F3`             | `TestLoopRunner_BlockedReturnsTypedError`      |
| 4   | Reaching `MaxIterations` without achievement returns `*MaxIterationsError`; a negative limit is rejected    | `Query.1`, `Query.4`, `MaxIterationsError.1`, `F3`, `F7` | `TestLoopRunner_MaxIterationsAndValidation` |
| 5   | `NewLoopRunner` registers the recorder once on the `LoopQuerier` before the first iteration                | `NewLoopRunner.1`, `F1`, `F6`                     | `TestLoopRunner_RegistersRecorderOnce`         |
| 6   | `*Querier[C]` satisfies `LoopQuerier` and adds the tool beside empty and populated tool catalogs           | `Querier.RegisterExtraTool.1`, `RegisterExtraTool.1`, `F6` | `TestQuerier_RegisterExtraTool`                |

## Test cases

| Test                                           | Level          | Proves                                                         | Given                                                                                                                                           | Then                                                                                                                                                             | Must not                                    |
| ---------------------------------------------- | -------------- | -------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------- |
| `TestLoopRunner_AchievedFirstIteration`        | contract suite | `Query.4`, `F3`, AC 1                                          | Fake `LoopQuerier` that records `LoopStatusAchieved` on iteration 1                                                                             | `Query` returns `nil` after 1 call                                                                                                                               | Start a second iteration                    |
| `TestLoopRunner_HandoverAndStopEventIsolation` | contract suite | `Query.2`, `Query.3`, `Query.4`, `F4`, `F5`, `L1`, AC 2        | Fake `LoopQuerier` that cancels `utils.ContextCancelKey` on iter 1 without submitting, then submits achieved on iter 2; plus caller cancel case | Iter 2 runs with fresh ID, same profile, identical first `user` message, iter 1 handover as `assistant`, and goal in continuation `user` msg; caller cancel aborts immediately | Let iter 1 `StopEvent` cancel caller `ctx`  |
| `TestLoopRunner_BlockedReturnsTypedError`      | contract suite | `Query.4`, `LoopBlockedError.1`, `F3`, AC 3                    | Fake `LoopQuerier` that records `LoopStatusBlocked` with reason                                                                                 | Returns error matching `errors.Is(err, ErrLoopBlocked)` and `errors.As` `*LoopBlockedError`                                                                      | Continue to iteration 2                     |
| `TestLoopRunner_MaxIterationsAndValidation`    | contract suite | `Query.1`, `Query.4`, `MaxIterationsError.1`, `F3`, `F7`, AC 4 | Negative `MaxIterations`, `MaxIterations: 0` with achievement on iteration 3, and `MaxIterations: 2` where neither submits                            | Negative limit errors with `"max-iterations"` in the message before any `TextQuery`; zero runs without a bound; exhausted limit returns `*MaxIterationsError` | Exceed `MaxIterations` calls to `TextQuery` |
| `TestLoopRunner_RegistersRecorderOnce`         | contract suite | `NewLoopRunner.1`, `F1`, `F6`, AC 5                            | Fake `LoopQuerier` recording `RegisterExtraTool` calls; a 3-iteration run                                                                        | Exactly one registration of the recorder, before the first `TextQuery`                                                                                           | Register per iteration                      |
| `TestQuerier_RegisterExtraTool`                | contract suite | `Querier.RegisterExtraTool.1`, `RegisterExtraTool.1`, `F6`, AC 6 | `*Querier[C]` assigned to a `LoopQuerier` variable, with a `ToolBox` model and with and without existing selected tools                           | The extra tool is callable on the next `TextQuery` and every pre-existing tool is still callable                                                                 | Drop or overwrite existing selected tools   |

## Error management

| Failure                                              | Expected outcome                                                                                  | Test                                           |
| ---------------------------------------------------- | ------------------------------------------------------------------------------------------------- | ---------------------------------------------- |
| `MaxIterations < 0`                                  | Error whose message contains `max-iterations`, before any model call runs                         | `TestLoopRunner_MaxIterationsAndValidation`    |
| `LoopQuerier.TextQuery` returns an error             | Wrapped error returned immediately; no further iteration starts                                   | `TestLoopRunner_HandoverAndStopEventIsolation` |
| Caller `ctx` cancelled before or during an iteration | Returns `ctx.Err()` without starting the next iteration                                           | `TestLoopRunner_HandoverAndStopEventIsolation` |
| Model submits `LoopStatusBlocked`                    | `*LoopBlockedError` (unwraps to `ErrLoopBlocked`) carrying iteration number and summary           | `TestLoopRunner_BlockedReturnsTypedError`      |
| `MaxIterations` exhausted without achievement        | `*MaxIterationsError` (unwraps to `ErrMaxIterations`) carrying `MaxIterations` and `LastHandover` | `TestLoopRunner_MaxIterationsAndValidation`    |

## Golden diff

None.

## Removes

None.

## Local facts

**L1 — Prompt structure and handover source.** The loop instructions appended
to the `system` message instruct the model to work toward the goal, call the
completion tool F1 names when achieved or blocked, and summarize progress for
handover when stopping early. The handover is the `Content` of the last
`assistant` message in the chat `TextQuery` returns, with its tool calls
dropped. When that chat has no `assistant` message, or the message is empty
(for example after a tool-budget hard stop), Iteration $k$ uses the
non-empty placeholder assistant message
`"(Previous iteration ended without a summary.)"` so strict `user` →
`assistant` → `user` alternation holds.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                                                                          | Result   |
| ----- | ----------- | -------------------------------------------------------------------------------- | -------- |
| start | c95cf6c+wip | `go test ./internal/text/ ./internal/vendors/ -race -count=1 -timeout=120s`        | passed   |
| end   | c95cf6c+wip | `go test ./internal/text/ ./internal/vendors/ -race -count=3 -cover -timeout=30s`  | passed   |
| end   | c95cf6c+wip | `go test ./... -race -cover -count=3 -timeout=30s -p 1`                            | passed   |
| end   | c95cf6c+wip | `go test ./... -race -cover -count=3 -timeout=30s`                                 | passed on retry, see Retry note |
| end   | c95cf6c+wip | `go test ./e2e/ -race -count=1 -timeout=120s`                                      | passed   |
| end   | c95cf6c+wip | `make qa`                                                                          | passed   |
| end   | c95cf6c+wip | `gofumpt`, `staticcheck ./...`, `go vet ./...`, `go fix ./...`                     | passed   |
| end   | c95cf6c+wip | `dupl -t 80 .`                                                                     | no clones in `loop.go` or `loop_test.go` |

`go tool cover -func` reports 100% for every function in `internal/text/loop.go`
except `iterationChat` (92.3%). The uncovered statement is the `chatid.New`
failure return.

`chatid.New` fails only when the system random source fails. `Query` returns
that error wrapped. The failure has no test seam short of injecting an ID
generator, which the Scope does not cover, so it is accepted untested.

Decisions:

- D1 — 2026-10-21, worker session `clai-loop-phase-2`: iteration 2 and later
  build a fresh minimal chat with only a new ID, `Created`, and messages. The
  seed's `GroupKey`, `Queries`, `TokenUsage`, title, and summary are not
  inherited, so `Save` stamps the new `GroupKey` from the preserved first user
  message (F5).
- D2 — The handover is the trimmed `Content` of the last assistant message with
  its tool calls dropped; a missing or blank message uses L1's placeholder.
- D3 — `RegisterExtraTool` writes the per-run dispatch and selectable catalogs
  before consulting the model tool box, so a `Querier` whose model is not a
  `ToolBox` still dispatches the tool through the per-run map. Model
  registration is idempotent by tool name.
- D4 — Loop instructions append to the first system message, or a system
  message carrying only the instructions is prepended when the seed has none.
  The seed's own messages are copied, never mutated.
- D5 — A submission recorded after a nil `TextQuery` is inspected before the
  caller-cancellation check, so an achieved goal is not reported as an
  interrupt. Caller cancellation wins over a simultaneous `TextQuery` error.
- D6 — The failure-of-expectation default in the submission switch (a custom
  recorder returning an unknown status) is a returned error, not a panic.

Retry note: the first exact parallel `go test ./... -race -cover -count=3 -timeout=30s`
failed only in `e2e`: the 30 s alarm raced the existing
`e2e.captureStdoutStderr` `os.Stdout`/`os.Stderr` restore while `testing` wrote
the coverage profile. The e2e package passed alone, the same full suite with
`-p 1` passed, and the exact parallel command passed unchanged on the next
attempt. Host load average was 11.5 on 8 cores at the first run. No test was
skipped or edited.

Surprises: none.

## Review findings

None.
