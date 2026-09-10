package text

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

var fakeGeneratedAt = time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)

// fakeSummarizer records every call and runs fn; started closes on the
// first call, done when that call returns (also after a panic).
type fakeSummarizer struct {
	fn      func(ctx context.Context, req models.SummaryRequest) (models.Summary, error)
	mu      sync.Mutex
	calls   []models.SummaryRequest
	ctxs    []context.Context
	started chan struct{}
	done    chan struct{}
}

func newFakeSummarizer(fn func(ctx context.Context, req models.SummaryRequest) (models.Summary, error)) *fakeSummarizer {
	return &fakeSummarizer{fn: fn, started: make(chan struct{}), done: make(chan struct{})}
}

func (f *fakeSummarizer) Summarize(ctx context.Context, req models.SummaryRequest) (models.Summary, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.ctxs = append(f.ctxs, ctx)
	first := len(f.calls) == 1
	f.mu.Unlock()
	if first {
		close(f.started)
		defer close(f.done)
	}
	return f.fn(ctx, req)
}

func (f *fakeSummarizer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSummarizer) firstCall() models.SummaryRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return models.SummaryRequest{}
	}
	return f.calls[0]
}

func (f *fakeSummarizer) firstCtx() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ctxs) == 0 {
		return nil
	}
	return f.ctxs[0]
}

func fakeSummary() models.Summary {
	return models.Summary{
		Title:       "T",
		Summary:     "S",
		Model:       "fake",
		Queries:     []pub_models.QueryCost{{Model: "fake", Purpose: "summary", Usage: pub_models.Usage{TotalTokens: 7}}},
		GeneratedAt: fakeGeneratedAt,
	}
}

func instantSummarizer() *fakeSummarizer {
	return newFakeSummarizer(func(context.Context, models.SummaryRequest) (models.Summary, error) {
		return fakeSummary(), nil
	})
}

// blockingSummarizer returns only when its context is cancelled.
func blockingSummarizer() *fakeSummarizer {
	return newFakeSummarizer(func(ctx context.Context, _ models.SummaryRequest) (models.Summary, error) {
		<-ctx.Done()
		return models.Summary{}, ctx.Err()
	})
}

func erroringSummarizer() *fakeSummarizer {
	return newFakeSummarizer(func(context.Context, models.SummaryRequest) (models.Summary, error) {
		return models.Summary{}, errors.New("summarizer boom")
	})
}

func panickingSummarizer() *fakeSummarizer {
	return newFakeSummarizer(func(context.Context, models.SummaryRequest) (models.Summary, error) {
		panic("summarizer panic")
	})
}

// waitClosed fails the test when ch does not close within the deadline.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen in time", what)
	}
}

func echoStream(text string) func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
	return func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 2)
		out <- text
		out <- models.StopEvent{}
		close(out)
		return out, nil
	}
}

// seedEmptyChatIndex writes a current, empty chat index so the first save
// does not print the pre-existing "Building cache index" chatter.
func seedEmptyChatIndex(t *testing.T, confDir string) {
	t.Helper()
	dir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chat_index.cache"), []byte(`{"version":2,"rows":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(chat_index.cache): %v", err)
	}
}

func captureStdoutStderrText(t *testing.T, fn func()) (string, string) {
	t.Helper()
	var stderr string
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		stderr = testboil.CaptureStderr(t, func(*testing.T) { fn() })
	})
	return stdout, stderr
}

// launchQuerier is a raw querier with an echoing mock model, a fake
// summarizer, an injected interrupt channel and a generous join bound.
func launchQuerier(t *testing.T, fake *fakeSummarizer, initial pub_models.Chat) *Querier[*MockQuerier] {
	t.Helper()
	confDir := t.TempDir()
	seedEmptyChatIndex(t, confDir)
	q := &Querier[*MockQuerier]{
		Raw:                    true,
		out:                    &strings.Builder{},
		configDir:              confDir,
		Model:                  &MockQuerier{streamFn: echoStream("answer"), usage: &pub_models.Usage{TotalTokens: 3}},
		summarizeConversations: true,
		shouldSaveReply:        true,
		summaryJoinTimeout:     5 * time.Second,
		summaryInterrupt:       make(chan struct{}),
		runModel:               "run-model",
		chat:                   initial,
	}
	if fake != nil {
		q.SetSummarizer(fake)
	}
	return q
}

