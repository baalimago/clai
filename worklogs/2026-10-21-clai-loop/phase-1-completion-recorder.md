# Phase 1 — CompletionRecorder

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Validate and record `complete_task` tool calls for one loop iteration, gating
achieved completions on the optional verification command.

## Interface

Written to `internal/text/loop_tool.go`. L3's mock scripting is written to
`internal/vendors/mock.go` and its test file.

```go
package text

import (
	"context"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// LoopStatus classifies the terminal state reported by the completion tool F1 names. (F1)
type LoopStatus string

const (
	LoopStatusNone     LoopStatus = ""
	LoopStatusAchieved LoopStatus = "achieved"
	LoopStatusBlocked  LoopStatus = "blocked"
)

// LoopSubmission holds the validated outcome recorded by a CompletionRecorder. (F1)
type LoopSubmission struct {
	Status  LoopStatus
	Summary string
}

// CompletionRecorder is an LLMTool that validates and records a loop
// iteration's terminal completion call. (F1, F2, L1, L2)
type CompletionRecorder interface {
	pub_models.LLMTool

	// CallWithContext validates input, runs the optional verification command
	// when status is achieved, and records the submission on success.
	//
	// - Requires the two string fields F1 defines; missing, unknown, or
	//   whitespace-only values return an error and leave Submission unchanged.
	//   (CallWithContext.1, F1, L1)
	// - When status is achieved and checkCmd is non-empty, executes checkCmd
	//   bound to ctx as F2 specifies; a non-zero exit or execution error
	//   returns an error containing the combined output (truncated to L2's rune
	//   cap) and leaves Submission unchanged. (CallWithContext.2, F2, L2)
	// - When status is blocked, does not execute checkCmd, records the
	//   submission with trimmed summary, and returns a confirmation string.
	//   (CallWithContext.3, F1, F2, L1)
	// - When status is achieved and checkCmd is empty or exits 0, records the
	//   submission with trimmed summary and returns a confirmation string.
	//   (CallWithContext.4, F1, F2, L1)
	CallWithContext(ctx context.Context, input pub_models.Input) (string, error)

	// Submission returns the last valid LoopSubmission recorded since creation
	// or the latest Reset, and true when one exists. Safe for concurrent use.
	// (Submission.1, F1)
	Submission() (LoopSubmission, bool)

	// Reset clears any recorded submission so the instance can be reused on the
	// next loop iteration. Safe for concurrent use. (Reset.1, F1)
	Reset()
}

// NewCompletionRecorder returns a CompletionRecorder configured with the
// optional verification command checkCmd.
//
// - Specification() advertises the tool name and required schema properties F1
//   defines; Call delegates to CallWithContext with context.Background().
//   (NewCompletionRecorder.1, F1, L1)
func NewCompletionRecorder(checkCmd string) CompletionRecorder
```

Consumes: `pkg/text/models:LLMTool`, `pkg/text/models:Input`,
`pkg/text/models:Specification`, `pkg/text/models:InputSchema`,
`pkg/text/models:ParameterObject` (import); `internal/vendors:Mock` (import).

## Acceptance criteria

| #   | Criterion                                                                                                      | Proves                                               | Proof                                         |
| --- | -------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------- | --------------------------------------------- |
| 1   | Valid achieved call without a check command records `LoopStatusAchieved` and trimmed summary                   | `CallWithContext.4`, `Submission.1`, `F1`            | `TestCompletionRecorder_AchievedWithoutCheck` |
| 2   | Valid achieved call with a passing check command records `LoopStatusAchieved`; a failing check command rejects | `CallWithContext.2`, `CallWithContext.4`, `F2`, `L2` | `TestCompletionRecorder_CheckGate`            |
| 3   | Valid blocked call records `LoopStatusBlocked` without running the configured check command                    | `CallWithContext.3`, `F1`, `F2`                      | `TestCompletionRecorder_BlockedSkipsCheck`    |
| 4   | Missing, invalid, or whitespace-only fields are rejected and leave the prior submission state untouched        | `CallWithContext.1`, `F1`, `L1`                      | `TestCompletionRecorder_Validation`           |
| 5   | `Reset` clears a recorded submission and `Specification` matches the schema contract                           | `Reset.1`, `NewCompletionRecorder.1`, `F1`           | `TestCompletionRecorder_ResetAndSpec`         |
| 6   | `vendors.Mock` scripts `complete_task` calls per L3, and each scripted input is accepted by the recorder       | `L3`, `CallWithContext.1`                            | `TestMock_scriptsCompleteTask`                |

## Test cases

