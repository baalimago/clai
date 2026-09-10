package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type fakeSummarizer struct {
	calls atomic.Int32
	fn    func(ctx context.Context, req models.SummaryRequest) (models.Summary, error)
}

func (f *fakeSummarizer) Summarize(ctx context.Context, req models.SummaryRequest) (models.Summary, error) {
	f.calls.Add(1)
	return f.fn(ctx, req)
}

func fakeLabel(id string) models.Summary {
	return models.Summary{
		Title:       "title-" + id,
		Summary:     "summary-" + id,
		Model:       "fake",
		Queries:     []pub_models.QueryCost{{Model: "fake", Purpose: "summary"}},
		GeneratedAt: time.Now().UTC(),
	}
}

func instantSummarizer() *fakeSummarizer {
	return &fakeSummarizer{fn: func(_ context.Context, req models.SummaryRequest) (models.Summary, error) {
		return fakeLabel(req.Chat.ID), nil
	}}
}

type summarizeFixture struct {
	h      *ChatHandler
	out    *bytes.Buffer
	errOut *bytes.Buffer
	dir    string

	mu      sync.Mutex
	flushes [][]string
}

func newSummarizeFixture(t *testing.T, s models.Summarizer, ids ...string) *summarizeFixture {
	t.Helper()
	oldLive := utils.Live
	t.Cleanup(func() { utils.Live = oldLive })
	utils.Live = true
	dir := t.TempDir()
	for _, id := range ids {
		if err := Save(dir, pub_models.Chat{ID: id, Created: time.Now().Add(-time.Hour), Messages: []pub_models.Message{{Role: "user", Content: "hello " + id}}}); err != nil {
			t.Fatalf("Save(%q): %v", id, err)
		}
	}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	f := &summarizeFixture{out: out, errOut: errOut, dir: dir}
	f.h = &ChatHandler{
		convDir:    dir,
		subCmd:     "summarize",
		out:        out,
		errOut:     errOut,
		input:      strings.NewReader("y\n"),
		summarizer: s,
		summarizeOptions: summarizeOptions{
			since:   time.Now().Add(-24 * time.Hour),
			yes:     true,
			workers: 2,
		},
	}
	return f
}

func (f *summarizeFixture) countFlush(_ string, chats []pub_models.Chat) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(chats))
	for _, c := range chats {
		ids = append(ids, c.ID)
	}
	slices.Sort(ids)
	f.flushes = append(f.flushes, ids)
	return nil
}

func (f *summarizeFixture) load(t *testing.T, id string) pub_models.Chat {
	t.Helper()
	c, err := FromPath(filepath.Join(f.dir, id+".json"))
	if err != nil {
		t.Fatalf("FromPath(%q): %v", id, err)
	}
	return c
}

func (f *summarizeFixture) indexRows(t *testing.T) map[string]chatIndexRow {
	t.Helper()
	rows, err := readChatIndex(f.dir)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	byID := map[string]chatIndexRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	return byID
}

func TestHandleSummarize_selectsWindow(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rows := []chatIndexRow{
		{ID: "inside", Created: since.Add(-time.Hour), Updated: since.Add(time.Hour)},
		{ID: "exact", Created: since},
		{ID: "older", Created: since.Add(-time.Hour)},
		{ID: "labelled", Created: since.Add(time.Hour), Summary: "S"},
		{ID: "globalScope", Created: since.Add(time.Hour)},
	}
	ids := func(rows []chatIndexRow) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.ID)
		}
		slices.Sort(out)
		return out
	}
	if got := ids(selectSummarizeRows(rows, since, false)); !slices.Equal(got, []string{"exact", "inside"}) {
		t.Fatalf("selected %v, want the unlabelled rows inside the window", got)
	}
	if got := ids(selectSummarizeRows(rows, since, true)); !slices.Equal(got, []string{"exact", "inside", "labelled"}) {
		t.Fatalf("selected with -force %v, want the labelled row too", got)
	}

	t.Run("handler reads the window from the index", func(t *testing.T) {
		s := instantSummarizer()
		f := newSummarizeFixture(t, s, "recent", "old")
		rows := f.indexRows(t)
		old := rows["old"]
		old.Updated = time.Now().Add(-48 * time.Hour)
		if err := writeChatIndex(f.dir, []chatIndexRow{rows["recent"], old, {ID: "globalScope", Created: time.Now()}}); err != nil {
			t.Fatalf("writeChatIndex: %v", err)
		}
		if err := f.h.handleSummarize(context.Background()); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		if s.calls.Load() != 1 {
			t.Fatalf("summarizer calls = %d, want the one row inside the window", s.calls.Load())
		}
		if f.load(t, "recent").Title != "title-recent" || f.load(t, "old").Title != "" {
			t.Fatal("only the row inside the window may be labelled")
		}
	})
}

