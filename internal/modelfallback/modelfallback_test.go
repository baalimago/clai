package modelfallback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/retry"
	"github.com/baalimago/clai/pkg/claierr"
)

// fakeQuerier is one model's run: it records that it ran and fails with err.
type fakeQuerier struct {
	model    string
	err      error
	produced bool
	ran      bool
}

func (f *fakeQuerier) Query(context.Context) error {
	f.ran = true
	return f.err
}

func (f *fakeQuerier) Produced() bool { return f.produced }

// silentQuerier has no Produced method: the mechanism must treat it as having
// produced output, so it is never replayed.
type silentQuerier struct{ model string }

func (s *silentQuerier) Query(context.Context) error {
	return claierr.NewRateLimited(nil, timeZero, 0, 0)
}

var timeZero = time.Time{}

var (
	errMissingKey = errors.New("environment variable 'ANTHROPIC_API_KEY' not set")
	errFiltered   = errors.New("content filtered")
)

func rateLimited() error { return claierr.NewRateLimited(nil, timeZero, 0, 0) }

// wordyError carries guidance a notice cannot hold and unwraps to the short
// claierr sentinel.
type wordyError struct{ guidance string }

func (e *wordyError) Error() string { return e.guidance }
func (e *wordyError) Unwrap() error { return claierr.ErrModelNotFound }

// answerOf returns the run a fallback chain reports as having answered.
func answerOf(t *testing.T, q models.Querier) models.Querier {
	t.Helper()
	answerer, ok := q.(interface{ Answer() models.Querier })
	if !ok {
		t.Fatalf("querier %T does not report the run that answered", q)
	}
	return answerer.Answer()
}

// builder returns a Builder that records every model it is asked for and hands
// out one fakeQuerier per model.
func builder(buildErr map[string]error, runErr map[string]error, produced map[string]bool) (Builder, *[]string, map[string]*fakeQuerier) {
	var built []string
	queriers := map[string]*fakeQuerier{}
	build := func(_ context.Context, model string) (models.Querier, error) {
		built = append(built, model)
		if err := buildErr[model]; err != nil {
			return nil, err
		}
		q := &fakeQuerier{model: model, err: runErr[model], produced: produced[model]}
		queriers[model] = q
		return q, nil
	}
	return build, &built, queriers
}

