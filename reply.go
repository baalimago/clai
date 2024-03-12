package main

import (
	"fmt"
	"os"
)

func readPreviousQuery() (Chat, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Chat{}, fmt.Errorf("failed to get home dir: %w", err)
	}
	return getChatFromPath(home + "/.clai/conversations/prevQuery.json")
}

func saveAsPreviousQuery(msgs []Message) error {
	chat := Chat{
		ID:       "prevQuery",
		Messages: msgs,
	}
	return saveChat(chat)
}
