package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// runStoplossE2E runs the CLI in-process and returns the exit status plus the
// captured stdout and stderr. The run finalizer persists the conversation
// under <confDir>/conversations.
func runStoplossE2E(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	var status int
	stdout, stderr := captureStdoutStderr(t, func() {
		status = run(args)
	})
	return status, stdout, stderr
}

// loadSavedStoplossChat reads the conversation file written by the run
// finalizer (the promoted chat with the generated ID, not globalScope.json).
func loadSavedStoplossChat(t *testing.T, confDir string) pub_models.Chat {
	t.Helper()
	b := readStringFile(t, findSavedConversationFile(t, confDir))
	var chat pub_models.Chat
	if err := json.Unmarshal([]byte(b), &chat); err != nil {
		t.Fatalf("Unmarshal(saved conversation): %v", err)
	}
	return chat
}

// indexOfMessage returns the index of the first message matching pred, or -1.
func indexOfMessage(messages []pub_models.Message, pred func(pub_models.Message) bool) int {
	for i, m := range messages {
		if pred(m) {
			return i
		}
	}
	return -1
}

// assertValidToolExchangesE2E fails when an assistant tool-call message has no
// immediately following tool result, or when the consecutive tool results do
// not cover every declared call (no dangling calls; R11-01: one result per
// declared call).
func assertValidToolExchangesE2E(t *testing.T, messages []pub_models.Message) {
	t.Helper()
	for i, m := range messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		results := 0
		for j := i + 1; j < len(messages) && messages[j].Role == "tool"; j++ {
			results++
		}
		if results != len(m.ToolCalls) {
			t.Fatalf("assistant tool-call at index %d declares %d calls but %d tool results follow; transcript: %+v", i, len(m.ToolCalls), results, messages)
		}
	}
}

// writeStoplossTextConfig writes the given textConfig.json for a stoploss e2e
// run, always pinning the mock model and tooling on.
func writeStoplossTextConfig(t *testing.T, confDir string, extra map[string]any) {
	t.Helper()
	cfg := map[string]any{
		"model":     "test",
		"use-tools": true,
	}
	maps.Copy(cfg, extra)
	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), cfg)
}

// Test_e2e_stoploss_handover_injection_and_summary proves the crossing flow
// through the real CLI with the mock vendor: usage 6 crosses max-tokens 5 on
// the first tool step, the handover user message lands AFTER the crossing
// tool result, the mock then produces the summary, and the run exits 0 with
// the stoploss notice printed (phase 6 case 1).
func Test_e2e_stoploss_handover_injection_and_summary(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeStoplossTextConfig(t, confDir, map[string]any{
		"stoploss": map[string]any{
			"max-tokens":                       5,
			"max-tokens-handover-instructions": "wrap up now",
		},
	})

	status, stdout, stderr := runStoplossE2E(t, "-r", "-cm", "test", "q", "run", "tool_ls")
	combined := stdout + stderr
	if status != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !strings.Contains(combined, "stoploss: context usage") {
		t.Fatalf("expected the stoploss notice on crossing, got %q", combined)
	}

	chat := loadSavedStoplossChat(t, confDir)
	lsIdx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
		return m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].Name == "ls"
	})
	if lsIdx == -1 || lsIdx+1 >= len(chat.Messages) || chat.Messages[lsIdx+1].Role != "tool" {
		t.Fatalf("expected an ls assistant tool-call followed by a tool result, transcript: %+v", chat.Messages)
	}
	handoverIdx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
		return m.Role == "user" && m.Content == "wrap up now"
	})
	if handoverIdx == -1 {
		t.Fatalf("expected the handover user message, transcript: %+v", chat.Messages)
	}
	// Chat order stays valid: [assistant tool-call] [tool result] [handover user msg].
	if lsIdx+1 > handoverIdx {
		t.Fatalf("expected the handover message AFTER the ls tool result, got lsResult=%d handover=%d", lsIdx+1, handoverIdx)
	}
	last := chat.Messages[len(chat.Messages)-1]
	if last.Role != "assistant" || last.Content != "done after tool for: wrap up now" {
		t.Fatalf("expected the final summary as the last assistant message, got %+v", last)
	}
	assertValidToolExchangesE2E(t, chat.Messages)
}

