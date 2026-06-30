package vendors

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var toolTokenPattern = regexp.MustCompile(`\btool_([a-zA-Z0-9_]+)\b`)

// Mock is a StreamCompleter that streams a fixed, mocked response.
type Mock struct {
	usage        *pub_models.Usage
	allowedTools map[string]struct{}
}

func (m *Mock) Setup() error {
	return nil
}

func (m *Mock) TokenUsage() *pub_models.Usage {
	return m.usage
}

func (m *Mock) RegisterTool(tool pub_models.LLMTool) {
	if m.allowedTools == nil {
		m.allowedTools = make(map[string]struct{})
	}
	m.allowedTools[tool.Specification().Name] = struct{}{}
}

func (m *Mock) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	ch := make(chan models.CompletionEvent, 2)
	go func() {
		defer close(ch)

		uMsg, _, err := chat.LastOfRole("user")
		if err != nil {
			m.usage = mockUsageForPrompt("")
			ch <- ""
			ch <- models.StopEvent{}
			return
		}

		nextTool, ok := nextToolCall(chat, m.allowedTools)
		if ok {
			inputs := inputsForTool(nextTool)
			m.usage = mockUsageForPrompt(uMsg.Content)
			ch <- pub_models.Call{
				ID:     "mock-call-" + nextTool,
				Name:   nextTool,
				Inputs: &inputs,
			}
			ch <- models.StopEvent{}
			return
		}

		m.usage = mockUsageForPrompt(uMsg.Content)
		if hasToolMessage(chat.Messages) {
			ch <- finalMockResponse(uMsg.Content)
			ch <- models.StopEvent{}
			return
		}

		ch <- uMsg.Content
		ch <- models.StopEvent{}
	}()
	return ch, nil
}

func mockUsageForPrompt(prompt string) *pub_models.Usage {
	promptTokens := len(strings.Fields(prompt))
	if promptTokens == 0 {
		promptTokens = 1
	}
	completionTokens := promptTokens * 2
	return &pub_models.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}

func hasToolMessage(messages []pub_models.Message) bool {
	for _, msg := range messages {
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}

func finalMockResponse(prompt string) string {
	return fmt.Sprintf("done after tool for: %s", prompt)
}

func nextToolCall(chat pub_models.Chat, allowedTools map[string]struct{}) (string, bool) {
	userMsg, _, err := chat.LastOfRole("user")
	if err != nil {
		return "", false
	}

	allMatches := toolTokenPattern.FindAllStringSubmatch(userMsg.Content, -1)
	if len(allMatches) == 0 {
		return "", false
	}

	executedCounts := map[string]int{}
	for _, msg := range chat.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.Name == "" {
				continue
			}
			executedCounts[call.Name]++
		}
	}

	for _, match := range allMatches {
		if len(match) != 2 {
			continue
		}
		toolName := match[1]
		if len(allowedTools) > 0 {
			if _, ok := allowedTools[toolName]; !ok {
				continue
			}
		}
		if executedCounts[toolName] > 0 {
			executedCounts[toolName]--
			continue
		}
		return toolName, true
	}

	return "", false
}

func inputsForTool(toolName string) pub_models.Input {
	switch toolName {
	case "ls":
		return pub_models.Input{"directory": "."}
	case "load_skill":
		input := pub_models.Input{"skill": "review"}
		if prompt := os.Getenv("CLAI_MOCK_LOAD_SKILL_NAME"); prompt != "" {
			input["skill"] = prompt
		}
		if prompt := os.Getenv("CLAI_MOCK_LOAD_SKILL_ARGS"); prompt != "" {
			input["arguments"] = prompt
		}
		return input
	case "search_conversations":
		input := pub_models.Input{"query": envOr("CLAI_MOCK_SEARCH_QUERY", "hello")}
		if d := os.Getenv("CLAI_MOCK_SEARCH_DIRECTORY"); d != "" {
			input["directory"] = d
		}
		if s := os.Getenv("CLAI_MOCK_SEARCH_SUBTREE"); s != "" {
			input["subtree"] = s == "true"
		}
		return input
	case "inspect_conversation":
		input := pub_models.Input{"chat_id": os.Getenv("CLAI_MOCK_INSPECT_CHAT_ID")}
		if r := os.Getenv("CLAI_MOCK_INSPECT_ROLE"); r != "" {
			input["role"] = r
		}
		if m := os.Getenv("CLAI_MOCK_INSPECT_MATCH"); m != "" {
			input["match"] = m
		}
		return input
	case "read_message":
		input := pub_models.Input{"chat_id": os.Getenv("CLAI_MOCK_READ_CHAT_ID")}
		if idx := os.Getenv("CLAI_MOCK_READ_INDEX"); idx != "" {
			if n, err := strconv.Atoi(idx); err == nil {
				input["message_index"] = n
			}
		}
		return input
	case "cmd":
		return pub_models.Input{"command": envOr("CLAI_MOCK_CMD_COMMAND", `printf mocked-cmd`)}
	case "freetext_command":
		return pub_models.Input{"command": envOr("CLAI_MOCK_CMD_COMMAND", `printf mocked-freetext-cmd`)}
	case "async_cmd_run":
		input := pub_models.Input{"command": envOr("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")}
		if rawArgs := os.Getenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS"); rawArgs != "" {
			input["args"] = splitMockArgs(rawArgs)
		}
		if cwd := os.Getenv("CLAI_MOCK_ASYNC_CMD_RUN_CWD"); cwd != "" {
			input["cwd"] = cwd
		}
		return input
	case "async_cmd_status":
		return pub_models.Input{"async_cmd_id": readAsyncCmdIDFromEnv("CLAI_MOCK_ASYNC_CMD_STATUS")}
	case "async_cmd_logs":
		if raw := os.Getenv("CLAI_MOCK_ASYNC_CMD_LOGS_DELAY_MS"); raw != "" {
			if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
				time.Sleep(time.Duration(ms) * time.Millisecond)
			}
		}
		return pub_models.Input{"async_cmd_id": readAsyncCmdIDFromEnv("CLAI_MOCK_ASYNC_CMD_LOGS")}
	case "async_cmd_cancel":
		return pub_models.Input{"async_cmd_id": readAsyncCmdIDFromEnv("CLAI_MOCK_ASYNC_CMD_CANCEL")}
	case "async_cmd_await":
		timeout := 1.0
		if raw := os.Getenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS"); raw != "" {
			if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
				timeout = parsed
			}
		}
		return pub_models.Input{
			"async_cmd_ids":   []string{readAsyncCmdIDFromEnv("CLAI_MOCK_ASYNC_CMD_AWAIT")},
			"timeout_seconds": timeout,
		}
	default:
		return pub_models.Input{}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func readAsyncCmdIDFromEnv(prefix string) string {
	if file := os.Getenv("CLAI_MOCK_ASYNC_CMD_ID_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	if file := os.Getenv(prefix + "_IDS_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	if asyncCmdID := os.Getenv(prefix + "_ASYNC_CMD_ID"); asyncCmdID != "" {
		return asyncCmdID
	}
	return "async_cmd_missing"
}

func splitMockArgs(raw string) []string {
	if raw == "" {
		return nil
	}
	var (
		ret     []string
		cur     strings.Builder
		inQuote bool
	)
	for _, r := range raw {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				ret = append(ret, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		ret = append(ret, cur.String())
	}
	return ret
}
