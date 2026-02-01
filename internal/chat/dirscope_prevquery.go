package chat

import (
	"fmt"
	"path/filepath"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// SaveDirScopedAsPrevQuery overwrites <confDir>/conversations/prevQuery.json with the
// directory-scoped conversation bound to the current working directory.
//
// This allows us to reuse the existing global "reply" plumbing (-re) while letting
// users opt into directory-scoped replies via -dre/-dir-reply.
func SaveDirScopedAsPrevQuery(confDir string) (origChatID string, err error) {
	cq := &ChatHandler{confDir: confDir}
	ds, ok, err := cq.LoadDirScope("")
	if err != nil {
		return "", fmt.Errorf("load dirscope: %w", err)
	}
	if !ok || ds.ChatID == "" {
		return "", fmt.Errorf("no directory-scoped conversation bound to current directory")
	}

	convPath := filepath.Join(confDir, "conversations", ds.ChatID+".json")
	c, err := FromPath(convPath)
	if err != nil {
		return "", fmt.Errorf("load conversation for chat_id %q: %w", ds.ChatID, err)
	}

	var msgs []pub_models.Message
	for _, m := range c.Messages {
		msgs = append(msgs, pub_models.Message{Role: m.Role, Content: m.Content})
	}

	if err := SaveAsPreviousQuery(confDir, msgs); err != nil {
		return "", fmt.Errorf("save as previous query: %w", err)
	}
	return ds.ChatID, nil
}
