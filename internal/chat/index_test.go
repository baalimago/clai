package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// TestRebuildChatIndex_ComputesGroupKey verifies that rebuilding the index
// computes GroupKey for conversations that lack it (old chats from before
// the GroupKey feature), while preserving existing GroupKeys.
func TestRebuildChatIndex_ComputesGroupKey(t *testing.T) {
	tmp := t.TempDir()

	// Simulate an old chat saved without GroupKey (the JSON lacks "group_key").
	oldChat := pub_models.Chat{
		ID:       "old-chat",
		Messages: []pub_models.Message{{Role: "user", Content: "fix the auth bug"}},
	}
	// Write directly to disk WITHOUT going through Save(), so GroupKey is not stamped.
	b, err := json.MarshalIndent(oldChat, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(tmp, "old-chat.json"), b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Simulate a new chat saved via Save(), which stamps GroupKey.
	newChat := pub_models.Chat{
		ID:       "new-chat",
		Messages: []pub_models.Message{{Role: "user", Content: "refactor database"}},
	}
	if err := Save(tmp, newChat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Delete the index cache if it exists (Save creates it), then rebuild.
	indexPath := chatIndexPath(tmp)
	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove index: %v", err)
	}

	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}

	// Both rows should have non-empty GroupKey.
	for _, row := range rows {
		if row.GroupKey == "" {
			t.Fatalf("row %q has empty GroupKey after rebuild", row.ID)
		}
	}

	// Verify GroupKey matches the expected hash of the first user message.
	wantOld := ComputeGroupKeyFromText("fix the auth bug")
	wantNew := ComputeGroupKeyFromText("refactor database")
	for _, row := range rows {
		switch row.ID {
		case "old-chat":
			if row.GroupKey != wantOld {
				t.Fatalf("old-chat GroupKey = %q, want %q", row.GroupKey, wantOld)
			}
		case "new-chat":
			if row.GroupKey != wantNew {
				t.Fatalf("new-chat GroupKey = %q, want %q", row.GroupKey, wantNew)
			}
		default:
			t.Fatalf("unexpected row ID: %q", row.ID)
		}
	}
}

func TestSave_UpdatesChatIndex(t *testing.T) {
	tmp := t.TempDir()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := pub_models.Chat{
		ID:      "my_chat",
		Created: created,
		Profile: "prof",
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello world"},
			{Role: "assistant", Content: "reply"},
		},
		TokenUsage: &pub_models.Usage{TotalTokens: 1234},
		Queries:    []pub_models.QueryCost{{CostUSD: 1.25, Model: "openai/gpt-4.1-mini"}},
	}

	if err := Save(tmp, ch); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	indexPath := filepath.Join(tmp, chatIndexFileName)
	b, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var cache chatIndexCache
	if err := json.Unmarshal(b, &cache); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	rows := cache.Rows
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}

	got := rows[0]
	if got.ID != ch.ID {
		t.Fatalf("row ID = %q, want %q", got.ID, ch.ID)
	}
	if !got.Created.Equal(created) {
		t.Fatalf("row Created = %v, want %v", got.Created, created)
	}
	if got.Profile != "prof" {
		t.Fatalf("row Profile = %q, want prof", got.Profile)
	}
	if got.MessageCount != 3 {
		t.Fatalf("row MessageCount = %d, want 3", got.MessageCount)
	}
	if got.TotalTokens != 1234 {
		t.Fatalf("row TotalTokens = %d, want 1234", got.TotalTokens)
	}
	if got.TotalCostUSD != 1.25 {
		t.Fatalf("row TotalCostUSD = %v, want 1.25", got.TotalCostUSD)
	}
	if got.Model != "openai/gpt-4.1-mini" {
		t.Fatalf("row Model = %q, want %q", got.Model, "openai/gpt-4.1-mini")
	}
	if got.FirstUserMessage != "hello world" {
		t.Fatalf("row FirstUserMessage = %q, want %q", got.FirstUserMessage, "hello world")
	}
}

