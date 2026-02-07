package chat

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type stubChatQuerier struct{}

func (stubChatQuerier) Query(ctx context.Context) error { return nil }

func (stubChatQuerier) TextQuery(ctx context.Context, ch pub_models.Chat) (pub_models.Chat, error) {
	return ch, nil
}

func TestChatContinue_printsObfuscatedSummary_withPaddingAndPreview(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	convDir := filepath.Join(confDir, "conversations")

	// Need > 6 messages to exercise the "old messages" obfuscated branch.
	msgs := []pub_models.Message{
		{Role: "system", Content: strings.Repeat("a", 200)},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
		{Role: "user", Content: "m3"},
		{Role: "assistant", Content: "m4"},
		{Role: "user", Content: "m5"},
		{Role: "assistant", Content: "m6"},
	}

	conv := pub_models.Chat{
		Created:  time.Now(),
		ID:       HashIDFromPrompt("hello"),
		Messages: msgs,
	}
	if err := Save(convDir, conv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cq := &ChatHandler{
		q:       stubChatQuerier{},
		subCmd:  "continue",
		prompt:  "0",
		confDir: confDir,
		convDir: convDir,
		out:     io.Discard,
	}

	ch, err := cq.findChatByID(cq.prompt)
	if err != nil {
		t.Fatalf("findChatByID: %v", err)
	}

	var b strings.Builder
	cq.out = &b
	if err := cq.printChat(ch); err != nil {
		t.Fatalf("printChat: %v", err)
	}

	got := b.String()
	// First message is in the "old messages" section now; ensure it prints as an obfuscated line.
	if !strings.Contains(got, "[#0") || !strings.Contains(got, "r:") || !strings.Contains(got, "l:") || !strings.Contains(got, "]: ") {
		t.Fatalf("expected obfuscated output format, got: %q", got)
	}
	// Length is padded to 5 digits like 00005.
	if !strings.Contains(got, "l: 00200") {
		t.Fatalf("expected zero-padded len field for first message, got: %q", got)
	}
	// Pretty printed shortened output includes the ... marker.
	if !strings.Contains(got, "...") {
		t.Fatalf("expected shortened output for non-last messages, got: %q", got)
	}
	// Last message preview includes the actual string.
	if !strings.Contains(got, "m6") {
		t.Fatalf("expected last message preview to include content, got: %q", got)
	}
}

func TestChatContinue_highMessageCount_obfuscatesOldMessages_andPrettyPrintsLastSix(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	convDir := filepath.Join(confDir, "conversations")

	msgs := make([]pub_models.Message, 0, 50)
	for i := 0; i < 49; i++ {
		msgs = append(msgs, pub_models.Message{Role: "user", Content: strings.Repeat("x", 10)})
	}
	// Make last message unique to assert it is included.
	msgs = append(msgs, pub_models.Message{Role: "assistant", Content: "LAST"})

	conv := pub_models.Chat{
		Created:  time.Now(),
		ID:       HashIDFromPrompt("high-msg-count"),
		Messages: msgs,
	}
	if err := Save(convDir, conv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cq := &ChatHandler{
		q:       stubChatQuerier{},
		subCmd:  "continue",
		prompt:  "0",
		confDir: confDir,
		convDir: convDir,
		out:     io.Discard,
	}

	ch, err := cq.findChatByID(cq.prompt)
	if err != nil {
		t.Fatalf("findChatByID: %v", err)
	}

	var b strings.Builder
	cq.out = &b
	if err := cq.printChat(ch); err != nil {
		t.Fatalf("printChat: %v", err)
	}

	got := b.String()

	// With 50 messages, prettyStart = 44, so message #0 must be obfuscated.
	if !strings.Contains(got, "[#0") {
		t.Fatalf("expected early message to be obfuscated, got: %q", got)
	}
	// But message #44 is the first in the last-six block and should be pretty-printed (no obfuscated prefix).
	if strings.Contains(got, "[#44") {
		t.Fatalf("expected message #44 to be pretty-printed (no obfuscated prefix), got: %q", got)
	}
	// Last message body should be present since it's fully printed.
	if !strings.Contains(got, "LAST") {
		t.Fatalf("expected last message content to be present, got: %q", got)
	}
}
