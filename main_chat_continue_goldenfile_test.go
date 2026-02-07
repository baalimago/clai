package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_CHAT_CONTINUE_obfuscated_preview(t *testing.T) {
	// Desired CLI contract:
	// - `clai ... chat continue <index>` prints messages as:
	//   [#nr r: "<role>" l: 00042]: <msg-preview>
	// - It should NOT enter interactive mode.
	// - It binds the current working directory to the continued chat, so it can be
	//   replied to using -dre.
	// - It prints a notice about the new replyable mechanism.

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("HOME", confDir)
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	convDir := filepath.Join(confDir, "conversations")
	conv := pub_models.Chat{
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      chat.HashIDFromPrompt("hello"),
		Messages: []pub_models.Message{
			{Role: "system", Content: strings.Repeat("a", 200)},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}
	if err := chat.Save(convDir, conv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var status int
	out := testboil.CaptureStdout(t, func(t *testing.T) {
		status = run(strings.Split("-r -cm test c c 0", " "))
	})
	if status != 0 {
		t.Fatalf("expected status code 0, got %v. stdout=%q", status, out)
	}

	// shortened pretty output for the long system message
	testboil.AssertStringContains(t, out, "...")
	testboil.AssertStringContains(t, out, "and 100 more runes")

	// last message is fully printed
	testboil.AssertStringContains(t, out, "world")

	// replyable notice
	testboil.AssertStringContains(t, out, "is now replyable with flag")

	// ensure dirscope binding exists and points to chat
	chatID, err := chat.LoadDirScopeChatID(confDir)
	if err != nil {
		t.Fatalf("LoadDirScopeChatID: %v", err)
	}
	if chatID != conv.ID {
		t.Fatalf("expected dirscope chat id %q got %q", conv.ID, chatID)
	}
}
