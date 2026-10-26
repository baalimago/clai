package chat

import (
	"context"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestChatHandlerListAndFind(t *testing.T) {
	tmp := t.TempDir()
	chats := []pub_models.Chat{
		{ID: "one", Created: time.Now().Add(-time.Hour)},
		{ID: "two", Created: time.Now()},
	}
	for _, c := range chats {
		if err := Save(tmp, c); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	h := &ChatHandler{convDir: tmp}
	got, err := h.list()
	if err != nil {
		t.Fatalf("list err: %v", err)
	}
	if len(got) != 2 || got[0].ID != "two" {
		t.Fatalf("unexpected list result: %+v", got)
	}

	res, err := h.findChatByID(context.Background(), "1 extra words", foreignClone)
	if err != nil {
		t.Fatalf("findChatByID err: %v", err)
	}
	if res.ID != "one" || h.prompt != "extra words" {
		t.Errorf("unexpected chat or prompt: %+v %q", res, h.prompt)
	}
	res, err = h.findChatByID(context.Background(), "two", foreignClone)
	if err != nil || res.ID != "two" {
		t.Errorf("find by id failed: %v %+v", err, res)
	}
}
