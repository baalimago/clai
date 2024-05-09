package reply

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// SaveAsPreviousQuery at claiConfDir/conversations/prevQuery.json with ID prevQuery
func SaveAsPreviousQuery(claiConfDir string, msgs []models.Message) error {
	prevQueryChat := models.Chat{
		Created:  time.Now(),
		ID:       "prevQuery",
		Messages: msgs,
	}
	// This check avoid storing queries without any replies, which would most likely
	// flood the conversations needlessly
	if len(msgs) > 2 {
		firstUserMsg, err := prevQueryChat.FirstUserMessage()
		if err != nil {
			return fmt.Errorf("failed to get first user message: %w", err)
		}
		convChat := models.Chat{
			Created:  time.Now(),
			ID:       chat.IdFromPrompt(firstUserMsg.Content),
			Messages: msgs,
		}
		err = chat.Save(path.Join(claiConfDir, "conversations"), convChat)
		if err != nil {
			return fmt.Errorf("failed to save previous query as new conversation: %w", err)
		}
	}

	return chat.Save(path.Join(claiConfDir, "conversations"), prevQueryChat)
}

// Load the prevQuery.json from the claiConfDir/conversations directory
func Load(claiConfDir string) (models.Chat, error) {
	c, err := chat.FromPath(path.Join(claiConfDir, "conversations", "prevQuery.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ancli.PrintWarn("no previous query found\n")
		} else {
			return models.Chat{}, fmt.Errorf("failed to read from path: %w", err)
		}
	}
	return c, nil
}
