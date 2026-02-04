package chat

import (
	"fmt"
	"path"

	"github.com/baalimago/clai/internal/utils"
)

// LoadDirScopeChatID loads the bound chat id for the current working directory.
// Returns "" if no binding exists.
func LoadDirScopeChatID(claiConfDir string) (string, error) {
	if claiConfDir == "" {
		var err error
		claiConfDir, err = utils.GetClaiConfigDir()
		if err != nil {
			return "", fmt.Errorf("get clai config dir: %w", err)
		}
	}

	cq := &ChatHandler{
		confDir: claiConfDir,
		convDir: path.Join(claiConfDir, "conversations"),
	}
	ds, ok, err := cq.LoadDirScope("")
	if err != nil {
		return "", fmt.Errorf("load dir scope: %w", err)
	}
	if !ok {
		return "", nil
	}
	return ds.ChatID, nil
}