func newChat(id string) pub_models.Chat {
	return pub_models.Chat{ID: id, Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
}

// newQuerierWithFlags runs the real flag cascade and NewQuerier so the
// launch conditions are proven through the production plumbing.
func newQuerierWithFlags(t *testing.T, conf Configurations, mods func(tf *internal.TextFlags)) *Querier[*MockQuerier] {
	t.Helper()
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	conf.Model = "mock"
	conf.ConfigDir = t.TempDir()
	seedEmptyChatIndex(t, conf.ConfigDir)
	conf.Raw = true
	conf.Out = &strings.Builder{}
	conf.SaveReplyAsConv = true
	conf.InitialChat = newChat("flag-chat")
	ApplyFlagOverrides(&conf, tfWith(t, mods))
	q, err := NewQuerier(t.Context(), conf, &MockQuerier{})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	q.Model.streamFn = echoStream("answer")
	q.Model.usage = &pub_models.Usage{TotalTokens: 3}
	q.costEnricher = costEnricher{}
	q.summaryInterrupt = make(chan struct{})
	return &q
}

func TestQuery_launchConditions(t *testing.T) {
	t.Setenv("DEBUG_SUMMARY", "")
	labelled := newChat("labelled")
	labelled.Title, labelled.Summary = "old", "old summary"
	preFeature := pub_models.Chat{ID: "pre-feature", Messages: []pub_models.Message{
		{Role: "user", Content: "first"}, {Role: "assistant", Content: "reply"}, {Role: "user", Content: "again"},
	}}

	rows := []struct {
		desc     string
		querier  func(t *testing.T, fake *fakeSummarizer) *Querier[*MockQuerier]
		launched bool
	}{
		{"all conditions hold, new chat", func(t *testing.T, f *fakeSummarizer) *Querier[*MockQuerier] {
			return launchQuerier(t, f, newChat("new"))
		}, true},
		{"InitialChat.Summary non-empty", func(t *testing.T, f *fakeSummarizer) *Querier[*MockQuerier] {
			return launchQuerier(t, f, labelled)
		}, false},
		{"-dre continuation of an unlabelled pre-feature chat", func(t *testing.T, f *fakeSummarizer) *Querier[*MockQuerier] {
			q := launchQuerier(t, f, preFeature)
			q.replyMode, q.dirReplyMode = true, true
			return q
		}, true},
		{"summarize-conversations false", func(t *testing.T, f *fakeSummarizer) *Querier[*MockQuerier] {
			q := launchQuerier(t, f, newChat("off"))
			q.summarizeConversations = false
			return q
		}, false},
		{"-summarize=false over a true config", func(t *testing.T, f *fakeSummarizer) *Querier[*MockQuerier] {
			q := newQuerierWithFlags(t, Configurations{SummarizeConversations: true}, func(tf *internal.TextFlags) {
				mustSet(t, &tf.QueryText.Summarize, "false")
			})
			q.SetSummarizer(f)
			return q
		}, false},
		{"-summarize over a false config", func(t *testing.T, f *fakeSummarizer) *Querier[*MockQuerier] {
			q := newQuerierWithFlags(t, Configurations{SummarizeConversations: false}, func(tf *internal.TextFlags) {
				mustSet(t, &tf.QueryText.Summarize, "true")
			})
			q.SetSummarizer(f)
			return q
		}, true},
		{"ShouldSaveReply false", func(t *testing.T, f *fakeSummarizer) *Querier[*MockQuerier] {
			q := launchQuerier(t, f, newChat("no-save"))
			q.shouldSaveReply = false
			return q
		}, false},
		{"no summarizer attached", func(t *testing.T, _ *fakeSummarizer) *Querier[*MockQuerier] {
			return launchQuerier(t, nil, newChat("no-summarizer"))
		}, false},
	}
	for _, row := range rows {
		t.Run(row.desc, func(t *testing.T) {
			fake := instantSummarizer()
			q := row.querier(t, fake)
			if err := q.Query(t.Context()); err != nil {
				t.Fatalf("Query: %v", err)
			}
			if got := fake.callCount(); (got == 1) != row.launched {
				t.Fatalf("summarizer calls = %d, want launched=%t", got, row.launched)
			}
			if q.summaryRun != nil {
				t.Fatal("summaryRun must be released after the run")
			}
			if row.launched != (q.chat.Title == "T") {
				t.Fatalf("chat title = %q, want labelled=%t", q.chat.Title, row.launched)
			}
			if req := fake.firstCall(); row.launched && (req.Model == "" || len(req.Chat.Messages) == 0) {
				t.Fatalf("request = %+v, want a model and the initial chat", req)
			}
		})
	}
}

// TestQuery_summaryModelResolution pins the query-path rungs of D22:
// -sm > config summary-model > the run's own model.
func TestQuery_summaryModelResolution(t *testing.T) {
	t.Setenv("DEBUG_SUMMARY", "")
	rows := []struct {
		desc string
		conf Configurations
		mods func(tf *internal.TextFlags)
		want string
	}{
		{"-sm wins over config", Configurations{SummarizeConversations: true, SummaryModel: "from-config"}, func(tf *internal.TextFlags) {
			mustSet(t, &tf.QueryText.SummaryModel, "from-flag")
		}, "from-flag"},
		{"config summary-model wins over the run model", Configurations{SummarizeConversations: true, SummaryModel: "from-config"}, nil, "from-config"},
		{"run model is the last rung", Configurations{SummarizeConversations: true}, nil, "mock"},
	}
	for _, row := range rows {
		t.Run(row.desc, func(t *testing.T) {
			fake := instantSummarizer()
			q := newQuerierWithFlags(t, row.conf, row.mods)
			q.SetSummarizer(fake)
			if err := q.Query(t.Context()); err != nil {
				t.Fatalf("Query: %v", err)
			}
			if got := fake.firstCall().Model; got != row.want {
				t.Fatalf("SummaryRequest.Model = %q, want %q", got, row.want)
			}
		})
	}
}

type ctxTestKey struct{}

// TestSummaryContext_isolation executes the context table: the run's cancel
// func never reaches the summary context, the launcher's cancel reaches only
// the summary context, the cancel key is absent, and values pass through.
func TestSummaryContext_isolation(t *testing.T) {
	newRoot := func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		ctx = context.WithValue(ctx, utils.ContextCancelKey, cancel)
		ctx = context.WithValue(ctx, ctxTestKey{}, "visible")
		return ctx, cancel
	}
	launch := func(t *testing.T) (*Querier[*MockQuerier], *fakeSummarizer, context.Context, context.CancelFunc) {
		t.Helper()
		fake := blockingSummarizer()
		q := launchQuerier(t, fake, newChat("ctx"))
		root, cancel := newRoot()
		t.Cleanup(cancel)
		q.launchSummary(root)
		t.Cleanup(q.abandonSummary)
		waitClosed(t, fake.started, "summarizer start")
		return q, fake, root, cancel
	}

	t.Run("root cancel func leaves the summary context alive", func(t *testing.T) {
		_, fake, root, _ := launch(t)
		root.Value(utils.ContextCancelKey).(context.CancelFunc)()
		if root.Err() == nil {
			t.Fatal("root must be cancelled by its own cancel func")
		}
		if err := fake.firstCtx().Err(); err != nil {
			t.Fatalf("summary context done after the root cancel: %v", err)
		}
	})

	t.Run("summaryRun cancel reaches only the summary context", func(t *testing.T) {
		q, fake, root, _ := launch(t)
		q.summaryRun.cancel()
		waitClosed(t, fake.done, "summarizer cancellation")
		if !errors.Is(fake.firstCtx().Err(), context.Canceled) {
			t.Fatalf("summary context err = %v, want Canceled", fake.firstCtx().Err())
		}
		if root.Err() != nil {
			t.Fatalf("run context done after the summary cancel: %v", root.Err())
		}
	})

	t.Run("summary context carries no cancel func under the key", func(t *testing.T) {
		_, fake, _, _ := launch(t)
		if v := fake.firstCtx().Value(utils.ContextCancelKey); v != nil {
			t.Fatalf("summary context exposes a cancel func under ContextCancelKey: %T", v)
		}
	})

	t.Run("run context values are visible on the summary context", func(t *testing.T) {
		_, fake, _, _ := launch(t)
		if got := fake.firstCtx().Value(ctxTestKey{}); got != "visible" {
			t.Fatalf("value = %v, want visible", got)
		}
	})
}

