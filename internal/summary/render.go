package summary

import (
	"fmt"
	"strings"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	ellipsis = "…"
	// A loop-phase transcript gives the goal and the previous handover their
	// own budgets. Head-preserving truncation of the whole transcript would cut
	// the handover, which is the only evidence of progression.
	loopGoalInputRunes     = 600
	loopHandoverInputRunes = 1300
)

// renderTranscript renders the user messages and the last assistant message
// into one transcript block followed by the submission instruction.
func renderTranscript(chat pub_models.Chat) string {
	return renderTranscriptBlock(transcriptText(chat), SummaryMaxRunes, "describing the conversation above")
}

// renderLoopPhaseTranscript renders the goal and the previous iteration's
// handover. The repeated goal in the continuation prompt is skipped: it adds
// no progression and would push the handover out of a capped transcript.
func renderLoopPhaseTranscript(chat pub_models.Chat) string {
	goal := firstUserMessageText(chat)
	if goal == "" {
		goal = "(not recorded)"
	}
	handover := lastAssistantMessageText(chat)
	if handover == "" {
		handover = "(no previous iteration)"
	}
	body := "goal: " + capRunes(goal, loopGoalInputRunes) +
		"\n\nprevious iteration handover: " + capRunes(handover, loopHandoverInputRunes)
	return renderTranscriptBlock(body, LoopPhaseSummaryMaxRunes,
		"describing what the previous iteration achieved and what remains toward the goal")
}

// renderLoopPlanTranscript renders the goal of a loop's first iteration. There
// is no handover yet, so the summary is the approach toward the goal.
func renderLoopPlanTranscript(chat pub_models.Chat) string {
	goal := firstUserMessageText(chat)
	if goal == "" {
		goal = "(not recorded)"
	}
	body := "goal: " + capRunes(goal, loopGoalInputRunes)
	return renderTranscriptBlock(body, LoopPhaseSummaryMaxRunes,
		"describing the approach the loop will take toward the goal, without restating the goal")
}

func renderTranscriptBlock(body string, summaryMax int, purpose string) string {
	return fmt.Sprintf("<transcript>\n%s\n</transcript>\n\nCall %s with `title` (one line, at most %d runes) and `summary` (at most %d runes) %s.",
		body, ToolName, TitleMaxRunes, summaryMax, purpose)
}

func transcriptText(chat pub_models.Chat) string {
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
	return capRunes(strings.Join(lines, "\n"), InputMaxRunes)
}

func firstUserMessageText(chat pub_models.Chat) string {
	for _, msg := range chat.Messages {
		if msg.Role != "user" {
			continue
		}
		if text := messageText(msg); text != "" {
			return text
		}
	}
	return ""
}

func lastAssistantMessageText(chat pub_models.Chat) string {
	for i := len(chat.Messages) - 1; i >= 0; i-- {
		if chat.Messages[i].Role != "assistant" {
			continue
		}
		if text := messageText(chat.Messages[i]); text != "" {
			return text
		}
	}
	return ""
}

func messageText(msg pub_models.Message) string {
	return msg.Describe()
}

func capRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + ellipsis
}
