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

func TestChatHandler_continue_emptyPrompt_prefersDirScope_thenGlobalScope(t *testing.T) {
	t.Setenv("DEBUG", "")
	ctx := context.Background()

	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	convDir := filepath.Join(confDir, "conversations")

	global := pub_models.Chat{ID: "globalScope", Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: "global msg"}}}
	if err := Save(convDir, global); err != nil {
		t.Fatalf("Save global: %v", err)
	}

	dirChat := pub_models.Chat{ID: HashIDFromPrompt("dir"), Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: "dir msg"}}}
	if err := Save(convDir, dirChat); err != nil {
		t.Fatalf("Save dir chat: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	cq := &ChatHandler{confDir: confDir, convDir: convDir, prompt: "", out: io.Discard}
	if err := cq.SaveDirScope(cwd, dirChat.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	var buf strings.Builder
	cq2 := &ChatHandler{confDir: confDir, convDir: convDir, prompt: "", out: &buf}
	if err := cq2.cont(ctx); err != nil {
		t.Fatalf("cont: %v", err)
	}
	out := buf.String()
	testboil.AssertStringContains(t, out, "dir msg")
}

func TestChatHandler_continue_emptyPrompt_fallsBackToGlobalScope(t *testing.T) {
	t.Setenv("DEBUG", "")
	ctx := context.Background()

	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	convDir := filepath.Join(confDir, "conversations")

	global := pub_models.Chat{ID: "globalScope", Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: "global msg"}}}
	if err := Save(convDir, global); err != nil {
		t.Fatalf("Save global: %v", err)
	}

	var buf strings.Builder
	cq := &ChatHandler{confDir: confDir, convDir: convDir, prompt: "", out: &buf}
	if err := cq.cont(ctx); err != nil {
		t.Fatalf("cont: %v", err)
	}
	testboil.AssertStringContains(t, buf.String(), "global msg")
}

func TestChatHandler_continueByID_preservesStoredQueries(t *testing.T) {
	t.Setenv("DEBUG", "")
	ctx := context.Background()

	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	convDir := filepath.Join(confDir, "conversations")

	chatID := HashIDFromPrompt("continue this")
	stored := pub_models.Chat{
		ID:      chatID,
		Created: time.Now(),
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "continue this"},
			{Role: "assistant", Content: "done"},
		},
		Queries: []pub_models.QueryCost{{
			CreatedAt: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
			CostUSD:   0.123,
			Model:     "openrouter/test-model",
			Usage:     pub_models.Usage{TotalTokens: 321},
		}},
	}
	if err := Save(convDir, stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var buf strings.Builder
	cq := &ChatHandler{confDir: confDir, convDir: convDir, prompt: chatID, out: &buf}

	if err := cq.cont(ctx); err != nil {
		t.Fatalf("cont: %v", err)
	}

	continued, err := FromPath(filepath.Join(convDir, chatID+".json"))
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	if len(continued.Queries) != 1 {
		t.Fatalf("queries length mismatch: got %d", len(continued.Queries))
	}
	if continued.Queries[0].CostUSD != 0.123 {
		t.Fatalf("query cost mismatch: got %v", continued.Queries[0].CostUSD)
	}
}