func TestHandleSummarize_confirmation(t *testing.T) {
	seed := func(t *testing.T) (*summarizeFixture, *fakeSummarizer) {
		s := instantSummarizer()
		return newSummarizeFixture(t, s, "a"), s
	}
	for _, tc := range []struct {
		name       string
		input      string
		wantCalls  int32
		wantNotice string
	}{
		{name: "y proceeds", input: "y\n", wantCalls: 1},
		{name: "yes proceeds", input: "YES\n", wantCalls: 1},
		{name: "n aborts", input: "n\n", wantCalls: 0, wantNotice: "aborted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, s := seed(t)
			f.h.summarizeOptions.yes = false
			f.h.input = strings.NewReader(tc.input)
			if err := f.h.handleSummarize(context.Background()); err != nil {
				t.Fatalf("handleSummarize: %v", err)
			}
			if s.calls.Load() != tc.wantCalls {
				t.Fatalf("summarizer calls = %d, want %d", s.calls.Load(), tc.wantCalls)
			}
			if !strings.Contains(f.out.String(), "1 conversation") {
				t.Fatalf("prompt must state the count, got %q", f.out.String())
			}
			if tc.wantNotice != "" && !strings.Contains(f.out.String(), tc.wantNotice) {
				t.Fatalf("output %q must contain %q", f.out.String(), tc.wantNotice)
			}
			if labelled := f.load(t, "a").Title != ""; labelled != (tc.wantCalls == 1) {
				t.Fatalf("labelled = %v, want %v", labelled, tc.wantCalls == 1)
			}
		})
	}

	t.Run("-y skips the prompt", func(t *testing.T) {
		f, s := seed(t)
		f.h.input = iotest.ErrReader(errors.New("prompt must not be read"))
		if err := f.h.handleSummarize(context.Background()); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		if s.calls.Load() != 1 {
			t.Fatalf("summarizer calls = %d, want 1", s.calls.Load())
		}
	})

	t.Run("-n without -y errors before touching anything", func(t *testing.T) {
		f, s := seed(t)
		f.h.summarizeOptions.yes = false
		f.h.input = iotest.ErrReader(errors.New("prompt must not be read"))
		utils.Live = false
		err := f.h.handleSummarize(context.Background())
		if err == nil || !strings.Contains(err.Error(), "-y") {
			t.Fatalf("err = %v, want one naming -y", err)
		}
		if s.calls.Load() != 0 || f.load(t, "a").Title != "" {
			t.Fatal("nothing may be summarized without confirmation")
		}
	})

	t.Run("zero rows prints a notice", func(t *testing.T) {
		f, s := seed(t)
		f.h.summarizeOptions.since = time.Now().Add(time.Hour)
		f.h.input = iotest.ErrReader(errors.New("prompt must not be read"))
		f.h.upsertIndexBatch = f.countFlush
		if err := f.h.handleSummarize(context.Background()); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		if s.calls.Load() != 0 || len(f.flushes) != 0 {
			t.Fatal("zero rows must call neither the summarizer nor the index writer")
		}
		if !strings.Contains(f.out.String(), "0 conversations") {
			t.Fatalf("output %q must carry the zero notice", f.out.String())
		}
	})
}

