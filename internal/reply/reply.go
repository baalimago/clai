package reply

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// SaveAsPreviousQuery at claiConfDir/conversations/prevQuery.json with ID prevQuery
func SaveAsPreviousQuery(claiConfDir string, c models.Chat) error {
	prevQueryChat := models.Chat{
		Created:  time.Now(),
		ID:       "prevQuery",
		Messages: c.Messages,
	}
	// This check avoid storing queries without any replies, which would most likely
	// flood the conversations needlessly
	if len(c.Messages) > 2 {
		if misc.Truthy(os.Getenv("DEBUG_ID")) {
			ancli.PrintOK(fmt.Sprintf("now the chat id is: %v", c.ID))
		}
		err := chat.Save(path.Join(claiConfDir, "conversations"), c)
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
		confDir, err := os.UserConfigDir()
		if err != nil {
			return models.Chat{}, fmt.Errorf("failed to find home dir: %v", err)
		}
		claiConfDir = path.Join(confDir, ".clai")
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
