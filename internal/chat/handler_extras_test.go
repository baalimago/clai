package chat

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestActOnSubCmd_DebugQueryAndUnknown(t *testing.T) {
	cq, _ := newTestHandler(t)
	cq.debug = true
	cq.subCmd = "query"
	testboil.CaptureStdout(t, func(t *testing.T) {
		err := cq.actOnSubCmd(context.Background())
		if err == nil || !strings.Contains(err.Error(), "not yet implemented") {
			t.Fatalf("err = %v, want the not-implemented error", err)
		}

		cq.subCmd = "nonsense"
		if err := cq.actOnSubCmd(context.Background()); err == nil {
			t.Fatal("expected an error for an unknown subcommand")
		}
	})
}

func TestPrintChat_WriteFailure(t *testing.T) {
	cq := &ChatHandler{out: &failWriteAt{remaining: 0}}
	chat := pub_models.Chat{ID: "x", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	if err := cq.printChat(chat); err == nil {
		t.Fatal("expected the obfuscated printer to fail")
	}
}

func TestFindChatByID_NumericIndexFallback(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	chat := pub_models.Chat{
		ID:       "abc",
		Created:  time.Now(),
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	old := SkipIndex
	SkipIndex = true
	t.Cleanup(func() { SkipIndex = old })

	got, err := cq.findChatByID("0 the rest")
	if err != nil {
		t.Fatalf("findChatByID: %v", err)
	}
	if got.ID != "abc" {
		t.Fatalf("got chat %q, want abc", got.ID)
	}
	if cq.prompt != "the rest" {
		t.Fatalf("prompt = %q, want the reassembled tokens", cq.prompt)
	}

	if _, err := cq.findChatByID("5"); err == nil {
		t.Fatal("expected an out-of-range error")
	}
}

func TestFindChatByID_IndexReadFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cq := &ChatHandler{convDir: filepath.Join(blocker, "conversations")}
	if _, err := cq.findChatByID("0"); err == nil {
		t.Fatal("expected an index read failure")
	}
}

func TestFindChatByID_IDLoadFailureIsNotNotFound(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cq := &ChatHandler{convDir: filepath.Join(blocker, "conversations")}
	_, err := cq.findChatByID("some-id")
	if err == nil || !strings.Contains(err.Error(), `load chat by id "some-id"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestCont_ProfileStampingAndPrintFailure(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	stored := pub_models.Chat{
		ID:       "with-profile",
		Created:  time.Now(),
		Profile:  "stored-profile",
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	}
	if err := Save(convDir, stored); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cq.profile = "cli-profile"
	cq.prompt = "with-profile"
	cq.out = io.Discard
	if err := cq.cont(context.Background()); err != nil {
		t.Fatalf("cont: %v", err)
	}
	if cq.profile != "stored-profile" {
		t.Fatalf("profile = %q, want the stored profile to win", cq.profile)
	}

	// No stored profile: the -p override is stamped onto the in-memory
	// conversation before printing (cont does not persist).
	plain := pub_models.Chat{
		ID:       "no-profile",
		Created:  time.Now(),
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	}
	if err := Save(convDir, plain); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cq.prompt = "no-profile"
	cq.profile = "cli-profile"
	if err := cq.cont(context.Background()); err != nil {
		t.Fatalf("cont: %v", err)
	}
	if cq.profile != "cli-profile" {
		t.Fatalf("profile = %q, want the CLI override kept", cq.profile)
	}
}

func TestCont_DebugAndPrintFailure(t *testing.T) {
	t.Setenv("DEBUG", "1")
	cq, confDir := newTestHandler(t)
	stored := pub_models.Chat{
		ID:       "abc",
		Created:  time.Now(),
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	}
	if err := Save(conversationsDir(confDir), stored); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cq.prompt = "abc"
	cq.out = &failWriteAt{remaining: 0}
	err := cq.cont(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to print chat") {
		t.Fatalf("err = %v", err)
	}
}

func TestCont_ListChatsFailurePropagates(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cq := &ChatHandler{convDir: filepath.Join(blocker, "conversations"), out: io.Discard}
	cq.prompt = "not-an-index"
	err := cq.cont(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestDeleteFromPrompt(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	stored := pub_models.Chat{
		ID:       "doomed",
		Created:  time.Now(),
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	}
	if err := Save(convDir, stored); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cq.prompt = "doomed"
	cq.out = io.Discard
	if err := cq.deleteFromPrompt(); err != nil {
		t.Fatalf("deleteFromPrompt: %v", err)
	}
	if _, err := os.Stat(conversationPathFromDir(convDir, "doomed")); !os.IsNotExist(err) {
		t.Fatalf("expected the conversation removed, stat err=%v", err)
	}

	cq.prompt = "doomed"
	if err := cq.deleteFromPrompt(); err == nil {
		t.Fatal("expected an error deleting a missing chat")
	}
}

func TestNew_NilOutAndMacroInput(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	ch, err := New(confDir, "list 0 b", "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ch.out == nil {
		t.Fatal("expected a default output writer")
	}
	if ch.input == nil {
		t.Fatal("expected the macro input reader to be wired")
	}

	if _, err := New("", "list", "", false, io.Discard); err == nil {
		t.Fatal("expected an error without a config dir")
	}
}
