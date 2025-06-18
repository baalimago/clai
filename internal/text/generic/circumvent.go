package generic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/models"
)

// CircumventRateLimit summarizes the conversation and restarts it with
// instructions for using the recall tool.
func CircumventRateLimit(ctx context.Context, cq models.ChatQuerier, chat models.Chat, inputCount, tokensRemaining, maxInputTokens int) error {
	// Build a textual representation of the conversation
	var conv strings.Builder
	for i, m := range chat.Messages {
		conv.WriteString(fmt.Sprintf("[%d][%s]: %s\n", i, m.Role, m.Content))
	}

	summaryChat := models.Chat{
		Messages: []models.Message{
			{
				Role:    "system",
				Content: "You are assisting with circumventing a token limit. Summarize the conversation below. Include key information like file paths, commit hashes, lines of code, function names or debugging steps. Use indices to reference earlier messages in the form '" + chat.ID + ":<index>'.",
			},
			{Role: "user", Content: conv.String()},
		},
	}

	summarized, err := cq.TextQuery(ctx, summaryChat)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}
	if len(summarized.Messages) == 0 {
		return errors.New("summary returned no messages")
	}
	summary := summarized.Messages[len(summarized.Messages)-1].Content

	sysMsg, _ := chat.FirstSystemMessage()
	firstUser, _ := chat.FirstUserMessage()
	last := chat.Messages[len(chat.Messages)-1]

	instructions := summary + "\n\nUse the recall tool to read previous messages using the indices above. Example: recall{\"conversation\":\"" + chat.ID + "\", \"index\":0}."

	newChat := models.Chat{
		Created:  time.Now(),
		ID:       chat.ID,
		Messages: []models.Message{sysMsg, firstUser, {Role: "system", Content: instructions}},
	}

	if last.Role == "user" && last.Content != firstUser.Content {
		newChat.Messages = append(newChat.Messages, last)
	}

	if _, err := cq.TextQuery(ctx, newChat); err != nil {
		return fmt.Errorf("failed to continue with summarized chat: %w", err)
	}

	return nil
}
