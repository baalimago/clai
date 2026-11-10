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
	"io"

	"github.com/baalimago/clai/internal/jsonl"
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
	//
	// A Presenter from NewJSONPresenter writes one F14 "error" record per
	// terminal error through its Encoder, one cause per independent cause,
	// showing what ReportError.1 and ReportError.2 show at its Level, with the
	// provider fields of a claierr API error, and under the Deliver.2
	// redaction. (ReportError.3, F7, F11, F12, F14)
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
	// It uses a fixed per-level palette on a terminal, plain text on a pipe or
	// with truthy NO_COLOR, and timestamps per the Terminal timestamps
	// parameter (README Parameters). (Deliver.3)
	//
	// A Presenter from NewJSONPresenter writes jsonl.EventRecord(e), after
	// the Deliver.2 redaction, through its Encoder, with no theme, viewport
	// placement, or timestamp, and Attach has no effect on it. The format
	// changes the encoding only, never the destination. (Deliver.5, F7, F11,
	// F12)
	//
	// It returns a failed or short write. (Deliver.4, F6)
	diagnostic.Deliverer

	ErrorReporter
}

// Color is the text presenter's color choice, which the owner derives from the
// destination's terminal state and NO_COLOR (Deliver.3).
type Color uint8

const (
	Plain Color = iota
	Colored
)

// NewTextPresenter returns the Presenter that writes text to w, in color, at
// level. (NewTextPresenter.1, F11)
func NewTextPresenter(w io.Writer, color Color, level diagnostic.Level) Presenter

