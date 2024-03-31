package reply

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func SaveAsPreviousQuery(msgs []models.Message) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	c := models.Chat{
		ID:       "prevQuery",
		Messages: msgs,
	}
	return chat.Save(fmt.Sprintf("%v/.clai", home), c)
}

func Load() (models.Chat, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to get home dir: %w", err)
	}
	c, err := chat.FromPath(home + "/.clai/conversations/prevQuery.json")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ancli.PrintWarn("no previous query found\n")
		} else {
			return models.Chat{}, fmt.Errorf("failed to read from path: %w", err)
		}
	}
	return c, nil
}