// hookWriter runs onFirst before the first write reaches the buffer.
type hookWriter struct {
	strings.Builder
	once    sync.Once
	onFirst func()
}

func (h *hookWriter) Write(p []byte) (int, error) {
	h.once.Do(h.onFirst)
	return h.Builder.Write(p)
}

// TestQuery_stopEventDoesNotCancelSummary runs the real mock vendor on a
// root context shaped like main.go's: its StopEvent cancels the root, and
// the blocking fake is still running when the finalizer prints the answer
// and starts the join.
func TestQuery_stopEventDoesNotCancelSummary(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("DEBUG_SUMMARY", "")
	confDir := t.TempDir()
	writeMockPriceFile(t, confDir)
	seedEmptyChatIndex(t, confDir)
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	root = context.WithValue(root, utils.ContextCancelKey, cancel)

	fake := blockingSummarizer()
	interrupt := make(chan struct{})
	var rootDoneAtDisplay, fakeAliveAtDisplay bool
	out := &hookWriter{onFirst: func() {
		waitClosed(t, fake.started, "summarizer start")
		rootDoneAtDisplay = root.Err() != nil
		fakeAliveAtDisplay = fake.firstCtx().Err() == nil
		close(interrupt)
	}}
	conf := Configurations{
		Model:                  "test",
		ConfigDir:              confDir,
		SaveReplyAsConv:        true,
		SummarizeConversations: true,
		Out:                    out,
		ResponseFormat:         &pub_models.ResponseFormat{Type: "json_object"},
		InitialChat:            newChat("stop-event"),
	}
	q, err := NewQuerier(root, conf, &vendors.Mock{})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	q.SetSummarizer(fake)
	q.summaryInterrupt = interrupt

	if err := q.Query(root); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("answer not printed: %q", out.String())
	}
	if !rootDoneAtDisplay {
		t.Fatal("the mock's StopEvent must have cancelled the root context before the display")
	}
	if !fakeAliveAtDisplay {
		t.Fatal("the summarizer must still be running when the finalizer starts the join")
	}
	waitClosed(t, fake.done, "summarizer abandonment")
	if q.chat.Title != "" {
		t.Fatalf("interrupted join must leave the chat unlabelled, got %q", q.chat.Title)
	}
	if _, err := chat.FromPath(filepath.Join(confDir, "conversations", "stop-event.json")); err != nil {
		t.Fatalf("conversation not persisted: %v", err)
	}
}

