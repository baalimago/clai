package runenv

import (
	"context"
	"errors"
	"sync"

	"github.com/baalimago/clai/internal/diag"
)

// ErrChildrenStillWriting is Wait's error when a held relay is still open
// as its bound ends.
var ErrChildrenStillWriting = errors.New("runenv: a child relay is still writing")

// Children counts the run's relays of child output that may still write to
// the run's streams after its command returns, so the composition can wait
// for them before it closes the streams.
type Children struct {
	mu   sync.Mutex
	open int
	idle chan struct{}
}

// NewChildren returns a Children with no open relay.
func NewChildren() *Children {
	return &Children{}
}

func (c *Children) hold() (release func()) {
	c.mu.Lock()
	c.open++
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.open--
			if c.open == 0 && c.idle != nil {
				close(c.idle)
				c.idle = nil
			}
		})
	}
}

// Wait returns once every hold is released, or an error matching
// ErrChildrenStillWriting and the cause of ctx when ctx ends first.
func (c *Children) Wait(ctx context.Context) error {
	c.mu.Lock()
	if c.open == 0 {
		c.mu.Unlock()
		return nil
	}
	if c.idle == nil {
		c.idle = make(chan struct{})
	}
	idle := c.idle
	c.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return diag.Errorf("%w: %w", ErrChildrenStillWriting, context.Cause(ctx))
	}
}

// Hold registers a relay of child output that writes to the run's streams
// until the returned function is called. Without Children it does nothing.
func (e Env) Hold() (release func()) {
	if e.cfg.Children == nil {
		return func() {}
	}
	return e.cfg.Children.hold()
}
