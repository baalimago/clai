package vendors

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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
	default:
		return pub_models.Input{}
	}
}
