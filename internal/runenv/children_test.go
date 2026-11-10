package runenv

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChildrenWaitReturnsOnceEveryHoldIsReleased(t *testing.T) {
	children := NewChildren()
	env := New(Config{Children: children})
	release := env.Hold()
	waited := make(chan error, 1)
	go func() { waited <- children.Wait(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("Wait returned %v while a hold was open", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	release()
	if err := <-waited; err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestChildrenWaitWithoutHoldsReturnsAtOnce(t *testing.T) {
	if err := NewChildren().Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestChildrenWaitBoundExpiresWithTypedError(t *testing.T) {
	children := NewChildren()
	release := New(Config{Children: children}).Hold()
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := children.Wait(ctx)
	if !errors.Is(err, ErrChildrenStillWriting) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want ErrChildrenStillWriting and the deadline", err)
	}
}

func TestChildrenHoldAfterWaitStartedIsAwaited(t *testing.T) {
	children := NewChildren()
	env := New(Config{Children: children})
	first := env.Hold()
	waited := make(chan error, 1)
	go func() { waited <- children.Wait(context.Background()) }()
	second := env.Hold()
	first()
	select {
	case err := <-waited:
		t.Fatalf("Wait returned %v while the second hold was open", err)
	case <-time.After(20 * time.Millisecond):
	}
	second()
	if err := <-waited; err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestHoldWithoutChildrenIsANoOp(t *testing.T) {
	New(Config{}).Hold()()
}
