package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func seedIndexCache(t *testing.T, convDir string, rows []chatIndexRow) []byte {
	t.Helper()
	b, err := json.Marshal(chatIndexCache{Version: chatIndexVersion, Rows: rows})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(chatIndexPath(convDir), b, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	return b
}

func journalChat(id, prompt string) pub_models.Chat {
	return pub_models.Chat{ID: id, Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: prompt}}}
}

// TestUpsertChatIndex_appendsWithoutRewritingTheCache pins the per-query
// cost: an upsert appends one journal line and leaves the cache untouched.
func TestUpsertChatIndex_appendsWithoutRewritingTheCache(t *testing.T) {
	tmp := t.TempDir()
	before := seedIndexCache(t, tmp, []chatIndexRow{{ID: "old", Created: time.Now().Add(-time.Hour), MessageCount: 1}})
	if err := upsertChatIndex(tmp, journalChat("new", "hi")); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}
	after, err := os.ReadFile(chatIndexPath(tmp))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("an upsert must not rewrite the cache")
	}
	if _, err := os.Stat(chatIndexJournalPath(tmp)); err != nil {
		t.Fatalf("journal: %v", err)
	}
	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "new" {
		t.Fatalf("rows = %+v, want the journal row merged, newest first", rows)
	}
}

func TestUpsertChatIndex_latestJournalLineWins(t *testing.T) {
	tmp := t.TempDir()
	seedIndexCache(t, tmp, []chatIndexRow{{ID: "a", Created: time.Now(), Title: "from cache"}})
	first := journalChat("a", "hi")
	first.Title = "first"
	second := first
	second.Title = "second"
	for _, c := range []pub_models.Chat{first, second} {
		if err := upsertChatIndex(tmp, c); err != nil {
			t.Fatalf("upsertChatIndex: %v", err)
		}
	}
	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 1 || rows[0].Title != "second" {
		t.Fatalf("rows = %+v, want one row carrying the latest title", rows)
	}
}

func TestUpsertChatIndexBatch_appendsEveryRow(t *testing.T) {
	tmp := t.TempDir()
	seedIndexCache(t, tmp, nil)
	if err := UpsertChatIndexBatch(tmp, []pub_models.Chat{journalChat("a", "x"), journalChat("b", "y")}); err != nil {
		t.Fatalf("UpsertChatIndexBatch: %v", err)
	}
	rows, err := readChatIndex(tmp)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v; want both", rows, err)
	}
}

func TestUpsertChatIndex_compactsAPastThresholdJournal(t *testing.T) {
	tmp := t.TempDir()
	seedIndexCache(t, tmp, nil)
	prompt := strings.Repeat("x", indexPreviewRunes)
	upserts := 0
	for ; upserts < 10_000; upserts++ {
		if err := upsertChatIndex(tmp, journalChat(fmt.Sprintf("c%05d", upserts), prompt)); err != nil {
			t.Fatalf("upsertChatIndex %d: %v", upserts, err)
		}
		if _, err := os.Stat(chatIndexJournalPath(tmp)); os.IsNotExist(err) {
			upserts++
			break
		}
	}
	if upserts == 10_000 {
		t.Fatal("the journal never compacted")
	}
	b, err := os.ReadFile(chatIndexPath(tmp))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cache chatIndexCache
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatalf("decode cache: %v", err)
	}
	if len(cache.Rows) != upserts {
		t.Fatalf("cache rows = %d, want all %d upserts folded in", len(cache.Rows), upserts)
	}
	if _, err := os.Stat(chatIndexCompactingPath(tmp)); !os.IsNotExist(err) {
		t.Fatalf("compacting journal left behind: %v", err)
	}
}

// TestReadChatIndex_replaysACompactingJournal pins the crash window: a
// journal moved aside but never folded in is still read.
func TestReadChatIndex_replaysACompactingJournal(t *testing.T) {
	tmp := t.TempDir()
	seedIndexCache(t, tmp, nil)
	if err := upsertChatIndex(tmp, journalChat("aside", "x")); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}
	if err := os.Rename(chatIndexJournalPath(tmp), chatIndexCompactingPath(tmp)); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := upsertChatIndex(tmp, journalChat("live", "y")); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}
	rows, err := readChatIndex(tmp)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v; want both journals replayed", rows, err)
	}
}

func TestIsIndexFile(t *testing.T) {
	for name, want := range map[string]bool{
		chatIndexFileName: true, chatIndexJournalName: true, chatIndexCompactingName: true, "abc.json": false,
	} {
		if got := isIndexFile(name); got != want {
			t.Fatalf("isIndexFile(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestReadChatIndex_corruptJournalRebuildsFromFiles(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, journalChat("real", "hi")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(chatIndexJournalPath(tmp), []byte("{broken\n"), 0o644); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	var rows []chatIndexRow
	var err error
	captureStderrQuiet(t, func() { rows, err = readChatIndex(tmp) })
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "real" {
		t.Fatalf("rows = %+v, want the rebuilt row", rows)
	}
	if _, err := os.Stat(chatIndexJournalPath(tmp)); !os.IsNotExist(err) {
		t.Fatalf("journal after rebuild: err = %v, want it gone", err)
	}
}

func TestRebuildChatIndex_ignoresTheJournal(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, journalChat("real", "hi")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Remove(chatIndexPath(tmp)); err != nil {
		t.Fatalf("remove cache: %v", err)
	}
	var rows []chatIndexRow
	var err error
	stderr := captureStderrQuiet(t, func() { rows, err = rebuildChatIndex(tmp, 0, "test") })
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, %v; want only the conversation", rows, err)
	}
	if containsSkip(stderr) {
		t.Fatalf("rebuild treated an index file as a chat: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(tmp, chatIndexJournalName)); !os.IsNotExist(err) {
		t.Fatalf("journal after rebuild: err = %v, want it gone", err)
	}
}

func captureStderrQuiet(t *testing.T, fn func()) string {
	t.Helper()
	return testboil.CaptureStderr(t, func(*testing.T) { fn() })
}

func containsSkip(stderr string) bool {
	return strings.Contains(stderr, "skipping unreadable chat")
}
