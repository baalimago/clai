package text

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// guardedLoopQuerier adds the optional replay guard to a loop querier.
type guardedLoopQuerier struct {
	LoopQuerier
	produced bool
}

func (g guardedLoopQuerier) Produced() bool { return g.produced }

// TestQuerier_Produced pins the replay guard of one text run: a reply that
// already reached the screen, or a tool that already ran, is never replayed on
// another model (architecture/model-aliases.md).
func TestQuerier_Produced(t *testing.T) {
	t.Run("a fresh run produced nothing", func(t *testing.T) {
		if rollingLoopQuerier(t, &strings.Builder{}).Produced() {
			t.Fatal("Produced() = true, want false before anything reached the user")
		}
	})

	t.Run("a printed reply is produced", func(t *testing.T) {
		q := rollingLoopQuerier(t, &strings.Builder{})
		q.produced = true
		if !q.Produced() {
			t.Fatal("Produced() = false, want true after the reply printed")
		}
	})

	t.Run("a tool call is produced", func(t *testing.T) {
		q := rollingLoopQuerier(t, &strings.Builder{})
		q.tooling.calls = 1
		if !q.Produced() {
			t.Fatal("Produced() = false, want true after a tool ran")
		}
	})
}

// TestQuerier_ProducedAfterFailedRun pins that finalizing a failed run is not
// production: a run that failed before anything reached the user may be
// replayed on another model, while one whose text already streamed may not
// (architecture/model-aliases.md).
func TestQuerier_ProducedAfterFailedRun(t *testing.T) {
	runAndFail := func(t *testing.T, tokens ...string) *Querier[*MockQuerier] {
		t.Helper()
		q := &Querier[*MockQuerier]{
			Raw:   true,
			out:   &strings.Builder{},
			Model: &MockQuerier{completionChan: make(chan models.CompletionEvent), errChan: make(chan error)},
		}
		go func() {
			for _, token := range tokens {
				q.Model.completionChan <- token
			}
			q.Model.errChan <- errors.New("connection dropped")
			q.Model.completionChan <- "CLOSE"
		}()
		if err := q.Query(context.Background()); err == nil {
			t.Fatal("Query error = nil, want the stream failure")
		}
		return q
	}

	t.Run("a run that failed before any output is replayable", func(t *testing.T) {
		if got := runAndFail(t).Produced(); got {
			t.Fatal("Produced() = true, want false: nothing reached the user")
		}
	})

	t.Run("a run whose text streamed before failing is not replayable", func(t *testing.T) {
		if got := runAndFail(t, "partial answer").Produced(); !got {
			t.Fatal("Produced() = false, want true: the text reached the user")
		}
	})

	t.Run("a run that executed a tool call under default MaxToolCalls before failing is not replayable", func(t *testing.T) {
		step := 0
		q := &Querier[*MockQuerier]{
			Raw: true,
			out: &strings.Builder{},
			tooling: tooling{
				run: map[string]pub_models.LLMTool{
					"echo": loopTestTool{name: "echo", out: "ran"},
				},
			},
			Model: &MockQuerier{
				streamFn: func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
					step++
					if step == 1 {
						ch := make(chan models.CompletionEvent, 2)
						ch <- pub_models.Call{ID: "call-1", Name: "echo"}
						ch <- models.StopEvent{}
						close(ch)
						return ch, nil
					}
					return nil, claierr.NewProviderUnavailable(nil)
				},
			},
		}
		if err := q.Query(context.Background()); err == nil {
			t.Fatal("Query error = nil, want the step-1 failure")
		}
		if !q.Produced() {
			t.Fatal("Produced() = false, want true after a tool executed under default MaxToolCalls")
		}
	})

	t.Run("a run that emitted reasoning before failing is not replayable", func(t *testing.T) {
		q := &Querier[*MockQuerier]{
			Raw:   true,
			out:   &strings.Builder{},
			Model: &MockQuerier{completionChan: make(chan models.CompletionEvent), errChan: make(chan error)},
		}
		go func() {
			q.Model.completionChan <- models.ReasoningEvent{Content: "thinking..."}
			q.Model.errChan <- claierr.NewProviderUnavailable(nil)
			q.Model.completionChan <- "CLOSE"
		}()
		if err := q.Query(context.Background()); err == nil {
			t.Fatal("Query error = nil, want the stream failure")
		}
		if !q.Produced() {
			t.Fatal("Produced() = false, want true after reasoning reached the user")
		}
	})

	t.Run("a run that emitted an image before failing is not replayable", func(t *testing.T) {
		q := &Querier[*MockQuerier]{
			Raw:       true,
			configDir: t.TempDir(),
			out:       &strings.Builder{},
			Model:     &MockQuerier{completionChan: make(chan models.CompletionEvent), errChan: make(chan error)},
		}
		pngData := pngOf(t, 1)
		go func() {
			q.Model.completionChan <- models.ImageEvent{
				Image: pub_models.ImagePart{MIME: "image/png", Data: pngData},
			}
			q.Model.errChan <- claierr.NewProviderUnavailable(nil)
			q.Model.completionChan <- "CLOSE"
		}()
		if err := q.Query(context.Background()); err == nil {
			t.Fatal("Query error = nil, want the stream failure")
		}
		if !q.Produced() {
			t.Fatal("Produced() = false, want true after an image was written")
		}
	})

	t.Run("an unproduced terminal loop iteration abandons its summarizer and clears its header on model unavailability", func(t *testing.T) {
		var printed strings.Builder
		q := rollingLoopQuerier(t, &printed)
		q.configDir = t.TempDir()
		q.summarizer = blockingSummarizer()
		q.summarizeConversations = true
		q.shouldSaveReply = true
		q.summaryJoinTimeout = 5 * time.Second
		q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
		q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
			return nil, claierr.NewProviderUnavailable(nil)
		}
		chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
		if _, err := q.TextQuery(t.Context(), chat); !errors.Is(err, claierr.ErrProviderUnavailable) {
			t.Fatalf("TextQuery error = %v, want ErrProviderUnavailable", err)
		}
		if q.summaryRun != nil {
			t.Fatal("summaryRun must be abandoned on an unproduced model-unavailable loop iteration")
		}
		if rows := q.activityViewport.Rows(); len(rows) != 0 {
			t.Fatalf("activityViewport.Rows() = %q, want cleared header on unproduced fallback", rows)
		}
	})
}

// TestLoopRunner_Produced pins the loop's share of the guard: only the first
// iteration may be replayed, because a later one means the autonomous run
// already began (architecture/model-aliases.md).
func TestLoopRunner_Produced(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner *loopRunner
		want   bool
	}{
		{
			name:   "the first iteration asks the querier",
			runner: &loopRunner{querier: guardedLoopQuerier{LoopQuerier: &recordingLoopQuerier{}}, started: 1},
			want:   false,
		},
		{
			name:   "a querier that produced answers for the iteration",
			runner: &loopRunner{querier: guardedLoopQuerier{LoopQuerier: &recordingLoopQuerier{}, produced: true}, started: 1},
			want:   true,
		},
		{
			name:   "a later iteration is produced",
			runner: &loopRunner{querier: guardedLoopQuerier{LoopQuerier: &recordingLoopQuerier{}}, started: 2},
			want:   true,
		},
		{
			name:   "a querier without the guard is produced",
			runner: &loopRunner{querier: &recordingLoopQuerier{}, started: 1},
			want:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.runner.Produced(); got != tc.want {
				t.Fatalf("Produced() = %v, want %v", got, tc.want)
			}
		})
	}
}