| Test                                          | Level          | Proves                                                     | Given                                                                | Then                                                                                                              | Must not                                      |
| --------------------------------------------- | -------------- | ---------------------------------------------------------- | -------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| `TestCompletionRecorder_AchievedWithoutCheck` | contract suite | `CallWithContext.4`, `Submission.1`, `F1`, `L1`, AC 1      | Recorder with empty `checkCmd`, input achieved + padded summary      | Returns confirmation, `Submission()` returns `LoopStatusAchieved` and trimmed summary                             | Spawn a shell process                         |
| `TestCompletionRecorder_CheckGate`            | contract suite | `CallWithContext.2`, `CallWithContext.4`, `F2`, `L2`, AC 2 | Recorder with failing then passing `checkCmd`, plus oversized output | Failing command returns error with truncated output and `Submission()` is false; passing command records achieved | Record submission when `checkCmd` exits non-0 |
| `TestCompletionRecorder_BlockedSkipsCheck`    | contract suite | `CallWithContext.3`, `F1`, `F2`, `L1`, AC 3                | Recorder with `checkCmd` that writes a marker file and exits 1       | Calling with blocked status succeeds and records `LoopStatusBlocked`                                              | Execute `checkCmd` or create the marker file  |
| `TestCompletionRecorder_Validation`           | contract suite | `CallWithContext.1`, `F1`, `L1`, AC 4                      | Inputs with missing/non-string/unknown `status` or blank `summary`   | Returns an error naming the invalid field and `Submission()` stays false                                          | Mutate recorded submission state              |
| `TestCompletionRecorder_ResetAndSpec`         | contract suite | `Reset.1`, `NewCompletionRecorder.1`, `F1`, AC 5           | Recorder after a valid call                                          | `Reset()` clears `Submission()`; `Specification()` advertises `complete_task` with required `status` and `summary` | Retain stale submission after `Reset()`       |
| `TestMock_scriptsCompleteTask`                | unit           | `L3`, `CallWithContext.1`, AC 6                            | `vendors.Mock` with `tool_complete_task` in the prompt, with and without the two `CLAI_MOCK_COMPLETE_TASK_*` variables | Emits one `complete_task` call per ordinal with the scripted or default status and summary; the recorder accepts each | Emit a call when the prompt lacks `tool_complete_task` |

## Error management

| Failure                                              | Expected outcome                                                                                | Test                                |
| ---------------------------------------------------- | ----------------------------------------------------------------------------------------------- | ----------------------------------- |
| Missing, non-string, or unknown `status`             | Error naming `status` and the allowed values; `Submission()` unchanged                          | `TestCompletionRecorder_Validation` |
| Missing, non-string, or whitespace-only `summary`    | Error naming `summary`; `Submission()` unchanged                                                | `TestCompletionRecorder_Validation` |
| Verification command exits non-zero or ctx cancelled | Error naming the verification failure and bounded combined output; `Submission()` remains unset | `TestCompletionRecorder_CheckGate`  |

## Golden diff

None.

## Removes

None.

## Local facts

**L1 — Normalization.** `summary` is trimmed of leading and trailing whitespace
before validation and storage; internal newlines are preserved so multi-line
summaries remain readable.

**L2 — Verification output cap.** Combined stdout and stderr from a failing
verification command is capped at 4000 runes before being formatted into the
returned error.

**L3 — Mock scripting.** `vendors.Mock` gains a `complete_task` case in its
scripted tool inputs: a `tool_complete_task` token in the last user message
triggers one call per ordinal, with `status` from
`CLAI_MOCK_COMPLETE_TASK_STATUS` (default `achieved`) and `summary` from
`CLAI_MOCK_COMPLETE_TASK_SUMMARY` (default `Mock loop summary.`). Phase 3
consumes this scripting as a recorded fixture.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                                                                           | Result             |
| ----- | ----------- | --------------------------------------------------------------------------------- | ------------------ |
| start | c95cf6c+wip | `go test ./internal/text/ ./internal/vendors/ -race -count=1 -timeout=60s`       | passed             |
| end   | c95cf6c+wip | `go test ./internal/text/ ./internal/vendors/ -race -count=3 -cover -timeout=30s` | passed             |
| end   | c95cf6c+wip | `go test ./... -race -cover -count=3 -timeout=30s -p 1`                           | passed             |
| end   | c95cf6c+wip | `make qa`                                                                         | host-load timeouts |

The exact `make qa` test command timed out in `e2e`, `internal/audio`,
`internal/text` and `internal/tools/mcp` while the host load average was 16.8
on 8 cores. The same suite with `-p 1` passed. The lint gates (`gofumpt`,
`staticcheck`, `go vet`, `go fix`) passed, and `dupl -t 80 .` found no clones
in the changed files. `go tool cover -func` reports 100% coverage for every
function in `internal/text/loop_tool.go`.

Decisions:

- D1 — 2026-10-21, worker session `clai-loop-phase-1`: validation trims
  `status` and `summary` and runs before the verification command, so an
  invalid call never executes a shell command.
- D2 — Success returns `"recorded"`, matching the phase 2/3 flow.
- D3 — The 4000-rune cap uses the local `boundedVerificationOutput` helper.
  The existing `limitToolOutput` writes oversized output to a temporary file
  and describes that file, which does not satisfy F2's combined-output
  requirement.
- D4 — The mock uses the literal default `"achieved"`; importing
  `internal/text` from `internal/vendors` would create a production import
  cycle, and the tool argument is protocol text.

Andon: exact `make qa` could not pass on the loaded host; sequential suite
passed.

Surprises: none.

## Review findings

None.
