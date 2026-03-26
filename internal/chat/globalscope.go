package chat

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	traceChatf("load global scope start conf_dir=%q", confDir)
	if confDir == "" {
		dir, err := utils.GetClaiConfigDir()
		if err != nil {
			return pub_models.Chat{}, fmt.Errorf("get clai config dir: %w", err)
		}
		confDir = dir
		traceChatf("load global scope resolved empty conf_dir to=%q", confDir)
	}

	convDir := conversationsDir(confDir)
	newPath := globalScopePath(confDir)
	oldPath := prevQueryPath(confDir)
	traceChatf("load global scope paths conv_dir=%q new_path=%q old_path=%q", convDir, newPath, oldPath)

	if _, err := os.Stat(oldPath); err == nil {
		traceChatf("load global scope migrating legacy file from=%q to=%q", oldPath, newPath)
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
	traceChatf("global scope msgs: %v, queries: %v", len(c.Messages), len(c.Queries))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			traceChatf("load global scope no previous query at path=%q", newPath)
			ancli.PrintWarn("no previous query found\n")
			return pub_models.Chat{}, nil
		}
		return pub_models.Chat{}, fmt.Errorf("read global scope chat %q: %w", newPath, err)
	}
	traceChatf("load global scope loaded path=%q chat_id=%q messages=%d", newPath, c.ID, len(c.Messages))

	if c.ID != globalScopeChatID {
		traceChatf("load global scope normalizing chat id from=%q to=%q", c.ID, globalScopeChatID)
		c.ID = globalScopeChatID
		if err := Save(convDir, c); err != nil {
			return pub_models.Chat{}, fmt.Errorf("rewrite global scope chat with normalized id: %w", err)
		}
	}

	return c, nil
}