// Test_e2e_stoploss_post_handover_refusal_visible proves the post-handover
// refusal ladder through the real CLI: the handover message contains real
// tool tokens, every post-handover tool call is refused BEFORE its
// implementation runs (the tool result carries the ladder text, not tool
// output), and the run still ends with a final assistant summary and exit 0
// (phase 6 case 2).
func Test_e2e_stoploss_post_handover_refusal_visible(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeStoplossTextConfig(t, confDir, map[string]any{
		"stoploss": map[string]any{
			"max-tokens":                       5,
			"max-tokens-handover-instructions": "tool_ls tool_cat tool_rg tool_git",
		},
	})

	status, stdout, stderr := runStoplossE2E(t, "-r", "-cm", "test", "q", "run", "tool_ls")
	if status != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}

	chat := loadSavedStoplossChat(t, confDir)
	handoverIdx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
		return m.Role == "user" && m.Content == "tool_ls tool_cat tool_rg tool_git"
	})
	if handoverIdx == -1 {
		t.Fatalf("expected the handover user message, transcript: %+v", chat.Messages)
	}

	refusals := 0
	for i := handoverIdx + 1; i < len(chat.Messages); i++ {
		m := chat.Messages[i]
		if m.Role != "tool" {
			continue
		}
		if strings.HasPrefix(m.Content, "ERROR: No more tool calls allowed") {
			refusals++
			continue
		}
		t.Fatalf("post-handover tool result must carry only refusal text (side effect must not run), got %q", m.Content)
	}
	if refusals == 0 {
		t.Fatalf("expected at least one post-handover refusal, transcript: %+v", chat.Messages)
	}

	last := chat.Messages[len(chat.Messages)-1]
	if last.Role != "assistant" || last.Content == "" {
		t.Fatalf("expected the final summary as the last assistant message, got %+v", last)
	}
	assertValidToolExchangesE2E(t, chat.Messages)
}

// Test_e2e_stoploss_max_tool_calls_still_enforced pins the positive-budget
// ladder through the real CLI: with max-tool-calls 1 the first call executes
// (remaining-count prefix on its result), the second call is refused before
// its implementation runs, and the run exits 0 (phase 6 case 3, R3-04).
func Test_e2e_stoploss_max_tool_calls_still_enforced(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeStoplossTextConfig(t, confDir, map[string]any{"max-tool-calls": 1})

	status, stdout, stderr := runStoplossE2E(t, "-r", "-cm", "test", "q", "run", "tool_ls", "tool_cat")
	if status != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}

	chat := loadSavedStoplossChat(t, confDir)
	lsIdx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
		return m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].Name == "ls"
	})
	if lsIdx == -1 || lsIdx+1 >= len(chat.Messages) {
		t.Fatalf("expected an ls assistant tool-call with a result, transcript: %+v", chat.Messages)
	}
	if got := chat.Messages[lsIdx+1].Content; !strings.HasPrefix(got, "[ Tool calls remaining: 1 ] ") {
		t.Fatalf("expected the remaining-count prefix on the allowed result, got %q", got)
	}

	catIdx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
		return m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].Name == "cat"
	})
	if catIdx == -1 || catIdx+1 >= len(chat.Messages) {
		t.Fatalf("expected a cat assistant tool-call with a result, transcript: %+v", chat.Messages)
	}
	if got := chat.Messages[catIdx+1].Content; !strings.HasPrefix(got, "ERROR: No more tool calls allowed") {
		t.Fatalf("expected the refusal ladder text for the over-budget cat call (no invocation error), got %q", got)
	}
	assertValidToolExchangesE2E(t, chat.Messages)
}

