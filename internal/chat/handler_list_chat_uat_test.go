package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// These tests are "user acceptance" style integration tests.
// They intentionally exercise the real handler path (list -> select -> continue)
// by stubbing only terminal input.

func TestUAT_ListSelectContinue_ForeignClaudeChat_ClonesAndThenDedups(t *testing.T) {
	ctx := context.Background()

	// Ensure a controlled CWD so dirscope binding is deterministic.
	_ = chdirToTemp(t)

	// Create a temp HOME with a Claude project.
	home := t.TempDir()
	t.Setenv("HOME", home)

	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonlPath := filepath.Join(projDir, "sess.jsonl")
	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s-uat","cwd":"/work","message":{"content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s-uat","cwd":"/work","message":{"content":"hello"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	// Make ordering stable even if timestamp parse degrades to mtime.
	if err := os.Chtimes(jsonlPath, time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Stub the interactive terminal: select row 0, then continue clone.
	in := []string{"0", "c"}
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		if len(in) == 0 {
			return "", nil
		}
		next := in[0]
		in = in[1:]
		return next, nil
	})
	t.Cleanup(restore)

	// Capture output to ensure this is running through the real list/act flow.
	var out bytes.Buffer
	cq, confDir := newTestHandler(t)
	cq.out = &out

	// First run: foreign row discovered, selected, and cloned.
	if err := cq.handleListCmd(ctx); err != nil {
		t.Fatalf("handleListCmd: %v", err)
	}

	convDir := conversationsDir(confDir)
	p, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var clonedID string
	for _, r := range p.rows {
		if r.Source == "claude-code" && r.SourceID == "s-uat" {
			clonedID = r.ID
			break
		}
	}
	if clonedID == "" {
		t.Fatalf("expected cloned chat to exist in index")
	}
	if clonedID == "s-uat" {
		t.Fatalf("expected cloned chat to get a new unique clai ID, got %q", clonedID)
	}
	if !strings.Contains(out.String(), "(press [c]ontinue") {
		t.Fatalf("expected foreign chat info continue prompt in output, got:\n%s", out.String())
	}

	// Second run: now the foreign listing should be deduped.
	var out2 bytes.Buffer
	cq2, _ := newTestHandler(t)
	cq2.out = &out2
	// Only list, then quit.
	in2 := []string{"q"}
	restore2 := utils.UseReadUserInputForTests(func() (string, error) {
		if len(in2) == 0 {
			return "q", nil
		}
		next := in2[0]
		in2 = in2[1:]
		return next, nil
	})
	t.Cleanup(restore2)

	_ = cq2.handleListCmd(ctx)
	if strings.Contains(out2.String(), "s-uat") {
		t.Fatalf("expected foreign session id to be suppressed after clone; output:\n%s", out2.String())
	}
}

// seedChatAt saves a native chat with an explicit Created so list ordering
// (Created desc) and pagination are deterministic.
func seedChatAt(t *testing.T, convDir, id string, created time.Time, msgs ...pub_models.Message) {
	t.Helper()
	if err := Save(convDir, pub_models.Chat{ID: id, Created: created, Messages: msgs}); err != nil {
		t.Fatalf("Save(%q): %v", id, err)
	}
}

// seedPeekFixture seeds 12 chats (two chat-list pages at the default 10
// rows/page) with distinct first user messages so no rows group. conv-01 —
// index 10 on chat-list page 1 — gets 12 messages so the message picker
// itself has two pages.
func seedPeekFixture(t *testing.T, convDir string) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 12 {
		msgs := []pub_models.Message{
			msg("user", fmt.Sprintf("unique prompt %02d", i)),
			msg("assistant", "reply"),
		}
		if i == 1 {
			msgs = msgs[:1]
			for j := range 11 {
				msgs = append(msgs, msg("assistant", fmt.Sprintf("reply %02d", j+1)))
			}
		}
		seedChatAt(t, convDir, fmt.Sprintf("conv-%02d", i), base.Add(time.Duration(i)*time.Minute), msgs...)
	}
}

// runPeekScript stubs terminal input with the given script and runs the list
// command, failing the test if the script is over- or under-consumed.
func runPeekScript(t *testing.T, cq *ChatHandler, script []string) {
	t.Helper()
	in := script
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		if len(in) == 0 {
			t.Fatal("input script exhausted: flow did not return to the expected table")
		}
		next := in[0]
		in = in[1:]
		return next, nil
	})
	t.Cleanup(restore)

	err := cq.handleListCmd(context.Background())
	if err != nil && !errors.Is(err, utils.ErrUserInitiatedExit) {
		t.Fatalf("handleListCmd: %v", err)
	}
	if len(in) != 0 {
		t.Fatalf("input script not fully consumed, remaining: %v", in)
	}
}

