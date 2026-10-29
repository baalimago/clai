package retry

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var (
	errRateLimited = errors.New("rate limited")
	errNotFound    = errors.New("model not found")
	errFiltered    = errors.New("content filtered")
)

// onlyUnavailable is a Policy standing in for claierr.ModelUnavailable.
func onlyUnavailable(err error) bool {
	return errors.Is(err, errRateLimited) || errors.Is(err, errNotFound)
}

func attempt(name string, calls *[]string, err error, value string) Attempt[string] {
	return Attempt[string]{
		Name: name,
		Do: func(context.Context) (string, error) {
			*calls = append(*calls, name)
			return value, err
		},
	}
}

// TestDo_FirstAttemptSucceeds pins that a healthy chain runs one attempt and
// returns its value.
func TestDo_FirstAttemptSucceeds(t *testing.T) {
	var calls, notices []string
	got, err := Do(context.Background(), []Attempt[string]{
		attempt("a", &calls, nil, "answer"),
		attempt("b", &calls, nil, "other"),
	}, onlyUnavailable, func(failed, next string, cause error) {
		notices = append(notices, failed+"->"+next)
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got != "answer" {
		t.Fatalf("Do = %q, want %q", got, "answer")
	}
	if !reflect.DeepEqual(calls, []string{"a"}) {
		t.Fatalf("attempts run = %v, want only [a]", calls)
	}
	if len(notices) != 0 {
		t.Fatalf("notices = %v, want none", notices)
	}
}

// TestDo_RetryableFailureAdvances pins the mechanism: an accepted failure
// advances to the next attempt and reports the failure through the notice
// hook before it does.
func TestDo_RetryableFailureAdvances(t *testing.T) {
	var calls, notices []string
	got, err := Do(context.Background(), []Attempt[string]{
		attempt("a", &calls, errRateLimited, ""),
		attempt("b", &calls, errNotFound, ""),
		attempt("c", &calls, nil, "answer"),
	}, onlyUnavailable, func(failed, next string, cause error) {
		notices = append(notices, fmt.Sprintf("%s->%s(%v)", failed, next, cause))
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got != "answer" {
		t.Fatalf("Do = %q, want %q", got, "answer")
	}
	if !reflect.DeepEqual(calls, []string{"a", "b", "c"}) {
		t.Fatalf("attempts run = %v, want [a b c]", calls)
	}
	want := []string{"a->b(rate limited)", "b->c(model not found)"}
	if !reflect.DeepEqual(notices, want) {
		t.Fatalf("notices = %v, want %v", notices, want)
	}
}

// TestDo_SingleAttemptReturnsItsErrorUnchanged pins backwards compatibility:
// a chain of one, which is any run whose model is not an alias, reports
// exactly what it would have reported without the mechanism.
func TestDo_SingleAttemptReturnsItsErrorUnchanged(t *testing.T) {
	var calls, notices []string
	_, err := Do(context.Background(), []Attempt[string]{
		attempt("a", &calls, errRateLimited, ""),
	}, onlyUnavailable, func(failed, next string, cause error) {
		notices = append(notices, failed+"->"+next)
	})
	if !errors.Is(err, errRateLimited) {
		t.Fatalf("Do error = %v, want the attempt's own error", err)
	}
	var chain *ChainError
	if errors.As(err, &chain) {
		t.Fatalf("Do error = %v, want no *ChainError for a single attempt", err)
	}
	if len(notices) != 0 {
		t.Fatalf("notices = %v, want none", notices)
	}
}

// TestDo_ExhaustionReturnsEveryFailure pins that a chain which runs out of
// attempts reports each model and its cause, and keeps the causes findable.
func TestDo_ExhaustionReturnsEveryFailure(t *testing.T) {
	var calls []string
	_, err := Do(context.Background(), []Attempt[string]{
		attempt("a", &calls, errRateLimited, ""),
		attempt("b", &calls, errNotFound, ""),
	}, onlyUnavailable, nil)
	if err == nil {
		t.Fatal("Do = nil error, want a chain failure")
	}
	var chain *ChainError
	if !errors.As(err, &chain) {
		t.Fatalf("Do error = %v, want *ChainError", err)
	}
	if len(chain.Failures) != 2 {
		t.Fatalf("Failures = %v, want two", chain.Failures)
	}
	if chain.Failures[0].Name != "a" || chain.Failures[1].Name != "b" {
		t.Fatalf("Failures = %v, want a then b", chain.Failures)
	}
	if !errors.Is(err, errRateLimited) || !errors.Is(err, errNotFound) {
		t.Fatalf("error %v does not keep every cause findable", err)
	}
	for _, name := range []string{"a", "b"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q does not name attempt %q", err, name)
		}
	}
	if !reflect.DeepEqual(calls, []string{"a", "b"}) {
		t.Fatalf("attempts run = %v, want [a b]", calls)
	}
}

// TestDo_RejectedFailureStopsTheChain pins that a failure the policy refuses
// ends the walk: a content refusal is about the request, so the next model
// must not be asked.
func TestDo_RejectedFailureStopsTheChain(t *testing.T) {
	var calls []string
	_, err := Do(context.Background(), []Attempt[string]{
		attempt("a", &calls, errFiltered, ""),
		attempt("b", &calls, nil, "answer"),
	}, onlyUnavailable, nil)
	if !errors.Is(err, errFiltered) {
		t.Fatalf("Do error = %v, want the content-filter cause", err)
	}
	if !reflect.DeepEqual(calls, []string{"a"}) {
		t.Fatalf("attempts run = %v, want only [a]", calls)
	}
	var chain *ChainError
	if !errors.As(err, &chain) {
		t.Fatalf("Do error = %v, want *ChainError", err)
	}
	if len(chain.Failures) != 1 {
		t.Fatalf("Failures = %v, want the one rejected failure", chain.Failures)
	}
}

// TestDo_FinalFailureIsReturnedAsIs pins the produced-output guard: a run
// that already emitted output ends the chain, and the caller sees that run's
// error rather than a summary of models it never tried.
func TestDo_FinalFailureIsReturnedAsIs(t *testing.T) {
	var calls []string
	_, err := Do(context.Background(), []Attempt[string]{
		attempt("a", &calls, Final(errRateLimited), ""),
		attempt("b", &calls, nil, "answer"),
	}, onlyUnavailable, nil)
	if !errors.Is(err, errRateLimited) {
		t.Fatalf("Do error = %v, want the final cause", err)
	}
	if !errors.Is(err, ErrFinal) {
		t.Fatalf("Do error = %v, want it to read as final", err)
	}
	if err.Error() != errRateLimited.Error() {
		t.Fatalf("Do error text = %q, want the cause's own text", err)
	}
	if !reflect.DeepEqual(calls, []string{"a"}) {
		t.Fatalf("attempts run = %v, want only [a]", calls)
	}
}

// TestDo_NoAttemptsIsAnError pins that an empty chain is refused instead of
// reported as success.
func TestDo_NoAttemptsIsAnError(t *testing.T) {
	_, err := Do[string](context.Background(), nil, onlyUnavailable, nil)
	if !errors.Is(err, ErrNoAttempts) {
		t.Fatalf("Do error = %v, want ErrNoAttempts", err)
	}
}

// TestDo_CancelledContextStopsTheWalk pins that a cancelled run does not start
// another attempt, and that the cancellation stays findable.
func TestDo_CancelledContextStopsTheWalk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls []string
	_, err := Do(ctx, []Attempt[string]{
		attempt("a", &calls, errRateLimited, ""),
		attempt("b", &calls, nil, "answer"),
	}, onlyUnavailable, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}
	if len(calls) != 0 {
		t.Fatalf("attempts run = %v, want none after cancellation", calls)
	}
}

// TestDo_CancelledAfterOneFailureKeepsBothCauses pins that a cancellation
// between attempts reports why the walk started and why it stopped.
func TestDo_CancelledAfterOneFailureKeepsBothCauses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls []string
	first := attempt("a", &calls, errRateLimited, "")
	first.Do = func(context.Context) (string, error) {
		calls = append(calls, "a")
		cancel()
		return "", errRateLimited
	}
	_, err := Do(ctx, []Attempt[string]{
		first,
		attempt("b", &calls, nil, "answer"),
	}, onlyUnavailable, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do error = %v, want context.Canceled", err)
	}
	if !errors.Is(err, errRateLimited) {
		t.Fatalf("Do error = %v, want the first failure too", err)
	}
	if !reflect.DeepEqual(calls, []string{"a"}) {
		t.Fatalf("attempts run = %v, want only [a]", calls)
	}
}
