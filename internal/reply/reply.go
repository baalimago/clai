package reply

import (
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func SaveAsPreviousQuery(claiConfDir string, msgs []models.Message) error {
	c := models.Chat{
		ID:       "prevQuery",
		Messages: msgs,
	}
	return chat.Save(path.Join(claiConfDir, "conversations"), c)
}

func Load(claiConfDir string) (models.Chat, error) {
	c, err := chat.FromPath(path.Join(claiConfDir, "conversations", fmt.Sprintf("prevQuery.json")))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ancli.PrintWarn("no previous query found\n")
		} else {
			return models.Chat{}, fmt.Errorf("failed to read from path: %w", err)
		}
	}
	return c, nil
}
