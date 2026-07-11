package text

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestConfigurations_SetupInitialChat_DirReplyModeSkipsGlobalScope(t *testing.T) {
	confDir := t.TempDir()
	convDir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", convDir, err)
	}

	// Populate globalScope.json with stale content — this MUST be ignored.
	stale := pub_models.Chat{
		Created: time.Now(),
		ID:      "stale-chat",
		Messages: []pub_models.Message{
			{Role: "system", Content: "cwd: /other/dir"},
			{Role: "user", Content: "stale query"},
			{Role: "assistant", Content: "stale response"},
		},
	}
	b, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("Marshal(stale): %v", err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "globalScope.json"), b, 0o644); err != nil {
		t.Fatalf("WriteFile(globalScope.json): %v", err)
	}

	// Simulate dirscope-preloaded InitialChat (same as LoadDirScopedContext would produce).
	dirHead := pub_models.Chat{
		ID:      "chat-dir-head",
		Profile: "work",
		Messages: []pub_models.Message{
			{Role: "system", Content: "cwd: /home/imago/Projects/public/sakfraga"},
			{Role: "user", Content: "dir-scoped first message"},
			{Role: "assistant", Content: "dir-scoped response"},
		},
	}

	conf := Default
	conf.ConfigDir = confDir
	conf.ReplyMode = true
	conf.DirReplyMode = true
	conf.InitialChat = dirHead

	if err := conf.SetupInitialChat([]string{"q", "new prompt"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}

	// Verify the stale globalScope was not loaded (no "stale query" message present).
	for _, msg := range conf.InitialChat.Messages {
		if msg.Content == "stale query" || msg.Content == "stale response" {
			t.Fatalf("stale globalScope content leaked into InitialChat: %q", msg.Content)
		}
	}

	// Verify the dirscope head messages are present.
	if len(conf.InitialChat.Messages) < len(dirHead.Messages)+1 {
		t.Fatalf("expected at least %d messages (dir head + new prompt), got %d", len(dirHead.Messages)+1, len(conf.InitialChat.Messages))
	}
	if conf.InitialChat.Messages[0].Content != dirHead.Messages[0].Content {
		t.Fatalf("expected first message %q, got %q", dirHead.Messages[0].Content, conf.InitialChat.Messages[0].Content)
	}
	if conf.InitialChat.Messages[1].Content != "dir-scoped first message" {
		t.Fatalf("expected 'dir-scoped first message', got %q", conf.InitialChat.Messages[1].Content)
	}

	// DirReplyMode must preserve the dir head's ID and Profile for applyDirReplyChatID.
	if conf.InitialChat.ID != "chat-dir-head" {
		t.Fatalf("expected ID 'chat-dir-head', got %q", conf.InitialChat.ID)
	}
	if conf.InitialChat.Profile != "work" {
		t.Fatalf("expected Profile 'work', got %q", conf.InitialChat.Profile)
	}
}

func TestConfigurations_SetupInitialChat_ReplyModeKeepsPreviousQueryMessages(t *testing.T) {
	confDir := t.TempDir()
	convDir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", convDir, err)
	}

	prev := pub_models.Chat{
		Created: time.Now(),
		ID:      "existing-chat",
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "before"},
			{Role: "assistant", Content: "after"},
		},
	}
	b, err := json.Marshal(prev)
	if err != nil {
		t.Fatalf("Marshal(prev): %v", err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "globalScope.json"), b, 0o644); err != nil {
		t.Fatalf("WriteFile(globalScope.json): %v", err)
	}

	conf := Default
	conf.ConfigDir = confDir
	conf.ReplyMode = true

	if err := conf.SetupInitialChat([]string{"q", "new prompt"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}

	if len(conf.InitialChat.Messages) < len(prev.Messages)+1 {
		t.Fatalf("expected previous query messages to remain in reply chat, got %d messages", len(conf.InitialChat.Messages))
	}
	if conf.InitialChat.Messages[0].Content != prev.Messages[0].Content {
		t.Fatalf("expected first reply message content %q, got %q", prev.Messages[0].Content, conf.InitialChat.Messages[0].Content)
	}
}
