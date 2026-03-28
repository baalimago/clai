package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_e2e_COST_nested_tool_call_queries_visible_in_chat_dir_json(t *testing.T) {
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	confDir := setupMainTestConfigDir(t)
	t.Setenv("OPENROUTER_API_KEY", "")
	workDir := t.TempDir()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir(%q): %v", workDir, err)
	}

	configPath := filepath.Join(confDir, "mock_test_test.json")
	configJSON := `{
	  "price": {
	    "input_usd_per_token": 0.001,
	    "input_cached_usd_per_token": 0.0005,
	    "output_usd_per_token": 0.002
	  }
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", configPath, err)
	}
	mockConfigPath := filepath.Join(confDir, "mock_test_mock_test.json")
	if err := os.WriteFile(mockConfigPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", mockConfigPath, err)
	}
	textConfigPath := filepath.Join(confDir, "textConfig.json")
	textConfigJSON := `{
	  "model": "test",
	  "save-reply-as-prompt": true
	}`
	if err := os.WriteFile(textConfigPath, []byte(textConfigJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", textConfigPath, err)
	}
	textConfigBytes, err := os.ReadFile(textConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", textConfigPath, err)
	}
	if got := strings.TrimSpace(string(textConfigBytes)); got != strings.TrimSpace(textConfigJSON) {
		t.Fatalf("textConfig.json mismatch: got %q want %q", got, strings.TrimSpace(textConfigJSON))
	}
	if _, err := os.Stat(filepath.Join(confDir, "globalScope.json")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Stat(globalScope.json): %v", err)
	}

	runOne := func(t *testing.T, args string) (string, int) {
		t.Helper()
		oldArgs := os.Args
		t.Cleanup(func() {
			os.Args = oldArgs
		})

		var status int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			status = run(strings.Split(args, " "))
		})
		return stdout, status
	}

	prompt := "hello first tool_pwd now second tool_pwd different tool tool_ls a third tool tool_cat note how cat isnt included here so it shouldnt try to call the tool"
	queryOut, status := runOne(t, "-r -cm test -t pwd,ls q "+prompt)
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, queryOut, "Call: 'pwd'")
	testboil.AssertStringContains(t, queryOut, "Call: 'ls'")
	testboil.AssertStringContains(t, queryOut, workDir)
	testboil.AssertStringContains(t, queryOut, "done after tool for: "+prompt)
	if strings.Contains(queryOut, "Call: 'cat'") {
		t.Fatalf("expected cat tool call to be skipped, output=%q", queryOut)
	}

	dirOut, status := runOne(t, "-r -cm test chat dir")
	testboil.FailTestIfDiff(t, status, 0)

	type chatDirInfo struct {
		Scope        string  `json:"scope"`
		ChatID       string  `json:"chat_id"`
		CostUSD      float64 `json:"cost_usd"`
		Cost         string  `json:"cost"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
	}
	var info chatDirInfo
	trimmed := strings.TrimSpace(strings.TrimSuffix(dirOut, "\a"))
	if err := json.Unmarshal([]byte(trimmed), &info); err != nil {
		t.Fatalf("Unmarshal(chat dir json): %v\nstdout=%q", err, dirOut)
	}
	if info.Scope != "dir" {
		t.Fatalf("expected scope %q, got %q", "dir", info.Scope)
	}
	if info.ChatID == "" {
		t.Fatalf("expected non-empty chat_id")
	}

	chatPath := filepath.Join(confDir, "conversations", info.ChatID+".json")
	chatBytes, err := os.ReadFile(chatPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", chatPath, err)
	}

	var chat pub_models.Chat
	if err := json.Unmarshal(chatBytes, &chat); err != nil {
		t.Fatalf("Unmarshal(%q): %v", chatPath, err)
	}

	if len(chat.Queries) != 1 {
		t.Fatalf("expected 1 outer query cost entry, got %d\nchat=%s", len(chat.Queries), string(chatBytes))
	}

	wantPromptTokens := len(strings.Fields(prompt))
	wantCompletionTokens := wantPromptTokens * 2
	wantTotalTokens := wantPromptTokens + wantCompletionTokens
	wantCost := float64(wantPromptTokens)*0.001 + float64(wantCompletionTokens)*0.002

	if math.Abs(chat.Queries[0].CostUSD-wantCost) > 1e-12 {
		t.Fatalf("query[0] cost_usd: got %v want %v", chat.Queries[0].CostUSD, wantCost)
	}
	if math.Abs(chat.TotalCostUSD()-wantCost) > 1e-12 {
		t.Fatalf("chat total cost: got %v want %v", chat.TotalCostUSD(), wantCost)
	}
	if math.Abs(info.CostUSD-wantCost) > 1e-12 {
		t.Fatalf("chat dir cost_usd: got %v want %v", info.CostUSD, wantCost)
	}
	wantCostStr := "$0.135"
	if info.Cost != wantCostStr {
		t.Fatalf("chat dir cost: got %q want %q", info.Cost, wantCostStr)
	}

	if chat.TokenUsage == nil {
		t.Fatalf("expected token usage to be populated")
	}
	if chat.TokenUsage.PromptTokens != wantPromptTokens {
		t.Fatalf("chat prompt tokens: got %d want %d", chat.TokenUsage.PromptTokens, wantPromptTokens)
	}
	if chat.TokenUsage.CompletionTokens != wantCompletionTokens {
		t.Fatalf("chat completion tokens: got %d want %d", chat.TokenUsage.CompletionTokens, wantCompletionTokens)
	}
	if chat.TokenUsage.TotalTokens != wantTotalTokens {
		t.Fatalf("chat total tokens: got %d want %d", chat.TokenUsage.TotalTokens, wantTotalTokens)
	}
	if info.InputTokens != wantPromptTokens {
		t.Fatalf("chat dir input_tokens: got %d want %d", info.InputTokens, wantPromptTokens)
	}
	if info.OutputTokens != wantCompletionTokens {
		t.Fatalf("chat dir output_tokens: got %d want %d", info.OutputTokens, wantCompletionTokens)
	}

	if len(chat.Messages) != 9 {
		t.Fatalf("expected 9 chat messages, got %d", len(chat.Messages))
	}
	if chat.Messages[1].Role != "user" || chat.Messages[1].Content != prompt {
		t.Fatalf("unexpected user message: %+v", chat.Messages[1])
	}
	if len(chat.Messages[2].ToolCalls) != 1 || chat.Messages[2].ToolCalls[0].Name != "pwd" {
		t.Fatalf("expected first tool call to be pwd, got %+v", chat.Messages[2].ToolCalls)
	}
	if len(chat.Messages[4].ToolCalls) != 1 || chat.Messages[4].ToolCalls[0].Name != "pwd" {
		t.Fatalf("expected second tool call to be pwd, got %+v", chat.Messages[4].ToolCalls)
	}
	if len(chat.Messages[6].ToolCalls) != 1 || chat.Messages[6].ToolCalls[0].Name != "ls" {
		t.Fatalf("expected third tool call to be ls, got %+v", chat.Messages[6].ToolCalls)
	}
	if !strings.Contains(chat.Messages[3].Content, workDir) {
		t.Fatalf("expected first tool output to contain cwd %q, got %q", workDir, chat.Messages[3].Content)
	}
	if !strings.Contains(chat.Messages[5].Content, workDir) {
		t.Fatalf("expected second tool output to contain cwd %q, got %q", workDir, chat.Messages[5].Content)
	}
	if strings.Contains(chat.Messages[7].Content, "unknown tool call: cat") {
		t.Fatalf("expected cat tool token to be ignored entirely, got %q", chat.Messages[7].Content)
	}
	if chat.Messages[8].Content != "done after tool for: "+prompt {
		t.Fatalf("unexpected final message content: %q", chat.Messages[8].Content)
	}
}
