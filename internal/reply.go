package internal

import (
	"fmt"
	"os"
)

func ReadPreviousQuery() (Chat, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Chat{}, fmt.Errorf("failed to get home dir: %w", err)
	}
	return getChatFromPath(home + "/.clai/conversations/prevQuery.json")
}

func (cq *ChatModelQuerier) SaveAsPreviousQuery(msgs []Message) error {
	chat := Chat{
		ID:       "prevQuery",
		Messages: msgs,
	}
	return cq.saveChat(chat)
}
