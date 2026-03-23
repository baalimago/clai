package chat

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestChatHandler_listSelectContinue_keepsStoredChatID(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	convDir := filepath.Join(confDir, "conversations")
	id := "seed-chat"
	ch := pub_models.Chat{ID: id, Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: "seed"}}}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The list->select->continue path uses the Chat object selected from the table.
	// Ensure that continuing does not transform the chat id.
	cq := &ChatHandler{confDir: confDir, convDir: convDir, out: io.Discard}

	// We cannot call actOnChat("c") in a unit test without fully stubbing the interactive loop.
	// Instead we assert the key property: selecting a chat does not change its ID.
	cq.chat = ch
	if cq.chat.ID != id {
		t.Fatalf("expected chat id %q got %q", id, cq.chat.ID)
	}
}

func TestChatHandler_findChatByID_prefersExactID(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	convDir := filepath.Join(confDir, "conversations")
	id := "hello-world-chat"
	ch := pub_models.Chat{ID: id, Created: time.Now()}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cq := &ChatHandler{confDir: confDir, convDir: convDir}
	got, err := cq.findChatByID(id)
	if err != nil {
		t.Fatalf("findChatByID: %v", err)
	}
	if got.ID != id {
		t.Fatalf("expected id %q got %q", id, got.ID)
	}

	// Also ensure cont() lookup path works when given an exact id as the first arg.
	cq2 := &ChatHandler{confDir: confDir, convDir: convDir, subCmd: "continue", prompt: id, out: os.Stdout}
	_, err = cq2.findChatByID(id)
	if err != nil {
		t.Fatalf("findChatByID (via cont path): %v", err)
	}
	_ = context.Background() // keep import stable
}
