package chat

import (
	"fmt"
	"path/filepath"
)

// SaveDirScopedAsPrevQuery overwrites <confDir>/conversations/globalScope.json with the
// directory-scoped conversation bound to the current working directory.
//
// This allows us to reuse the existing global "reply" plumbing (-re) while letting
// users opt into directory-scoped replies via -dre/-dir-reply.
func SaveDirScopedAsPrevQuery(confDir string) (origChatID string, err error) {
	cq := &ChatHandler{confDir: confDir}
	ds, err := cq.LoadDirScope("")
	if err != nil {
		return "", fmt.Errorf("load dirscope: %w", err)
	}
	if ds.ChatID == "" {
		return "", fmt.Errorf("no directory-scoped conversation bound to current directory")
	}

	convPath := filepath.Join(confDir, "conversations", ds.ChatID+".json")
	c, err := FromPath(convPath)
	if err != nil {
		return "", fmt.Errorf("load conversation for chat_id %q: %w", ds.ChatID, err)
	}

	if err := SaveAsPreviousQuery(confDir, c); err != nil {
		return "", fmt.Errorf("save as previous query: %w", err)
	}
	return ds.ChatID, nil
}
