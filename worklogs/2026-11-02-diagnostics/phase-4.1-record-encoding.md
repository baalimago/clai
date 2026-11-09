# Phase 4.1 — Record encoding

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Encode one output record as one JSON line, the unit of every `-json` stream.

## Interface

Written to `internal/jsonl/jsonl.go`.

```go
package jsonl

import (
	"io"
	"log/slog"

	"github.com/baalimago/clai/pkg/diagnostic"
)

// Format is the output encoding the owner selects for one run. (F11)
type Format uint8

const (
	// FormatText is clai's terminal or plain text output.
	FormatText Format = iota
	// FormatJSON is one JSON object per line. (F12)
	FormatJSON
)

// Record is one line of JSON output: a record type from the Record types
// table (README F14) and its fields, in order.
type Record struct {
	Type   string
	Fields []slog.Attr
}

type Encoder interface {
	// Encode writes r as one JSON object followed by "\n": the key "type"
	// first with r.Type, then each field in order. Values encode as JSON:
	// a string as a string; a bool as a bool; an integer, float, or duration
	// (in seconds) as a number; a time as an RFC 3339 string; a non-nil
	// error as its Error text; a group attribute or a map[string]any as an
	// object with its keys, an empty one as "{}"; a slice as an array, its
	// elements by the same rules; nil as null. Every field in r is written; a
	// caller leaves an absent optional field out of r. (Encode.1, F11, F12)
	//
	// Every string is valid JSON: control characters, including ESC and
	// "\r", are escaped, never written raw, and invalid UTF-8 is replaced
	// with U+FFFD. (Encode.2, F12)
	//
	// Encode holds the Encoder's lock across its one Write to the
	// destination, so concurrent calls never interleave within a line and
	// the destination needs no lock of its own. (Encode.3, F12)
	//
	// An empty r.Type, or a field named "type", writes nothing and returns
	// an error matching ErrInvalidRecord. A failed or short write returns the
	// writer's error or io.ErrShortWrite. (Encode.4)
	Encode(r Record) error
}

// ErrInvalidRecord matches a record Encode refuses.
var ErrInvalidRecord error

// NewEncoder returns the Encoder that writes to w. (NewEncoder.1)
func NewEncoder(w io.Writer) Encoder

// EventRecord is the "diagnostic" record of e: the fields name, subsystem,
// level (the F7 level name), message, and facts, an object of e.Facts that
// is "{}" when e has none. (EventRecord.1, F14)
func EventRecord(e diagnostic.Event) Record
```

Consumes:
`Event` (phase 4)
`Level` (phase 3)

## Acceptance criteria

| #   | Criterion                                                                          | Proves                 | Proof                 |
| --- | ---------------------------------------------------------------------------------- | ---------------------- | --------------------- |
| 1   | Each record is one JSON object on one line with `type` first and fields in order.  | Encode.1, NewEncoder.1, F12 | `TestEncoderContract` |
| 2   | No raw control character or invalid UTF-8 reaches the destination.                 | Encode.2, F12          | `TestEncoderContract` |
| 3   | Concurrent records never interleave within a line, without a locking destination.  | Encode.3, F12          | `TestEncoderContract` |
| 4   | Invalid records write nothing and return errors; failed and short writes return errors. | Encode.4          | `TestEncoderContract` |
| 5   | A diagnostic event maps to the `diagnostic` record of the Record types table.      | EventRecord.1, F14     | `TestEventRecord`     |

## Test cases

| Test                  | Level          | Proves                       | Given                                                                                                                      | Then                                                                                               | Must not                                   |
| --------------------- | -------------- | ---------------------------- | -------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- | ------------------------------------------ |
| `TestEncoderContract` | contract suite | Encode.1–4, NewEncoder.1     | Records with every value kind of Encode.1 (strings, bools, numbers, durations, times, errors, groups, empty groups, maps, slices of objects, nil), ANSI, `\r`, invalid UTF-8; 8 concurrent writers into an unlocked buffer under `-race`; a failing and a short writer | Each line parses with `type` first; empty groups are `{}`; strings round-trip escaped; lines are whole; no race; errors returned | Write a partial line or a raw control byte |
| `TestEventRecord`     | unit           | EventRecord.1                | Events at every Level, with nested facts and with none                                                                     | The record has type `diagnostic` and the five fields, `facts` `{}` when empty                      | Drop a fact or invent a field              |

## Error management

| Failure                         | Expected outcome                                            | Test                  |
| ------------------------------- | ----------------------------------------------------------- | --------------------- |
| Empty type or a `type` field    | Nothing written; error matches `ErrInvalidRecord`.          | `TestEncoderContract` |
| Destination write fails         | The writer's error is returned.                             | `TestEncoderContract` |
| Short write                     | `io.ErrShortWrite` is returned.                             | `TestEncoderContract` |

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
| start | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 87 packages ok, 0 FAIL; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 88 packages ok, 0 FAIL; `internal/jsonl` 88.2% coverage; dupl 41 clone groups |
| end   | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 88 packages ok, 0 FAIL; `internal/jsonl` 88.2% coverage; dupl 41 clone groups |
| fix-start | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 90 packages ok, 0 FAIL; dupl 41 clone groups |
| fix-end | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 91 packages ok, 0 FAIL; `internal/jsonl` 91.6% coverage, `internal/errctx` 98.3%; dupl 41 clone groups |

Andon: none.
Surprises:
- The first end pair failed phase 1's `TestLocatorInventory` on this phase's `fmt.Errorf` `%w` wrappers; they now use `diag.Errorf`.
- `var ErrInvalidRecord error` is kept verbatim and assigned in the package's `init`.
- Encode.1 leaves NaN and ±Inf unstated: they encode as null; a value outside its kinds (a channel, a func, a map without string keys) returns an error and writes nothing; map keys are sorted.
- V9 fix: the `diag.Errorf` calls now use leaf `internal/errctx.Errorf`, so `internal/diag` may import `internal/jsonl` (phase 6).

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- `type` is written first, then the fields in order, in one `Write` under the encoder lock.
- C0, C1, DEL, U+2028 and U+2029 are escaped, and invalid UTF-8 becomes U+FFFD, in keys too.
- NaN and Inf become null; empty groups and maps become `{}`.
- An unsupported kind returns an error and writes nothing.
- `EventRecord` writes exactly F14's five fields.

Note: `levelName` writes `level(n)` for an unregistered `Level`. `NewReporter` does not validate `Type.Level`; only clai's own types reach the JSON presenter.