func TestChatIndexRowFromChat_AggregatesSessionTokensFromQueries(t *testing.T) {
	ch := pub_models.Chat{
		ID:      "agg_tokens",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "reply"},
		},
		TokenUsage: &pub_models.Usage{TotalTokens: 8},
		Queries: []pub_models.QueryCost{
			{Usage: pub_models.Usage{TotalTokens: 10}},
			{Usage: pub_models.Usage{TotalTokens: 20}},
		},
	}

	got := chatIndexRowFromChat(ch)
	if got.TotalTokens != 30 {
		t.Fatalf("row TotalTokens = %d, want 30", got.TotalTokens)
	}
}

func TestChatIndexPaginator_ReturnsSortedPages(t *testing.T) {
	tmp := t.TempDir()
	chats := []pub_models.Chat{
		{ID: "old", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "old"}}},
		{ID: "new", Created: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "new"}}},
		{ID: "mid", Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "mid"}}},
	}
	for _, ch := range chats {
		if err := Save(tmp, ch); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	paginator, err := NewChatIndexPaginator(tmp)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator() error = %v", err)
	}

	if got := paginator.Len(); got != 3 {
		t.Fatalf("totalAm() = %d, want 3", got)
	}

	page, err := paginator.Page(0, 2)
	if err != nil {
		t.Fatalf("findPage() error = %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("len(page) = %d, want 2", len(page))
	}
	if page[0].ID != "new" || page[1].ID != "mid" {
		t.Fatalf("page IDs = [%s %s], want [new mid]", page[0].ID, page[1].ID)
	}
}

func TestFindChatByID_IndexLoadsOnlySelectedChat(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, pub_models.Chat{ID: "old", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "old"}}}); err != nil {
		t.Fatalf("Save() old error = %v", err)
	}
	if err := Save(tmp, pub_models.Chat{ID: "new", Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "new"}}}); err != nil {
		t.Fatalf("Save() new error = %v", err)
	}

	h := &ChatHandler{convDir: tmp}
	got, err := h.findChatByID("0 trailing prompt")
	if err != nil {
		t.Fatalf("findChatByID() error = %v", err)
	}
	if got.ID != "new" {
		t.Fatalf("got ID = %q, want new", got.ID)
	}
	if h.prompt != "trailing prompt" {
		t.Fatalf("prompt = %q, want trailing prompt", h.prompt)
	}
}

// TestReadChatIndex_AutoMigratesStaleGroupKeys verifies that when the cached
// index has rows with FirstUserMessage set but GroupKey empty, readChatIndex
// automatically triggers a rebuild to stamp GroupKeys.
func TestReadChatIndex_AutoMigratesStaleGroupKeys(t *testing.T) {
	tmp := t.TempDir()

	// Write a stale index cache: row has FirstUserMessage but no GroupKey.
	staleRows := []chatIndexRow{
		{ID: "a", FirstUserMessage: "fix the auth bug", GroupKey: ""},
		{ID: "b", FirstUserMessage: "fix the auth bug", GroupKey: ""},
		{ID: "c", FirstUserMessage: "refactor", GroupKey: ""},
	}
	b, err := json.Marshal(staleRows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(chatIndexPath(tmp), b, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	// Also write the corresponding chat files (needed for rebuild).
	for _, r := range staleRows {
		ch := pub_models.Chat{
			ID:       r.ID,
			Messages: []pub_models.Message{{Role: "user", Content: r.FirstUserMessage}},
		}
		bb, err := json.MarshalIndent(ch, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		bb = append(bb, '\n')
		if err := os.WriteFile(filepath.Join(tmp, r.ID+".json"), bb, 0o644); err != nil {
			t.Fatalf("write chat file: %v", err)
		}
	}

	// readChatIndex should detect stale cache and rebuild.
	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}

	// All rows should have non-empty GroupKey now.
	for _, r := range rows {
		if r.FirstUserMessage != "" && r.GroupKey == "" {
			t.Fatalf("row %q: expected GroupKey after migration, got empty", r.ID)
		}
	}

	// Verify correct GroupKeys.
	wantFix := ComputeGroupKeyFromText("fix the auth bug")
	wantRefactor := ComputeGroupKeyFromText("refactor")
	for _, r := range rows {
		switch r.ID {
		case "a", "b":
			if r.GroupKey != wantFix {
				t.Fatalf("row %q GroupKey = %q, want %q", r.ID, r.GroupKey, wantFix)
			}
		case "c":
			if r.GroupKey != wantRefactor {
				t.Fatalf("row %q GroupKey = %q, want %q", r.ID, r.GroupKey, wantRefactor)
			}
		}
	}
}

// TestReadChatIndex_CorruptedCacheRecovers verifies that a malformed cache file
// (object that is neither a valid chatIndexCache nor a legacy array) triggers
// an automatic rebuild instead of returning a fatal error.
func TestReadChatIndex_CorruptedCacheRecovers(t *testing.T) {
	tmp := t.TempDir()

	// Write chat files so rebuild has data to work with.
	ch := pub_models.Chat{
		ID:       "a",
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
	}
	bb, err := json.MarshalIndent(ch, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "a.json"), append(bb, '\n'), 0o644); err != nil {
		t.Fatalf("write chat file: %v", err)
	}

	// Write a corrupted cache: an object that is neither a valid
	// chatIndexCache (no "rows" field) nor a legacy array.
	corrupted := []byte(`{"garbage": true}`)
	if err := os.WriteFile(chatIndexPath(tmp), corrupted, 0o644); err != nil {
		t.Fatalf("write corrupted cache: %v", err)
	}

	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex should recover from corrupted cache: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after recovery, got %d", len(rows))
	}
	if rows[0].ID != "a" {
		t.Fatalf("expected row ID 'a', got %q", rows[0].ID)
	}
}

