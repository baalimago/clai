package vendors

import (
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// normalizeToolCallSequence transforms parallel/interleaved tool call
// patterns into the strict sequential format required by OpenAI-compatible
// APIs: assistant(tool_calls) → tool_results → assistant → ...
//
// It handles two common patterns observed in external tool logs:
//  1. Consecutive tool-call-only assistant messages — merged into one.
//  2. Interleaved tool-call-only assistants that appear between a pending
//     batch and its remaining tool results — merged into the pending batch.
func NormalizeToolCallSequence(msgs []pub_models.Message) []pub_models.Message {
	if len(msgs) == 0 {
		return msgs
	}
	// Pass 1: merge consecutive tool-call-only assistant messages.
	msgs = MergeConsecutiveToolCallOnlyAssistantMessages(msgs)
	// Pass 2: merge interleaved tool-call-only assistants.
	for i := 1; i < len(msgs); i++ {
		if !IsToolCallOnlyAssistant(msgs[i]) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if msgs[j].Role == "assistant" && msgs[j].Content != "" {
				break
			}
			if msgs[j].Role == "user" && msgs[j].Content != "" {
				break
			}
			if IsToolCallOnlyAssistant(msgs[j]) {
				tcIDs := make(map[string]bool)
				for _, tc := range msgs[j].ToolCalls {
					tcIDs[tc.ID] = true
				}
				found := make(map[string]bool)
				for k := j + 1; k < i; k++ {
					if msgs[k].Role == "tool" {
						found[msgs[k].ToolCallID] = true
					}
				}
				unresolved := false
				for id := range tcIDs {
					if !found[id] {
						unresolved = true
						break
					}
				}
				if unresolved {
					msgs[j].ToolCalls = append(msgs[j].ToolCalls, msgs[i].ToolCalls...)
					if msgs[i].ReasoningContent != "" {
						msgs[j].ReasoningContent = JoinNonEmpty(msgs[j].ReasoningContent, msgs[i].ReasoningContent)
					}
					msgs = append(msgs[:i], msgs[i+1:]...)
					i--
				}
				break
			}
		}
	}
	return msgs
}

// MergeConsecutiveToolCallOnlyAssistantMessages merges consecutive assistant
// messages that contain only tool_calls (no text content) into a single message
// with all tool_calls combined.
func MergeConsecutiveToolCallOnlyAssistantMessages(msgs []pub_models.Message) []pub_models.Message {
	merged := make([]pub_models.Message, 0, len(msgs))
	for _, m := range msgs {
		if IsToolCallOnlyAssistant(m) && len(merged) > 0 {
			last := &merged[len(merged)-1]
			if IsToolCallOnlyAssistant(*last) {
				last.ToolCalls = append(last.ToolCalls, m.ToolCalls...)
				if m.ReasoningContent != "" {
					last.ReasoningContent = JoinNonEmpty(last.ReasoningContent, m.ReasoningContent)
				}
				continue
			}
		}
		merged = append(merged, m)
	}
	return merged
}

func IsToolCallOnlyAssistant(m pub_models.Message) bool {
	return m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) > 0
}

// truncateOneLine collapses newlines to spaces, trims, and truncates to max.
func TruncateOneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// JoinNonEmpty joins two strings with "\n" if both are non-empty.
// If one is empty, the other is returned as-is.
func JoinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
}
