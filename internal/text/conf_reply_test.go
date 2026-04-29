package text

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

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
