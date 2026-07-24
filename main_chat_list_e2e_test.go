package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ============================================================
// Phase 6: UAT-to-e2e migration — clai chat list macro tests
// ============================================================
// All tests use -n (--non-interactive) for deterministic output.
// HOME is isolated to prevent foreign-source leaks.

// -- helpers ---------------------------------------------------------------

func seedConv(t *testing.T, convDir string, c pub_models.Chat) {
	t.Helper()
	if err := chat.Save(convDir, c); err != nil {
		t.Fatalf("Save(%q): %v", c.ID, err)
	}
}

func makeConv(id string, created time.Time, msgs ...pub_models.Message) pub_models.Chat {
	return pub_models.Chat{
		ID:       id,
		Created:  created,
		Messages: msgs,
	}
}

func msgUser(content string) pub_models.Message {
	return pub_models.Message{Role: "user", Content: content}
}

func msgAsst(content string) pub_models.Message {
	return pub_models.Message{Role: "assistant", Content: content}
}

// ============================================================
// Tests 1–5: Foreign-chat and complex picker flows
// ============================================================

// Test_e2e_chat_list_foreign_clone_and_dedup verifies that selecting a foreign
// (Claude) chat row and continuing clones it, and that subsequent list runs
// suppress the foreign row (dedup).
func Test_e2e_chat_list_foreign_clone_and_dedup(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a fake Claude project so the anthropic source reader discovers it.
	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s-clone","cwd":"/work","message":{"content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s-clone","cwd":"/work","message":{"content":"hello"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(projDir, "sess.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// First run: select row 0, then continue ("c") to clone. Trailing "q"s from -n
	// exit the list loop after clone.
	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 c")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "cloned claude-code session s-clone") {
		t.Fatalf("expected clone notice in output, got:\n%s", stdout)
	}

	// Second run: the foreign row should be deduped. Select row 0, then "q" exits.
	stdout2, status2 := runOne(t, confDir, "-n -r -cm test c l 0")
	if status2 != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status2, stdout2)
	}
	if strings.Contains(stdout2, "s-clone") {
		t.Fatalf("expected foreign session id to be suppressed after clone; output:\n%s", stdout2)
	}
}

