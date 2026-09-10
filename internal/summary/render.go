package summary

import (
	"fmt"
	"strings"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const ellipsis = "…"

// renderTranscript renders the user messages and the last assistant message
// into one transcript block followed by the submission instruction.
func renderTranscript(chat pub_models.Chat) string {
	var lines []string
	lastAssistant := -1
	for i, msg := range chat.Messages {
		if msg.Role == "assistant" && messageText(msg) != "" {
			lastAssistant = i
		}
	}
	for i, msg := range chat.Messages {
		if msg.Role != "user" && i != lastAssistant {
			continue
		}
		text := messageText(msg)
		if text == "" {
			continue
		}
		lines = append(lines, msg.Role+": "+text)
	}
	body := capRunes(strings.Join(lines, "\n"), InputMaxRunes)
	return fmt.Sprintf("<transcript>\n%s\n</transcript>\n\nCall %s with `title` (one line, at most %d runes) and `summary` (at most %d runes) describing the conversation above.",
		body, ToolName, TitleMaxRunes, SummaryMaxRunes)
}

func messageText(msg pub_models.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	var parts []string
	for _, part := range msg.ContentParts {
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, " ")
}

func capRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + ellipsis
}