// NewJSONPresenter returns the Presenter that writes records through enc,
// the one Encoder of its destination, at level. (NewJSONPresenter.1, F11,
// F12)
func NewJSONPresenter(enc jsonl.Encoder, level diagnostic.Level) Presenter
```
Consumes:
`Locator` (phase 1)
`SourceLocation` (phase 1)
`Remedier` (phase 2)
`Remedy` (phase 2)
`Level` (phase 3)
`Deliverer` (phase 4)
`Event` (phase 4)
`Encoder` (phase 4.1)
`EventRecord` (phase 4.1)
`pkg/claierr:APIErrorer` (import)
`internal/board:Board.Log` (import, the `Viewport` a board satisfies)

## Acceptance criteria

| #   | Criterion                                                                                  | Proves                    | Proof                                           |
| --- | ------------------------------------------------------------------------------------------ | ------------------------- | ----------------------------------------------- |
| 1   | Diagnostics use the diagnostic destination and do not corrupt an attached viewport.        | Attach.1, Deliver.2, F1   | `TestPresenterContract`, `TestPresenterViewport` |
| 2   | Terminal, pipe, and `NO_COLOR` output obey the presentation policy.                        | Deliver.3                 | `TestPresenterContract`                         |
| 3   | Output failures return their original causes.                                              | Deliver.4, ReportError.1  | `TestPresenterContract`                         |
| 4   | Independent errors, locations, stacks, and remedies are shown without duplicate incidents. | ReportError.1, ReportError.2, F2, F8 | `TestPresenterErrors`                           |
| 5   | Sensitive data receives best-effort redaction and control characters are escaped.          | Deliver.2, F7             | `TestPresenterSafety`                           |
| 6   | The presenter is a conforming Deliverer.                                                   | Deliver.1                 | `TestDelivererContract`                         |
| 7   | Under the JSON format, diagnostics and terminal errors are records on the same destination. | Deliver.5, ReportError.3, NewJSONPresenter.1, F11, F12, F14 | `TestPresenterJSON` |
| 8   | The text presenter writes to its writer with its color and level.                          | NewTextPresenter.1, F11   | `TestPresenterContract`                         |

## Test cases

| Test                     | Level          | Proves                          | Given                                                                            | Then                                                                         | Must not                                                   |
| ------------------------ | -------------- | ------------------------------- | -------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `TestPresenterContract`  | contract suite | Deliver.2, Deliver.3, Deliver.4, NewTextPresenter.1 | A terminal, a pipe, `NO_COLOR`, and failed or short writers                      | Each destination gets the expected plain or colored record, or a write error | Add diagnostics to a result writer                         |
| `TestPresenterViewport`  | contract suite | Attach.1, Deliver.2, F1         | A recording Viewport attached, nested, and detached, with a persistent diagnostic at each step | Lines go through the innermost attached Viewport, and to the destination after detach | Interleave or mutate process-wide color state              |
| `TestPresenterErrors`    | contract suite | ReportError.1, ReportError.2, F2, F8 | Joined errors, classification sentinels, duplicate remedies, and optional stacks, at each Level | Independent causes and safe remedies appear once; locations from LevelDebug, stacks at LevelTrace | Change the original error tree or invent a stack           |
| `TestPresenterSafety`    | contract suite | Deliver.2, Deliver.5, ReportError.3, F7 | Known secret patterns and terminal controls in external text, in the text and the JSON format | Known secrets are redacted and controls are escaped                          | Write raw credentials or ANSI controls from untrusted text |
| `TestDelivererContract`  | contract suite | Deliver.1                       | The phase 4 suite with both presenters added to its implementations map                                | Delivery or its failure is returned without races                            | Apply a level policy                                       |
| `TestPresenterJSON`      | contract suite | Deliver.5, ReportError.3, NewJSONPresenter.1 | The JSON format with events at each Level, an attached viewport, joined errors, and a claierr auth failure with status and provider code | Each line is a `diagnostic` or `error` record with the Record types fields, on the diagnostic destination | Write ANSI, timestamps, or text that is not a record; use the viewport |

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

The phase joins phase 4's `TestDelivererContract` by adding entries to its
implementations map in `pkg/diagnostic/event_test.go`; phase 4's clauses and
cases stay unchanged.
`internal/diag` cannot import `internal/look`: `look` imports `utils`, which
imports `diag`. The presenter therefore takes `Color` from its owner instead
of a `look.Style`.
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
| replan    | 0ea7c95+wip | —       | `-json`: Deliver.5 and ReportError.3 add the JSON format on the same destination (F11, F12, F14); consumes phase 4.1. |
| replan    | 0ea7c95+wip | —       | spawn-review round 1: `NewTextPresenter` (with its own `Color`; `internal/look` would be an import cycle) and `NewJSONPresenter` declared (the format is chosen by constructor, one Encoder per destination); redaction applies to JSON (F7); Consumes one member per line. |
| replan    | 0ea7c95+wip | —       | A03 sweep: joins phase 4's suite through its implementations map. |
| start     | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 91 packages ok, 0 FAIL, dupl 41 clone groups |
| end       | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && go run github.com/mibk/dupl@latest -t 80 .` | run order test, dupl, vet, fix, staticcheck, gofumpt: all rc 0, 91 packages ok, 0 FAIL, dupl 41 clone groups |
| end       | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && go run github.com/mibk/dupl@latest -t 80 .` | run order fix, gofumpt, vet, test, staticcheck, dupl: all rc 0, 91 packages ok, 0 FAIL, dupl 41 clone groups |
| replan    | 0ea7c95+wip | —       | R1-10: Deliver.3 says a fixed per-level palette (the presenter cannot reach the user theme). A05: F14 gains the optional `stack` the JSON `error` record carries at LevelTrace (ReportError.3 unchanged). |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, `go test` with `-shuffle=on` | all rc 0, 93 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, fresh shuffle seed | all rc 0, 93 packages ok, 0 FAIL, dupl 43 clone groups |

Andon: A01.
Surprises:
- The timed-out `internal/audio` binary orphaned its `/bin/sh` ffmpeg shim, looping on `sleep 0.1` under the sandbox's pid 1; killed by hand.

Andon: none.
Surprises:
- R1-14: the JSON `error` record writes each cause's origin stack at `LevelTrace` in F14's optional `stack`.
- The constructors carry only `Color`, so the colored text presenter uses a fixed level palette; the user's `theme.json` (in `internal/utils`) is out of reach of `internal/diag`.
- Remedies are clai-authored, so they are escaped but not redacted (redaction turned `OPENAI_API_KEY=...` examples into `[REDACTED]`).
- Text level names reuse `levelLabel` from `buffered.go`; no third copy of the switch.

## Review findings

### Review 1, 2026-11-09 (root)

**A05 · design andon (README).** ReportError.3 requires the JSON `error` record to show "what ReportError.1 and ReportError.2 show at its Level", which at `LevelTrace` includes the origin stack. F14 has no `stack` field, and "Fields not listed are not written".
- No implementation meets both, so `internal/diag/presenter.go:220-241` writes no stack.
- Effect: `-json` with `LOG_LEVEL=trace` can never deliver the trace detail F8 promises.
- The maintainer decides one of:
  - Add an optional F14 cause `stack` (minor, through an injected phase).
  - Amend ReportError.3 to "within F14's fields".

**R1-10 · minor · owner plan.** Deliver.3 says "the configured theme on a terminal", but `presenter.go:16-23,166-177` uses a fixed level palette, and `NewTextPresenter(w, Color, level)` cannot carry a theme. `utils.Theme` has no warning or error slot, so nothing is lost today.
- [ ] Reword Deliver.3 to a fixed palette, or route the theme through `Color`.

Verified good:
- The Interface matches.
- Text and JSON redact and escape message, facts, operations and causes.
- Remedies are escaped, not redacted (they are clai-authored).
- Location shows from `LevelDebug`, the stack only at trace (text).
- Attach nests and detaches in any order; JSON Attach does nothing.

Note: `NO_COLOR` is read twice. `compose.go:213` uses `misc.Truthy` for the presenter, while `compose.go:260` (the runenv `Color` for producers) keeps ancli's `!= "true"` rule. So `NO_COLOR=1` gives plain diagnostics but coloured boards, which is HEAD's behaviour for boards.
