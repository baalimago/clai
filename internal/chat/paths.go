package chat

import (
	"fmt"
	"os"
	"path/filepath"
)

func conversationsDir(confDir string) string {
	return filepath.Join(confDir, "conversations")
}

func conversationFileName(chatID string) string {
	return chatID + ".json"
}

func conversationPath(confDir, chatID string) string {
	return filepath.Join(conversationsDir(confDir), conversationFileName(chatID))
}

func conversationPathFromDir(convDir, chatID string) string {
	return filepath.Join(convDir, conversationFileName(chatID))
}

func globalScopePath(confDir string) string {
	return filepath.Join(conversationsDir(confDir), globalScopeFile)
}

func prevQueryPath(confDir string) string {
	return filepath.Join(conversationsDir(confDir), prevQueryFile)
}

func dirscopeRoot(confDir string) string {
	return filepath.Join(conversationsDir(confDir), "dirs")
}

func dirscopePath(confDir, hash string) string {
	return filepath.Join(dirscopeRoot(confDir), conversationFileName(hash))
}

func currentWorkingDirectory() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current working directory: %w", err)
	}
	return wd, nil
}