// Test_e2e_stoploss_max_tool_calls_zero_is_unlimited pins the D5 semantics
// through the real CLI: max-tool-calls 0 means no limit, both tools execute
// with visible outputs, and no refusal text appears anywhere (phase 6 case 4).
func Test_e2e_stoploss_max_tool_calls_zero_is_unlimited(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeStoplossTextConfig(t, confDir, map[string]any{"max-tool-calls": 0})

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	status, stdout, stderr := runStoplossE2E(t, "-r", "-cm", "test", "q", "run", "tool_ls", "tool_pwd")
	if status != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "No more tool calls allowed") {
		t.Fatalf("expected no refusal text with max-tool-calls 0, got %q", stdout+stderr)
	}

	chat := loadSavedStoplossChat(t, confDir)
	executed := map[string]bool{}
	for i, m := range chat.Messages {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, "No more tool calls allowed") {
			t.Fatalf("expected no refusal text in the conversation, got %q", m.Content)
		}
		if i > 0 && len(chat.Messages[i-1].ToolCalls) > 0 {
			executed[chat.Messages[i-1].ToolCalls[0].Name] = true
		}
	}
	if !executed["ls"] || !executed["pwd"] {
		t.Fatalf("expected both tools to execute, executed=%v transcript=%+v", executed, chat.Messages)
	}
	pwdIdx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
		return m.Role == "tool" && strings.Contains(m.Content, cwd)
	})
	if pwdIdx == -1 {
		t.Fatalf("expected the pwd output to contain the cwd %q, transcript=%+v", cwd, chat.Messages)
	}
	assertValidToolExchangesE2E(t, chat.Messages)
}

// Test_e2e_stoploss_legacy_config_compat proves the token-warn-limit sunset
// through the real CLI: a config still carrying the old key loads, runs to
// completion, and never blocks on stdin (phase 6 case 5, R8-03).
func Test_e2e_stoploss_legacy_config_compat(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{
		"model":            "test",
		"token-warn-limit": 333333,
	})

	status, stdout, stderr := runStoplossE2E(t, "-n", "-r", "-cm", "test", "q", "hello")
	combined := stdout + stderr
	if status != 0 {
		t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !strings.Contains(stdout, "hello") {
		t.Fatalf("expected the query to run to completion, got %q", stdout)
	}
	if strings.Contains(combined, "token-warn") {
		t.Fatalf("legacy key must be ignored without warning, got %q", combined)
	}
}

// Test_e2e_stoploss_flag_overrides_file proves the Phase 4 flag cascade
// through the real CLI: -max-tokens and -max-tool-calls beat the file values
// for this run (phase 6 case 6).
func Test_e2e_stoploss_flag_overrides_file(t *testing.T) {
	t.Run("max-tokens flag overrides file limit", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		writeStoplossTextConfig(t, confDir, map[string]any{
			"stoploss": map[string]any{
				"max-tokens":                       100000,
				"max-tokens-handover-instructions": "wrap up now",
			},
		})

		status, stdout, stderr := runStoplossE2E(t, "-r", "-cm", "test", "-max-tokens=5", "q", "run", "tool_ls")
		combined := stdout + stderr
		if status != 0 {
			t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		if !strings.Contains(combined, "stoploss: context usage") {
			t.Fatalf("expected the flag limit to take effect (crossing notice), got %q", combined)
		}
		chat := loadSavedStoplossChat(t, confDir)
		if idx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
			return m.Role == "user" && m.Content == "wrap up now"
		}); idx == -1 {
			t.Fatalf("expected the handover message under the flag limit, transcript: %+v", chat.Messages)
		}
	})

	t.Run("max-tool-calls flag overrides file limit", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		writeStoplossTextConfig(t, confDir, map[string]any{"max-tool-calls": 5})

		status, stdout, stderr := runStoplossE2E(t, "-r", "-cm", "test", "-max-tool-calls=1", "q", "run", "tool_ls", "tool_cat")
		if status != 0 {
			t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		chat := loadSavedStoplossChat(t, confDir)
		if idx := indexOfMessage(chat.Messages, func(m pub_models.Message) bool {
			return m.Role == "tool" && strings.HasPrefix(m.Content, "ERROR: No more tool calls allowed")
		}); idx == -1 {
			t.Fatalf("expected the flag budget (1) to refuse the second call, transcript: %+v", chat.Messages)
		}
	})
}