// TestSignalInterrupt pins the nil-channel default: the launcher's own
// SIGINT/SIGTERM registration closes the interrupt channel, and release
// unregisters without closing it.
func TestSignalInterrupt(t *testing.T) {
	t.Run("release without a signal leaves the channel open", func(t *testing.T) {
		interrupt, release := signalInterrupt()
		release()
		select {
		case <-interrupt:
			t.Fatal("interrupt closed without a signal")
		case <-time.After(20 * time.Millisecond):
		}
	})
	t.Run("SIGINT closes the channel", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("self-signalling is not supported on windows")
		}
		interrupt, release := signalInterrupt()
		defer release()
		self, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Fatalf("FindProcess: %v", err)
		}
		if err := self.Signal(os.Interrupt); err != nil {
			t.Fatalf("Signal: %v", err)
		}
		waitClosed(t, interrupt, "interrupt on SIGINT")
	})
}

// TestJoinSummary_emptySummaryIsFailure pins that a nil-error result with an
// empty Summary is dropped like an error: no label, no usage rows, no
// SummaryAt (review 3, R3-08).
func TestJoinSummary_emptySummaryIsFailure(t *testing.T) {
	t.Setenv("DEBUG_SUMMARY", "")
	empty := fakeSummary()
	empty.Title, empty.Summary = "", ""
	fake := newFakeSummarizer(func(context.Context, models.SummaryRequest) (models.Summary, error) {
		return empty, nil
	})
	q, session, _ := joinFixture(t, t.Context(), fake, &fakeCostManager{enrichFn: appendCostRow}, 5*time.Second)
	var err error
	stdout, stderr := captureStdoutStderrText(t, func() {
		err = (sessionFinalizer[*MockQuerier]{querier: q}).Finalize(t.Context(), session)
	})
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("err=%v stdout=%q stderr=%q, want nil and silence", err, stdout, stderr)
	}
	assertUnlabelled(t, session.Chat)
	assertUnlabelled(t, savedFixtureChat(t, q))
	if len(session.Chat.Queries) != 1 {
		t.Fatalf("Queries = %+v, want only the main row", session.Chat.Queries)
	}
	if q.summaryRun != nil {
		t.Fatal("summaryRun must be released after the join")
	}
}

