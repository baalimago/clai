package chat

import (
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal/diag"
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

// previousQueryStateDir holds the machine-local conversation state that is not
// a conversation: it sits one level below the conversations dir so the index,
// the asset sweep and the cloud sync allowlist skip it by construction.
func previousQueryStateDir(confDir string) string {
	return filepath.Join(conversationsDir(confDir), "state")
}

// PreviousQueryPath is the previous-query record under confDir.
func PreviousQueryPath(confDir string) string {
	return filepath.Join(previousQueryStateDir(confDir), "previousQuery.json")
}

func dirscopeRoot(confDir string) string {
	return filepath.Join(conversationsDir(confDir), "dirs")
}

// AssetsDir is the README asset directory under confDir.
func AssetsDir(confDir string) string {
	return assetsDirFromConvDir(conversationsDir(confDir))
}

func assetsDirFromConvDir(convDir string) string {
	return filepath.Join(convDir, "assets")
}

// reasoningDirFromConvDir is the per-chat sidecar directory holding opaque OpenAI
// reasoning items, keyed off the conversations dir (as Save/FromPath operate on).
func reasoningDirFromConvDir(convDir, chatID string) string {
	return filepath.Join(convDir, "reasoning", chatID)
}

// reasoningFileFromConvDir is the sidecar file for one Responses turn, named by its
// response id.
func reasoningFileFromConvDir(convDir, chatID, responseID string) string {
	return filepath.Join(reasoningDirFromConvDir(convDir, chatID), responseID+".json")
}

func dirscopePath(confDir, hash string) string {
	return filepath.Join(dirscopeRoot(confDir), conversationFileName(hash))
}

func currentWorkingDirectory() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", diag.Errorf("get current working directory: %w", err)
	}
	return wd, nil
}
