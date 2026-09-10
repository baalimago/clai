package text

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Test_Finalizer_PersistFailure_JoinsRunError pins the S8 repair: a failed
// reply persist at run end joins the run's returned error, so a later
// -re/-dre cannot silently read stale state (worklog
// 2026-09-05-error-propagation, phase 8).
func Test_Finalizer_PersistFailure_JoinsRunError(t *testing.T) {
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 2)
		out <- "the final answer"
		close(out)
		return out, nil
	}

	// configDir points at a regular file, so the persist's MkdirAll for
	// <configDir>/conversations fails deterministically and the cause stays a
	// *os.PathError through every %w wrap.
	blocker := filepath.Join(t.TempDir(), "config-dir-blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	q := &Querier[*MockQuerier]{
		out:       &strings.Builder{},
		Model:     model,
		configDir: blocker,
	}
	session := &QuerySession{
		Chat: pub_models.Chat{
			Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
		},
		ShouldSaveReply: true,
	}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		finalizer:    sessionFinalizer[*MockQuerier]{querier: q},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err == nil {
		t.Fatal("expected the persist failure to surface from Run, got nil")
	}
	if !strings.Contains(err.Error(), "failed to save previous query") {
		t.Errorf("err = %v, want it to name the failed reply persist", err)
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("err = %v, want the persist's *os.PathError cause reachable", err)
	}
	if !session.Failed {
		t.Error("session must be marked failed when the persist failure joins the run error")
	}
}

// finalizeFixture builds a raw-output querier whose cost enricher is a fake
// manager, and a completed session carrying one user turn and its usage.
func finalizeFixture(t *testing.T, manager *fakeCostManager, ready <-chan struct{}, usage *pub_models.Usage, save bool) (*Querier[*MockQuerier], *QuerySession, *strings.Builder) {
	t.Helper()
	var warned strings.Builder
	q := &Querier[*MockQuerier]{
		Raw:       true,
		out:       &strings.Builder{},
		configDir: t.TempDir(),
		costEnricher: costEnricher{
			manager: manager,
			ready:   ready,
			waitFor: time.Millisecond,
			warnf:   func(format string, a ...any) { fmt.Fprintf(&warned, format, a...) },
		},
	}
	session := &QuerySession{
		Chat: pub_models.Chat{
			ID:       "finalize-fixture",
			Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
		},
		FinalAssistantText: "answer",
		FinalUsage:         usage,
		ShouldSaveReply:    save,
	}
	return q, session, &warned
}

func closedReady() <-chan struct{} {
	ready := make(chan struct{})
	close(ready)
	return ready
}

func appendCostRow(chat pub_models.Chat) (pub_models.Chat, error) {
	chat.Queries = append(chat.Queries, pub_models.QueryCost{CostUSD: 0.42, Model: "fake", Usage: *chat.TokenUsage})
	return chat, nil
}

// configDirEntries lists every file below dir; a non-persisting run must
// leave it empty.
func configDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			entries = append(entries, strings.TrimPrefix(path, dir))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return entries
}

// TestFinalize_enrichesWithoutPersistence pins D19: a non-persisting run
// still carries the run's cost row; a catalog that is not ready keeps the
// chat, warns once and does not fail the run.
func TestFinalize_enrichesWithoutPersistence(t *testing.T) {
	usage := &pub_models.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}

	t.Run("catalog ready", func(t *testing.T) {
		manager := &fakeCostManager{enrichFn: appendCostRow}
		q, session, warned := finalizeFixture(t, manager, closedReady(), usage, false)

		if err := (sessionFinalizer[*MockQuerier]{querier: q}).Finalize(t.Context(), session); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if len(session.Chat.Queries) != 1 || session.Chat.Queries[0].Usage.TotalTokens != 3 {
			t.Fatalf("Queries = %+v, want one row with the run's usage", session.Chat.Queries)
		}
		if len(q.chat.Queries) != 1 {
			t.Fatalf("querier chat not updated with the enriched chat: %+v", q.chat.Queries)
		}
		if warned.Len() != 0 {
			t.Errorf("unexpected warning: %q", warned.String())
		}
		if entries := configDirEntries(t, q.configDir); len(entries) != 0 {
			t.Errorf("non-persisting run wrote %v", entries)
		}
	})

	t.Run("catalog not ready", func(t *testing.T) {
		manager := &fakeCostManager{enrichFn: appendCostRow}
		q, session, warned := finalizeFixture(t, manager, make(chan struct{}), usage, false)

		if err := (sessionFinalizer[*MockQuerier]{querier: q}).Finalize(t.Context(), session); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if len(session.Chat.Queries) != 0 {
			t.Fatalf("Queries = %+v, want none when the catalog is not ready", session.Chat.Queries)
		}
		if manager.calls != 0 {
			t.Errorf("Enrich called %d times before readiness", manager.calls)
		}
		if got := strings.Count(warned.String(), "skipping wait"); got != 1 {
			t.Errorf("want exactly one readiness warning, got %d in %q", got, warned.String())
		}
	})
}

