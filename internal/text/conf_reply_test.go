package text

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestConfigurations_SetupInitialChat_DirReplyModeSkipsPreviousQuery(t *testing.T) {
	confDir := t.TempDir()

	// Populate the previous query with stale content — this MUST be ignored.
	stale := pub_models.Chat{
		Created: time.Now(),
		ID:      "stale-chat",
		Messages: []pub_models.Message{
			{Role: "system", Content: "cwd: /other/dir"},
			{Role: "user", Content: "stale query"},
			{Role: "assistant", Content: "stale response"},
		},
	}
	seedPreviousQuery(t, confDir, stale)

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

	if err := conf.SetupInitialChat(context.Background(), []string{"q", "new prompt"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}

	// Verify the stale previous query was not loaded (no "stale query" message present).
	for _, msg := range conf.InitialChat.Messages {
		if msg.Content == "stale query" || msg.Content == "stale response" {
			t.Fatalf("stale previous query content leaked into InitialChat: %q", msg.Content)
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

func seedPreviousQuery(t *testing.T, confDir string, c pub_models.Chat) {
	t.Helper()
	if err := chat.SaveAsPreviousQuery(context.Background(), confDir, c); err != nil {
		t.Fatalf("SaveAsPreviousQuery(%q): %v", c.ID, err)
	}
}

func TestConfigurations_SetupInitialChat_ReplyModeKeepsPreviousQueryMessages(t *testing.T) {
	confDir := t.TempDir()
	prev := pub_models.Chat{
		Created: time.Now(),
		ID:      "existing-chat",
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "before"},
			{Role: "assistant", Content: "after"},
		},
	}
	seedPreviousQuery(t, confDir, prev)

	conf := Default
	conf.ConfigDir = confDir
	conf.ReplyMode = true

	if err := conf.SetupInitialChat(context.Background(), []string{"q", "new prompt"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}

	if len(conf.InitialChat.Messages) < len(prev.Messages)+1 {
		t.Fatalf("expected previous query messages to remain in reply chat, got %d messages", len(conf.InitialChat.Messages))
	}
	if conf.InitialChat.Messages[0].Content != prev.Messages[0].Content {
		t.Fatalf("expected first reply message content %q, got %q", prev.Messages[0].Content, conf.InitialChat.Messages[0].Content)
	}
}

func TestSetupInitialChat_replyContinuesPreviousConversation(t *testing.T) {
	at := time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		labelled bool
	}{
		{name: "labelled conversation keeps its fields", labelled: true},
		{name: "unlabelled conversation stays unlabelled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			confDir := t.TempDir()
			prev := pub_models.Chat{
				Created:   created,
				ID:        "seed-chat",
				OriginDir: "/origin",
				Messages:  []pub_models.Message{{Role: "user", Content: "before"}, {Role: "assistant", Content: "after"}},
			}
			if tc.labelled {
				prev.Title, prev.Summary, prev.SummaryAt = "T", "S", at
			}
			seedPreviousQuery(t, confDir, prev)

			conf := Default
			conf.ConfigDir = confDir
			conf.ReplyMode = true
			if err := conf.SetupInitialChat(context.Background(), []string{"q", "more"}); err != nil {
				t.Fatalf("SetupInitialChat: %v", err)
			}

			got := conf.InitialChat
			if got.ID != prev.ID {
				t.Fatalf("id = %q, want the previous conversation's %q", got.ID, prev.ID)
			}
			if !got.Created.Equal(created) || got.OriginDir != "/origin" {
				t.Fatalf("created=%v origin=%q, want the previous conversation's", got.Created, got.OriginDir)
			}
			if got.Title != prev.Title || got.Summary != prev.Summary || !got.SummaryAt.Equal(prev.SummaryAt) {
				t.Fatalf("title=%q summary=%q at=%v, want %q %q %v", got.Title, got.Summary, got.SummaryAt, prev.Title, prev.Summary, prev.SummaryAt)
			}
		})
	}
}

func TestSetupInitialChat_replyAdoptsThePreviousConversationWhole(t *testing.T) {
	confDir := t.TempDir()
	usage := &pub_models.Usage{TotalTokens: 7}
	prev := pub_models.Chat{
		Created:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ID:               "forked-chat",
		Source:           "clai",
		SourceID:         "parent-chat",
		Profile:          "work",
		OriginDir:        "/origin",
		GroupKey:         "group",
		Title:            "T",
		Summary:          "S",
		SummaryAt:        time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC),
		TokenUsage:       usage,
		RecentTokenUsage: usage,
		Queries:          []pub_models.QueryCost{{Model: "m"}},
		Messages:         []pub_models.Message{{Role: "user", Content: "before"}, {Role: "assistant", Content: "after"}},
	}
	seedPreviousQuery(t, confDir, prev)

	conf := Default
	conf.ConfigDir = confDir
	conf.ReplyMode = true
	conf.InitialChat.Messages = []pub_models.Message{{Role: "user", Content: "glob"}}
	if err := conf.SetupInitialChat(context.Background(), []string{"q", "more"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}

	got := conf.InitialChat
	for name, pair := range map[string][2]string{
		"Source": {got.Source, "clai"}, "SourceID": {got.SourceID, "parent-chat"},
		"Profile": {got.Profile, "work"}, "OriginDir": {got.OriginDir, "/origin"},
		"GroupKey": {got.GroupKey, "group"}, "Title": {got.Title, "T"}, "ID": {got.ID, "forked-chat"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	if got.TokenUsage == nil || got.RecentTokenUsage == nil || len(got.Queries) != 1 {
		t.Errorf("usage or queries dropped: %+v", got)
	}
	var contents []string
	for _, m := range got.Messages {
		contents = append(contents, m.Content)
	}
	if want := []string{"glob", "before", "after", "more"}; !slices.Equal(contents, want) {
		t.Errorf("messages = %v, want %v", contents, want)
	}
}
