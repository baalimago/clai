package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	stdsync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func manyRows(source string, n, messages int) ([]vendors.SourceRow, map[string]pub_models.Chat) {
	rows := make([]vendors.SourceRow, 0, n)
	chats := map[string]pub_models.Chat{}
	for i := range n {
		id := fmt.Sprintf("s%03d", i)
		rows = append(rows, vendors.SourceRow{Source: source, SourceID: id, MessageCount: messages})
		chats[id] = pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: id}}}
	}
	return rows, chats
}

func manifestOf(t *testing.T, convDir string) map[string]int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(convDir, "imported", exportManifest))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]int{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPendingForeign_listsWhatExportWouldWriteAndWritesNothing(t *testing.T) {
	_, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	raw := filepath.Join(t.TempDir(), "c.jsonl")
	if err := os.WriteFile(raw, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int64
	reader := exportReader{
		name: "codex",
		rows: []vendors.SourceRow{
			{Source: "codex", SourceID: "a", MessageCount: 1},
			{Source: "codex", SourceID: "b", MessageCount: 1},
		},
		chats: map[string]pub_models.Chat{
			"a": {Messages: []pub_models.Message{{Role: "user", Content: "a"}}},
			"b": {Messages: []pub_models.Message{{Role: "user", Content: "b"}}},
			"c": {Messages: []pub_models.Message{{Role: "user", Content: "c"}}},
		},
		reads: &reads,
	}
	defer useTestSourceReaders([]vendors.SourceReader{reader})()
	if err := ExportForeign(context.Background(), confDir, nil, nil); err != nil {
		t.Fatal(err)
	}
	reader.rows = []vendors.SourceRow{
		{Source: "codex", SourceID: "a", MessageCount: 1},
		{Source: "codex", SourceID: "b", MessageCount: 3},
		{Source: "codex", SourceID: "c", MessageCount: 2, RawPath: raw},
	}
	defer useTestSourceReaders([]vendors.SourceReader{reader})()
	before := reads.Load()

	got, err := PendingForeign(context.Background(), confDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []ForeignConversation{
		{Source: "codex", SourceID: "b", Change: ForeignGrown, Messages: 3},
		{Source: "codex", SourceID: "c", Change: ForeignNew, Messages: 2, RawBytes: 10},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pending = %+v\nwant %+v", got, want)
	}
	if reads.Load() != before {
		t.Fatal("the preview read a conversation")
	}
	if _, err := os.Stat(importedPath(convDir, "codex", "c")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the preview exported a conversation")
	}
}

// gateReader's reads wait for a second one to start, so it finishes only
// when the export runs at least two at once.
type gateReader struct {
	exportReader
	inflight *atomic.Int64
	both     chan struct{}
	once     *stdsync.Once
}

func (r gateReader) Read(ctx context.Context, cache vendors.SourceCache, id string) (pub_models.Chat, error) {
	if r.inflight.Add(1) >= 2 {
		r.once.Do(func() { close(r.both) })
	}
	defer r.inflight.Add(-1)
	select {
	case <-r.both:
	case <-time.After(5 * time.Second):
		return pub_models.Chat{}, errors.New("read alone: the export is not parallel")
	}
	return r.exportReader.Read(ctx, cache, id)
}

func TestExportForeign_runsInParallelAndReportsProgress(t *testing.T) {
	_, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	rows, chats := manyRows("claude-code", 150, 1)
	var reads atomic.Int64
	reader := gateReader{
		exportReader: exportReader{name: "claude-code", rows: rows, chats: chats, reads: &reads},
		inflight:     &atomic.Int64{},
		both:         make(chan struct{}),
		once:         &stdsync.Once{},
	}
	defer useTestSourceReaders([]vendors.SourceReader{reader})()
	var calls []string
	checkpointed := -1
	err := ExportForeign(context.Background(), confDir, nil, func(done, total int) {
		calls = append(calls, fmt.Sprintf("%d/%d", done, total))
		if done == exportCheckpoint {
			checkpointed = len(manifestOf(t, convDir))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 151 || calls[0] != "0/150" || calls[150] != "150/150" {
		t.Fatalf("progress calls %d: first %v last %v", len(calls), calls[0], calls[len(calls)-1])
	}
	if checkpointed != exportCheckpoint {
		t.Fatalf("the manifest held %d entries after %d exports", checkpointed, exportCheckpoint)
	}
	if n := len(manifestOf(t, convDir)); n != 150 {
		t.Fatalf("manifest has %d entries", n)
	}
}

// cancelReader cancels the run once it has read `after` conversations.
type cancelReader struct {
	exportReader
	after  int64
	cancel context.CancelFunc
}

func (r cancelReader) Read(ctx context.Context, cache vendors.SourceCache, id string) (pub_models.Chat, error) {
	c, err := r.exportReader.Read(ctx, cache, id)
	if r.reads.Load() >= r.after {
		r.cancel()
	}
	return c, err
}

func TestExportForeign_anInterruptedRunResumesWhereItStopped(t *testing.T) {
	_, confDir := newTestHandler(t)
	rows, chats := manyRows("codex", 60, 1)
	var reads atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := cancelReader{
		exportReader: exportReader{name: "codex", rows: rows, chats: chats, reads: &reads},
		after:        10,
		cancel:       cancel,
	}
	defer useTestSourceReaders([]vendors.SourceReader{reader})()
	err := ExportForeign(ctx, confDir, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	first := reads.Load()
	if first >= 60 {
		t.Fatalf("the cancelled run read all %d", first)
	}
	reader.after = 1 << 30
	defer useTestSourceReaders([]vendors.SourceReader{reader})()
	if err := ExportForeign(context.Background(), confDir, nil, nil); err != nil {
		t.Fatal(err)
	}
	if total := reads.Load(); total != 60 {
		t.Fatalf("read %d conversations over both runs, want each of the 60 once (first run %d)", total, first)
	}
}

func TestExportForeign_namesEveryFailure(t *testing.T) {
	_, confDir := newTestHandler(t)
	var reads atomic.Int64
	reader := exportReader{
		name:  "gemini",
		rows:  []vendors.SourceRow{{Source: "gemini", SourceID: "x", MessageCount: 1}, {Source: "gemini", SourceID: "y", MessageCount: 1}},
		chats: map[string]pub_models.Chat{},
		reads: &reads,
	}
	defer useTestSourceReaders([]vendors.SourceReader{reader})()
	err := ExportForeign(context.Background(), confDir, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "gemini x") || !strings.Contains(err.Error(), "gemini y") {
		t.Fatalf("err = %v, want both failures named", err)
	}
}