// TestFinalize_withoutPersistence_writesNothing pins that a run without
// usage attempts no enrichment, warns nothing and writes nothing.
func TestFinalize_withoutPersistence_writesNothing(t *testing.T) {
	manager := &fakeCostManager{enrichFn: func(pub_models.Chat) (pub_models.Chat, error) {
		return pub_models.Chat{}, errors.New("must not be called")
	}}
	q, session, warned := finalizeFixture(t, manager, closedReady(), nil, false)

	if err := (sessionFinalizer[*MockQuerier]{querier: q}).Finalize(t.Context(), session); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if manager.calls != 0 {
		t.Errorf("Enrich called %d times without usage", manager.calls)
	}
	if warned.Len() != 0 {
		t.Errorf("unexpected warning: %q", warned.String())
	}
	if len(session.Chat.Queries) != 0 {
		t.Errorf("Queries = %+v, want none", session.Chat.Queries)
	}
	if entries := configDirEntries(t, q.configDir); len(entries) != 0 {
		t.Errorf("non-persisting run wrote %v", entries)
	}
}

// TestFinalize_persistingRunEnrichesOnce pins that hoisting the enrichment
// left the persisting path with one enrichment and the saved conversation
// carrying the cost row.
func TestFinalize_persistingRunEnrichesOnce(t *testing.T) {
	manager := &fakeCostManager{enrichFn: appendCostRow}
	q, session, _ := finalizeFixture(t, manager, closedReady(), &pub_models.Usage{TotalTokens: 3}, true)

	if err := (sessionFinalizer[*MockQuerier]{querier: q}).Finalize(t.Context(), session); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if manager.calls != 1 {
		t.Fatalf("Enrich called %d times, want 1", manager.calls)
	}
	saved, err := chat.FromPath(filepath.Join(q.configDir, "conversations", "finalize-fixture.json"))
	if err != nil {
		t.Fatalf("persisted conversation not readable: %v", err)
	}
	if len(saved.Queries) != 1 || saved.Queries[0].CostUSD != 0.42 {
		t.Fatalf("saved Queries = %+v, want the enriched row", saved.Queries)
	}
}

// joinFixture builds a persisting, raw querier with a fake summarizer already
// launched on ctx, so Finalize exercises the display → join → persist path.
func joinFixture(t *testing.T, ctx context.Context, fake *fakeSummarizer, manager *fakeCostManager, joinTimeout time.Duration) (*Querier[*MockQuerier], *QuerySession, chan struct{}) {
	t.Helper()
	q, session, _ := finalizeFixture(t, manager, closedReady(), &pub_models.Usage{TotalTokens: 3}, true)
	interrupt := make(chan struct{})
	seedEmptyChatIndex(t, q.configDir)
	q.shouldSaveReply = true
	q.summarizeConversations = true
	q.summaryJoinTimeout = joinTimeout
	q.summaryInterrupt = interrupt
	q.runModel = "run-model"
	q.chat = session.Chat
	if fake != nil {
		q.SetSummarizer(fake)
	}
	q.launchSummary(ctx)
	return q, session, interrupt
}

func savedFixtureChat(t *testing.T, q *Querier[*MockQuerier]) pub_models.Chat {
	t.Helper()
	saved, err := chat.FromPath(filepath.Join(q.configDir, "conversations", "finalize-fixture.json"))
	if err != nil {
		t.Fatalf("persisted conversation not readable: %v", err)
	}
	return saved
}

func assertUnlabelled(t *testing.T, c pub_models.Chat) {
	t.Helper()
	if c.Title != "" || c.Summary != "" || !c.SummaryAt.IsZero() {
		t.Fatalf("expected an unlabelled chat, got title=%q summary=%q at=%v", c.Title, c.Summary, c.SummaryAt)
	}
}

func assertLabelled(t *testing.T, c pub_models.Chat) {
	t.Helper()
	if c.Title != "T" || c.Summary != "S" || !c.SummaryAt.Equal(fakeGeneratedAt) {
		t.Fatalf("expected the fake label, got title=%q summary=%q at=%v", c.Title, c.Summary, c.SummaryAt)
	}
}