func TestHandleSummarize_tokenEstimate(t *testing.T) {
	want := 3 * models.SummaryInputRunes / summaryEstimateRunesPerToken
	if got := summarizeTokenEstimate(3); got != want {
		t.Fatalf("summarizeTokenEstimate(3) = %d, want %d", got, want)
	}
	f := newSummarizeFixture(t, instantSummarizer(), "a", "b", "c")
	f.h.summarizeOptions.yes = false
	f.h.input = strings.NewReader("n\n")
	if err := f.h.handleSummarize(context.Background()); err != nil {
		t.Fatalf("handleSummarize: %v", err)
	}
	if out := f.out.String(); !strings.Contains(out, "3 conversations") || !strings.Contains(out, fmt.Sprint(want)) {
		t.Fatalf("prompt %q must state the count and the token estimate %d", out, want)
	}
}

func TestHandleSummarize_workerBound(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 16)
	var inflight, maxSeen atomic.Int32
	s := &fakeSummarizer{fn: func(_ context.Context, req models.SummaryRequest) (models.Summary, error) {
		cur := inflight.Add(1)
		for {
			m := maxSeen.Load()
			if cur <= m || maxSeen.CompareAndSwap(m, cur) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		inflight.Add(-1)
		return fakeLabel(req.Chat.ID), nil
	}}
	f := newSummarizeFixture(t, s, "a", "b", "c", "d", "e")
	f.h.summarizeOptions.workers = 2
	done := make(chan error, 1)
	go func() { done <- f.h.handleSummarize(context.Background()) }()
	<-entered
	<-entered
	select {
	case <-entered:
		t.Fatal("a third job entered with -workers 2")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("handleSummarize: %v", err)
	}
	if maxSeen.Load() != 2 {
		t.Fatalf("max concurrent summarizer calls = %d, want exactly the worker bound 2", maxSeen.Load())
	}
	if s.calls.Load() != 5 {
		t.Fatalf("summarizer calls = %d, want every job", s.calls.Load())
	}
}

func TestHandleSummarize_singleIndexWrite(t *testing.T) {
	f := newSummarizeFixture(t, instantSummarizer(), "a", "b", "c")
	f.h.upsertIndexBatch = f.countFlush
	if err := f.h.handleSummarize(context.Background()); err != nil {
		t.Fatalf("handleSummarize: %v", err)
	}
	if len(f.flushes) != 1 || !slices.Equal(f.flushes[0], []string{"a", "b", "c"}) {
		t.Fatalf("index flushes = %v, want exactly one with every labelled chat", f.flushes)
	}
	for _, id := range []string{"a", "b", "c"} {
		if f.load(t, id).Title != "title-"+id {
			t.Fatalf("chat %q not labelled on disk", id)
		}
	}

	t.Run("flush failure", func(t *testing.T) {
		f := newSummarizeFixture(t, instantSummarizer(), "a")
		f.h.upsertIndexBatch = func(string, []pub_models.Chat) error { return errors.New("disk full") }
		err := f.h.handleSummarize(context.Background())
		if err == nil || !strings.Contains(err.Error(), "index") || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("err = %v, want one naming the index flush", err)
		}
		if f.load(t, "a").Title != "title-a" {
			t.Fatal("files already written must stand after a failed index flush")
		}
	})

	t.Run("default writer is the batch upsert", func(t *testing.T) {
		f := newSummarizeFixture(t, instantSummarizer(), "a")
		if err := f.h.handleSummarize(context.Background()); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		if row := f.indexRows(t)["a"]; row.Title != "title-a" || row.Summary != "summary-a" {
			t.Fatalf("index row = %+v, want the label", row)
		}
	})
}

