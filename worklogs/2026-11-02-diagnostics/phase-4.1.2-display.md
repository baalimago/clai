# Phase 4.1.2 — Display

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Give the reply, reasoning, tool activity, progress, and prompts one display
contract for the JSON format.

## Interface

Written to `internal/display/display.go`.

```go
package display

import "github.com/baalimago/clai/internal/jsonl"

// ToolCall is one tool invocation the model requested.
type ToolCall struct {
	Name   string
	Inputs map[string]any
}

// ToolResult is the text one tool invocation returned to the model.
type ToolResult struct {
	Name   string
	Output string
}

// Progress is the state of one row of a board, or of a status line when Row
// is empty. State is one of the progress states of F14.
type Progress struct {
	Title  string
	Row    string
	State  string
	Detail string
}

// Prompt is a question clai asks before it reads an answer. Kind is one of
// the prompt kinds of F14.
type Prompt struct {
	Kind    string
	Text    string
	Choices []string
}

type ReplyWriter interface {
	// ReplyDelta shows one streamed part of the reply; the deltas of a reply
	// concatenate to its text. (ReplyDelta.1, F14)
	ReplyDelta(text string) error
	// ReasoningDelta shows one streamed part of the model's reasoning, kept
	// apart from the reply. (ReasoningDelta.1, F14)
	ReasoningDelta(text string) error
	// Reply shows the whole final reply once. (Reply.1, F14)
	Reply(text string) error
}

type ToolReporter interface {
	// ReportToolCall shows a call before the tool runs. (ReportToolCall.1, F14)
	ReportToolCall(ToolCall) error
	// ReportToolResult shows the result, with the failure state F14 defines
	// for tool_result. (ReportToolResult.1, F14)
	ReportToolResult(ToolResult) error
}

type ProgressReporter interface {
	// ReportProgress shows one state change. (ReportProgress.1, F14)
	ReportProgress(Progress) error
}

type Prompter interface {
	// ShowPrompt shows the question only; reading the answer stays with the
	// caller and its input. (ShowPrompt.1, F13, F14)
	ShowPrompt(Prompt) error
}

// Displayer is the JSON display of one destination. Each method writes one
// record to that destination only, in call order, and returns a failed
// write. (Displayer.1, F1, F11, F12)
type Displayer interface {
	ReplyWriter
	ToolReporter
	ProgressReporter
	Prompter
}

// NewJSONDisplayer returns the Displayer that encodes every call as the
// record F14 names, through enc, and nothing else. (NewJSONDisplayer.1, F12,
// F14)
func NewJSONDisplayer(enc jsonl.Encoder) Displayer
```

Consumes:
`Encoder` (phase 4.1)
`Record` (phase 4.1)

## Acceptance criteria

| #   | Criterion                                                                                   | Proves                                                                                                       | Proof                   |
| --- | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ | ----------------------- |
| 1   | Each call is one record on the Displayer's destination, in order; write failures return.    | Displayer.1, ReplyDelta.1, ReasoningDelta.1, Reply.1, ReportToolCall.1, ReportToolResult.1, ReportProgress.1, ShowPrompt.1 | `TestDisplayerContract` |
| 2   | The records are exactly the types and fields F14 names, including a tool failure.          | NewJSONDisplayer.1, ReportToolResult.1, F12, F14                                                             | `TestDisplayerContract` |

## Test cases

| Test                    | Level          | Proves                                                                                                                              | Given                                                                                                                                          | Then                                                                                                     | Must not                                |
| ----------------------- | -------------- | ----------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------- |
| `TestDisplayerContract` | contract suite | Displayer.1, ReplyDelta.1, ReasoningDelta.1, Reply.1, ReportToolCall.1, ReportToolResult.1, ReportProgress.1, ShowPrompt.1, NewJSONDisplayer.1 | The implementation over a recording and a failing Encoder; one call of each method, tool inputs with nested values and none, a tool output with and without the `ERROR: ` prefix, a prompt with choices | One record per call with the type and fields F14 names, in order; `ok:false` and `error` for the failure; `inputs` `{}` when none; write errors returned | Write text that is not a record, or to another destination |

## Error management

| Failure                 | Expected outcome                         | Test                    |
| ----------------------- | ---------------------------------------- | ----------------------- |
| Encoder write fails     | The method returns the Encoder's error.  | `TestDisplayerContract` |

## Golden diff

None.

## Removes

None.

## Local facts

None.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                      | Result                |
| ----- | ----------- | ---------------------------- | --------------------- |
| start | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 89 packages ok, 0 FAIL; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s -shuffle=on && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 90 packages ok, 0 FAIL; `internal/display` 100.0% coverage; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s -shuffle=on && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 90 packages ok, 0 FAIL; `internal/display` 100.0% coverage; dupl 41 clone groups |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s -shuffle=on ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 94 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates | all rc 0, 96 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, fresh shuffle seed | all rc 0, 96 packages ok, 0 FAIL, dupl 43 clone groups |

Andon: none.
Surprises:
- `display.go` holds the interface verbatim; `NewJSONDisplayer`'s body returns the `jsonDisplayer` in `json.go`.
- A tool output of exactly `ERROR: ` is a failure with an empty first line, so the optional `error` field is omitted.
- Encoder errors return unwrapped; the Encoder already adds the record type as context.
- R3-06: the `error` field drops the `\r` of a CRLF first line.

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- `inputs` is `{}` when nil.
- `ok` is false exactly when the output starts with `ERROR: `, and `error` holds the rest of that first line.
- Optional fields are omitted when empty.
- Callers pass only F14 states and the prompt kind `trust`.
- On the `prefix+out` path (`internal/text/tool_executor.go:460`), `ok` follows what the model receives.
