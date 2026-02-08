package chat

import (
	"fmt"
	"os"
	"path"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SaveAsPreviousQuery saves the global-scoped chat at
// <claiConfDir>/conversations/globalScope.json with ID globalScope.
//
// Name kept for backwards compatibility.
func SaveAsPreviousQuery(claiConfDir string, chat pub_models.Chat) error {
	prevQueryChat := pub_models.Chat{
		Created:    time.Now(),
		ID:         globalScopeChatID,
		Profile:    chat.Profile,
		Messages:   chat.Messages,
		TokenUsage: chat.TokenUsage,
	}
	// This check avoid storing queries without any replies, which would most likely
	// flood the conversations needlessly
	if len(chat.Messages) > 2 {
		firstUserMsg, err := prevQueryChat.FirstUserMessage()
		if err != nil {
			return fmt.Errorf("failed to get first user message: %w", err)
		}
		convChat := pub_models.Chat{
			Created:    time.Now(),
			ID:         HashIDFromPrompt(firstUserMsg.Content),
			Profile:    chat.Profile,
			Messages:   chat.Messages,
			TokenUsage: chat.TokenUsage,
		}
		convPath := path.Join(claiConfDir, "conversations")
		if _, convDirExistsErr := os.Stat(convPath); convDirExistsErr != nil {
			os.MkdirAll(convPath, 0o755)
		}
		err = Save(convPath, convChat)
		if err != nil {
			return fmt.Errorf("failed to save previous query as new conversation: %w", err)
		}
	}

	return Save(path.Join(claiConfDir, "conversations"), prevQueryChat)
}

// LoadPrevQuery loads the global-scoped chat.
// Name kept for backwards compatibility.
func LoadPrevQuery(claiConfDir string) (pub_models.Chat, error) {
	c, err := LoadGlobalScope(claiConfDir)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("load global scope chat: %w", err)
	}
	return c, nil
}