func TestHandleSummarize_jobContextsIsolated(t *testing.T) {
	t.Run("a job's cancel reaches only its own context", func(t *testing.T) {
		var rootCancelled atomic.Bool
		rootCtx, rootCancel := context.WithCancel(context.Background())
		defer rootCancel()
		ctx := context.WithValue(rootCtx, utils.ContextCancelKey, context.CancelFunc(func() {
			rootCancelled.Store(true)
			rootCancel()
		}))
		aCancelled := make(chan struct{})
		s := &fakeSummarizer{fn: func(ctx context.Context, req models.SummaryRequest) (models.Summary, error) {
			switch req.Chat.ID {
			case "a":
				cancel, ok := ctx.Value(utils.ContextCancelKey).(context.CancelFunc)
				if !ok {
					return models.Summary{}, errors.New("no cancel func under the key")
				}
				cancel()
				if ctx.Err() == nil {
					return models.Summary{}, errors.New("own context not done after its cancel")
				}
				close(aCancelled)
			case "b":
				<-aCancelled
				if err := ctx.Err(); err != nil {
					return models.Summary{}, fmt.Errorf("sibling context cancelled: %w", err)
				}
			}
			return fakeLabel(req.Chat.ID), nil
		}}
		f := newSummarizeFixture(t, s, "a", "b")
		if err := f.h.handleSummarize(ctx); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		if f.load(t, "a").Title != "title-a" || f.load(t, "b").Title != "title-b" {
			t.Fatal("both jobs must complete")
		}
		if ctx.Err() != nil || rootCancelled.Load() {
			t.Fatalf("command context done=%v, root cancel called=%v; want neither", ctx.Err(), rootCancelled.Load())
		}
	})

	t.Run("no cancel func on the command context", func(t *testing.T) {
		s := &fakeSummarizer{fn: func(ctx context.Context, req models.SummaryRequest) (models.Summary, error) {
			if _, ok := ctx.Value(utils.ContextCancelKey).(context.CancelFunc); !ok {
				return models.Summary{}, errors.New("job context must carry its own cancel func")
			}
			return fakeLabel(req.Chat.ID), nil
		}}
		f := newSummarizeFixture(t, s, "a", "b")
		if err := f.h.handleSummarize(context.Background()); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		if s.calls.Load() != 2 {
			t.Fatalf("summarizer calls = %d, want 2", s.calls.Load())
		}
	})
}

func TestHandleSummarize_flushesOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan string, 8)
	var mu sync.Mutex
	blockedErrs := map[string]error{}
	s := &fakeSummarizer{fn: func(ctx context.Context, req models.SummaryRequest) (models.Summary, error) {
		if req.Chat.ID == "a" {
			return fakeLabel("a"), nil
		}
		entered <- req.Chat.ID
		<-ctx.Done()
		mu.Lock()
		blockedErrs[req.Chat.ID] = ctx.Err()
		mu.Unlock()
		return models.Summary{}, ctx.Err()
	}}
	f := newSummarizeFixture(t, s, "a", "b", "c", "d")
	f.h.upsertIndexBatch = f.countFlush
	// "a" sorts youngest-first only by Created; feed order is the index order,
	// so make "a" the newest row and the first job.
	if err := Save(f.dir, pub_models.Chat{ID: "a", Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: "hello a"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- f.h.handleSummarize(ctx) }()
	first, second := <-entered, <-entered
	cancel()
	err := <-done
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a cancellation error", err)
	}
	if len(f.flushes) != 1 || !slices.Equal(f.flushes[0], []string{"a"}) {
		t.Fatalf("index flushes = %v, want one flush with the completed chat", f.flushes)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{first, second} {
		if !errors.Is(blockedErrs[id], context.Canceled) {
			t.Fatalf("job %q context err = %v, want cancelled", id, blockedErrs[id])
		}
	}
	if f.load(t, "a").Title != "title-a" {
		t.Fatal("the completed job must be on disk")
	}
	if f.load(t, first).Title != "" {
		t.Fatalf("cancelled job %q must not be labelled", first)
	}
}