// TestRebuildChatIndex_SkipsUnreadableChatFiles verifies one corrupt/stray file
// in the conversations dir does not permanently break the index rebuild (and
// with it list/search/save) — it is skipped with a warning instead.
func TestRebuildChatIndex_SkipsUnreadableChatFiles(t *testing.T) {
	tmp := t.TempDir()

	good := pub_models.Chat{ID: "good", Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	bb, err := json.MarshalIndent(good, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "good.json"), append(bb, '\n'), 0o644); err != nil {
		t.Fatalf("write chat file: %v", err)
	}
	// A truncated/garbage conversation file, as left by a crash mid-write.
	if err := os.WriteFile(filepath.Join(tmp, "corrupt.json"), []byte(`{"id":"corr`), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	rows, err := rebuildChatIndex(tmp, 0, "test")
	if err != nil {
		t.Fatalf("rebuildChatIndex should skip unreadable files, got: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "good" {
		t.Fatalf("expected only the readable chat indexed, got %+v", rows)
	}
}

func TestNewChatIndexPaginator_RebuildsFromExistingChatFiles(t *testing.T) {
	tmp := t.TempDir()
	chats := []pub_models.Chat{
		{ID: "old", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "old message"}}},
		{ID: "new", Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "new message"}}},
	}
	for _, ch := range chats {
		b, err := json.Marshal(ch)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmp, ch.ID+".json"), b, 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	paginator, err := NewChatIndexPaginator(tmp)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator() error = %v", err)
	}
	if got := paginator.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	page, err := paginator.Page(0, 2)
	if err != nil {
		t.Fatalf("Page() error = %v", err)
	}
	if page[0].ID != "new" || page[1].ID != "old" {
		t.Fatalf("page IDs = [%s %s], want [new old]", page[0].ID, page[1].ID)
	}
}

// TestReadChatIndex_ReadonlySilentAndNoPersist pins the read-only listing
// contract at the index layer: when utils.NoCreateConfig is set, a missing
// cache still rebuilds in memory (so listing works) but must not print the
// "Building cache index" progress chatter and must not attempt to persist the
// cache file.
func TestReadChatIndex_ReadonlySilentAndNoPersist(t *testing.T) {
	tmp := t.TempDir()
	ch := pub_models.Chat{
		ID:       "a",
		Created:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
	}
	b, err := json.Marshal(ch)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "a.json"), b, 0o644); err != nil {
		t.Fatalf("write chat file: %v", err)
	}

	utils.NoCreateConfig = true
	t.Cleanup(func() { utils.NoCreateConfig = false })

	var rows []chatIndexRow
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		rows, err = readChatIndex(tmp)
	})
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "a" {
		t.Fatalf("expected 1 row for chat a, got %+v", rows)
	}
	if strings.Contains(stderr, "Building cache index") {
		t.Fatalf("read-only rebuild must not print progress header, stderr: %q", stderr)
	}
	if strings.Contains(stderr, "failed to persist chat index cache") {
		t.Fatalf("read-only rebuild must not attempt persist, stderr: %q", stderr)
	}
	if _, statErr := os.Stat(chatIndexPath(tmp)); !os.IsNotExist(statErr) {
		t.Fatalf("read-only rebuild must not write chat_index.cache, stat err=%v", statErr)
	}
}

