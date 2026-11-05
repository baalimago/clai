package text

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var stepRetrySchedule = []time.Duration{time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}

func stepRetry503() error {
	return claierr.NewProviderUnavailable(&claierr.APIError{StatusCode: 503, Body: "upstream connect error"})
}

func stepRetryReply(text string) chan models.CompletionEvent {
	ch := make(chan models.CompletionEvent, 2)
	ch <- text
	ch <- models.StopEvent{}
	close(ch)
	return ch
}

func stepRetryToolCall() chan models.CompletionEvent {
	ch := make(chan models.CompletionEvent, 2)
	ch <- pub_models.Call{ID: "c1", Name: "echo"}
	ch <- models.StopEvent{}
	close(ch)
	return ch
}

func stepRetryChat() pub_models.Chat {
	return pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
}

type stepRetryTool struct{ ran *int }

func (c stepRetryTool) Call(pub_models.Input) (string, error) { *c.ran++; return "out", nil }

func (c stepRetryTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: "echo"}
}

// stepRetryFixture scripts one model response per request and records every
// wait, so a test states the whole conversation and nothing sleeps.
type stepRetryFixture struct {
	requests [][]pub_models.Message
	waits    []time.Duration
	stdout   strings.Builder
	stderr   strings.Builder
	toolRuns int
}

func (f *stepRetryFixture) querier(script func(request int) (chan models.CompletionEvent, error)) *Querier[*MockQuerier] {
	return &Querier[*MockQuerier]{
		Raw:     true,
		out:     &f.stdout,
		errOut:  &f.stderr,
		tooling: tooling{run: map[string]pub_models.LLMTool{"echo": stepRetryTool{ran: &f.toolRuns}}},
		stepRetry: StepRetry{
			Delays: stepRetrySchedule,
			Wait: func(_ context.Context, d time.Duration) error {
				f.waits = append(f.waits, d)
				return nil
			},
		},
		Model: &MockQuerier{streamFn: func(_ context.Context, c pub_models.Chat) (chan models.CompletionEvent, error) {
			f.requests = append(f.requests, slices.Clone(c.Messages))
			return script(len(f.requests))
		}},
	}
}

func TestStepRetry_ContinuesTheConversationWhereItIs(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(request int) (chan models.CompletionEvent, error) {
		switch request {
		case 1:
			return stepRetryToolCall(), nil
		case 2, 3:
			return nil, stepRetry503()
		}
		return stepRetryReply("done"), nil
	})

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	if f.toolRuns != 1 {
		t.Fatalf("tool ran %d times, want 1", f.toolRuns)
	}
	if len(f.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(f.requests))
	}
	for i := 2; i < 4; i++ {
		if len(f.requests[i]) != len(f.requests[1]) {
			t.Fatalf("request %d resent %d messages, want the same %d as the failed request", i+1, len(f.requests[i]), len(f.requests[1]))
		}
	}
	if !slices.Equal(f.waits, stepRetrySchedule[:2]) {
		t.Fatalf("waits = %v, want %v", f.waits, stepRetrySchedule[:2])
	}
}

func TestStepRetry_GivesUpAfterTheScheduleWithTheOriginalError(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(int) (chan models.CompletionEvent, error) { return nil, stepRetry503() })

	_, err := q.TextQuery(context.Background(), stepRetryChat())

	if !errors.Is(err, claierr.ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if !strings.Contains(err.Error(), "run step 0") {
		t.Fatalf("err = %q, want it to name the step", err)
	}
	if len(f.requests) != 5 {
		t.Fatalf("requests = %d, want 5", len(f.requests))
	}
	if !slices.Equal(f.waits, stepRetrySchedule) {
		t.Fatalf("waits = %v, want %v", f.waits, stepRetrySchedule)
	}
}

// A failure in one step must not spend a later step's retries.
func TestStepRetry_EachStepGetsTheWholeSchedule(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(request int) (chan models.CompletionEvent, error) {
		switch request {
		case 1, 2, 3, 4: // step 0: four failures use every delay
			return nil, stepRetry503()
		case 5:
			return stepRetryToolCall(), nil
		case 6, 7, 8, 9: // step 1 starts over
			return nil, stepRetry503()
		}
		return stepRetryReply("done"), nil
	})

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	want := slices.Concat(stepRetrySchedule, stepRetrySchedule)
	if !slices.Equal(f.waits, want) {
		t.Fatalf("waits = %v, want %v", f.waits, want)
	}
}