// TestFinalize_join executes the phase-4 join table and error rows
// (worklog 2026-09-09-conversation-summaries): every outcome leaves the
// finalizer's error nil unless the persist failed.
func TestFinalize_join(t *testing.T) {
	t.Setenv("DEBUG_SUMMARY", "")
	t.Setenv("DEBUG", "")
	finalize := func(t *testing.T, ctx context.Context, q *Querier[*MockQuerier], session *QuerySession) (time.Duration, error) {
		t.Helper()
		start := time.Now()
		err := (sessionFinalizer[*MockQuerier]{querier: q}).Finalize(ctx, session)
		return time.Since(start), err
	}

	t.Run("nothing launched", func(t *testing.T) {
		q, session, _ := joinFixture(t, t.Context(), nil, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		if q.summaryRun != nil {
			t.Fatal("no summarizer must mean no summaryRun")
		}
		elapsed, err := finalize(t, t.Context(), q, session)
		if err != nil || elapsed > time.Second {
			t.Fatalf("err=%v elapsed=%v, want nil and no wait", err, elapsed)
		}
		assertUnlabelled(t, savedFixtureChat(t, q))
	})

	t.Run("instant fake", func(t *testing.T) {
		fake := instantSummarizer()
		manager := &fakeCostManager{enrichFn: appendCostRow}
		q, session, _ := joinFixture(t, t.Context(), fake, manager, 5*time.Second)
		if _, err := finalize(t, t.Context(), q, session); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		assertLabelled(t, session.Chat)
		if len(session.Chat.Queries) != 2 || session.Chat.Queries[0].CostUSD != 0.42 || session.Chat.Queries[1].Purpose != "summary" {
			t.Fatalf("Queries = %+v, want the main row then the summary row", session.Chat.Queries)
		}
		saved := savedFixtureChat(t, q)
		assertLabelled(t, saved)
		if len(saved.Queries) != 2 || manager.calls != 1 {
			t.Fatalf("saved Queries = %+v (enrich calls %d), want both rows persisted once", saved.Queries, manager.calls)
		}
		if q.summaryRun != nil {
			t.Fatal("summaryRun must be released after the join")
		}
	})

	t.Run("instant fake without GeneratedAt stamps SummaryAt now", func(t *testing.T) {
		fake := newFakeSummarizer(func(context.Context, models.SummaryRequest) (models.Summary, error) {
			return models.Summary{Title: "T", Summary: "S"}, nil
		})
		q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		before := time.Now()
		if _, err := finalize(t, t.Context(), q, session); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if saved := savedFixtureChat(t, q); saved.Title != "T" || saved.SummaryAt.Before(before) {
			t.Fatalf("saved title=%q summary_at=%v, want the label stamped now", saved.Title, saved.SummaryAt)
		}
	})

	t.Run("fake slower than the bound", func(t *testing.T) {
		fake := blockingSummarizer()
		q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 20*time.Millisecond)
		elapsed, err := finalize(t, t.Context(), q, session)
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		waitClosed(t, fake.done, "summarizer cancellation")
		if !errors.Is(fake.firstCtx().Err(), context.Canceled) {
			t.Fatalf("fake context err = %v, want Canceled", fake.firstCtx().Err())
		}
		if elapsed > 20*time.Millisecond+2*time.Second {
			t.Fatalf("join took %v, want below the bound plus slack", elapsed)
		}
		assertUnlabelled(t, session.Chat)
		assertUnlabelled(t, savedFixtureChat(t, q))
	})

	t.Run("erroring fake", func(t *testing.T) {
		fake := erroringSummarizer()
		q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		var err error
		stderr := testboil.CaptureStderr(t, func(t *testing.T) {
			_, err = finalize(t, t.Context(), q, session)
		})
		if err != nil || stderr != "" {
			t.Fatalf("err=%v stderr=%q, want nil and silence", err, stderr)
		}
		assertUnlabelled(t, savedFixtureChat(t, q))
		if len(session.Chat.Queries) != 1 {
			t.Fatalf("Queries = %+v, want only the main row", session.Chat.Queries)
		}
	})

	t.Run("main run failed", func(t *testing.T) {
		fake := blockingSummarizer()
		q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		session.Failed = true
		elapsed, err := finalize(t, t.Context(), q, session)
		if err != nil || elapsed > time.Second {
			t.Fatalf("err=%v elapsed=%v, want nil and no join", err, elapsed)
		}
		waitClosed(t, fake.done, "summarizer cancellation")
		if q.summaryRun != nil {
			t.Fatal("summaryRun must be cancelled and dropped")
		}
		assertUnlabelled(t, savedFixtureChat(t, q))
	})

	t.Run("interrupt before completion", func(t *testing.T) {
		fake := blockingSummarizer()
		ctx, cancel := context.WithCancel(context.Background())
		q, session, _ := joinFixture(t, ctx, fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		cancel()
		session.SawStopEvent = false
		elapsed, err := finalize(t, ctx, q, session)
		if err != nil || elapsed > time.Second {
			t.Fatalf("err=%v elapsed=%v, want nil and no join", err, elapsed)
		}
		waitClosed(t, fake.done, "summarizer cancellation")
		assertUnlabelled(t, savedFixtureChat(t, q))
	})

	t.Run("root cancelled by the StopEvent still joins", func(t *testing.T) {
		fake := instantSummarizer()
		ctx, cancel := context.WithCancel(context.Background())
		q, session, _ := joinFixture(t, ctx, fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		cancel()
		session.SawStopEvent = true
		if _, err := finalize(t, ctx, q, session); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		assertLabelled(t, savedFixtureChat(t, q))
	})

	t.Run("interrupt during the join", func(t *testing.T) {
		fake := blockingSummarizer()
		q, session, interrupt := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 30*time.Second)
		go func() {
			<-fake.started
			close(interrupt)
		}()
		elapsed, err := finalize(t, t.Context(), q, session)
		if err != nil || elapsed > 5*time.Second {
			t.Fatalf("err=%v elapsed=%v, want nil and an immediate return", err, elapsed)
		}
		waitClosed(t, fake.done, "summarizer cancellation")
		assertUnlabelled(t, session.Chat)
		assertUnlabelled(t, savedFixtureChat(t, q))
	})

	t.Run("structured output", func(t *testing.T) {
		fake := instantSummarizer()
		q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		q.structuredOutput = true
		if _, err := finalize(t, t.Context(), q, session); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		assertLabelled(t, savedFixtureChat(t, q))
		if got := q.out.(*strings.Builder).String(); strings.Count(got, "answer") != 1 {
			t.Fatalf("out = %q, want the final answer printed once", got)
		}
	})

	t.Run("panicking fake", func(t *testing.T) {
		fake := panickingSummarizer()
		q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		q.structuredOutput = true
		_, err := finalize(t, t.Context(), q, session)
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		waitClosed(t, fake.done, "summarizer return")
		assertUnlabelled(t, savedFixtureChat(t, q))
		if got := q.out.(*strings.Builder).String(); strings.Count(got, "answer") != 1 {
			t.Fatalf("out = %q, want the answer printed", got)
		}
	})

	t.Run("persist fails after a successful join", func(t *testing.T) {
		fake := instantSummarizer()
		q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
		q.structuredOutput = true
		blocker := filepath.Join(t.TempDir(), "config-dir-blocker")
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("write blocker file: %v", err)
		}
		q.configDir = blocker
		_, err := finalize(t, t.Context(), q, session)
		if err == nil || !strings.Contains(err.Error(), "failed to save previous query") {
			t.Fatalf("err = %v, want the persist failure", err)
		}
		assertLabelled(t, session.Chat)
		if got := q.out.(*strings.Builder).String(); !strings.Contains(got, "answer") {
			t.Fatalf("out = %q, want the answer printed before the failed persist", got)
		}
	})

	t.Run("summary rows present but main enrichment failed", func(t *testing.T) {
		fake := instantSummarizer()
		manager := &fakeCostManager{enrichFn: func(pub_models.Chat) (pub_models.Chat, error) {
			return pub_models.Chat{}, errors.New("catalog broken")
		}}
		q, session, _ := joinFixture(t, t.Context(), fake, manager, 5*time.Second)
		if _, err := finalize(t, t.Context(), q, session); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		saved := savedFixtureChat(t, q)
		if len(saved.Queries) != 1 || saved.Queries[0].Purpose != "summary" {
			t.Fatalf("saved Queries = %+v, want the summary row alone", saved.Queries)
		}
	})
}

// TestFinalize_printsAnswerBeforeJoin pins D21: the answer bytes reach the
// writer before the join waits; the blocking fake is cancelled only after.
func TestFinalize_printsAnswerBeforeJoin(t *testing.T) {
	t.Setenv("DEBUG_SUMMARY", "")
	var mu sync.Mutex
	var events []string
	record := func(e string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}
	fake := newFakeSummarizer(func(ctx context.Context, _ models.SummaryRequest) (models.Summary, error) {
		<-ctx.Done()
		record("cancelled")
		return models.Summary{}, ctx.Err()
	})
	q, session, interrupt := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 30*time.Second)
	q.structuredOutput = true
	q.out = &hookWriter{onFirst: func() {
		record("answer")
		close(interrupt)
	}}
	if err := (sessionFinalizer[*MockQuerier]{querier: q}).Finalize(t.Context(), session); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	waitClosed(t, fake.done, "summarizer cancellation")
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"answer", "cancelled"}; !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	assertUnlabelled(t, savedFixtureChat(t, q))
}
