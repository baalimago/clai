package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
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
