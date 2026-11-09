# Phase 4.1.1.1 — Streams

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Hand every producer its stdout, stderr, and stdin for one run, in the run's
format, so no producer touches the process streams itself.

## Interface

Written to `internal/stdio/stdio.go`.

```go
package stdio

import (
	"io"

	"github.com/baalimago/clai/internal/jsonl"
)

// Stream names an output destination.
type Stream uint8

const (
	Stdout Stream = iota
	Stderr
)

// Config is what the owner reads from the process once and injects.
type Config struct {
	Format         jsonl.Format
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	StdinTerminal  bool
	StdoutTerminal bool
	StderrTerminal bool
}

type OutputProvider interface {
	// Writer returns the writer for s. Under FormatText it is the configured
	// writer; under FormatJSON it is the "output" Wrapper of s, with field
	// stream "stdout" or "stderr", over Encoder(s). (Writer.1, F11, F12)
	Writer(s Stream) io.Writer

	// Encoder returns the one Encoder of s for the run, shared by every
	// record writer of that destination; the same call returns the same
	// Encoder. Under FormatText it returns nil and an error matching
	// ErrTextFormat. (Encoder.1, F12)
	Encoder(s Stream) (jsonl.Encoder, error)

	// IsTerminal reports the configured terminal state of s under
	// FormatText, and false under FormatJSON, so producers choose their
	// pipe form. (IsTerminal.1, F12)
	IsTerminal(s Stream) bool
}

type InputProvider interface {
	// Input returns the configured stdin. Under FormatJSON each Read first
	// flushes the Wrappers of both outputs; when a flush fails, the Read
	// returns that error and reads nothing. A question written before a read
	// is a record before the read starts. (Input.1, F13)
	Input() io.Reader

	// InputIsTerminal reports the configured stdin terminal state in every
	// format. (InputIsTerminal.1, F11, F13)
	InputIsTerminal() bool
}

// StreamProvider is the run's streams. Close closes both Wrappers under
// FormatJSON and returns their errors joined; under FormatText it does
// nothing. (Close.1)
type StreamProvider interface {
	OutputProvider
	InputProvider
	io.Closer
}

// ErrTextFormat matches Encoder under FormatText.
var ErrTextFormat error

// New returns the StreamProvider for cfg. (New.1)
func New(cfg Config) StreamProvider
```

Consumes:
`Format` (phase 4.1)
`NewEncoder` (phase 4.1)
`Encoder` (phase 4.1)
`NewWrapper` (phase 4.1.1)
`Wrapper` (phase 4.1.1)

## Acceptance criteria

| #   | Criterion                                                                                  | Proves                                    | Proof                        |
| --- | ------------------------------------------------------------------------------------------ | ----------------------------------------- | ---------------------------- |
| 1   | Under the text format, producers get the configured streams and terminal states.           | Writer.1, IsTerminal.1, F11               | `TestStreamProviderContract` |
| 2   | Under the JSON format, writes become `output` records through one Encoder per destination. | Writer.1, Encoder.1, F12                  | `TestStreamProviderContract` |
| 3   | Under the JSON format, outputs are never terminals, and stdin keeps its terminal state.    | IsTerminal.1, InputIsTerminal.1, F12, F13 | `TestStreamProviderContract` |
| 4   | A question written without a newline is a record before the read that follows it.         | Input.1, F13                              | `TestStreamProviderContract` |
| 5   | Close emits pending text and reports failures.                                             | Close.1, New.1                            | `TestStreamProviderContract` |

## Test cases

| Test                         | Level          | Proves                                                                         | Given                                                                                                                                                                                  | Then                                                                                                                                                  | Must not                                    |
| ---------------------------- | -------------- | ------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------- |
| `TestStreamProviderContract` | contract suite | Writer.1, Encoder.1, IsTerminal.1, Input.1, InputIsTerminal.1, Close.1, New.1 | Both formats with recording writers and every terminal combination; text written to `Writer` and records to `Encoder` from several goroutines while another reads `Input`, under `-race`; a prompt without `\n` then a Read; a failing flush; a failing destination at Close | Text format passes through; JSON format yields whole records on the right destination, one Encoder each; the prompt record precedes the read; a failed flush fails the Read; errors returned | Read the process streams or the environment |

## Error management

| Failure                         | Expected outcome                                     | Test                         |
| ------------------------------- | ---------------------------------------------------- | ---------------------------- |
| `Encoder` under the text format | nil and an error matching `ErrTextFormat`.           | `TestStreamProviderContract` |
| Flush before a read fails       | The Read returns the flush error and reads nothing.  | `TestStreamProviderContract` |
| Close fails on a destination    | The joined Wrapper errors are returned.              | `TestStreamProviderContract` |

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
| start | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 88 packages ok, 0 FAIL; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s -shuffle=on && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 89 packages ok, 0 FAIL; `internal/stdio` 100.0% coverage; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s -shuffle=on && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 89 packages ok, 0 FAIL; `internal/stdio` 100.0% coverage; dupl 41 clone groups |

Andon: none.
Surprises:
- `stdio.go` holds the interface verbatim; `New`'s body calls `newProvider` in `provider.go`, and `ErrTextFormat` is assigned in `init`.
- An out-of-range `Stream` gets a Writer that always fails, a nil Encoder with an error, and `IsTerminal` false; the interface leaves it unspecified.
- Stdin errors, `io.EOF` included, pass through `Input` unwrapped; only a flush failure is wrapped.

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- `IsTerminal` is false under JSON, while `InputIsTerminal` keeps the real stdin state.
- Every JSON read flushes both Wrappers first.
- Close joins the Wrappers' errors.

See R1-03 (phase 9): the JSON `Input()` hides the stdin's `Stat`.
