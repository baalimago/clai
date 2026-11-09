// Package interrupt names why a run stopped before it finished: a process
// signal, the caller's cancel, or a deadline.
package interrupt

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// signalExitBase is the shell convention for a process a signal ended: its
// exit status is this plus the signal number.
const signalExitBase = 128

// ExitStatus is the process exit status of a run a signal interrupted.
type ExitStatus int

// SignalError is the cancel cause of a run context that a process signal
// cancelled. It matches context.Canceled.
type SignalError struct {
	Signal os.Signal
}

func (e *SignalError) Error() string {
	if e.Signal == nil {
		return "interrupted"
	}
	return "interrupted by signal: " + e.Signal.String()
}

func (e *SignalError) Is(target error) bool {
	return target == context.Canceled
}

// ExitStatus is 128 plus the signal number, so 130 for SIGINT and 143 for
// SIGTERM; a signal without a number yields 1.
func (e *SignalError) ExitStatus() ExitStatus {
	if number, ok := e.Signal.(syscall.Signal); ok {
		return ExitStatus(signalExitBase + int(number))
	}
	return 1
}

// StoppedError reports that a run stopped because its context ended. It
// matches the context's error and its cancel cause.
type StoppedError struct {
	// Err is the context's error: context.Canceled or
	// context.DeadlineExceeded.
	Err error
	// Cause is the context's cancel cause, Err itself when none was given.
	Cause error
}

func (e *StoppedError) Error() string {
	return "stopped: " + e.Cause.Error()
}

func (e *StoppedError) Unwrap() []error {
	if e.Cause == e.Err {
		return []error{e.Err}
	}
	return []error{e.Err, e.Cause}
}

// Stopped returns a *StoppedError when ctx has ended and nil while it runs.
func Stopped(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil {
		cause = err
	}
	return &StoppedError{Err: err, Cause: cause}
}

// Signal returns the signal that stopped the run err reports.
func Signal(err error) (*SignalError, bool) {
	if signal, ok := errors.AsType[*SignalError](err); ok {
		return signal, true
	}
	return nil, false
}
