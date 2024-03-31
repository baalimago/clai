package chat

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func FromPath(path string) (models.Chat, error) {
	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("DEBUG_REPLY_MODE")) {
		ancli.PrintOK(fmt.Sprintf("reading chat from '%v'\n", path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to read file: %w", err)
	}
	var chat models.Chat
	err = json.Unmarshal(b, &chat)
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to decode JSON: %w", err)
	}

	return chat, nil
}

func Save(saveAt string, chat models.Chat) error {
	b, err := json.Marshal(chat)
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	fileName := saveAt + "/.clai/conversations/" + chat.ID + ".json"
	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("DEBUG_REPLY_MODE")) {
		ancli.PrintOK(fmt.Sprintf("saving chat to: '%v', content (on new line):\n'%v'\n", fileName, string(b)))
	}
	return os.WriteFile(fileName, b, 0o644)
}