// TestLaunchSummary_relaunchAbandonsPrevious pins the launch guard: a second
// launch cancels and releases the previous run before installing its own
// (review 3, R3-21).
func TestLaunchSummary_relaunchAbandonsPrevious(t *testing.T) {
	t.Setenv("DEBUG_SUMMARY", "")
	fake := blockingSummarizer()
	q := launchQuerier(t, fake, newChat("relaunch"))
	q.launchSummary(t.Context())
	t.Cleanup(q.abandonSummary)
	first := q.summaryRun
	if first == nil {
		t.Fatal("first launch installed no run")
	}
	waitClosed(t, fake.started, "summarizer start")
	released := make(chan struct{})
	first.release = func() { close(released) }

	q.launchSummary(t.Context())
	if q.summaryRun == nil || q.summaryRun == first {
		t.Fatal("second launch must install a new run")
	}
	waitClosed(t, released, "release of the first run")
	waitClosed(t, fake.done, "cancellation of the first run")
	if !errors.Is(fake.firstCtx().Err(), context.Canceled) {
		t.Fatalf("first run context err = %v, want Canceled", fake.firstCtx().Err())
	}
}

// TestTraceSummaryf_stderr pins the trace sink: DEBUG_SUMMARY traces go to
// stderr, never the answer stream (review 3, R3-17).
func TestTraceSummaryf_stderr(t *testing.T) {
	t.Setenv("DEBUG", "")
	t.Run("enabled writes stderr only", func(t *testing.T) {
		t.Setenv("DEBUG_SUMMARY", "1")
		stdout, stderr := captureStdoutStderrText(t, func() { traceSummaryf("hello %d", 7) })
		if stdout != "" || !strings.Contains(stderr, "[DEBUG_SUMMARY] hello 7") {
			t.Fatalf("stdout = %q, stderr = %q, want the trace on stderr only", stdout, stderr)
		}
	})
	t.Run("disabled writes nothing", func(t *testing.T) {
		t.Setenv("DEBUG_SUMMARY", "")
		stdout, stderr := captureStdoutStderrText(t, func() { traceSummaryf("hello") })
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q, want silence", stdout, stderr)
		}
	})
}