// TestNew_PlainModelIsBuiltUnwrapped pins backwards compatibility: a model the
// table does not name is composed once, and the caller receives that querier
// itself, not a chain around it.
func TestNew_PlainModelIsBuiltUnwrapped(t *testing.T) {
	build, built, queriers := builder(nil, nil, nil)
	got, err := New(context.Background(), nil, "gpt-5.2", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !reflect.DeepEqual(*built, []string{"gpt-5.2"}) {
		t.Fatalf("built = %v, want [gpt-5.2]", *built)
	}
	if got != models.Querier(queriers["gpt-5.2"]) {
		t.Fatalf("New returned %T, want the built querier itself", got)
	}
	if err := got.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
}

// TestNew_AliasStopsAtTheFirstModelThatAnswers pins the mechanism: the chain
// tries the list in order and reports the answering model.
func TestNew_AliasStopsAtTheFirstModelThatAnswers(t *testing.T) {
	table := modelalias.Table{"strong": {"claude-sonnet-5-5", "gpt-5.2", "gemini-3.6-flash"}}
	build, built, queriers := builder(nil, map[string]error{"claude-sonnet-5-5": rateLimited()}, nil)
	var notice string
	got, err := New(context.Background(), table, "strong", build, func(failed, next string, cause error) {
		notice = failed + "->" + next
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := got.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !reflect.DeepEqual(*built, []string{"claude-sonnet-5-5", "gpt-5.2"}) {
		t.Fatalf("built = %v, want the first two models only", *built)
	}
	if notice != "claude-sonnet-5-5->gpt-5.2" {
		t.Fatalf("notice = %q, want the first model's failure", notice)
	}
	if answered := answerOf(t, got); answered != models.Querier(queriers["gpt-5.2"]) {
		t.Fatalf("Answer() = %T, want the querier of the model that answered", answered)
	}
}

// TestNew_BuildFailureAdvances pins that a model the machine cannot compose, a
// missing key being the common case, moves to the next model instead of ending
// the run.
func TestNew_BuildFailureAdvances(t *testing.T) {
	table := modelalias.Table{"strong": {"claude-sonnet-5-5", "gpt-5.2"}}
	build, built, queriers := builder(map[string]error{"claude-sonnet-5-5": errMissingKey}, nil, nil)
	got, err := New(context.Background(), table, "strong", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := got.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !reflect.DeepEqual(*built, []string{"claude-sonnet-5-5", "gpt-5.2"}) {
		t.Fatalf("built = %v, want both models", *built)
	}
	if answered := answerOf(t, got); answered != models.Querier(queriers["gpt-5.2"]) {
		t.Fatalf("Answer() = %T, want the querier of the model that answered", answered)
	}
}

// TestNew_FinalBuildFailureEndsTheChain pins that a build failure wrapped in
// retry.Final (such as an explicit MCP server startup failure) stops the chain
// immediately without trying the next model or wrapping in *retry.ChainError.
func TestNew_FinalBuildFailureEndsTheChain(t *testing.T) {
	table := modelalias.Table{"strong": {"a", "b"}}
	build, built, _ := builder(map[string]error{"a": retry.Final(errFiltered)}, nil, nil)
	got, err := New(context.Background(), table, "strong", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = got.Query(context.Background())
	if !errors.Is(err, errFiltered) {
		t.Fatalf("Query error = %v, want the final cause", err)
	}
	if !reflect.DeepEqual(*built, []string{"a"}) {
		t.Fatalf("built = %v, want only the first model", *built)
	}
	var chain *retry.ChainError
	if errors.As(err, &chain) {
		t.Fatalf("Query error = %v, want the final error itself, not a chain summary", err)
	}
}

// TestNew_RejectedFailureEndsTheChain pins that a failure meaning the request
// is wrong is reported, not retried on another model.
func TestNew_RejectedFailureEndsTheChain(t *testing.T) {
	table := modelalias.Table{"strong": {"a", "b"}}
	build, built, _ := builder(nil, map[string]error{"a": errFiltered}, nil)
	got, err := New(context.Background(), table, "strong", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = got.Query(context.Background())
	if !errors.Is(err, errFiltered) {
		t.Fatalf("Query error = %v, want the content-filter cause", err)
	}
	if !reflect.DeepEqual(*built, []string{"a"}) {
		t.Fatalf("built = %v, want only the first model", *built)
	}
	if answered := answerOf(t, got); answered != nil {
		t.Fatalf("Answer() = %T, want nil before a model answers", answered)
	}
}

// TestNew_ProducedRunIsNeverReplayed pins that a run which already printed or
// acted is reported as it failed, even when its failure would otherwise be
// retryable.
func TestNew_ProducedRunIsNeverReplayed(t *testing.T) {
	table := modelalias.Table{"strong": {"a", "b"}}
	build, built, _ := builder(nil, map[string]error{"a": rateLimited()}, map[string]bool{"a": true})
	got, err := New(context.Background(), table, "strong", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = got.Query(context.Background())
	if !errors.Is(err, claierr.ErrRateLimited) {
		t.Fatalf("Query error = %v, want the rate-limit cause", err)
	}
	if !reflect.DeepEqual(*built, []string{"a"}) {
		t.Fatalf("built = %v, want only the first model", *built)
	}
	var chain *retry.ChainError
	if errors.As(err, &chain) {
		t.Fatalf("Query error = %v, want the run's own error, not a chain summary", err)
	}
}

// TestNew_QuerierWithoutProducedIsNeverReplayed pins the safe default for a
// future mode: a querier that cannot say what it emitted is treated as having
// emitted everything.
func TestNew_QuerierWithoutProducedIsNeverReplayed(t *testing.T) {
	table := modelalias.Table{"strong": {"a", "b"}}
	var built []string
	build := func(_ context.Context, model string) (models.Querier, error) {
		built = append(built, model)
		return &silentQuerier{model: model}, nil
	}
	got, err := New(context.Background(), table, "strong", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := got.Query(context.Background()); !errors.Is(err, claierr.ErrRateLimited) {
		t.Fatalf("Query error = %v, want the rate-limit cause", err)
	}
	if !reflect.DeepEqual(built, []string{"a"}) {
		t.Fatalf("built = %v, want only the first model", built)
	}
}

// TestNew_EveryModelFailingReportsEachOne pins the exhausted chain: every model
// is named with its cause, and the causes stay findable.
func TestNew_EveryModelFailingReportsEachOne(t *testing.T) {
	table := modelalias.Table{"strong": {"a", "b"}}
	build, built, _ := builder(nil, map[string]error{"a": rateLimited(), "b": claierr.NewModelNotFound(nil)}, nil)
	got, err := New(context.Background(), table, "strong", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = got.Query(context.Background())
	if !errors.Is(err, claierr.ErrRateLimited) || !errors.Is(err, claierr.ErrModelNotFound) {
		t.Fatalf("Query error = %v, want both causes", err)
	}
	var chain *retry.ChainError
	if !errors.As(err, &chain) {
		t.Fatalf("Query error = %v, want *retry.ChainError", err)
	}
	for _, model := range []string{"a", "b"} {
		if !strings.Contains(err.Error(), model) {
			t.Fatalf("error %q does not name model %q", err, model)
		}
	}
	if !reflect.DeepEqual(*built, []string{"a", "b"}) {
		t.Fatalf("built = %v, want every model", *built)
	}
}

// TestNew_MalformedAliasIsAnError pins that a broken table entry fails before
// any model is built.
func TestNew_MalformedAliasIsAnError(t *testing.T) {
	table := modelalias.Table{"strong": nil}
	build, built, _ := builder(nil, nil, nil)
	_, err := New(context.Background(), table, "strong", build, nil)
	if !errors.Is(err, modelalias.ErrInvalidAlias) {
		t.Fatalf("New error = %v, want modelalias.ErrInvalidAlias", err)
	}
	if len(*built) != 0 {
		t.Fatalf("built = %v, want no build attempt", *built)
	}
}

// TestNew_AttemptContextCancelledBeforeNextCandidate pins that resources bound
// to a failed candidate's attempt context (such as per-attempt MCP subprocesses
// or costManager goroutines) are released before the next candidate builds.
func TestNew_AttemptContextCancelledBeforeNextCandidate(t *testing.T) {
	table := modelalias.Table{"strong": {"a", "b"}}
	var firstCtx context.Context
	var firstDoneBeforeSecond bool
	build := func(ctx context.Context, model string) (models.Querier, error) {
		if model == "a" {
			firstCtx = ctx
			return &fakeQuerier{model: "a", err: rateLimited()}, nil
		}
		firstDoneBeforeSecond = firstCtx != nil && firstCtx.Err() != nil
		return &fakeQuerier{model: "b"}, nil
	}
	got, err := New(context.Background(), table, "strong", build, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := got.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !firstDoneBeforeSecond {
		t.Fatal("first candidate's attempt context was not cancelled before second candidate built")
	}
}

// TestNoticeTo_WritesTheFallbackInClaisLook pins the human line a fallback
// prints: the model that did not answer, the one-phrase reason and the model
// tried next, in the look the progress board and the run summary use.
func TestNoticeTo_WritesTheFallbackInClaisLook(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		want  string
	}{
		{
			name: "a typed provider meaning prints one phrase, not the provider prose",
			cause: claierr.NewInsufficientCredits(&claierr.APIError{
				StatusCode:   400,
				ProviderCode: "invalid_request_error",
				Message:      "Your credit balance is too low to access the Anthropic API.",
			}),
			want: "▸ model claude-sonnet-5-5 unavailable · likely insufficient credits · trying gemini-3.6-flash\n",
		},
		{
			name:  "a run that could not be composed prints its own short reason",
			cause: errMissingKey,
			want:  "▸ model claude-sonnet-5-5 unavailable · environment variable 'ANTHROPIC_API_KEY' not set · trying gemini-3.6-flash\n",
		},
		{
			name:  "a wrapped meaning stays one phrase",
			cause: fmt.Errorf("run step 0: stream completions: %w", rateLimited()),
			want:  "▸ model claude-sonnet-5-5 unavailable · rate limited · trying gemini-3.6-flash\n",
		},
		{
			name:  "a setup layer's wrapping is dropped for the innermost cause",
			cause: fmt.Errorf("failed to create text querier: %w", errMissingKey),
			want:  "▸ model claude-sonnet-5-5 unavailable · environment variable 'ANTHROPIC_API_KEY' not set · trying gemini-3.6-flash\n",
		},
		{
			name:  "a wordy error prints the short form it offers",
			cause: &wordyError{guidance: "no image generator for model \"x\": expected gpt-image-*, dall-e-*"},
			want:  "▸ model claude-sonnet-5-5 unavailable · model not found · trying gemini-3.6-flash\n",
		},
		{
			name:  "a multi-line cause is folded onto one line",
			cause: errors.New("first line\nsecond line"),
			want:  "▸ model claude-sonnet-5-5 unavailable · first line second line · trying gemini-3.6-flash\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			NoticeTo(&out)("claude-sonnet-5-5", "gemini-3.6-flash", tc.cause)
			if got := out.String(); got != tc.want {
				t.Fatalf("notice = %q, want %q", got, tc.want)
			}
		})
	}
}
