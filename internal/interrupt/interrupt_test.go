package interrupt

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type unnumberedSignal struct{}

func (unnumberedSignal) String() string { return "custom" }
func (unnumberedSignal) Signal()        {}

func TestSignalError(t *testing.T) {
	for _, tc := range []struct {
		signal os.Signal
		status ExitStatus
		text   string
	}{
		{os.Interrupt, 130, "interrupted by signal: interrupt"},
		{syscall.SIGTERM, 143, "interrupted by signal: terminated"},
		{unnumberedSignal{}, 1, "interrupted by signal: custom"},
		{nil, 1, "interrupted"},
	} {
		err := &SignalError{Signal: tc.signal}
		if err.ExitStatus() != tc.status || err.Error() != tc.text {
			t.Fatalf("signal %v: status %d text %q, want %d %q", tc.signal, err.ExitStatus(), err.Error(), tc.status, tc.text)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("signal %v does not match context.Canceled", tc.signal)
		}
	}
}

func TestStopped(t *testing.T) {
	if err := Stopped(t.Context()); err != nil {
		t.Fatalf("a running context is stopped: %v", err)
	}
	t.Run("plain cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := Stopped(ctx)
		if !errors.Is(err, context.Canceled) || err.Error() != "stopped: context canceled" {
			t.Fatalf("got %v, want a stop matching context.Canceled", err)
		}
		if _, ok := Signal(err); ok {
			t.Fatal("a plain cancel names a signal")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()
		if err := Stopped(ctx); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want a stop matching only the deadline", err)
		}
	})
	t.Run("signal cause", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(&SignalError{Signal: syscall.SIGTERM})
		err := Stopped(ctx)
		signal, ok := Signal(err)
		if !ok || signal.ExitStatus() != 143 || !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want the SIGTERM cause matching context.Canceled", err)
		}
		if err.Error() != "stopped: interrupted by signal: terminated" {
			t.Fatalf("text %q", err.Error())
		}
	})
}
