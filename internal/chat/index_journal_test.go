package chat

import (
	"bytes"
	"encoding/json"
	"errors"
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

// seedJournalPastCompaction writes a journal already over the compaction
// threshold in one go, so the next save is the one that must compact.
func seedJournalPastCompaction(t *testing.T, convDir string) int {
	t.Helper()
	var buf bytes.Buffer
	n := 0
	for buf.Len() < chatIndexJournalCompactBytes {
		row := chatIndexRowFromChat(journalChat(fmt.Sprintf("seed%05d", n), strings.Repeat("x", indexPreviewRunes)))
		row.Updated = time.Now().UTC()
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("marshal row %d: %v", n, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
		n++
	}
	if err := os.WriteFile(chatIndexJournalPath(convDir), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	return n
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
	seeded := seedJournalPastCompaction(t, tmp)
	if err := upsertChatIndex(tmp, journalChat("last", "x")); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}
	if _, err := os.Stat(chatIndexJournalPath(tmp)); !os.IsNotExist(err) {
		t.Fatalf("journal after a save past the threshold: %v, want it folded into the cache", err)
	}
	b, err := os.ReadFile(chatIndexPath(tmp))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cache chatIndexCache
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatalf("decode cache: %v", err)
	}
	if len(cache.Rows) != seeded+1 {
		t.Fatalf("cache rows = %d, want all %d saves folded in", len(cache.Rows), seeded+1)
	}
}

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

// TestCompactIndexJournal_recoversALeftoverCompactingJournal pins the crash
// window: a compaction that died after moving the journal aside must not
// have its rows overwritten by the next compaction's move.
func TestCompactIndexJournal_recoversALeftoverCompactingJournal(t *testing.T) {
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
	if err := compactIndexJournal(tmp); err != nil {
		t.Fatalf("compactIndexJournal: %v", err)
	}
	for _, p := range []string{chatIndexCompactingPath(tmp), chatIndexJournalPath(tmp), chatIndexCompactLockPath(tmp)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s left behind: %v", p, err)
		}
	}
	b, err := os.ReadFile(chatIndexPath(tmp))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cache chatIndexCache
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatalf("decode cache: %v", err)
	}
	if len(cache.Rows) != 2 {
		t.Fatalf("cache rows = %+v, want both the recovered and the live row", cache.Rows)
	}
}

func TestCompactIndexJournal_heldLockLeavesTheJournal(t *testing.T) {
	tmp := t.TempDir()
	seedIndexCache(t, tmp, nil)
	if err := upsertChatIndex(tmp, journalChat("live", "y")); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}
	if err := os.WriteFile(chatIndexCompactLockPath(tmp), nil, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := compactIndexJournal(tmp); !errors.Is(err, errCompactionHeld) {
		t.Fatalf("compactIndexJournal = %v, want errCompactionHeld", err)
	}
	if _, err := os.Stat(chatIndexJournalPath(tmp)); err != nil {
		t.Fatalf("journal must stay for the lock holder: %v", err)
	}
}

func TestCompactIndexJournal_breaksAStaleLock(t *testing.T) {
	tmp := t.TempDir()
	seedIndexCache(t, tmp, nil)
	if err := upsertChatIndex(tmp, journalChat("live", "y")); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}
	lock := chatIndexCompactLockPath(tmp)
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	old := time.Now().Add(-2 * chatIndexCompactLockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if err := compactIndexJournal(tmp); err != nil {
		t.Fatalf("compactIndexJournal: %v", err)
	}
	if _, err := os.Stat(chatIndexJournalPath(tmp)); !os.IsNotExist(err) {
		t.Fatalf("journal not compacted: %v", err)
	}
}

func TestAppendIndexJournal_heldCompactionLockStillSaves(t *testing.T) {
	tmp := t.TempDir()
	seedIndexCache(t, tmp, nil)
	if err := os.WriteFile(chatIndexCompactLockPath(tmp), nil, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	seeded := seedJournalPastCompaction(t, tmp)
	for i := range 3 {
		if err := upsertChatIndex(tmp, journalChat(fmt.Sprintf("held%d", i), "x")); err != nil {
			t.Fatalf("upsertChatIndex %d under a held lock: %v", i, err)
		}
	}
	rows, err := readChatIndex(tmp)
	if err != nil || len(rows) != seeded+3 {
		t.Fatalf("rows = %d, %v; want every save readable", len(rows), err)
	}
}

func TestIsIndexFile(t *testing.T) {
	for name, want := range map[string]bool{
		chatIndexFileName: true, chatIndexJournalName: true, chatIndexCompactingName: true, chatIndexCompactLockName: true, "abc.json": false,
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
