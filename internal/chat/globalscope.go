package chat

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

const (
	globalScopeFile   = "globalScope.json"
	prevQueryFile     = "prevQuery.json"
	globalScopeChatID = "globalScope"
)

// LoadGlobalScope loads the global scoped chat from <confDir>/conversations/globalScope.json.
//
// Migration:
// If the legacy file <confDir>/conversations/prevQuery.json exists, it is moved to
// globalScope.json, overwriting any existing globalScope.json.
func LoadGlobalScope(confDir string) (pub_models.Chat, error) {
	if confDir == "" {
		dir, err := utils.GetClaiConfigDir()
		if err != nil {
			return pub_models.Chat{}, fmt.Errorf("get clai config dir: %w", err)
		}
		confDir = dir
	}

	convDir := filepath.Join(confDir, "conversations")
	newPath := filepath.Join(convDir, globalScopeFile)
	oldPath := filepath.Join(convDir, prevQueryFile)

	if _, err := os.Stat(oldPath); err == nil {
		b, err := os.ReadFile(oldPath)
		if err != nil {
			return pub_models.Chat{}, fmt.Errorf("read legacy global chat %q: %w", oldPath, err)
		}
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			return pub_models.Chat{}, fmt.Errorf("ensure conversations dir: %w", err)
		}
		if err := os.WriteFile(newPath, b, 0o644); err != nil {
			return pub_models.Chat{}, fmt.Errorf("write migrated global chat %q: %w", newPath, err)
		}
		if err := os.Remove(oldPath); err != nil {
			return pub_models.Chat{}, fmt.Errorf("remove legacy global chat %q: %w", oldPath, err)
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pub_models.Chat{}, fmt.Errorf("stat legacy global chat %q: %w", oldPath, err)
	}

	c, err := FromPath(newPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ancli.PrintWarn("no previous query found\n")
			return pub_models.Chat{}, nil
		}
		return pub_models.Chat{}, fmt.Errorf("read global scope chat %q: %w", newPath, err)
	}

	if c.ID != globalScopeChatID {
		c.ID = globalScopeChatID
		if err := Save(convDir, c); err != nil {
			return pub_models.Chat{}, fmt.Errorf("rewrite global scope chat with normalized id: %w", err)
		}
	}

	return c, nil
}
