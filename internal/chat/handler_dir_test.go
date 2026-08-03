package chat

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestChatHelpDocumentsDirV2(t *testing.T) {
	if !strings.Contains(chatUsage, "dirv2") {
		t.Fatalf("chat help does not document dirv2")
	}
}

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

func TestChatHandler_dirInfo_Raw_PriceBreakdownAggregatesAllQueries(t *testing.T) {
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
		ID:      "globalScopeAggregatedPrices",
		Created: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok"},
		},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     20,
			CompletionTokens: 8,
			TotalTokens:      28,
			PromptTokensDetails: pub_models.PromptTokensDetails{
				CachedTokens: 8,
			},
		},
		Queries: []pub_models.QueryCost{
			{
				CostUSD: 0.1,
				Usage: pub_models.Usage{
					PromptTokens:     12,
					CompletionTokens: 3,
					PromptTokensDetails: pub_models.PromptTokensDetails{
						CachedTokens: 5,
					},
				},
			},
			{
				CostUSD: 0.2,
				Usage: pub_models.Usage{
					PromptTokens:     8,
					CompletionTokens: 5,
					PromptTokensDetails: pub_models.PromptTokensDetails{
						CachedTokens: 3,
					},
				},
			},
		},
	}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cqInit := &ChatHandler{confDir: confDir, convDir: convDir}
	if err := cqInit.SaveDirScope("", ch.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	var out bytes.Buffer
	cq := &ChatHandler{confDir: confDir, convDir: convDir, raw: true, out: &out}

	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo: %v", err)
	}

	var got struct {
		CostUSD float64 `json:"cost_usd"`
		Price   struct {
			Input  string `json:"input"`
			Cached string `json:"cached"`
			Output string `json:"output"`
			Total  string `json:"total"`
		} `json:"price"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v, out=%q", err, out.String())
	}

	if math.Abs(got.CostUSD-0.3) > 1e-9 {
		t.Fatalf("cost_usd: got %v want %v", got.CostUSD, 0.3)
	}
	if got.Price.Total != "$0.3" {
		t.Fatalf("price.total: got %q want %q", got.Price.Total, "$0.3")
	}
	if got.Price.Input != "$0.129" {
		t.Fatalf("price.input: got %q want %q", got.Price.Input, "$0.129")
	}
	if got.Price.Cached != "$0.086" {
		t.Fatalf("price.cached: got %q want %q", got.Price.Cached, "$0.086")
	}
	if got.Price.Output != "$0.086" {
		t.Fatalf("price.output: got %q want %q", got.Price.Output, "$0.086")
	}
}

func TestChatHandler_dirInfo_V1RemainsBackwardCompatible(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	convDir := filepath.Join(confDir, "conversations")
	chat := pub_models.Chat{
		ID:       globalScopeChatID,
		Created:  time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     21,
			CompletionTokens: 9,
			TotalTokens:      30,
		},
		RecentTokenUsage: &pub_models.Usage{PromptTokens: 18, CompletionTokens: 3, TotalTokens: 21},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var out bytes.Buffer
	cq := &ChatHandler{confDir: confDir, convDir: convDir, raw: true, out: &out}
	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo raw: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v, out=%q", err, out.String())
	}
	if raw["input_tokens"] != float64(21) || raw["output_tokens"] != float64(9) {
		t.Fatalf("legacy token fields changed: %s", out.String())
	}
	for _, key := range []string{"version", "token_usage", "total_input_tokens", "recent_input_tokens"} {
		if _, exists := raw[key]; exists {
			t.Fatalf("v1 output contains v2 field %q: %s", key, out.String())
		}
	}

	out.Reset()
	cq.raw = false
	if err := cq.dirInfo(); err != nil {
		t.Fatalf("dirInfo pretty: %v", err)
	}
	if !strings.Contains(out.String(), "input: 21") || strings.Contains(out.String(), "recent:") || strings.Contains(out.String(), "most recent:") {
		t.Fatalf("v1 pretty output changed: %q", out.String())
	}
}

func TestChatHandler_dirInfoV2_Raw_CleanTokenUsageSchema(t *testing.T) {
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
		ID:      "globalScopeTokenSections",
		Created: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok"},
		},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     30,
			CompletionTokens: 10,
			TotalTokens:      40,
			PromptTokensDetails: pub_models.PromptTokensDetails{
				CachedTokens: 10,
			},
		},
		RecentTokenUsage: &pub_models.Usage{
			PromptTokens:     30,
			CompletionTokens: 4,
			TotalTokens:      34,
			PromptTokensDetails: pub_models.PromptTokensDetails{
				CachedTokens: 10,
			},
		},
		Queries: []pub_models.QueryCost{
			{
				CostUSD: 0.1,
				Usage: pub_models.Usage{
					PromptTokens:     12,
					CompletionTokens: 3,
					TotalTokens:      15,
					PromptTokensDetails: pub_models.PromptTokensDetails{
						CachedTokens: 5,
					},
				},
			},
			{
				CostUSD: 0.2,
				Usage: pub_models.Usage{
					PromptTokens:     30,
					CompletionTokens: 10,
					TotalTokens:      40,
					PromptTokensDetails: pub_models.PromptTokensDetails{
						CachedTokens: 10,
					},
				},
			},
		},
	}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cqInit := &ChatHandler{confDir: confDir, convDir: convDir}
	if err := cqInit.SaveDirScope("", ch.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	var out bytes.Buffer
	cq := &ChatHandler{confDir: confDir, convDir: convDir, raw: true, out: &out}

	if err := cq.dirInfoV2(); err != nil {
		t.Fatalf("dirInfoV2: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v, out=%q", err, out.String())
	}
	if got["version"] != float64(2) {
		t.Fatalf("version: got %#v want 2", got["version"])
	}
	if math.Abs(got["cost_usd"].(float64)-0.3) > 1e-9 {
		t.Fatalf("cost_usd: got %#v want 0.3", got["cost_usd"])
	}
	allowed := map[string]bool{
		"version": true, "scope": true, "chat_id": true, "profile": true,
		"updated": true, "conversation_created": true, "replies_by_role": true,
		"token_usage": true, "cost_usd": true,
	}
	for key := range got {
		if !allowed[key] {
			t.Fatalf("v2 output contains unknown top-level field %q: %s", key, out.String())
		}
	}
	for _, key := range []string{
		"cost", "price", "input_tokens", "non_cached_input_tokens", "cached_tokens", "output_tokens",
		"total_input_tokens", "total_cached_tokens", "total_output_tokens", "total_tokens",
		"recent_input_tokens", "recent_cached_tokens", "recent_output_tokens", "recent_tokens",
	} {
		if _, exists := got[key]; exists {
			t.Fatalf("v2 output contains legacy field %q: %s", key, out.String())
		}
	}

	tokenUsage, ok := got["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage: got %#v", got["token_usage"])
	}
	recent, ok := tokenUsage["recent"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage.recent: got %#v", tokenUsage["recent"])
	}
	total, ok := tokenUsage["total"].(map[string]any)
	if !ok {
		t.Fatalf("token_usage.total: got %#v", tokenUsage["total"])
	}
	for key, want := range map[string]float64{"uncached_input": 20, "cached_input": 10, "output": 4, "total": 34} {
		if recent[key] != want {
			t.Fatalf("token_usage.recent.%s: got %#v want %v", key, recent[key], want)
		}
	}
	for key, want := range map[string]float64{"uncached_input": 27, "cached_input": 15, "output": 13, "total": 55} {
		if total[key] != want {
			t.Fatalf("token_usage.total.%s: got %#v want %v", key, total[key], want)
		}
	}
}

func TestChatHandler_dirInfoV2_Pretty_IncludesTotalAndRecentUsage(t *testing.T) {
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

	if err := cq.dirInfoV2(); err != nil {
		t.Fatalf("dirInfoV2: %v", err)
	}

	printed := out.String()
	for _, want := range []string{
		"token usage:",
		"total:",
		"input: 0.021K",
		"cached: 0.004K",
		"output: 0.009K",
		"recent:",
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("expected %q in output, got: %q", want, printed)
		}
	}
	if strings.Contains(printed, "price details:") {
		t.Fatalf("v2 pretty output contains legacy price details: %q", printed)
	}
	totalIdx := strings.Index(printed, "total:")
	recentIdx := strings.Index(printed, "recent:")
	if totalIdx == -1 || recentIdx == -1 || totalIdx >= recentIdx {
		t.Fatalf("token sections out of order, got: %q", printed)
	}
}

func TestChatHandler_dirInfoV2_Pretty_AbbreviatesTokenCounts(t *testing.T) {
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
		ID:      "globalScopeBigTokens",
		Created: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "ok"},
		},
		TokenUsage: &pub_models.Usage{
			PromptTokens:     3717276,
			CompletionTokens: 1234567,
			TotalTokens:      4951843,
			PromptTokensDetails: pub_models.PromptTokensDetails{
				CachedTokens: 500000,
			},
		},
		Queries: []pub_models.QueryCost{
			{
				Usage: pub_models.Usage{
					PromptTokens:     3717276,
					CompletionTokens: 1234567,
					TotalTokens:      4951843,
					PromptTokensDetails: pub_models.PromptTokensDetails{
						CachedTokens: 500000,
					},
				},
			},
		},
	}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cqInit := &ChatHandler{confDir: confDir, convDir: convDir}
	if err := cqInit.SaveDirScope("", ch.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	var out bytes.Buffer
	cq := &ChatHandler{confDir: confDir, convDir: convDir, raw: false, out: &out}

	if err := cq.dirInfoV2(); err != nil {
		t.Fatalf("dirInfoV2: %v", err)
	}

	printed := out.String()
	for _, want := range []string{
		"input: 3.7M",
		"cached: 500K",
		"output: 1.2M",
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("expected %q in output, got: %q", want, printed)
		}
	}
}

func TestChatRecentUsagePrecedence(t *testing.T) {
	t.Run("explicit recent call usage", func(t *testing.T) {
		chat := pub_models.Chat{
			TokenUsage:       &pub_models.Usage{TotalTokens: 120},
			RecentTokenUsage: &pub_models.Usage{TotalTokens: 85},
		}

		got := chatRecentUsage(chat)
		if got == nil || got.TotalTokens != 85 {
			t.Fatalf("recent usage mismatch: got %+v", got)
		}
	})

	t.Run("last query fallback", func(t *testing.T) {
		chat := pub_models.Chat{Queries: []pub_models.QueryCost{
			{Usage: pub_models.Usage{TotalTokens: 10}},
			{Usage: pub_models.Usage{TotalTokens: 34}},
		}}

		got := chatRecentUsage(chat)
		if got == nil || got.TotalTokens != 34 {
			t.Fatalf("recent query fallback mismatch: got %+v", got)
		}
	})
}