func TestHandleSummarize_reportsErrors(t *testing.T) {
	s := &fakeSummarizer{fn: func(_ context.Context, req models.SummaryRequest) (models.Summary, error) {
		switch req.Chat.ID {
		case "fails":
			return models.Summary{}, errors.New("model refused")
		case "unsavable":
			label := fakeLabel("unsavable")
			label.Queries[0].CostUSD = math.NaN()
			return label, nil
		}
		return fakeLabel(req.Chat.ID), nil
	}}
	f := newSummarizeFixture(t, s, "ok", "missing", "fails", "unsavable")
	f.h.upsertIndexBatch = f.countFlush
	if err := os.Remove(filepath.Join(f.dir, "missing.json")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	err := f.h.handleSummarize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "3 of 4") {
		t.Fatalf("err = %v, want the failure count", err)
	}
	if len(f.flushes) != 1 || !slices.Equal(f.flushes[0], []string{"ok"}) {
		t.Fatalf("index flushes = %v, want one flush with only the labelled chat", f.flushes)
	}
	if f.load(t, "ok").Title != "title-ok" || f.load(t, "fails").Title != "" || f.load(t, "unsavable").Title != "" {
		t.Fatal("only the successful job may be labelled on disk")
	}
	out := f.out.String()
	for _, id := range []string{"missing", "fails", "unsavable"} {
		if !strings.Contains(out, id+": ERROR") {
			t.Fatalf("output %q must report %q", out, id)
		}
	}
	if !strings.Contains(out, "ok: title-ok") {
		t.Fatalf("output %q must report the label", out)
	}
}

func TestHandleSummarize_forceAndIdempotence(t *testing.T) {
	s := instantSummarizer()
	f := newSummarizeFixture(t, s, "a")
	if err := f.h.handleSummarize(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := f.load(t, "a")
	if first.Title != "title-a" || first.SummaryAt.IsZero() || len(first.Queries) != 1 {
		t.Fatalf("first run must label the chat, got %+v", first)
	}

	f.out.Reset()
	if err := f.h.handleSummarize(context.Background()); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if s.calls.Load() != 1 || !strings.Contains(f.out.String(), "0 conversations") {
		t.Fatalf("rerun must be a no-op, calls=%d out=%q", s.calls.Load(), f.out.String())
	}

	f.h.summarizeOptions.force = true
	if err := f.h.handleSummarize(context.Background()); err != nil {
		t.Fatalf("force run: %v", err)
	}
	forced := f.load(t, "a")
	if s.calls.Load() != 2 || !forced.SummaryAt.After(first.SummaryAt) || len(forced.Queries) != 2 {
		t.Fatalf("-force must regenerate, calls=%d before=%v after=%v queries=%d", s.calls.Load(), first.SummaryAt, forced.SummaryAt, len(forced.Queries))
	}
}

func TestHandleSummarize_rawOutput(t *testing.T) {
	jsonLines := func(t *testing.T, out string) []map[string]any {
		t.Helper()
		var objs []map[string]any
		for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Fatalf("line %q is not a JSON object: %v", line, err)
			}
			objs = append(objs, obj)
		}
		return objs
	}
	t.Run("one object per result plus totals", func(t *testing.T) {
		s := &fakeSummarizer{fn: func(_ context.Context, req models.SummaryRequest) (models.Summary, error) {
			if req.Chat.ID == "bad" {
				return models.Summary{}, errors.New("boom")
			}
			return fakeLabel(req.Chat.ID), nil
		}}
		f := newSummarizeFixture(t, s, "a", "bad")
		f.h.raw = true
		if err := f.h.handleSummarize(context.Background()); err == nil {
			t.Fatal("expected the failed job to surface as an error")
		}
		objs := jsonLines(t, f.out.String())
		if len(objs) != 3 {
			t.Fatalf("expected one line per result plus totals, got %v", objs)
		}
		byID := map[string]map[string]any{}
		for _, obj := range objs {
			if id, ok := obj["id"].(string); ok {
				byID[id] = obj
			}
		}
		if byID["a"]["title"] != "title-a" || byID["a"]["summary"] != "summary-a" || byID["a"]["error"] != "" {
			t.Fatalf("row a = %v", byID["a"])
		}
		if byID["bad"]["error"] != "boom" || byID["bad"]["title"] != "" {
			t.Fatalf("row bad = %v", byID["bad"])
		}
	})
	t.Run("zero rows is a totals object", func(t *testing.T) {
		f := newSummarizeFixture(t, instantSummarizer())
		f.h.raw = true
		f.h.summarizeOptions.yes = true
		if err := f.h.handleSummarize(context.Background()); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		objs := jsonLines(t, f.out.String())
		if len(objs) != 1 || objs[0]["selected"] != float64(0) || objs[0]["labelled"] != float64(0) || objs[0]["failed"] != float64(0) {
			t.Fatalf("zero-row raw output = %v, want one all-zero totals object", objs)
		}
	})
	t.Run("declined confirmation is an aborted object", func(t *testing.T) {
		oldLive := utils.Live
		t.Cleanup(func() { utils.Live = oldLive })
		utils.Live = true
		f := newSummarizeFixture(t, instantSummarizer(), "a")
		f.h.raw = true
		f.h.summarizeOptions.yes = false
		f.h.input = strings.NewReader("n\n")
		if err := f.h.handleSummarize(context.Background()); err != nil {
			t.Fatalf("handleSummarize: %v", err)
		}
		out := f.out.String()
		objs := jsonLines(t, out)
		if len(objs) != 1 || objs[0]["aborted"] != true {
			t.Fatalf("aborted raw output = %q, want exactly one aborted object", out)
		}
		if !strings.Contains(f.errOut.String(), "Summarize 1 conversations") {
			t.Fatalf("the confirmation prompt must go to stderr under -r, got %q", f.errOut.String())
		}
	})
}