// countPickerPagePrompts counts message-picker prompts shown on the given page.
func countPickerPagePrompts(out, selectionType, page string) int {
	count := 0
	for _, part := range strings.Split(out, selectionType)[1:] {
		prompt, _, ok := strings.Cut(part, "): ")
		if ok && strings.Contains(prompt, "page "+page) {
			count++
		}
	}
	return count
}

// TestUAT_ListEditMessage_PickerReopensOnSamePage drives the full peek+edit
// flow: page forward in the chat list, open a chat, page forward in the
// message picker, edit a message via $EDITOR, and land back in the picker on
// the same page — then back out to the chat list, which kept its page too.
func TestUAT_ListEditMessage_PickerReopensOnSamePage(t *testing.T) {
	_ = chdirToTemp(t)
	// Isolate HOME so no real Claude/pi sessions leak into the list.
	t.Setenv("HOME", t.TempDir())

	// Fake $EDITOR: overwrite whatever file it is given.
	editorScript := filepath.Join(t.TempDir(), "fake-editor.sh")
	if err := os.WriteFile(editorScript, []byte("#!/bin/sh\nprintf 'EDITED BY UAT' > \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	t.Setenv("EDITOR", editorScript)

	cq, _ := newTestHandler(t)
	var out bytes.Buffer
	cq.out = &out
	seedPeekFixture(t, cq.convDir)

	// list page 1 → open conv-01 → [e]dit → picker page 1 → mistype "d"
	// (re-prompts with a notice) → edit message 11 → picker reopens on
	// page 1 → [b]ack to list (still page 1) → quit.
	runPeekScript(t, cq, []string{"n", "10", "e", "n", "d", "11", "b", "q"})
	if !strings.Contains(out.String(), `invalid selection "d"`) {
		t.Fatalf("expected mistype notice in picker prompt, got:\n%s", out.String())
	}

	edited, err := FromPath(conversationPathFromDir(cq.convDir, "conv-01"))
	if err != nil {
		t.Fatalf("load edited chat: %v", err)
	}
	if edited.Messages[11].Content != "EDITED BY UAT" {
		t.Fatalf("expected message 11 edited, got %q", edited.Messages[11].Content)
	}

	// Picker page 1 prompted twice: once after [n]ext, once reopened post-edit.
	if got := countPickerPagePrompts(out.String(), editMessageChoicesFormat, "1/1"); got < 2 {
		t.Fatalf("expected picker to reopen on page 1 after edit (>=2 prompts), got %d:\n%s", got, out.String())
	}
	// And the chat list still prompted its page 1 after backing out.
	if got := strings.Count(out.String(), "page 1/1"); got < 4 {
		t.Fatalf("expected list+picker page-1 prompts (>=4 total), got %d:\n%s", got, out.String())
	}
}

// TestUAT_ListDeleteMessage_PickerReopensOnSamePage is the delete mirror of
// the peek+edit flow: the picker stays open across deletions on the same page.
func TestUAT_ListDeleteMessage_PickerReopensOnSamePage(t *testing.T) {
	_ = chdirToTemp(t)
	t.Setenv("HOME", t.TempDir())

	cq, _ := newTestHandler(t)
	var out bytes.Buffer
	cq.out = &out
	seedPeekFixture(t, cq.convDir)

	// list page 1 → open conv-01 → [d]elete → picker page 1 → delete message
	// 11 → picker reopens on page 1 → [b]ack to list → quit.
	runPeekScript(t, cq, []string{"n", "10", "d", "n", "11", "b", "q"})

	pruned, err := FromPath(conversationPathFromDir(cq.convDir, "conv-01"))
	if err != nil {
		t.Fatalf("load chat after delete: %v", err)
	}
	if len(pruned.Messages) != 11 {
		t.Fatalf("expected 11 messages after deleting one, got %d", len(pruned.Messages))
	}
	for _, m := range pruned.Messages {
		if m.Content == "reply 11" {
			t.Fatalf("expected message 11 ('reply 11') to be deleted")
		}
	}

	if got := countPickerPagePrompts(out.String(), deleteMessagesChoicesFormat, "1/1"); got < 2 {
		t.Fatalf("expected picker to reopen on page 1 after delete (>=2 prompts), got %d:\n%s", got, out.String())
	}
}
