package chat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	// It should succeed and return empty info when neither a dir binding nor global scope exists.
	if err := cq.dirInfo(); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestChatHandler_dirInfo_GlobalScope_Raw(t *testing.T) {
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
		ID:      "globalScope",
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

	// With no dir binding, it should show global scope info.
	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo: %v", err)
	}

	var got chatDirInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v, out=%q", err, out.String())
	}

	if got.Scope != "global" {
		t.Fatalf("scope: got %q", got.Scope)
	}
	if got.ChatID != "globalScope" {
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

	prev := pub_models.Chat{ID: "globalScope", Created: time.Now()}
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

func TestChatHandler_dirInfo_DirScopeIncludesCost_Raw(t *testing.T) {
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
		ID:      "bound_chat_with_cost",
		Created: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "u"},
			{Role: "assistant", Content: "a"},
		},
		Queries: []pub_models.QueryCost{
			{CostUSD: 0.1234},
			{CostUSD: 0.0066},
		},
	}
	if err := Save(convDir, bound); err != nil {
		t.Fatalf("Save(bound): %v", err)
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

	var got struct {
		CostUSD float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CostUSD != 0.13 {
		t.Fatalf("cost_usd: got %v", got.CostUSD)
	}
}

func TestChatHandler_dirInfo_GlobalScope_Raw_IncludesTokenAndPriceBreakdown(t *testing.T) {
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
	ch := pub_models.Chat{
		ID:      "globalScope",
		Created: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok"},
		},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     12,
			CompletionTokens: 7,
			TotalTokens:      19,
			PromptTokensDetails: pub_models.PromptTokensDetails{
				CachedTokens: 5,
			},
		},
		Queries: []pub_models.QueryCost{{
			CostUSD: 0.123,
			Usage: pub_models.Usage{
				PromptTokens:     12,
				CompletionTokens: 7,
				TotalTokens:      19,
				PromptTokensDetails: pub_models.PromptTokensDetails{
					CachedTokens: 5,
				},
			},
			Model: "openrouter/test-model",
		}},
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

	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo: %v", err)
	}

	var got struct {
		InputTokens          int `json:"input_tokens"`
		CachedTokens         int `json:"cached_tokens"`
		OutputTokens         int `json:"output_tokens"`
		NonCachedInputTokens int `json:"non_cached_input_tokens"`
		Price                struct {
			Input  string `json:"input"`
			Cached string `json:"cached"`
			Output string `json:"output"`
			Total  string `json:"total"`
		} `json:"price"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v, out=%q", err, out.String())
	}

	if got.InputTokens != 12 {
		t.Fatalf("input_tokens: got %v", got.InputTokens)
	}
	if got.CachedTokens != 5 {
		t.Fatalf("cached_tokens: got %v", got.CachedTokens)
	}
	if got.NonCachedInputTokens != 7 {
		t.Fatalf("non_cached_input_tokens: got %v", got.NonCachedInputTokens)
	}
	if got.OutputTokens != 7 {
		t.Fatalf("output_tokens: got %v", got.OutputTokens)
	}
	if got.Price.Total == "" {
		t.Fatalf("expected total price breakdown, got empty output: %q", out.String())
	}
	if got.Price.Input == "" {
		t.Fatalf("expected input price breakdown, got empty output: %q", out.String())
	}
	if got.Price.Cached == "" {
		t.Fatalf("expected cached price breakdown, got empty output: %q", out.String())
	}
	if got.Price.Output == "" {
		t.Fatalf("expected output price breakdown, got empty output: %q", out.String())
	}
}

func TestChatHandler_dirInfo_Pretty_IncludesTokenAndPriceBreakdown(t *testing.T) {
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
		ID:      "bound_chat_breakdown",
		Created: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "u"},
			{Role: "assistant", Content: "a"},
		},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     21,
			CompletionTokens: 9,
			TotalTokens:      30,
			PromptTokensDetails: pub_models.PromptTokensDetails{
				CachedTokens: 4,
			},
		},
		Queries: []pub_models.QueryCost{{
			CostUSD: 0.42,
			Usage: pub_models.Usage{
				PromptTokens:     21,
				CompletionTokens: 9,
				TotalTokens:      30,
				PromptTokensDetails: pub_models.PromptTokensDetails{
					CachedTokens: 4,
				},
			},
		}},
	}
	if err := Save(convDir, bound); err != nil {
		t.Fatalf("Save(bound): %v", err)
	}

	cq := &ChatHandler{confDir: confDir, convDir: convDir}
	if err := cq.SaveDirScope("", bound.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	var out bytes.Buffer
	cq.raw = false
	cq.out = &out

	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo: %v", err)
	}

	printed := out.String()
	for _, want := range []string{
		"tokens used:",
		"input: 21",
		"cached: 4",
		"output: 9",
		"price details:",
		"input:",
		"cached:",
		"output:",
		"total:",
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("expected %q in output, got: %q", want, printed)
		}
	}
}
