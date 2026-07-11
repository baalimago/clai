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

func TestPrintChatObfuscated_NO_COLOR_disablesAllColor(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}

	t.Setenv("NO_COLOR", "true")

	ch := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: strings.Repeat("a", 200)},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
		{Role: "user", Content: "m3"},
		{Role: "assistant", Content: "m4"},
		{Role: "user", Content: "m5"},
		{Role: "assistant", Content: "m6"},
	}}

	var b strings.Builder
	if err := printChatObfuscated(&b, ch, false); err != nil {
		t.Fatalf("printChatObfuscated: %v", err)
	}
	got := b.String()
	if strings.Contains(got, "\u001b[") {
		t.Fatalf("expected no ANSI escapes when NO_COLOR=true, got: %q", got)
	}
}

func TestPrintChatObfuscated_coloriziesPrefix(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}

	// Need > minForGap (11) messages to exercise the bridge obfuscation section.
	msgs := make([]pub_models.Message, 15)
	msgs[0] = pub_models.Message{Role: "system", Content: strings.Repeat("a", 200)}
	msgs[1] = pub_models.Message{Role: "user", Content: "hello"}
	for i := 2; i < 14; i++ {
		msgs[i] = pub_models.Message{Role: "assistant", Content: "middle"}
	}
	msgs[14] = pub_models.Message{Role: "user", Content: "last"}

	ch := pub_models.Chat{Messages: msgs}

	var b strings.Builder
	if err := printChatObfuscated(&b, ch, false); err != nil {
		t.Fatalf("printChatObfuscated: %v", err)
	}
	got := b.String()
	// The bridge section should contain colored obfuscated prefix.
	if !strings.Contains(got, utils.ThemePrimaryColor()+"[") {
		t.Fatalf("expected prefix to be colored, got: %q", got)
	}
}

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

	// Need > minForGap (11) messages to exercise the bridge obfuscation.
	msgs := make([]pub_models.Message, 15)
	msgs[0] = pub_models.Message{Role: "system", Content: strings.Repeat("a", 200)}
	msgs[1] = pub_models.Message{Role: "user", Content: "hello"}
	msgs[2] = pub_models.Message{Role: "assistant", Content: "world"}
	msgs[3] = pub_models.Message{Role: "user", Content: "m3"}
	for i := 4; i < 14; i++ {
		msgs[i] = pub_models.Message{Role: "assistant", Content: strings.Repeat("x", 10)}
	}
	msgs[14] = pub_models.Message{Role: "assistant", Content: "LAST_MSG"}

	conv := pub_models.Chat{
		Created:  time.Now(),
		ID:       "chat-hello",
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
	// Bridge section should contain obfuscated lines starting after head (index 5).
	if !strings.Contains(got, "[#5") || !strings.Contains(got, "r:") || !strings.Contains(got, "l:") || !strings.Contains(got, "]: ") {
		t.Fatalf("expected obfuscated output format in bridge, got: %q", got)
	}
	// The gap label should indicate 3 hidden entries.
	if !strings.Contains(got, "and 3 more entries") {
		t.Fatalf("expected gap label with count 3, got: %q", got)
	}
	// Last message should be fully visible.
	if !strings.Contains(got, "LAST_MSG") {
		t.Fatalf("expected last message to be present, got: %q", got)
	}
}