// SkipIndex tests — verify that all index operations become no-ops when
// SkipIndex is true, eliminating I/O and memory overhead for embedded
// consumers.

func TestSkipIndex_ReadChatIndexReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()

	// Write a chat file and an index — readChatIndex should ignore both.
	ch := pub_models.Chat{ID: "a", Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	b, _ := json.Marshal(ch)
	os.WriteFile(filepath.Join(tmp, "a.json"), b, 0o644)
	writeChatIndex(tmp, []chatIndexRow{{ID: "a", FirstUserMessage: "hello"}})

	SkipIndex = true
	t.Cleanup(func() { SkipIndex = false })

	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestSkipIndex_WriteChatIndexNoOp(t *testing.T) {
	tmp := t.TempDir()

	SkipIndex = true
	t.Cleanup(func() { SkipIndex = false })

	rows := []chatIndexRow{{ID: "a", FirstUserMessage: "hello"}}
	if err := writeChatIndex(tmp, rows); err != nil {
		t.Fatalf("writeChatIndex: %v", err)
	}

	// Verify no file was created.
	if _, err := os.Stat(chatIndexPath(tmp)); !os.IsNotExist(err) {
		t.Fatal("chat_index.cache should not exist when SkipIndex is true")
	}
}

func TestSkipIndex_UpsertChatIndexNoOp(t *testing.T) {
	tmp := t.TempDir()

	SkipIndex = true
	t.Cleanup(func() { SkipIndex = false })

	ch := pub_models.Chat{ID: "a", Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	if err := upsertChatIndex(tmp, ch); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}

	// Verify no index file was created.
	if _, err := os.Stat(chatIndexPath(tmp)); !os.IsNotExist(err) {
		t.Fatal("chat_index.cache should not exist when SkipIndex is true")
	}
}

func TestSkipIndex_NewChatIndexPaginatorReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()

	// Write some chat files — paginator should ignore them.
	ch := pub_models.Chat{ID: "a", Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	b, _ := json.Marshal(ch)
	os.WriteFile(filepath.Join(tmp, "a.json"), b, 0o644)

	SkipIndex = true
	t.Cleanup(func() { SkipIndex = false })

	paginator, err := NewChatIndexPaginator(tmp)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	if paginator.Len() != 0 {
		t.Fatalf("expected 0 rows, got %d", paginator.Len())
	}
}

func TestSkipIndex_SaveSkipsIndex(t *testing.T) {
	tmp := t.TempDir()

	SkipIndex = true
	t.Cleanup(func() { SkipIndex = false })

	ch := pub_models.Chat{
		ID:      "my_chat",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "reply"},
		},
	}
	if err := Save(tmp, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Chat file must exist.
	chatPath := filepath.Join(tmp, ch.ID+".json")
	if _, err := os.Stat(chatPath); os.IsNotExist(err) {
		t.Fatal("chat file should exist when SkipIndex is true")
	}

	// Index file must not exist.
	if _, err := os.Stat(chatIndexPath(tmp)); !os.IsNotExist(err) {
		t.Fatal("chat_index.cache should not exist when SkipIndex is true")
	}
}