// Test_e2e_chat_list_edit_message_picker_reopens drives the full peek+edit flow:
// page forward in list, open chat, page forward in picker, mistype → notice,
// edit message → picker reopens same page → back → list same page → quit.
func Test_e2e_chat_list_edit_message_picker_reopens(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	// Fake $EDITOR: overwrite whatever file it is given.
	editorScript := filepath.Join(t.TempDir(), "fake-editor.sh")
	if err := os.WriteFile(editorScript, []byte("#!/bin/sh\nprintf 'EDITED BY E2E' > \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	t.Setenv("EDITOR", editorScript)

	convDir := filepath.Join(confDir, "conversations")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Seed 12 chats (two pages at default 10 rows/page).
	// conv-01 (index 10 on page 1) gets 12 messages for a two-page picker.
	// Each chat must have a unique first user message to avoid grouping.
	for i := range 12 {
		msgs := []pub_models.Message{
			msgUser("unique prompt " + formatTwoDigit(i)),
			msgAsst("reply"),
		}
		if i == 1 {
			msgs = msgs[:1]
			for range 11 {
				msgs = append(msgs, msgAsst("reply"))
			}
		}
		seedConv(t, convDir, makeConv(
			"conv-"+formatTwoDigit(i),
			base.Add(time.Duration(i)*time.Minute),
			msgs...,
		))
	}

	// n=next page, 10=select conv-01, e=edit, n=next picker page,
	// d=mistype (triggers "invalid selection" notice), 11=edit msg 11,
	// b=back to list (still page 1), trailing q exits.
	stdout, status := runOne(t, confDir, "-n -r -cm test c l n 10 e n d 11 b")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, `invalid selection "d"`) {
		t.Fatalf("expected mistype notice in picker prompt, got:\n%s", stdout)
	}

	// Verify message 11 was edited.
	edited, err := chat.FromPath(filepath.Join(convDir, "conv-01.json"))
	if err != nil {
		t.Fatalf("load edited chat: %v", err)
	}
	if edited.Messages[11].Content != "EDITED BY E2E" {
		t.Fatalf("expected message 11 edited, got %q", edited.Messages[11].Content)
	}

	// Picker page 1 (1/1) appears at least 3 times: picker [n]ext, reopened
	// post-edit, and list page 1 after backing out.
	if n := strings.Count(stdout, "page 1/1"); n < 3 {
		t.Fatalf("expected picker+list page 1/1 at least 3 times, got %d:\n%s", n, stdout)
	}
}

// Test_e2e_chat_list_delete_message_picker_reopens is the delete mirror:
// picker stays open across deletions on the same page.
func Test_e2e_chat_list_delete_message_picker_reopens(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 12 {
		msgs := []pub_models.Message{msgUser("unique prompt " + formatTwoDigit(i)), msgAsst("reply")}
		if i == 1 {
			msgs = msgs[:1]
			for range 11 {
				msgs = append(msgs, msgAsst("reply"))
			}
		}
		seedConv(t, convDir, makeConv(
			"conv-"+formatTwoDigit(i),
			base.Add(time.Duration(i)*time.Minute),
			msgs...,
		))
	}

	// n=next page, 10=select conv-01, d=delete, n=next picker page,
	// 11=delete msg 11, b=back to list, trailing q exits.
	stdout, status := runOne(t, confDir, "-n -r -cm test c l n 10 d n 11 b")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	pruned, err := chat.FromPath(filepath.Join(convDir, "conv-01.json"))
	if err != nil {
		t.Fatalf("load chat after delete: %v", err)
	}
	if len(pruned.Messages) != 11 {
		t.Fatalf("expected 11 messages after deleting one, got %d", len(pruned.Messages))
	}

	if n := strings.Count(stdout, "page 1/1"); n < 2 {
		t.Fatalf("expected picker page 1/1 at least twice, got %d:\n%s", n, stdout)
	}
}

// Test_e2e_chat_list_foreign_back_to_list verifies that pressing "b" at
// a foreign chat's action prompt returns to the list loop.
func Test_e2e_chat_list_foreign_back_to_list(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s-back","cwd":"/work","message":{"content":"hello"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s-back","cwd":"/work","message":{"content":"hi"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(projDir, "sess.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// 0=select foreign row, b=back to list, trailing q exits.
	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 b")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "=== Chat info ===") {
		t.Fatalf("expected foreign chat info in output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "(press [c]ontinue") {
		t.Fatalf("expected foreign-chat action prompt in output, got:\n%s", stdout)
	}
}

// Test_e2e_chat_list_foreign_quit verifies that pressing "q" at a foreign chat's
// action prompt exits cleanly without cloning.
func Test_e2e_chat_list_foreign_quit(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s-quit","cwd":"/work","message":{"content":"hello"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s-quit","cwd":"/work","message":{"content":"hi"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(projDir, "sess.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// 0=select foreign row, trailing q exits from actOnForeignChat.
	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "=== Chat info ===") {
		t.Fatalf("expected foreign chat info in output, got:\n%s", stdout)
	}

	// Verify no conversations were cloned (only chat index file should exist).
	convDir := filepath.Join(confDir, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("read conv dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && e.Name() != "chat_index.cache" {
			t.Fatalf("expected no cloned conversations after foreign quit, found: %s", e.Name())
		}
	}
}

// ============================================================
// Tests 6–13: Basic macro tests (migrated from handler_list_chat_macro_test.go)
// ============================================================

// Test_e2e_chat_list_macro_empty covers: empty conversation dir, macro selects
// row 0 → "selection out of range" notice → trailing "q" exits cleanly.
func Test_e2e_chat_list_macro_empty(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "selection out of range") {
		t.Fatalf("expected 'selection out of range' notice, got:\n%s", stdout)
	}
}

// Test_e2e_chat_list_macro_out_of_range covers: one chat seeded, macro selects
// index 999, table shows invalid-selection notice, trailing "q" exits cleanly.
func Test_e2e_chat_list_macro_out_of_range(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	seedConv(t, convDir, makeConv(
		"only-chat",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		msgUser("hello"), msgAsst("hi"),
	))

	stdout, status := runOne(t, confDir, "-n -r -cm test c l 999")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "invalid selection") {
		t.Fatalf("expected 'invalid selection' notice, got:\n%s", stdout)
	}
}

// Test_e2e_chat_list_macro_continue covers: select chat 0, actOnChat reads
// trailing "q" → ErrUserInitiatedExit → listChats catches → clean exit.
func Test_e2e_chat_list_macro_continue(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	seedConv(t, convDir, makeConv(
		"chat-0",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		msgUser("test prompt"), msgAsst("test reply"),
	))

	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "=== Chat info ===") {
		t.Fatalf("expected chat info in output, got:\n%s", stdout)
	}
}

// Test_e2e_chat_list_macro_back_to_list covers: select chat 0, press "b" (back),
// returns to list loop, trailing "q" exits cleanly.
func Test_e2e_chat_list_macro_back_to_list(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	seedConv(t, convDir, makeConv(
		"chat-0",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		msgUser("test prompt"), msgAsst("test reply"),
	))

	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 b")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "=== Chat info ===") {
		t.Fatalf("expected chat info in output, got:\n%s", stdout)
	}
	// After back, the list prompt should appear again.
	if !strings.Contains(stdout, "(select") {
		t.Fatalf("expected 'select' prompt after backing to list, got:\n%s", stdout)
	}
}

// Test_e2e_chat_list_macro_delete_messages covers: select chat 0, "d" to delete,
// "0:5" range deletes messages 0-5, trailing "q" exits picker then list.
func Test_e2e_chat_list_macro_delete_messages(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	msgs := []pub_models.Message{
		msgUser("prompt"),
		msgAsst("r0"),
		msgAsst("r1"),
		msgAsst("r2"),
		msgAsst("r3"),
		msgAsst("r4"),
		msgAsst("r5"),
	}
	seedConv(t, convDir, makeConv(
		"chat-0",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		msgs...,
	))

	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 d 0:5")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	loaded, loadErr := chat.FromPath(filepath.Join(convDir, "chat-0.json"))
	if loadErr != nil {
		t.Fatalf("load chat: %v", loadErr)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 message remaining after deleting 0:5 (6 msgs), got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "r5" {
		t.Fatalf("expected remaining message to be 'r5', got %q", loaded.Messages[0].Content)
	}
	if !strings.Contains(stdout, "Index | Role") {
		t.Fatalf("expected message picker table in output, got:\n%s", stdout)
	}
}

// Test_e2e_chat_list_macro_delete_no_messages covers: select chat 0, "d" to open
// picker, trailing "q" exits picker without deletion, then exits list.
func Test_e2e_chat_list_macro_delete_no_messages(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	seedConv(t, convDir, makeConv(
		"chat-0",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		msgUser("prompt"), msgAsst("reply"),
	))

	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 d")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	loaded, loadErr := chat.FromPath(filepath.Join(convDir, "chat-0.json"))
	if loadErr != nil {
		t.Fatalf("load chat: %v", loadErr)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages (unchanged), got %d", len(loaded.Messages))
	}
}

// Test_e2e_chat_list_macro_edit_message covers: select chat 0, "e" to edit,
// "5" selects message 5, editor runs, trailing "q" exits picker then list.
func Test_e2e_chat_list_macro_edit_message(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	editorScript := filepath.Join(t.TempDir(), "fake-editor.sh")
	if err := os.WriteFile(editorScript, []byte("#!/bin/sh\nprintf 'EDITED BY E2E' > \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	t.Setenv("EDITOR", editorScript)

	convDir := filepath.Join(confDir, "conversations")
	msgs := []pub_models.Message{
		msgUser("prompt"),
		msgAsst("reply"),
		msgAsst("reply"),
		msgAsst("reply"),
		msgAsst("reply"),
		msgAsst("reply"),
		msgAsst("reply"),
	}
	seedConv(t, convDir, makeConv(
		"chat-0",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		msgs...,
	))

	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 e 5")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	loaded, loadErr := chat.FromPath(filepath.Join(convDir, "chat-0.json"))
	if loadErr != nil {
		t.Fatalf("load chat: %v", loadErr)
	}
	if loaded.Messages[5].Content != "EDITED BY E2E" {
		t.Fatalf("expected message 5 to be edited, got %q", loaded.Messages[5].Content)
	}
	if !strings.Contains(stdout, "Index | Role") {
		t.Fatalf("expected message picker table in output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "EDITED BY E2E") {
		t.Fatalf("expected edited content visible in reopened picker, got:\n%s", stdout)
	}
}

// ============================================================
// Phase 7: Expanded e2e macro regression suite
// ============================================================
// Tests 14-15: Group drill-down
// Tests 16-17: Dir filter toggle

// -- helpers ---------------------------------------------------------------

// writeDirScopeBinding creates a v2 dirscope binding for the given CWD and chatID.
func writeDirScopeBinding(t *testing.T, confDir, cwd, chatID string) {
	t.Helper()
	abs, err := filepath.Abs(cwd)
	if err != nil {
		t.Fatalf("Abs(%q): %v", cwd, err)
	}
	canonical := filepath.Clean(abs)
	sum := sha256.Sum256([]byte(canonical))
	dirHash := hex.EncodeToString(sum[:])

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	binding := map[string]any{
		"version":  2,
		"dir_hash": dirHash,
		"abs_path": canonical,
		"chat_id":  chatID,
		"history": []map[string]any{
			{"chat_id": chatID, "first_scoped": now.Format(time.RFC3339), "last_scoped": now.Format(time.RFC3339)},
		},
		"updated": now.Format(time.RFC3339),
	}

	dirsDir := filepath.Join(confDir, "conversations", "dirs")
	if err := os.MkdirAll(dirsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(dirs): %v", err)
	}
	b, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		t.Fatalf("Marshal(binding): %v", err)
	}
	bindingPath := filepath.Join(dirsDir, dirHash+".json")
	if err := os.WriteFile(bindingPath, b, 0o644); err != nil {
		t.Fatalf("WriteFile(binding): %v", err)
	}
}

// ============================================================
// Test 14: Group drill-down — select group, select member, back
// ============================================================

func Test_e2e_chat_list_macro_group_drill_and_back(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Two chats with the same first user message → they form a group.
	seedConv(t, convDir, makeConv(
		"chat-a",
		base.Add(1*time.Minute),
		msgUser("fix the auth bug"), msgAsst("reply a"),
	))
	seedConv(t, convDir, makeConv(
		"chat-b",
		base.Add(2*time.Minute),
		msgUser("fix the auth bug"), msgAsst("reply b"),
	))
	// Also seed an unrelated chat to ensure group row is the only collapsed one.
	seedConv(t, convDir, makeConv(
		"chat-unrelated",
		base,
		msgUser("refactor database"), msgAsst("reply"),
	))

	// 0=select group row → drill into group view
	// 0=select member chat-a → actOnChat
	// b=back to group view → trailing q exits
	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 0 b")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	// Group row should appear with [group:2] label.
	if !strings.Contains(stdout, "[group:2]") {
		t.Fatalf("expected [group:2] label in output, got:\n%s", stdout)
	}
	// Chat info should appear for the selected member.
	if !strings.Contains(stdout, "=== Chat info ===") {
		t.Fatalf("expected chat info in output, got:\n%s", stdout)
	}
	// The group view prompt should include the group indicator.
	if !strings.Contains(stdout, "group:") {
		t.Fatalf("expected group indicator in prompt, got:\n%s", stdout)
	}
}

// ============================================================
// Test 15: Group back without selecting a member
// ============================================================

func Test_e2e_chat_list_macro_group_back_without_select(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	convDir := filepath.Join(confDir, "conversations")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedConv(t, convDir, makeConv(
		"chat-a",
		base.Add(1*time.Minute),
		msgUser("fix the auth bug"), msgAsst("reply a"),
	))
	seedConv(t, convDir, makeConv(
		"chat-b",
		base.Add(2*time.Minute),
		msgUser("fix the auth bug"), msgAsst("reply b"),
	))

	// 0=select group row → drill into group view
	// b=back → return to top-level list → trailing q exits
	stdout, status := runOne(t, confDir, "-n -r -cm test c l 0 b")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	// Group row should appear.
	if !strings.Contains(stdout, "[group:2]") {
		t.Fatalf("expected [group:2] label in output, got:\n%s", stdout)
	}
	// After backing out, the top-level prompt should reappear (the [b]ack exits top-level via trailing q).
	if !strings.Contains(stdout, "(select") {
		t.Fatalf("expected 'select' prompt in output, got:\n%s", stdout)
	}
}

// ============================================================
// Test 16: Dir filter toggle — on then off
// ============================================================

func Test_e2e_chat_list_macro_dir_filter_toggle(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	// Work in a temp dir so the dirscope predicate can bind.
	workDir := t.TempDir()

	convDir := filepath.Join(confDir, "conversations")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Chat bound to CWD via dirscope binding.
	seedConv(t, convDir, makeConv(
		"bound-chat",
		base.Add(1*time.Minute),
		msgUser("in this directory"), msgAsst("yes"),
	))
	// Chat not bound — will be hidden when dir filter is on.
	seedConv(t, convDir, makeConv(
		"unbound-chat",
		base,
		msgUser("elsewhere"), msgAsst("no"),
	))

	// Create dirscope binding for the workDir → bound-chat.
	writeDirScopeBinding(t, confDir, workDir, "bound-chat")

	// d=toggle dir filter ON (shows only bound-chat)
	// d=toggle dir filter OFF (shows both) → trailing q exits
	stdout, status := runOne(t, workDir, "-n -r -cm test c l d d")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	// The [d]ir action label should appear in each of the three table renders.
	if n := strings.Count(stdout, "[d]irscoped"); n != 3 {
		t.Fatalf("expected [d]irscoped 3 times (one per table render), got %d:\n%s", n, stdout)
	}
	// After the d toggle cycle (on→off), the full list should show 2 rows.
	// The "Index" header appears once per table render → 3 renders total.
	if n := strings.Count(stdout, "Index"); n < 3 {
		t.Fatalf("expected at least 3 table renders, got %d:\n%s", n, stdout)
	}
}

// ============================================================
// Test 17: Dir filter empty — toggle on with no bound chats
// ============================================================

func Test_e2e_chat_list_macro_dir_filter_empty(t *testing.T) {
	_ = setupMainTestConfigDir(t)
	t.Setenv("HOME", t.TempDir())

	// Work in a temp dir with no dirscope bindings.
	workDir := t.TempDir()

	// No chats seeded — empty conv dir (except for the index/globalScope).

	// d=toggle dir filter ON → empty view → trailing q exits
	stdout, status := runOne(t, workDir, "-n -r -cm test c l d")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}

	// The [d]irscoped action should appear.
	// The output should show zero conversations (empty list after filtering).
	if !strings.Contains(stdout, "[d]irscoped") {
		t.Fatalf("expected [d]irscoped action label, got:\n%s", stdout)
	}
}

// formatTwoDigit formats i as a zero-padded two-digit string.
func formatTwoDigit(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
