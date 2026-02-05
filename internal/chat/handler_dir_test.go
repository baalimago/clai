package chat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestChatHandler_dirInfo_NoDirScopeNoPrevQuery(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	var out bytes.Buffer
	cq := &ChatHandler{
		confDir: confDir,
		convDir: filepath.Join(confDir, "conversations"),
		raw:     true,
		out:     &out,
	}

	// Now: it errors when neither a dir binding nor prevQuery exists.
	if err := cq.dirInfo(); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestChatHandler_dirInfo_GlobalPrevQuery_Raw(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	// Ensure CWD is deterministic for the binding lookup.
	wd := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	convDir := filepath.Join(confDir, "conversations")
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	ch := pub_models.Chat{
		ID:      "prevQuery",
		Created: created,
		Profile: "profA",
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: ""},
			{Role: "assistant", Content: "ok"},
		},
		TokenUsage: &pub_models.Usage{
			TotalTokens:      10,
			PromptTokens:     2,
			CompletionTokens: 3,
		},
	}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var out bytes.Buffer
	cq := &ChatHandler{
		confDir: confDir,
		convDir: convDir,
		raw:     true,
		out:     &out,
	}

	// With no dir binding, the command now errors (even if prevQuery exists).
	// Bind the current directory to force the "dir" path and keep this test meaningful.
	if err := cq.SaveDirScope("", "prevQuery"); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo: %v", err)
	}

	var got chatDirInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v, out=%q", err, out.String())
	}

	if got.Scope != "dir" {
		t.Fatalf("scope: got %q", got.Scope)
	}
	if got.ChatID != "prevQuery" {
		t.Fatalf("chat_id: got %q", got.ChatID)
	}
	if got.Profile != "profA" {
		t.Fatalf("profile: got %q", got.Profile)
	}
	if got.RepliesByRole["user"] != 1 {
		t.Fatalf("user replies: %v", got.RepliesByRole)
	}
	if got.RepliesByRole["assistant"] != 1 {
		t.Fatalf("assistant replies: %v", got.RepliesByRole)
	}
	if got.InputTokens != 2 {
		t.Fatalf("input_tokens: got %v", got.InputTokens)
	}
	if got.OutputTokens != 3 {
		t.Fatalf("output_tokens: got %v", got.OutputTokens)
	}
	if got.ConversationCreated != "2024-01-02T03:04:05Z" {
		t.Fatalf("conversation_created: got %q", got.ConversationCreated)
	}
}

func TestChatHandler_dirInfo_DirScopeWinsOverPrevQuery_Raw(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	wd := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	convDir := filepath.Join(confDir, "conversations")
	bound := pub_models.Chat{
		ID:      "bound_chat",
		Created: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "u"},
			{Role: "assistant", Content: "a"},
		},
	}
	if err := Save(convDir, bound); err != nil {
		t.Fatalf("Save(bound): %v", err)
	}

	prev := pub_models.Chat{ID: "prevQuery", Created: time.Now()}
	if err := Save(convDir, prev); err != nil {
		t.Fatalf("Save(prev): %v", err)
	}

	cq := &ChatHandler{confDir: confDir, convDir: convDir}
	if err := cq.SaveDirScope("", bound.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	var out bytes.Buffer
	cq.raw = true
	cq.out = &out

	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo: %v", err)
	}

	var got chatDirInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Scope != "dir" {
		t.Fatalf("scope: got %q", got.Scope)
	}
	if got.ChatID != bound.ID {
		t.Fatalf("chat_id: got %q", got.ChatID)
	}
	if got.Updated == "" {
		t.Fatalf("expected updated to be set")
	}
	if got.ConversationCreated != "2024-06-01T00:00:00Z" {
		t.Fatalf("conversation_created: got %q", got.ConversationCreated)
	}
}
