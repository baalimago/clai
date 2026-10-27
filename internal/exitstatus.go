package internal

import (
	"context"
	"errors"
)

// ExitCoder is an error that asks for an exit status of its own. Upstream
// cmd.Run collapses every failure to 1, so Command records the code in the
// ExitStatus the composition root put on the context.
type ExitCoder interface {
	error
	ExitCode() int
}

// ExitStatus holds the exit code an ExitCoder asked for during one run.
type ExitStatus struct {
	code      int
	onSuccess int
}

type exitStatusKey struct{}

// WithExitStatus returns a context that records ExitCoder failures.
func WithExitStatus(ctx context.Context) (context.Context, *ExitStatus) {
	s := &ExitStatus{}
	return context.WithValue(ctx, exitStatusKey{}, s), s
}

// Code is the recorded exit code, or 0 when no ExitCoder failed.
func (s *ExitStatus) Code() int { return s.code }

// Set records code for a run that succeeded but must still exit with a
// status of its own, such as a sync that needs a decision.
func (s *ExitStatus) Set(code int) { s.onSuccess = code }

// Resolve returns the status a run that ended with status should exit
// with: the recorded code replaces the generic failure 1, or success when
// Set asked for one.
func (s *ExitStatus) Resolve(status int) int {
	if status == 1 && s.code != 0 {
		return s.code
	}
	if status == 0 && s.onSuccess != 0 {
		return s.onSuccess
	}
	return status
}

func recordExit(ctx context.Context, err error) error {
	var coder ExitCoder
	if err == nil || !errors.As(err, &coder) {
		return err
	}
	if s, ok := ctx.Value(exitStatusKey{}).(*ExitStatus); ok {
		s.code = coder.ExitCode()
	}
	return err
}
