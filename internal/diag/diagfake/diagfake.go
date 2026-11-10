// Package diagfake holds the recording diagnostic.Deliverer tests use in
// place of a real destination. It runs phase 4's Deliverer contract suite
// (TestDelivererContract in pkg/diagnostic), so a test that fakes a
// Deliverer fakes one the suite proves.
package diagfake

import (
	"context"
	"sync"

	"github.com/baalimago/clai/pkg/diagnostic"
)

// Deliverer records each event it accepts. A call fails with its error once
// more calls than its allowance were made; without an error every call
// succeeds.
type Deliverer struct {
	mu        sync.Mutex
	err       error
	allowance int
	calls     int
	events    []diagnostic.Event
	attempts  []diagnostic.Event
	seen      chan struct{}
}

// New returns a Deliverer that accepts every event.
func New() *Deliverer {
	return &Deliverer{seen: make(chan struct{}, seenBacklog)}
}

// seenBacklog is how many calls Seen holds before it drops a signal.
const seenBacklog = 64

// Failing returns a Deliverer whose every call fails with err.
func Failing(err error) *Deliverer {
	return &Deliverer{err: err, seen: make(chan struct{}, seenBacklog)}
}

// FailingAfter returns a Deliverer that accepts the first n events and fails
// every later call with err.
func FailingAfter(n int, err error) *Deliverer {
	return &Deliverer{err: err, allowance: n, seen: make(chan struct{}, seenBacklog)}
}

// Deliver records event, or returns the Deliverer's error once its
// allowance is spent.
func (d *Deliverer) Deliver(_ context.Context, event diagnostic.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.signal()
	d.calls++
	d.attempts = append(d.attempts, event)
	if d.err != nil && d.calls > d.allowance {
		return d.err
	}
	d.events = append(d.events, event)
	return nil
}

func (d *Deliverer) signal() {
	select {
	case d.seen <- struct{}{}:
	default:
	}
}

// Seen receives once per Deliver call, accepted or failed, after the call
// is recorded; it holds up to 64 signals nobody received.
func (d *Deliverer) Seen() <-chan struct{} {
	return d.seen
}

// Fail makes every later call fail with err; a nil err makes them succeed.
func (d *Deliverer) Fail(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
	d.allowance = d.calls
}

// Events returns the accepted events in delivery order.
func (d *Deliverer) Events() []diagnostic.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]diagnostic.Event(nil), d.events...)
}

// Attempts returns every event passed to Deliver, accepted or failed, in
// call order.
func (d *Deliverer) Attempts() []diagnostic.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]diagnostic.Event(nil), d.attempts...)
}

// Calls returns the number of Deliver calls, accepted or failed.
func (d *Deliverer) Calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}
