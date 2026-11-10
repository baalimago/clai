package cli

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestSignalSource proves the composition's SignalSource (phase 7
// Subscribe.1, F9): a signal sent to the process arrives on its channel, and
// after stop no more arrive.
func TestSignalSource(t *testing.T) {
	signals, stop, err := osSignals{}.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// A guard keeps the process alive once the source has stopped.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case sig := <-signals:
		if sig != syscall.SIGTERM {
			t.Fatalf("signal = %v, want SIGTERM", sig)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the signal did not arrive")
	}
	<-guard

	stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	<-guard
	select {
	case sig := <-signals:
		t.Fatalf("signal %v arrived after stop", sig)
	case <-time.After(50 * time.Millisecond):
	}
}