// liveFixture is the summarize fixture drawing the board as on a terminal
// with a fixed width and a stepping clock.
func liveFixture(t *testing.T, s models.Summarizer, ids ...string) *summarizeFixture {
	t.Helper()
	prev := ancli.UseColor
	ancli.UseColor = false
	t.Cleanup(func() { ancli.UseColor = prev })
	f := newSummarizeFixture(t, s, ids...)
	f.h.forceLive = true
	f.h.boardWidth = 160
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	step := 0
	f.h.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		step++
		return t0.Add(time.Duration(step) * 10 * time.Second)
	}
	return f
}

// TestHandleSummarize_liveBoard pins the terminal display: one row per
// worker on the shared board, every result logged above it, the totals as
// the final footer, and no plain result lines.
func TestHandleSummarize_liveBoard(t *testing.T) {
	s := &fakeSummarizer{fn: func(_ context.Context, req models.SummaryRequest) (models.Summary, error) {
		if req.Chat.ID == "bad" {
			return models.Summary{}, errors.New("boom")
		}
		return fakeLabel(req.Chat.ID), nil
	}}
	f := liveFixture(t, s, "a", "b", "bad")
	f.h.summarizeOptions.model = "test"
	err := f.h.handleSummarize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "1 of 3") {
		t.Fatalf("err = %v, want the failed job reported", err)
	}
	got := f.out.String()
	header := fmt.Sprintf("  %-5s  %-*s  %-*s  %-*s  %-*s  time", "slot", sumConvMax, "conversation", sumStateWidth+2, "state", sumModelWidth, "model", sumTokensWidth, "tokens")
	for _, want := range []string{
		"▸ summarizing  3 conversations since", header,
		"phase  summarizing · 3 conversations · 2 workers · model test",
		"  1/3    ", "  2/3    ", "  3/3    ", "✗ failed", "✓ labelled",
		"✓ a  title-a", "✓ b  title-b", "✗ bad  boom",
		"labelled 2/3 · failed 1", "phase  done · 2 labelled · 1 failed",
		"  summarized 2 of 3 conversations, 1 failed",
		"\x1b[2K", "\x1b[",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in live output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "a: title-a") || strings.Contains(got, "bad: ERROR") {
		t.Fatalf("live mode must not print the plain result lines:\n%s", got)
	}
	if n := strings.Count(got, "title-a"); n != 1 {
		t.Fatalf("the title must appear once, in the log line, got %d:\n%s", n, got)
	}
	if strings.Contains(got, "  1/2    ") || strings.Contains(got, "  2/2    ") {
		t.Fatalf("the position column must show the job index, never the slot position:\n%s", got)
	}
	rows := f.indexRows(t)
	if rows["a"].Title != "title-a" || rows["b"].Title != "title-b" {
		t.Fatalf("index rows = %+v, want both labelled", rows)
	}
}

