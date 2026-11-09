# Phase 4.1.1 — Line wrapping

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Turn text that has no structured record into JSON line records, so every byte
of a `-json` stream is JSON.

## Interface

Written to `internal/jsonl/wrap.go`.

```go
package jsonl

import (
	"io"
	"log/slog"
)

// Wrapper turns written text into records: the catch-all "output" record of
// F12, or the "child_output" record of a relayed child process.
type Wrapper interface {
	// Write accepts any bytes. Each completed line ("\n") becomes one record
	// of the Wrapper's type, with its fields and a "text" field holding the
	// line without its terminator. (Write.1, F12, F14)
	//
	// Terminal presentation is removed from the text: ANSI CSI and OSC
	// sequences are dropped, and a "\r" not followed by "\n" discards the
	// text before it on that line, as a terminal redraw would; a "\r" that
	// ends one Write and a "\n" that starts the next are one "\r\n".
	// (Write.2, F12)
	//
	// Write returns len(p) and nil once the completed lines are encoded, and
	// the Encoder's error otherwise. (Write.3)
	//
	// Write, Flush, and Close are safe for concurrent use; the lines of one
	// call are encoded before another call's, and a Flush never splits a
	// line another Write is completing. (Write.4, F12)
	io.Writer

	// Flush encodes the pending unterminated line, if any, as one record,
	// so text written before an input read is visible before the read; it
	// returns that Encode's error. (Flush.1, F13)
	Flush() error

	// Close flushes, then makes every later Write return an error matching
	// ErrWrapperClosed. (Close.1)
	io.Closer
}

// ErrWrapperClosed matches a Write after Close.
var ErrWrapperClosed error

// NewWrapper returns the Wrapper that encodes records of recordType with
// fields, then "text", through enc. (NewWrapper.1)
func NewWrapper(enc Encoder, recordType string, fields ...slog.Attr) Wrapper
```

Consumes:
`Encoder` (phase 4.1)
`Record` (phase 4.1)

## Acceptance criteria

| #   | Criterion                                                                        | Proves                      | Proof                 |
| --- | -------------------------------------------------------------------------------- | --------------------------- | --------------------- |
| 1   | Completed lines become records with the given type, fields, and text.            | Write.1, NewWrapper.1, F12  | `TestWrapperContract` |
| 2   | ANSI sequences and redraws do not reach the record text.                         | Write.2, F12                | `TestWrapperContract` |
| 3   | Encoder failures are returned.                                                   | Write.3                     | `TestWrapperContract` |
| 4   | A pending partial line is emitted on Flush and on Close; Close ends the Wrapper. | Flush.1, Close.1, F13       | `TestWrapperContract` |
| 5   | Concurrent Write, Flush, and Close keep every line whole.                        | Write.4, F12                | `TestWrapperContract` |

## Test cases

| Test                  | Level          | Proves                                    | Given                                                                                                                                  | Then                                                                                   | Must not                                                |
| --------------------- | -------------- | ----------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- | ------------------------------------------------------- |
| `TestWrapperContract` | contract suite | Write.1–4, Flush.1, Close.1, NewWrapper.1 | Writers and a flusher on several goroutines under `-race`; lines split across writes, colored text, a spinner redrawn with `\r`, `\r\n` and a `\r` ending one write with `\n` starting the next, a prompt ending without `\n` then Flush, a trailing partial line then Close, a failing Encoder | One record per completed line with clean text; Flush and Close emit the partial line; errors returned | Emit a record for a discarded redraw frame, or lose a line |

## Error management

| Failure               | Expected outcome                                     | Test                  |
| --------------------- | ---------------------------------------------------- | --------------------- |
| Encoder fails         | Write, Flush, or Close returns the Encoder's error.  | `TestWrapperContract` |
| Write after Close     | Error matches `ErrWrapperClosed`.                    | `TestWrapperContract` |

## Golden diff

None.

## Removes

None.

## Local facts

A line that is empty after a redraw is still a line when it ends in "\n"; only
the discarded frames before a "\r" produce no record. Flush with no pending
text writes nothing.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                      | Result                |
| ----- | ----------- | ---------------------------- | --------------------- |
| start | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 88 packages ok, 0 FAIL; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 88 packages ok, 0 FAIL; `internal/jsonl` 91.6% coverage; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 88 packages ok, 0 FAIL; `internal/jsonl` 91.1% coverage; dupl 41 clone groups |
| fix-start | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 90 packages ok, 0 FAIL; dupl 41 clone groups |
| fix-end | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 91 packages ok, 0 FAIL; `internal/jsonl` 91.6% coverage, `internal/errctx` 98.3%; dupl 41 clone groups |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s -shuffle=on ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 94 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates | all rc 0, 96 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, fresh shuffle seed | all rc 0, 96 packages ok, 0 FAIL, dupl 43 clone groups |

Andon: none.
Surprises:
- `wrap.go` holds the interface verbatim; `NewWrapper`'s body calls `newWrapper` in `wrapper.go` so the file keeps the interface's imports, and `ErrWrapperClosed` is assigned in `init`.
- After a Flush, an empty rest of that line ending in "\n" writes no record; a non-empty rest is its own record.
- A "\n" inside an unterminated CSI or OSC sequence ends the sequence and the line, so no line is lost; an ESC that starts neither stays in the text, escaped by the Encoder.
- On an Encoder error, Write returns the bytes consumed through the failing line's "\n" and stops.
- V9 fix: the `diag.Errorf` calls now use leaf `internal/errctx.Errorf`, so `internal/diag` may import `internal/jsonl` (phase 6).
- R3-05: an ESC, its intermediate bytes (0x20–0x2f) and its final byte (0x30–0x7e) are dropped (`ESC ( B`, `ESC 7`, `ESC 8`); a stray ESC cut by a line end is dropped too. This supersedes the earlier note that such sequences stay in the text.

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- One mutex covers Write, Flush and Close.
- CSI and OSC are dropped even when split across writes.
- A `\r` ending one write and a `\n` starting the next count as `\r\n`.
- Close is idempotent, and later writes match `ErrWrapperClosed`.

Notes:
- Escape sequences other than CSI and OSC (for example `ESC ( B`) stay in the text as `\u001b`; no producer emits one.
- A Flush right after a trailing `\r` emits the frame before the `\r`.