func TestChatContinue_highMessageCount_obfuscatesOldMessages_andPrettyPrintsLastSix(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	convDir := filepath.Join(confDir, "conversations")

	msgs := make([]pub_models.Message, 0, 50)
	for range 49 {
		msgs = append(msgs, pub_models.Message{Role: "user", Content: strings.Repeat("x", 10)})
	}
	// Make last message unique to assert it is included.
	msgs = append(msgs, pub_models.Message{Role: "assistant", Content: "LAST"})

	conv := pub_models.Chat{
		Created:  time.Now(),
		ID:       "chat-high-msg-count",
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

	// First user message (index 0) is now fully pretty-printed with [#0] prefix.
	// The obfuscated format has "r:" after the index; pretty-print has "]".
	if !strings.Contains(got, "[#0  ]") {
		t.Fatalf("expected first user message with pretty-print prefix [#0], got: %q", got)
	}
	// Bridge section should contain obfuscated entries (with "r:" pattern).
	if !strings.Contains(got, "[#4   r:") {
		t.Fatalf("expected bridge obfuscated entries, got: %q", got)
	}
	// Gap label should appear.
	if !strings.Contains(got, "more entries") {
		t.Fatalf("expected gap label, got: %q", got)
	}
	// Tail should show pretty-print prefix (with "]"), not obfuscated format (with "r:").
	if strings.Contains(got, "[#46   r:") || strings.Contains(got, "[#47   r:") || strings.Contains(got, "[#48   r:") {
		t.Fatalf("expected tail messages to be pretty-printed (no obfuscated prefix), got: %q", got)
	}
	// Last message body should be present since it's fully printed.
	if !strings.Contains(got, "LAST") {
		t.Fatalf("expected last message content to be present, got: %q", got)
	}
}

// TestPrintChatObfuscated_RendersToolCallTurns guards the regression where
// assistant tool-call turns (persisted with empty Content, only ToolCalls) render
// as blank blocks on `clai chat continue`. They must show the call's pretty text.
func TestPrintChatObfuscated_RendersToolCallTurns(t *testing.T) {
	t.Parallel()

	chat := pub_models.Chat{
		ID: "c1",
		Messages: []pub_models.Message{
			{Role: "user", Content: "list the files"},
			{Role: "assistant", ToolCalls: []pub_models.Call{{Name: "ls", Inputs: &pub_models.Input{"dir": "/tmp"}}}},
			{Role: "tool", Content: "a.go\nb.go", ToolCallID: "call_1"},
		},
	}

	var b strings.Builder
	if err := printChatObfuscated(&b, chat, true); err != nil {
		t.Fatalf("printChatObfuscated: %v", err)
	}
	if !strings.Contains(b.String(), "Call: 'ls'") {
		t.Fatalf("expected the tool call to be rendered, got:\n%s", b.String())
	}
}

// TestPrintChatObfuscated_RendersReasoningContentInOldMessages verifies that
// reasoning content is included in the display for messages in the middle bridge.
func TestPrintChatObfuscated_RendersReasoningContentInOldMessages(t *testing.T) {
	t.Parallel()

	msgs := []pub_models.Message{
		{Role: "user", Content: "initial question"},
		{Role: "assistant", Content: "answer 1"},
		{Role: "user", Content: "follow up 1"},
		{Role: "assistant", ReasoningContent: "Let me think about this carefully.", Content: "answer 2"},
		{Role: "user", Content: "follow up 2"},
		{Role: "assistant", Content: "answer 3"},
		{Role: "user", Content: "follow up 3"},
		{Role: "assistant", Content: "answer 4"},
		{Role: "user", Content: "follow up 4"},
		{Role: "assistant", Content: "answer 5"},
		{Role: "user", Content: "follow up 5"},
		{Role: "assistant", Content: "answer 6"},
		{Role: "user", Content: "follow up 6"},
		{Role: "assistant", Content: "answer 7"},
	}

	var b strings.Builder
	if err := printChatObfuscated(&b, pub_models.Chat{ID: "c3", Messages: msgs}, true); err != nil {
		t.Fatalf("printChatObfuscated: %v", err)
	}
	got := b.String()
	if !strings.Contains(got, "[thinking]") {
		t.Fatalf("expected reasoning content to be displayed, got:\n%s", got)
	}
	if !strings.Contains(got, "Let me think about this carefully") {
		t.Fatalf("expected reasoning text to be visible, got:\n%s", got)
	}
}

// TestPrintChatObfuscated_NewFormat_LongChat verifies the new display layout:
// first user full, 3 head truncated, 3 obfuscated bridge + gap label,
// 3 tail truncated, last full.
func TestPrintChatObfuscated_NewFormat_LongChat(t *testing.T) {
	// Not parallel: shares global theme state.

	msgs := make([]pub_models.Message, 20)
	msgs[0] = pub_models.Message{Role: "user", Content: "FIRST_USER_MSG"}
	msgs[1] = pub_models.Message{Role: "assistant", Content: "response 1"}
	msgs[2] = pub_models.Message{Role: "user", Content: "question 2"}
	msgs[3] = pub_models.Message{Role: "assistant", Content: "response 2"}
	for i := 4; i < 16; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		msgs[i] = pub_models.Message{Role: role, Content: strings.Repeat("x", 10)}
	}
	msgs[16] = pub_models.Message{Role: "assistant", Content: "TAIL_MSG_1"}
	msgs[17] = pub_models.Message{Role: "user", Content: "TAIL_MSG_2"}
	msgs[18] = pub_models.Message{Role: "assistant", Content: "TAIL_MSG_3"}
	msgs[19] = pub_models.Message{Role: "assistant", Content: "LAST_MSG_FULL"}

	var b strings.Builder
	if err := printChatObfuscated(&b, pub_models.Chat{ID: "c-long", Messages: msgs}, true); err != nil {
		t.Fatalf("printChatObfuscated: %v", err)
	}
	got := b.String()

	// First user message must be fully visible (not obfuscated).
	if !strings.Contains(got, "FIRST_USER_MSG") {
		t.Fatalf("expected first user message to be fully visible, got:\n%s", got)
	}

	// Head truncated messages (indices 1-3) should appear and be shortened.
	if !strings.Contains(got, "response 1") {
		t.Fatalf("expected head truncated message to appear, got:\n%s", got)
	}

	// Gap label must appear.
	if !strings.Contains(got, "and") || !strings.Contains(got, "more") || !strings.Contains(got, "entries") {
		t.Fatalf("expected '... and N more entries' gap label, got:\n%s", got)
	}

	// Tail messages should appear.
	if !strings.Contains(got, "TAIL_MSG_1") {
		t.Fatalf("expected tail message 1, got:\n%s", got)
	}
	if !strings.Contains(got, "TAIL_MSG_2") {
		t.Fatalf("expected tail message 2, got:\n%s", got)
	}
	if !strings.Contains(got, "TAIL_MSG_3") {
		t.Fatalf("expected tail message 3, got:\n%s", got)
	}

	// Last message must be fully visible.
	if !strings.Contains(got, "LAST_MSG_FULL") {
		t.Fatalf("expected last message fully visible, got:\n%s", got)
	}
}

// TestPrintChatObfuscated_NewFormat_ShortChat verifies that short conversations
// skip the middle bridge and gap label.
func TestPrintChatObfuscated_NewFormat_ShortChat(t *testing.T) {
	// Not parallel: shares global theme state.

	msgs := []pub_models.Message{
		{Role: "user", Content: "FIRST"},
		{Role: "assistant", Content: "second"},
		{Role: "user", Content: "third"},
		{Role: "assistant", Content: "fourth"},
		{Role: "user", Content: "fifth"},
		{Role: "assistant", Content: "LAST"},
	}

	var b strings.Builder
	if err := printChatObfuscated(&b, pub_models.Chat{ID: "c-short", Messages: msgs}, true); err != nil {
		t.Fatalf("printChatObfuscated: %v", err)
	}
	got := b.String()

	// No gap label for short chats.
	if strings.Contains(got, "more entries") {
		t.Fatalf("expected no gap label for short chat, got:\n%s", got)
	}
	// First and last should be present.
	if !strings.Contains(got, "FIRST") {
		t.Fatalf("expected first message, got:\n%s", got)
	}
	if !strings.Contains(got, "LAST") {
		t.Fatalf("expected last message, got:\n%s", got)
	}
}