// TestHandleSummarize_liveBoardNarrow pins the width-aware layout: on an
// 80-column terminal the conversation column shrinks (a UUID is truncated)
// and the time column is still on screen; on a wide one the UUID fits.
func TestHandleSummarize_liveBoardNarrow(t *testing.T) {
	const uuid = "0192a7b3-4c5d-7e6f-8a9b-0c1d2e3f4a5b"
	for _, tc := range []struct {
		width    int
		convCell string
	}{
		{width: 80, convCell: "0192a7b3-4c5d-7e6f-8a…"},
		{width: 140, convCell: uuid},
	} {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			f := liveFixture(t, instantSummarizer(), uuid)
			f.h.boardWidth = tc.width
			f.h.summarizeOptions.workers = 1
			if err := f.h.handleSummarize(context.Background()); err != nil {
				t.Fatalf("handleSummarize: %v", err)
			}
			got := f.out.String()
			if !strings.Contains(got, tc.convCell) {
				t.Fatalf("conversation cell %q missing at width %d:\n%s", tc.convCell, tc.width, got)
			}
			for line := range strings.SplitSeq(got, "\n") {
				// Log lines carry the full id and title and may legitimately
				// hit the terminal edge; table lines must not.
				if strings.Contains(line, "✓ "+uuid) || strings.Contains(line, "✗ "+uuid) {
					continue
				}
				if strings.Contains(line, "…") && !strings.Contains(line, tc.convCell) {
					t.Fatalf("a table line other than the conversation cell was truncated at width %d: %q", tc.width, line)
				}
			}
			if !strings.Contains(got, "tokens  time") {
				t.Fatalf("the time column must stay on screen at width %d:\n%s", tc.width, got)
			}
		})
	}
	if got := summarizeConversationWidth(0); got != summarizeConversationWidth(80) {
		t.Fatalf("an unknown width must take the 80-column fallback, got %d", got)
	}
}

// TestHandleSummarize_liveOnlyOnInteractiveTerminal pins when the board is
// drawn: never under -r, never under -n, never on a non-terminal writer.
func TestHandleSummarize_liveOnlyOnInteractiveTerminal(t *testing.T) {
	f := newSummarizeFixture(t, instantSummarizer(), "a")
	if f.h.summarizeLive() {
		t.Fatal("a buffer is not a terminal: the board must stay off")
	}
	f.h.forceLive = true
	if !f.h.summarizeLive() {
		t.Fatal("forceLive must select the board")
	}
	f.h.forceLive = false
	f.h.raw = true
	oldLive := utils.Live
	t.Cleanup(func() { utils.Live = oldLive })
	utils.Live = true
	if f.h.summarizeLive() {
		t.Fatal("-r must keep the JSON lines, never the board")
	}
	f.h.raw = false
	utils.Live = false
	if f.h.summarizeLive() {
		t.Fatal("-n must keep the plain lines, never the board")
	}
	// The plain printer still runs and labels under the fixture's defaults.
	f.h.summarizeOptions.yes = true
	if err := f.h.handleSummarize(context.Background()); err != nil {
		t.Fatalf("handleSummarize: %v", err)
	}
	if !strings.Contains(f.out.String(), "a: title-a") || strings.Contains(f.out.String(), "▸") {
		t.Fatalf("plain output expected, got %q", f.out.String())
	}
}

