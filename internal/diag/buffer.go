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
func NewBufferedDeliverer(dir string, size ByteSize, immediate diagnostic.Deliverer) BufferedDeliverer {
	return &bufferedDeliverer{dir: dir, size: size, immediate: immediate}
}
