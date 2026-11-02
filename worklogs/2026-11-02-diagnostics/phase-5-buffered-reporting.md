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
```
Consumes: `Deliverer` and `Event` (phase 4).

## Acceptance criteria

| #   | Criterion                                                                       | Proves            | Proof                             |
| --- | ------------------------------------------------------------------------------- | ----------------- | --------------------------------- |
| 1   | A run has one bounded buffer shared by its buffered types.                      | Deliver.2, F4, F5 | `TestBufferedDelivererContract`   |
| 2   | Every overflow creates a retained timestamped file in the selected directory.   | Deliver.2, F5     | `TestBufferedDelivererOverflow`   |
| 3   | A spill failure falls back to the immediate Deliverer; a double failure returns both errors without growing the buffer. | Deliver.3, F6 | `TestBufferedDelivererFallback` |
| 4   | At completion, output lists paths and remaining events without replaying files. | Finish.1, F5      | `TestBufferedDelivererFinish`     |
| 5   | The buffer is a conforming Deliverer.                                           | Deliver.1         | `TestDelivererContract`           |

## Test cases

| Test                            | Level          | Proves        | Given                                                           | Then                                                                                       | Must not                                      |
| ------------------------------- | -------------- | ------------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------ | --------------------------------------------- |
| `TestBufferedDelivererContract` | contract suite | Deliver.2     | Two types and an oversized event                                | One buffer preserves order and marks truncation                                            | Grow memory with the event count              |
| `TestBufferedDelivererOverflow` | contract suite | Deliver.2, F5 | Repeated overflows into the directory the owner supplied        | Each flush creates a distinct retained timestamped file                                    | Replace an earlier spill file                 |
| `TestBufferedDelivererFallback` | contract suite | Deliver.3, F6 | A failed spill write, alone and with a failing immediate Deliverer | The immediate Deliverer receives the buffer, or the error matches `ErrUndeliverable` and retains both failures | Panic, cancel the operation, or retry without a bound |
| `TestBufferedDelivererFinish`   | contract suite | Finish.1      | Several spill files and a partial buffer                        | Paths appear in flush order followed by the remainder on the immediate Deliverer           | Deliver file contents or delete the files     |
| `TestDelivererContract`         | contract suite | Deliver.1     | The phase 4 suite run against this implementation               | Delivery or its failure is returned without races                                          | Apply a level policy                          |

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

## Review findings

None.
