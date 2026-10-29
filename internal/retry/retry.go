// Package retry walks an ordered list of attempts and stops at the first that
// succeeds. It is the mechanism behind model fallback: a mode resolves a
// configured model alias to an ordered model list (internal/modelalias) and
// hands one Attempt per model to Do (architecture/model-aliases.md).
//
// The package holds no clai vocabulary and prints nothing. Which failures are
// worth another attempt is injected as a Policy (a mode passes
// claierr.ModelUnavailable), and every advance is reported through a Notice
// hook, so the whole mechanism is tested with plain functions.
package retry

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Attempt is one link of a chain: the name to report and the work to try.
//
// Do must be atomic. It must not print, write a file or call a tool before it
// returns an error, because a failure that did so is never retried. Work that
// can emit output before failing wraps its error in Final.
type Attempt[T any] struct {
	Name string
	Do   func(context.Context) (T, error)
}

// Policy reports whether a failure means another attempt may succeed.
type Policy func(error) bool

// Notice reports that the attempt named failed did not answer, with its cause,
// and that next is about to be tried.
type Notice func(failed, next string, cause error)

// ErrNoAttempts reports a chain with nothing to try, which is a configuration
// error rather than a successful run.
var ErrNoAttempts = errors.New("no attempts to run")

// ErrFinal marks a failure that ends the chain whatever the Policy says.
var ErrFinal = errors.New("failure is final")

// Final marks err as the last failure of its chain: the attempt already
// emitted output or caused a side effect, so another model must not replay it.
// The returned error unwraps to both err and ErrFinal.
func Final(err error) error { return finalError{err: err} }

type finalError struct{ err error }

func (e finalError) Error() string { return e.err.Error() }

func (e finalError) Unwrap() []error { return []error{ErrFinal, e.err} }

// Failure is one attempt that did not answer: its name and its error.
type Failure struct {
	Name string
	Err  error
}

// ChainError reports the failures of a chain that did not answer, in the order
// the attempts ran. Unwrap returns every cause, so errors.Is and errors.As see
// through it.
type ChainError struct {
	Failures []Failure
}

func (e *ChainError) Error() string {
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, fmt.Sprintf("%v: %v", failure.Name, failure.Err))
	}
	return strings.Join(parts, "; ")
}

func (e *ChainError) Unwrap() []error {
	causes := make([]error, 0, len(e.Failures))
	for _, failure := range e.Failures {
		causes = append(causes, failure.Err)
	}
	return causes
}

// Do runs the attempts in order and returns the first answer.
//
// A failure the Policy accepts advances to the next attempt, and notice is
// called first. A failure the Policy rejects, a failure wrapped in Final, an
// exhausted list and a cancelled context all end the walk.
//
// A chain of one attempt returns that attempt's value and error unchanged, so
// a run whose model is not an alias behaves exactly as it did before the
// mechanism existed.
func Do[T any](ctx context.Context, attempts []Attempt[T], retryOn Policy, notice Notice) (T, error) {
	if len(attempts) == 0 {
		var zero T
		return zero, ErrNoAttempts
	}
	if len(attempts) == 1 {
		return attempts[0].Do(ctx)
	}
	var zero T
	failures := make([]Failure, 0, len(attempts))
	for i, attempt := range attempts {
		if err := ctx.Err(); err != nil {
			return zero, errors.Join(err, chainError(failures))
		}
		value, err := attempt.Do(ctx)
		if err == nil {
			return value, nil
		}
		if errors.Is(err, ErrFinal) {
			return zero, err
		}
		failures = append(failures, Failure{Name: attempt.Name, Err: err})
		if !retryOn(err) {
			return zero, chainError(failures)
		}
		if i == len(attempts)-1 {
			return zero, chainError(failures)
		}
		if notice != nil {
			notice(attempt.Name, attempts[i+1].Name, err)
		}
	}
	return zero, chainError(failures)
}

func chainError(failures []Failure) error {
	if len(failures) == 0 {
		return nil
	}
	return &ChainError{Failures: failures}
}
