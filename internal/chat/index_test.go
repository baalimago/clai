package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

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
		Queries:    []pub_models.QueryCost{{CostUSD: 1.25}},
	}

	if err := Save(tmp, ch); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	indexPath := filepath.Join(tmp, chatIndexFileName)
	b, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var rows []chatIndexRow
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
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
	if got.FirstUserMessage != "hello world" {
		t.Fatalf("row FirstUserMessage = %q, want %q", got.FirstUserMessage, "hello world")
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
