package reply

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// SaveAsPreviousQuery at claiConfDir/conversations/prevQuery.json with ID prevQuery
func SaveAsPreviousQuery(claiConfDir string, msgs []pub_models.Message) error {
	prevQueryChat := pub_models.Chat{
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
		convChat := pub_models.Chat{
			Created:  time.Now(),
			ID:       chat.IDFromPrompt(firstUserMsg.Content),
			Messages: msgs,
		}
		convPath := path.Join(claiConfDir, "conversations")
		if _, convDirExistsErr := os.Stat(convPath); convDirExistsErr != nil {
			os.MkdirAll(convPath, 0o755)
		}
		err = chat.Save(convPath, convChat)
		if err != nil {
			return fmt.Errorf("failed to save previous query as new conversation: %w", err)
		}
	}

	return chat.Save(path.Join(claiConfDir, "conversations"), prevQueryChat)
}

// Load the prevQuery.json from the claiConfDir/conversations directory
// If claiConfDir is left empty, it will be re-constructed. The technical debt
// is piling up quite fast here
func Load(claiConfDir string) (pub_models.Chat, error) {
	if claiConfDir == "" {
		dir, err := utils.GetClaiConfigDir()
		if err != nil {
			return pub_models.Chat{}, fmt.Errorf("failed to find home dir: %v", err)
		}
		claiConfDir = dir
	}

	c, err := chat.FromPath(path.Join(claiConfDir, "conversations", "prevQuery.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ancli.PrintWarn("no previous query found\n")
		} else {
			return pub_models.Chat{}, fmt.Errorf("failed to read from path: %w", err)
		}
	}
	return c, nil
}