func TestStepRetry_RetriesAConnectionFailure(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(request int) (chan models.CompletionEvent, error) {
		if request == 1 {
			return nil, claierr.NewTransport(errors.New("connection refused"))
		}
		return stepRetryReply("done"), nil
	})

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	if len(f.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(f.requests))
	}
}

func TestStepRetry_MakesOneRequestOnAnythingElse(t *testing.T) {
	api := &claierr.APIError{StatusCode: 400}
	cases := map[string]error{
		"rate limit":      claierr.NewRateLimited(&claierr.APIError{StatusCode: 429}, time.Time{}, 0, 0),
		"auth":            claierr.NewAuthFailed(&claierr.APIError{StatusCode: 401}),
		"bad request":     claierr.NewUnexpectedProviderResponse(400, []byte("bad")),
		"model missing":   claierr.NewModelNotFound(api),
		"credits":         claierr.NewInsufficientCredits(api),
		"untyped failure": errors.New("something else"),
	}
	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			f := &stepRetryFixture{}
			q := f.querier(func(int) (chan models.CompletionEvent, error) { return nil, failure })

			_, err := q.TextQuery(context.Background(), stepRetryChat())

			if !errors.Is(err, failure) {
				t.Fatalf("err = %v, want %v", err, failure)
			}
			if len(f.requests) != 1 || len(f.waits) != 0 {
				t.Fatalf("requests = %d, waits = %v, want one request and no wait", len(f.requests), f.waits)
			}
		})
	}
}

// A failure after the stream opened may already have reached the screen.
func TestStepRetry_DoesNotRetryAFailureInsideTheStream(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(int) (chan models.CompletionEvent, error) {
		ch := make(chan models.CompletionEvent, 2)
		ch <- "partial"
		ch <- stepRetry503()
		close(ch)
		return ch, nil
	})

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); !errors.Is(err, claierr.ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
}

func TestStepRetry_CancelDuringAWaitStopsAndIsNotModelUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &stepRetryFixture{}
	q := f.querier(func(int) (chan models.CompletionEvent, error) { return nil, stepRetry503() })
	q.stepRetry.Wait = func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}

	_, err := q.TextQuery(ctx, stepRetryChat())

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if claierr.ModelUnavailable(err) {
		t.Fatalf("err = %v reads as model unavailable, so an alias would try its next model", err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
}

func TestStepRetry_ACancelledRequestIsNeverRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &stepRetryFixture{}
	q := f.querier(func(int) (chan models.CompletionEvent, error) {
		cancel()
		return nil, claierr.NewTransport(context.Canceled)
	})

	if _, err := q.TextQuery(ctx, stepRetryChat()); err == nil {
		t.Fatal("TextQuery returned no error")
	}
	if len(f.requests) != 1 || len(f.waits) != 0 {
		t.Fatalf("requests = %d, waits = %v, want one request and no wait", len(f.requests), f.waits)
	}
}

func TestStepRetry_NoticeGoesToStderrAndNeverTheReply(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(request int) (chan models.CompletionEvent, error) {
		if request == 1 {
			return nil, stepRetry503()
		}
		return stepRetryReply("THE-REPLY"), nil
	})
	q.Model.modelName = "gpt-x"

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	if got := f.stderr.String(); !strings.Contains(got, "retry 1/4 in 1s") || !strings.Contains(got, "provider unavailable (503)") {
		t.Fatalf("stderr = %q, want the retry and its reason", got)
	}
	if got := f.stdout.String(); strings.Contains(got, "retry") {
		t.Fatalf("stdout = %q, want no retry notice", got)
	}
}

func TestStepRetry_RecordedCallStartsAtTheAttemptThatAnswered(t *testing.T) {
	f := &stepRetryFixture{}
	recorder := &stepRetryRecorder{}
	var answered time.Time
	q := f.querier(func(request int) (chan models.CompletionEvent, error) {
		if request == 1 {
			return nil, stepRetry503()
		}
		answered = time.Now()
		return stepRetryReply("done"), nil
	})
	q.callUsageRecorder = recorder
	q.stepRetry.Wait = func(context.Context, time.Duration) error {
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(recorder.calls))
	}
	if started := recorder.calls[0].StartedAt; started.Before(answered.Add(-10 * time.Millisecond)) {
		t.Fatalf("recorded start %v is %v before the answering request: the wait counted as model time", started, answered.Sub(started))
	}
}

