# Phase 6 — Diagnostic output

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Present diagnostics and terminal errors without corrupting the result stream.

## Interface

Written to `internal/diag/output.go` (F10).

```go
package diag

import (
	"context"

	"github.com/baalimago/clai/pkg/diagnostic"
)

type ErrorReporter interface {
	// ReportError displays each independent cause once, with known remedies,
	// on the owner's diagnostic destination. It preserves the supplied error
	// and returns a failed or short write. (ReportError.1, F2, F8)
	//
	// The owner supplies the presenter's Level. At LevelDebug or above each
	// cause shows its operation and location; at LevelTrace it also shows
	// its origin-captured stack; below LevelDebug it shows causes and
	// remedies only. (ReportError.2, F8)
	ReportError(context.Context, error) error
}

// Viewport is the consumed shape of board.Board.Log: an in-place display
// that places a persistent line above itself. This phase declares no clause
// for it; the owner detaches a viewport before it stops accepting lines.
type Viewport interface {
	Log(line string)
}

// Presenter is the Deliverer for the Immediate output mode and the terminal
// error presenter. It is internal to clai.
type Presenter interface {
	// Attach makes v the active viewport until the returned function runs.
	// While one is attached, Deliver and ReportError place their lines
	// through v.Log; attaches nest, and detaching restores the previous one.
	// The owner detaches before the viewport stops accepting lines, since
	// Attach cannot observe that. (Attach.1, F1)
	Attach(v Viewport) (detach func())

	// Deliver writes one diagnostic to the owner's diagnostic destination,
	// never to a result writer, through the attached Viewport when there is
	// one. It escapes terminal controls and applies best-effort redaction to
	// external text. (Deliver.2, F1, F7, F10)
	//
	// It uses the configured theme on a terminal, plain text on a pipe or
	// with truthy NO_COLOR, and timestamps per the Terminal timestamps
	// parameter (README Parameters). (Deliver.3)
	//
	// It returns a failed or short write. (Deliver.4, F6)
	diagnostic.Deliverer

	ErrorReporter
}
```
Consumes: `Locator` and `SourceLocation` (phase 1), `Remedier` and
`Remedy` (phase 2), `Level` (phase 3), `Deliverer` and `Event` (phase 4), `internal/look:Style`
(import), and `internal/board:Board.Log` (import, the `Viewport` a board
satisfies).

## Acceptance criteria

| #   | Criterion                                                                                  | Proves                    | Proof                                           |
| --- | ------------------------------------------------------------------------------------------ | ------------------------- | ----------------------------------------------- |
| 1   | Diagnostics use the diagnostic destination and do not corrupt an attached viewport.        | Attach.1, Deliver.2, F1   | `TestPresenterContract`, `TestPresenterViewport` |
| 2   | Terminal, pipe, and `NO_COLOR` output obey the presentation policy.                        | Deliver.3                 | `TestPresenterContract`                         |
| 3   | Output failures return their original causes.                                              | Deliver.4, ReportError.1  | `TestPresenterContract`                         |
| 4   | Independent errors, locations, stacks, and remedies are shown without duplicate incidents. | ReportError.1, ReportError.2, F2, F8 | `TestPresenterErrors`                           |
| 5   | Sensitive data receives best-effort redaction and control characters are escaped.          | Deliver.2, F7             | `TestPresenterSafety`                           |
| 6   | The presenter is a conforming Deliverer.                                                   | Deliver.1                 | `TestDelivererContract`                         |

## Test cases

| Test                     | Level          | Proves                          | Given                                                                            | Then                                                                         | Must not                                                   |
| ------------------------ | -------------- | ------------------------------- | -------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `TestPresenterContract`  | contract suite | Deliver.2, Deliver.3, Deliver.4 | A terminal, a pipe, `NO_COLOR`, and failed or short writers                      | Each destination gets the expected plain or colored record, or a write error | Add diagnostics to a result writer                         |
| `TestPresenterViewport`  | contract suite | Attach.1, Deliver.2, F1         | A recording Viewport attached, nested, and detached, with a persistent diagnostic at each step | Lines go through the innermost attached Viewport, and to the destination after detach | Interleave or mutate process-wide color state              |
| `TestPresenterErrors`    | contract suite | ReportError.1, ReportError.2, F2, F8 | Joined errors, classification sentinels, duplicate remedies, and optional stacks, at each Level | Independent causes and safe remedies appear once; locations from LevelDebug, stacks at LevelTrace | Change the original error tree or invent a stack           |
| `TestPresenterSafety`    | contract suite | Deliver.2, F7                   | Known secret patterns and terminal controls in external text                     | Known secrets are redacted and controls are escaped                          | Write raw credentials or ANSI controls from untrusted text |
| `TestDelivererContract`  | contract suite | Deliver.1                       | The phase 4 suite run against this implementation                                | Delivery or its failure is returned without races                            | Apply a level policy                                       |

## Error management

| Failure                            | Expected outcome                                                                              | Test                    |
| ---------------------------------- | --------------------------------------------------------------------------------------------- | ----------------------- |
| Failed or short diagnostic write   | Return the writer's error or `io.ErrShortWrite`; do not retry through the same failed writer. | `TestPresenterContract` |
| Missing location, stack, or remedy | Show the available cause without inventing missing data.                                      | `TestPresenterErrors`   |

## Golden diff

None.

## Removes

None.

## Local facts

The owner supplies the Level resolved for the default selector.
Best-effort redaction does not guarantee removal of unknown secrets. A joined
classification sentinel is not a separate incident. The original typed
errors retain their provider facts for library callers.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-10: embeds `Deliverer`. V1-11: `DiagnosticOutput` renamed `Presenter`, `ReportError` is `ErrorReporter`. V1-08: Deliver.3 cites the Terminal timestamps parameter. |
| fix-start | 2c9c4ff+wip | `go run mvdan.cc/gofumpt@latest -w -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./... && go vet ./... && go fix ./... && go run github.com/mibk/dupl@latest -t 80 .` | all rc 0 |
| fix-start | 2c9c4ff+wip | `go test ./... -race -cover -count=3 -timeout=30s` | run 1 FAIL, package unidentified (A01); runs 2 and 3: 84 ok, slowest 19–21 s at load 7–12 |
| fix-end   | 2c9c4ff+wip | `go run mvdan.cc/gofumpt@latest -w -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./... && go vet ./... && go fix ./... && go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, twice |
| fix-end   | 2c9c4ff+wip | `go test ./... -race -cover -count=3 -timeout=30s` | run 1: 6 FAIL (A01: 5 testenv `starved` timeouts at load 20–22, `internal/capture` shim race); run 2: 5 FAIL (same `starved` set, load 7→20 during the gate) |
| replan    | 2c9c4ff+wip | —       | dry run: Consumes names `Board.Log`, the member that places a line above the viewport. |
| replan    | 2c9c4ff+wip | —       | F10: moved to `internal/diag/output.go`; embeds `diagnostic.Deliverer`. |
| replan    | 2c9c4ff+wip | —       | round 3 note: `Viewport` and `Presenter.Attach` declared so commands can hand the presenter their board. |
| replan    | 2c9c4ff+wip | —       | round 4 notes: `Viewport` is the consumed shape of `Board.Log`; Attach.1 states the owner's detach-before-close duty. |

Andon: A01.
Surprises:
- The timed-out `internal/audio` binary orphaned its `/bin/sh` ffmpeg shim, looping on `sleep 0.1` under the sandbox's pid 1; killed by hand.

## Review findings

None.