// TestChatIndexRowFromChat_modelSkipsSummaryRows pins D19: a summarizer row
// appended last never becomes the row's model, while token aggregates keep
// summing every row.
func TestChatIndexRowFromChat_modelSkipsSummaryRows(t *testing.T) {
	ch := pub_models.Chat{
		ID:       "summary_rows",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
		Queries: []pub_models.QueryCost{
			{Model: "main-model", Usage: pub_models.Usage{TotalTokens: 10}},
			{Model: "summary-model", Purpose: "summary", Usage: pub_models.Usage{TotalTokens: 5}},
		},
	}

	got := chatIndexRowFromChat(ch)
	if got.Model != "main-model" {
		t.Fatalf("row Model = %q, want main-model", got.Model)
	}
	if got.TotalTokens != 15 {
		t.Fatalf("row TotalTokens = %d, want 15", got.TotalTokens)
	}

	onlySummary := pub_models.Chat{
		ID:      "only_summary",
		Queries: []pub_models.QueryCost{{Model: "summary-model", Purpose: "summary"}},
	}
	if got := chatIndexRowFromChat(onlySummary); got.Model != "" {
		t.Fatalf("row Model = %q, want empty when only summary rows exist", got.Model)
	}
}

func TestChatIndex_mirrorsTitleSummary(t *testing.T) {
	row := chatIndexRowFromChat(pub_models.Chat{ID: "c", Title: "T", Summary: "S"})
	if row.Title != "T" || row.Summary != "S" {
		t.Fatalf("expected title/summary mirrored, got %+v", row)
	}
}

func TestReadChatIndex_legacyRowsZeroUpdated(t *testing.T) {
	tmp := t.TempDir()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	legacy := `{"version":2,"rows":[{"id":"old","created":"2026-01-02T03:04:05Z","message_count":1}]}`
	if err := os.WriteFile(chatIndexPath(tmp), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "old" {
		t.Fatalf("expected the legacy row untouched, got %+v", rows)
	}
	if !rows[0].Updated.IsZero() {
		t.Fatalf("legacy row must decode with zero Updated, got %v", rows[0].Updated)
	}
	if !rows[0].effectiveUpdated().Equal(created) {
		t.Fatalf("effectiveUpdated = %v, want created %v", rows[0].effectiveUpdated(), created)
	}
}

func TestUpsertChatIndex_stampsUpdated(t *testing.T) {
	tmp := t.TempDir()
	before := time.Now().UTC()
	chat := pub_models.Chat{ID: "c", Created: before.Add(-time.Hour), Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	if err := upsertChatIndex(tmp, chat); err != nil {
		t.Fatalf("upsertChatIndex: %v", err)
	}
	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %+v", rows)
	}
	if rows[0].Updated.Before(before) || rows[0].Updated.After(time.Now()) {
		t.Fatalf("expected Updated stamped at upsert, got %v (before %v)", rows[0].Updated, before)
	}
	if rows[0].Updated.Location() != time.UTC {
		t.Fatalf("expected UTC stamp, got %v", rows[0].Updated.Location())
	}
}

type failingDirEntry struct{ os.DirEntry }

func (failingDirEntry) Info() (os.FileInfo, error) { return nil, os.ErrNotExist }

func TestRebuildChatIndex_updatedFromModTime(t *testing.T) {
	tmp := t.TempDir()
	want := map[string]time.Time{
		"a": time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
		"b": time.Date(2025, 12, 31, 23, 59, 58, 0, time.UTC),
	}
	for id, mtime := range want {
		c := pub_models.Chat{ID: id, Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: id}}}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		p := filepath.Join(tmp, id+".json")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	rows, err := rebuildChatIndex(tmp, 0, "test")
	if err != nil {
		t.Fatalf("rebuildChatIndex: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two rows, got %+v", rows)
	}
	for _, row := range rows {
		if !row.Updated.Equal(want[row.ID]) {
			t.Fatalf("row %q Updated = %v, want mtime %v", row.ID, row.Updated, want[row.ID])
		}
	}
	if got := entryModTime(failingDirEntry{}); !got.IsZero() {
		t.Fatalf("unstat-able entry must yield zero Updated, got %v", got)
	}
}

func TestChatIndexRow_effectiveUpdated(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	if got := (chatIndexRow{Created: created}).effectiveUpdated(); !got.Equal(created) {
		t.Fatalf("zero Updated must fall back to Created, got %v", got)
	}
	if got := (chatIndexRow{Created: created, Updated: updated}).effectiveUpdated(); !got.Equal(updated) {
		t.Fatalf("set Updated must win, got %v", got)
	}
}

