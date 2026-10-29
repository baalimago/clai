// Package modelfallback composes model fallback: it resolves a configured model
// name to an ordered model list (internal/modelalias), walks that list with the
// retry mechanism (internal/retry), and answers with the run that worked.
//
// A mode wires fallback with one call, handing New a builder that composes its
// own run for one model. Every attempt is built fresh, so a fallback attempt is
// decorated and configured exactly like the first
// (architecture/model-aliases.md).
package modelfallback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/baalimago/clai/internal/look"
	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/retry"
	"github.com/baalimago/clai/pkg/claierr"
)

// Builder composes the run for one model. An error means that model cannot be
// run at all: a missing key, an unknown vendor, an option the vendor refuses.
type Builder func(context.Context, string) (models.Querier, error)

// New resolves model through table and returns the querier to run.
//
// One candidate is built immediately and returned unwrapped, so a run whose
// model is not an alias keeps the querier type and the behaviour it had before
// fallback existed. Several candidates return a chain that builds each model
// only when it is its turn.
func New(ctx context.Context, table modelalias.Table, model string, build Builder, notice retry.Notice) (models.Querier, error) {
	candidates, err := table.Resolve(model)
	if err != nil {
		return nil, fmt.Errorf("resolve model %q: %w", model, err)
	}
	if len(candidates) == 1 {
		return build(ctx, candidates[0])
	}
	chain := &chain{notice: notice}
	chain.attempts = make([]retry.Attempt[models.Querier], 0, len(candidates))
	for _, candidate := range candidates {
		chain.attempts = append(chain.attempts, attempt(candidate, build))
	}
	return chain, nil
}

// NoticeTo writes a fallback notice to w in clai's look: which model did not
// answer, why, and which model is tried next. A failed write is dropped: a
// notice is best effort and must never change the run's outcome.
func NoticeTo(w io.Writer) retry.Notice {
	style := look.For(w)
	return func(failed, next string, cause error) {
		_, _ = fmt.Fprintln(w, style.Head("model "+failed+" unavailable", reason(cause), "trying "+next))
	}
}

func reason(cause error) string {
	if meaning := claierr.UnavailableMeaning(cause); meaning != nil {
		return meaning.Error()
	}
	return oneLine(rootCause(cause).Error())
}

func rootCause(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

type chain struct {
	attempts []retry.Attempt[models.Querier]
	notice   retry.Notice
	answered models.Querier
}

func (c *chain) Query(ctx context.Context) error {
	answered, err := retry.Do(ctx, c.attempts, unavailable, c.notice)
	if err != nil {
		return err
	}
	c.answered = answered
	return nil
}

// Answer returns the run that produced the output, or nil before a successful
// Query. The command adapter reads it to apply that run's own output policy.
func (c *chain) Answer() models.Querier { return c.answered }

func attempt(model string, build Builder) retry.Attempt[models.Querier] {
	return retry.Attempt[models.Querier]{
		Name: model,
		Do: func(ctx context.Context) (models.Querier, error) {
			attemptCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			querier, err := build(attemptCtx, model)
			if err != nil {
				if errors.Is(err, retry.ErrFinal) {
					return nil, err
				}
				return nil, &buildFailure{err: err}
			}
			if err := querier.Query(attemptCtx); err != nil {
				if produced(querier) {
					return nil, retry.Final(err)
				}
				return nil, err
			}
			return querier, nil
		},
	}
}

func unavailable(err error) bool {
	var build *buildFailure
	return errors.As(err, &build) || claierr.ModelUnavailable(err)
}

type buildFailure struct{ err error }

func (e *buildFailure) Error() string { return e.err.Error() }

func (e *buildFailure) Unwrap() error { return e.err }

func produced(querier models.Querier) bool {
	reporter, ok := querier.(interface{ Produced() bool })
	if !ok {
		return true
	}
	return reporter.Produced()
}
