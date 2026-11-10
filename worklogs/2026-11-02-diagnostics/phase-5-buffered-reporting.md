# Phase 5 — Buffered reporting

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Keep buffered diagnostics bounded and retain overflow in separate files.

## Interface

Written to `internal/diag/buffer.go` (F10).

```go
package diag

import (
	"context"
	"errors"

	"github.com/baalimago/clai/pkg/diagnostic"
)

// ErrUndeliverable marks an error from Deliver or Finish whose spill write
// and immediate delivery both failed; the owner decides what follows (F6).
var ErrUndeliverable = errors.New("diagnostic undeliverable")

// BufferedDeliverer is the Deliverer for the Buffered output mode. It is
// internal to clai.
type BufferedDeliverer interface {
	// Deliver adds the event to the run's shared buffer. When the buffer
	// reaches the Shared buffer size (README Parameters) it flushes to a new
	// timestamped spill file in the Spill-file directory (README Parameters)
	// and resets. An oversized event is truncated and marked. (Deliver.2, F4, F5, F10)
	//
	// A failed spill-file write delivers the buffer through the owner's
	// immediate Deliverer and resets. When that delivery fails too, Deliver
	// returns an error that matches ErrUndeliverable and retains both
	// failures; it never panics, the owner decides. (Deliver.3, F6)
	diagnostic.Deliverer

	// Finish delivers, through the owner's immediate Deliverer, one event that
	// lists the retained spill-file paths in flush order, then each remaining
	// buffered event. It delivers no spill-file contents. It returns a failed
	// delivery, matching ErrUndeliverable when nothing could be reported.
	// (Finish.1, F5, F6)
	Finish(context.Context) error
}

// ByteSize is a size in bytes.
type ByteSize int

// NewBufferedDeliverer returns the BufferedDeliverer of one run: its shared
// buffer holds up to size bytes (the owner passes the Shared buffer size,
// README Parameters), it spills into new files in dir (the owner passes the
// Spill-file directory), and it delivers through immediate. It reads no
// environment. A size of zero or less keeps no buffer: every event goes
// through the spill path. An empty dir makes every spill fail, never writing
// outside the Spill-file directory, so a spill falls back to immediate as
// Deliver.3 states.
// (NewBufferedDeliverer.1, F4, F5, F6)
func NewBufferedDeliverer(dir string, size ByteSize, immediate diagnostic.Deliverer) BufferedDeliverer
```
Consumes:
`Deliverer` (phase 4)
`Event` (phase 4)
phase 4 criterion 4 (its `TestDelivererContract`, whose implementations map is the suite's extension point)

## Acceptance criteria

| #   | Criterion                                                                       | Proves            | Proof                             |
| --- | ------------------------------------------------------------------------------- | ----------------- | --------------------------------- |
| 1   | A run has one bounded buffer shared by its buffered types.                      | Deliver.2, F4, F5 | `TestBufferedDelivererContract`   |
| 2   | Every overflow creates a retained timestamped file in the selected directory.   | Deliver.2, F5     | `TestBufferedDelivererOverflow`   |
| 3   | A spill failure falls back to the immediate Deliverer; a double failure returns both errors without growing the buffer. | Deliver.3, F6 | `TestBufferedDelivererFallback` |
| 4   | At completion, output lists paths and remaining events without replaying files. | Finish.1, F5      | `TestBufferedDelivererFinish`     |
| 5   | The buffer is a conforming Deliverer.                                           | Deliver.1         | `TestDelivererContract`           |
| 6   | The constructor wires the size, directory, and immediate Deliverer the owner passes, and reads no environment. | NewBufferedDeliverer.1 | `TestBufferedDelivererContract` |

## Test cases

| Test                            | Level          | Proves        | Given                                                           | Then                                                                                       | Must not                                      |
| ------------------------------- | -------------- | ------------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------ | --------------------------------------------- |
| `TestBufferedDelivererContract` | contract suite | Deliver.2, NewBufferedDeliverer.1 | Two types and an oversized event, with a small size, a temporary dir, and a recording immediate Deliverer; a size of 0 and an empty dir; `CLAI_CACHE_DIR` set to another temporary dir | One buffer of the given size preserves order and marks truncation; spills land in the given dir; size 0 sends every event down the spill path; an empty dir fails every spill and falls back; nothing lands under `CLAI_CACHE_DIR` |  Grow memory with the event count              |
| `TestBufferedDelivererOverflow` | contract suite | Deliver.2, F5 | Repeated overflows into the directory the owner supplied        | Each flush creates a distinct retained timestamped file                                    | Replace an earlier spill file                 |
| `TestBufferedDelivererFallback` | contract suite | Deliver.3, F6 | A failed spill write, alone and with a failing immediate Deliverer | The immediate Deliverer receives the buffer, or the error matches `ErrUndeliverable` and retains both failures | Panic, cancel the operation, or retry without a bound |
| `TestBufferedDelivererFinish`   | contract suite | Finish.1      | Several spill files and a partial buffer                        | Paths appear in flush order followed by the remainder on the immediate Deliverer           | Deliver file contents or delete the files     |
| `TestDelivererContract`         | contract suite | Deliver.1     | The phase 4 suite with this implementation added to its implementations map               | Delivery or its failure is returned without races                                          | Apply a level policy                          |

## Error management

| Failure                         | Expected outcome                                                           | Test                            |
| ------------------------------- | -------------------------------------------------------------------------- | ------------------------------- |
| Spill-file write fails          | Deliver the buffered diagnostics through the immediate Deliverer and reset. | `TestBufferedDelivererFallback` |
| Spill write and immediate delivery both fail | Return an error matching `ErrUndeliverable` that retains both failures; never panic. | `TestBufferedDelivererFallback` |

## Golden diff

None.

## Removes

None.

## Local facts

The phase joins phase 4's `TestDelivererContract` by adding an entry to its
implementations map in `pkg/diagnostic/event_test.go`; phase 4's clauses and
cases stay unchanged. Tests pass a small `size` instead of the parameter. The suite entry is built
with size 0 and a directory that cannot be written, so every
Deliver reaches the injected immediate Deliverer as phase 4's suite expects.
The owner supplies the immediate Deliverer and the Spill-file directory
(README Parameters); the deliverer reads no environment. Files use
restrictive permissions. File names do not collide across concurrent runs. A
run retains its spill files after completion.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-10: embeds `Deliverer`; the immediate Deliverer is the fallback and the Finish destination. V1-11: `BufferedSink` renamed `BufferedDeliverer`. V1-08: clauses cite the README parameters. |
| replan    | 2c9c4ff+wip | —       | dry run: Deliver.3 and its Error row cite F6's fallback directory instead of restating it. |
| replan    | 2c9c4ff+wip | —       | F10: moved to `internal/diag/buffer.go`; embeds `diagnostic.Deliverer`. |
| replan    | 2c9c4ff+wip | —       | maintainer decision: no cache fallback after a failed immediate delivery; Deliver.3 returns both failures and the owner decides (F6). |
| replan    | 2c9c4ff+wip | —       | round 4 notes: `ErrUndeliverable` declared for the double failure; the directory is supplied by the owner, `GetClaiCacheDir` moves to phase 9. |
| start     | 0ea7c95+wip | gofumpt, staticcheck, vet, `go test ./... -race -cover -count=3 -timeout=30s`, go fix, dupl | all rc 0; tests: 90 packages ok, 0 FAIL; dupl: 41 clone groups (signal) |
| replan    | 0ea7c95+wip | —       | A03: `NewBufferedDeliverer(dir, size, immediate)` and `ByteSize` declared; criterion 6; joins phase 4's suite through its implementations map; set Not Started. |
| start     | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 91 packages ok, 0 FAIL; dupl 41 clone groups |
| end       | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s -shuffle=on && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 91 packages ok, 0 FAIL; `internal/diag` 86.2%, `pkg/diagnostic` 93.3% coverage; dupl 41 clone groups |
| end       | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s -shuffle=on && go fix ./... && dupl -t 80 . (shuffled order) | all rc 0; 91 packages ok, 0 FAIL; `internal/diag` 86.2%, `pkg/diagnostic` 93.3% coverage; dupl 41 clone groups |

Andon: A03.
Surprises:
- `buffer.go` holds the interface verbatim; the implementation is in `buffered.go`.
- Finish delivers no path-list event when no spill file was written; spill lines are a `key="value"` text form of the event.
- An oversized event drops its facts and keeps the longest message prefix, at a rune boundary, that fits with ` [truncated]`.

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- The buffer never exceeds its size (an overflow flush runs before the append).
- An oversize event is truncated at a rune boundary and marked.
- Each flush makes one 0600 file in a 0700 directory, recorded in flush order.
- The spill fallback delivers and resets, and the double failure retains both errors.
- Finish lists the paths, then the remainder, and never file contents.

Note: during a flush, `buffered.go:105-107` returns `ErrUndeliverable` when any immediate delivery fails, even if others succeeded, while `Finish` returns it only when nothing was delivered. Not reachable today: no production fact fails to encode.