// TestBoardSummarizeProgress_footerEstimate pins the footer: the mean wall
// time of finished jobs, times the remaining rounds over the workers.
func TestBoardSummarizeProgress_footerEstimate(t *testing.T) {
	f := liveFixture(t, instantSummarizer())
	f.h.summarizeOptions.workers = 2
	// The board reads the clock while rendering, so the test advances it
	// explicitly instead of stepping on every read.
	var mu sync.Mutex
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	f.h.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return at
	}
	p := newBoardSummarizeProgress(context.Background(), f.h, 5)
	t.Cleanup(p.cancel)
	if got := p.footer(nil); !strings.Contains(got, "labelled 0/5") || !strings.Contains(got, "eta ·") {
		t.Fatalf("footer before any result = %q", got)
	}
	p.started(0, 0, "a")
	mu.Lock()
	at = at.Add(10 * time.Second)
	mu.Unlock()
	p.finished(summarizeResult{id: "a", worker: 0, chat: pub_models.Chat{ID: "a", Title: "t", Queries: []pub_models.QueryCost{{Model: "m", Purpose: "summary", Usage: pub_models.Usage{TotalTokens: 42}}}}})
	got := p.footer(p.b.Rows())
	// One job took 10 s; four remain over two workers → two rounds.
	if !strings.Contains(got, "labelled 1/5") || !strings.Contains(got, "eta ~20s") {
		t.Fatalf("footer after one 10 s job = %q, want eta ~20s", got)
	}
	rows := p.b.Rows()
	if rows[0].Cells[sumColModel] != "m" || rows[0].Cells[sumColTokens] != "42" || rows[0].Mark != board.MarkDone {
		t.Fatalf("finished row = %+v", rows[0])
	}
	p.done(5, 1, 0)
	if !strings.Contains(f.out.String(), "summarized 1 of 5 conversations, 0 failed") {
		t.Fatalf("final footer missing:\n%s", f.out.String())
	}
}

// TestConfirmSummarize_promptWriter pins R3-12: the confirmation prompt
// stays on stdout in plain mode and moves to stderr under -r.
func TestConfirmSummarize_promptWriter(t *testing.T) {
	f := newSummarizeFixture(t, instantSummarizer())
	f.h.input = strings.NewReader("y\n")
	if ok, err := f.h.confirmSummarize(3); err != nil || !ok {
		t.Fatalf("confirmSummarize = %v, %v", ok, err)
	}
	if !strings.Contains(f.out.String(), "Summarize 3 conversations") || f.errOut.Len() != 0 {
		t.Fatalf("plain mode prompts on stdout, got out=%q err=%q", f.out.String(), f.errOut.String())
	}
	f.out.Reset()
	f.h.raw = true
	f.h.input = strings.NewReader("\n")
	if ok, err := f.h.confirmSummarize(3); err != nil || ok {
		t.Fatalf("confirmSummarize = %v, %v", ok, err)
	}
	if f.out.Len() != 0 || !strings.Contains(f.errOut.String(), "Summarize 3 conversations") {
		t.Fatalf("raw mode prompts on stderr, got out=%q err=%q", f.out.String(), f.errOut.String())
	}
}

// TestHandleSummarize_liveBoardColour pins review 3, R3-01: with colour on
// at the fallback width the coloured header keeps its last column and
// every coloured table line ends with a reset.
func TestHandleSummarize_liveBoardColour(t *testing.T) {
	const uuid = "0192a7b3-4c5d-7e6f-8a9b-0c1d2e3f4a5b"
	f := liveFixture(t, instantSummarizer(), uuid)
	ancli.UseColor = true
	f.h.boardWidth = 80
	f.h.summarizeOptions.workers = 1
	if err := f.h.handleSummarize(context.Background()); err != nil {
		t.Fatalf("handleSummarize: %v", err)
	}
	got := f.out.String()
	if !strings.Contains(got, "tokens  time\x1b[0m") {
		t.Fatalf("the coloured header must keep its last column and its reset at width 80:\n%q", got)
	}
	cursor := regexp.MustCompile(`\x1b\[[0-9;]*[A-LN-Za-ln-z]`)
	sgr := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for line := range strings.SplitSeq(got, "\n") {
		line = strings.TrimPrefix(cursor.ReplaceAllString(line, ""), "\r")
		if m := sgr.FindAllString(line, -1); len(m) > 0 && m[len(m)-1] != "\x1b[0m" {
			t.Fatalf("line leaves colour open: %q", line)
		}
		if board.Truncate(line, 80) != line {
			t.Fatalf("line wider than 80 columns: %q", line)
		}
	}
}
