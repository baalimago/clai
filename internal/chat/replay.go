package chat

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/baalimago/clai/internal/utils"
)

// Replay prints the most recent message.
//
// If dirScoped is true, it prints the most recent message from the conversation
// bound to the current working directory (and errors if no binding exists).
// Otherwise it prints the most recent message from the global globalScope.json.
func Replay(raw bool, dirScoped bool) error {
	if dirScoped {
		return replayDirScoped(raw)
	}

	prevReply, err := LoadPrevQuery("")
	if err != nil {
		return fmt.Errorf("failed to load previous reply: %v", err)
	}
	amMessages := len(prevReply.Messages)
	if amMessages == 0 {
		return errors.New("failed to find any recent reply")
	}
	mostRecentMsg := prevReply.Messages[amMessages-1]
	return utils.AttemptPrettyPrint(nil, mostRecentMsg, "system", raw)
}

func replayDirScoped(raw bool) error {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("get config dir: %w", err)
	}

	cq := &ChatHandler{confDir: claiConfDir}
	ds, err := cq.LoadDirScope("")
	if err != nil {
		return fmt.Errorf("load dirscope: %w", err)
	}
	if ds.ChatID == "" {
		return errors.New("no directory-scoped conversation bound to current directory")
	}

	convPath := filepath.Join(claiConfDir, "conversations", ds.ChatID+".json")
	c, err := FromPath(convPath)
	if err != nil {
		return fmt.Errorf("load conversation for chat_id %q: %w", ds.ChatID, err)
	}
	if len(c.Messages) == 0 {
		return errors.New("directory-scoped conversation has no messages")
	}
	mostRecentMsg := c.Messages[len(c.Messages)-1]
	return utils.AttemptPrettyPrint(nil, mostRecentMsg, "system", raw)
}
