package reply

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
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
			ID:       chat.IDFromPrompt(firstUserMsg.Content),
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
// If claiConfDir is left empty, it will be re-constructed. The technical debt
// is piling up quite fast here
func Load(claiConfDir string) (models.Chat, error) {
	if claiConfDir == "" {
		dir, err := utils.GetClaiConfigDir()
		if err != nil {
			return models.Chat{}, fmt.Errorf("failed to find home dir: %v", err)
		}
		claiConfDir = dir
	}

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