type stepRetryRecorder struct{ calls []CompletedModelCall }

func (r *stepRetryRecorder) Record(_ context.Context, call CompletedModelCall) error {
	r.calls = append(r.calls, call)
	return nil
}

func TestStepRetry_ZeroValueMakesOneRequest(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(int) (chan models.CompletionEvent, error) { return nil, stepRetry503() })
	q.stepRetry = StepRetry{}

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); !errors.Is(err, claierr.ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
}

func TestStepRetry_NoticeNamesTheRoutedModel(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(request int) (chan models.CompletionEvent, error) {
		if request == 1 {
			return nil, stepRetry503()
		}
		return stepRetryReply("done"), nil
	})
	q.modelVersion = "gpt-route"

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	if got := f.stderr.String(); !strings.Contains(got, "gpt-route") {
		t.Fatalf("stderr = %q, want the routed model", got)
	}
}

func TestStepRetry_NoticeShowsTheConnectionCauseWithoutTheURL(t *testing.T) {
	f := &stepRetryFixture{}
	q := f.querier(func(request int) (chan models.CompletionEvent, error) {
		if request == 1 {
			return nil, claierr.NewTransport(&url.Error{
				Op:  "Post",
				URL: "https://host/v1?key=SECRET",
				Err: errors.New("connection\nrefused"),
			})
		}
		return stepRetryReply("done"), nil
	})

	if _, err := q.TextQuery(context.Background(), stepRetryChat()); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	got := f.stderr.String()
	if !strings.Contains(got, "connection failed: connection refused") {
		t.Fatalf("stderr = %q, want the cause on one line", got)
	}
	if strings.Contains(got, "SECRET") {
		t.Fatalf("stderr = %q leaks the request URL", got)
	}
}

type stepRetryFailingWriter struct{}

func (stepRetryFailingWriter) Write([]byte) (int, error) { return 0, errors.New("EIO") }

// A notice reports a retry, so failing to print one must not cost the retry.
func TestStepRetry_AFailedNoticeDoesNotEndTheRun(t *testing.T) {
	script := func(request int) (chan models.CompletionEvent, error) {
		if request == 1 {
			return nil, stepRetry503()
		}
		return stepRetryReply("done"), nil
	}
	session := func() *QuerySession { return &QuerySession{Chat: stepRetryChat()} }

	t.Run("stderr", func(t *testing.T) {
		f := &stepRetryFixture{}
		q := f.querier(script)
		q.errOut = stepRetryFailingWriter{}
		s := session()
		if _, err := q.streamWithRetry(context.Background(), s, s.Chat); err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(f.requests) != 2 {
			t.Fatalf("requests = %d, want 2", len(f.requests))
		}
	})
	t.Run("live frame", func(t *testing.T) {
		f := &stepRetryFixture{}
		q := f.querier(script)
		q.Raw = false
		q.ensureActivityViewport()
		q.out = stepRetryFailingWriter{}
		s := session()
		if _, err := q.streamWithRetry(context.Background(), s, s.Chat); err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(f.requests) != 2 {
			t.Fatalf("requests = %d, want 2", len(f.requests))
		}
	})
}

func TestDefaultStepRetry_IsTheAgreedSchedule(t *testing.T) {
	got := DefaultStepRetry()
	if !slices.Equal(got.Delays, stepRetrySchedule) {
		t.Fatalf("Delays = %v, want %v", got.Delays, stepRetrySchedule)
	}
	if got.Wait == nil {
		t.Fatal("Wait is nil")
	}
	if err := got.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestStepRetry_ValidateRefusesDelaysWithoutAWait(t *testing.T) {
	err := StepRetry{Delays: stepRetrySchedule}.validate()
	if !errors.Is(err, ErrStepRetryWithoutWait) {
		t.Fatalf("err = %v, want ErrStepRetryWithoutWait", err)
	}
	if err := (StepRetry{}).validate(); err != nil {
		t.Fatalf("zero value: %v", err)
	}
}

func TestWaitForStepRetry(t *testing.T) {
	t.Run("returns after the delay", func(t *testing.T) {
		if err := waitForStepRetry(context.Background(), time.Millisecond); err != nil {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("returns the cancellation at once", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		started := time.Now()
		if err := waitForStepRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("took %v", elapsed)
		}
	})
}