func TestSaveWithoutIndex(t *testing.T) {
	tmp := t.TempDir()
	if err := writeChatIndex(tmp, nil); err != nil {
		t.Fatalf("writeChatIndex: %v", err)
	}
	indexBefore, err := os.ReadFile(chatIndexPath(tmp))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	chat := pub_models.Chat{ID: "c", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	if err := SaveWithoutIndex(tmp, chat); err != nil {
		t.Fatalf("SaveWithoutIndex: %v", err)
	}
	got, err := FromPath(filepath.Join(tmp, "c.json"))
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	if got.GroupKey == "" {
		t.Fatal("SaveWithoutIndex must stamp GroupKey like Save")
	}
	indexAfter, err := os.ReadFile(chatIndexPath(tmp))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(indexAfter) != string(indexBefore) {
		t.Fatalf("SaveWithoutIndex must leave the index untouched, got %s", indexAfter)
	}
}

func TestUpsertChatIndexBatch(t *testing.T) {
	tmp := t.TempDir()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := pub_models.Chat{ID: "existing", Created: created, Messages: []pub_models.Message{{Role: "user", Content: "old"}}}
	if err := Save(tmp, existing); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Save(tmp, pub_models.Chat{ID: "untouched", Created: created}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before := time.Now().UTC()
	existing.Title, existing.Summary = "T", "S"
	fresh := pub_models.Chat{ID: "fresh", Created: created, Title: "F"}
	if err := UpsertChatIndexBatch(tmp, []pub_models.Chat{existing, fresh}); err != nil {
		t.Fatalf("UpsertChatIndexBatch: %v", err)
	}
	rows, err := readChatIndex(tmp)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	byID := map[string]chatIndexRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	if len(byID) != 3 {
		t.Fatalf("expected three rows, got %+v", rows)
	}
	if byID["existing"].Title != "T" || byID["existing"].Summary != "S" || byID["fresh"].Title != "F" {
		t.Fatalf("labels not upserted: %+v", byID)
	}
	for _, id := range []string{"existing", "fresh"} {
		if byID[id].Updated.Before(before) {
			t.Fatalf("row %q must be stamped Updated at the batch upsert, got %v", id, byID[id].Updated)
		}
	}
	if byID["untouched"].Updated.After(before) {
		t.Fatalf("untouched row must keep its stamp, got %v", byID["untouched"].Updated)
	}

	t.Run("empty batch writes nothing", func(t *testing.T) {
		indexBefore, err := os.ReadFile(chatIndexPath(tmp))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if err := UpsertChatIndexBatch(tmp, nil); err != nil {
			t.Fatalf("UpsertChatIndexBatch(nil): %v", err)
		}
		indexAfter, err := os.ReadFile(chatIndexPath(tmp))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(indexAfter) != string(indexBefore) {
			t.Fatal("empty batch must not rewrite the index")
		}
	})

	t.Run("skip index", func(t *testing.T) {
		old := SkipIndex
		t.Cleanup(func() { SkipIndex = old })
		SkipIndex = true
		if err := UpsertChatIndexBatch(t.TempDir(), []pub_models.Chat{fresh}); err != nil {
			t.Fatalf("UpsertChatIndexBatch with SkipIndex: %v", err)
		}
	})
}

// TestWriteChatIndex_atomic pins R3-10: the cache is replaced by rename, so
// no temp file remains beside it and the content reads back intact.
func TestWriteChatIndex_atomic(t *testing.T) {
	dir := t.TempDir()
	rows := []chatIndexRow{{ID: "a", FirstUserMessage: "hello", Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}}
	if err := writeChatIndex(dir, rows); err != nil {
		t.Fatalf("writeChatIndex: %v", err)
	}
	// A rename over a read-only cache succeeds; an in-place truncate would not.
	if err := os.Chmod(chatIndexPath(dir), 0o444); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if err := writeChatIndex(dir, rows); err != nil {
		t.Fatalf("second writeChatIndex: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != chatIndexFileName {
			t.Fatalf("unexpected file %q next to the cache", e.Name())
		}
	}
	got, err := readChatIndex(dir)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" || got[0].FirstUserMessage != "hello" {
		t.Fatalf("rows = %+v, want the written row", got)
	}
}
