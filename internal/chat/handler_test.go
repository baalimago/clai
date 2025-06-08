package chat

import (
	"context"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
)

func TestFormatChatName(t *testing.T) {
	name := "line1\nline2line2line2line2line2line2"
	got := formatChatName(name)
	// expected trimmed to 25 and newline replaced
	if got != "line1\\nline2line2line2line..." {
		t.Errorf("unexpected output: %q", got)
	}
}

type mockChatQuerier struct{}

func (mockChatQuerier) Query(ctx context.Context) error { return nil }
func (mockChatQuerier) TextQuery(ctx context.Context, c models.Chat) (models.Chat, error) {
	return c, nil
}

func TestChatHandlerListAndFind(t *testing.T) {
	tmp := t.TempDir()
	chats := []models.Chat{
		{ID: "one", Created: time.Now().Add(-time.Hour)},
		{ID: "two", Created: time.Now()},
	}
	for _, c := range chats {
		if err := Save(tmp, c); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	h := &ChatHandler{convDir: tmp, q: mockChatQuerier{}}
	got, err := h.list()
	if err != nil {
		t.Fatalf("list err: %v", err)
	}
	if len(got) != 2 || got[0].ID != "two" {
		t.Fatalf("unexpected list result: %+v", got)
	}

	res, err := h.findChatByID("1 extra words")
	if err != nil {
		t.Fatalf("findChatByID err: %v", err)
	}
	if res.ID != "one" || h.prompt != "extra words" {
		t.Errorf("unexpected chat or prompt: %+v %q", res, h.prompt)
	}
	res, err = h.findChatByID("two")
	if err != nil || res.ID != "two" {
		t.Errorf("find by id failed: %v %+v", err, res)
	}
}
