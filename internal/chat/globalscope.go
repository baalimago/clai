package chat

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
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
func LoadGlobalScope(ctx context.Context, confDir string) (pub_models.Chat, error) {
	traceChatf(ctx, "load global scope start conf_dir=%q", confDir)
	if confDir == "" {
		dir, err := utils.GetClaiConfigDir()
		if err != nil {
			return pub_models.Chat{}, diag.Errorf("get clai config dir: %w", err)
		}
		confDir = dir
		traceChatf(ctx, "load global scope resolved empty conf_dir to=%q", confDir)
	}

	convDir := conversationsDir(confDir)
	newPath := GlobalScopePath(confDir)
	oldPath := prevQueryPath(confDir)
	traceChatf(ctx, "load global scope paths conv_dir=%q new_path=%q old_path=%q", convDir, newPath, oldPath)

	if _, err := os.Stat(oldPath); err == nil {
		traceChatf(ctx, "load global scope migrating legacy file from=%q to=%q", oldPath, newPath)
		b, err := os.ReadFile(oldPath)
		if err != nil {
			return pub_models.Chat{}, diag.Errorf("read legacy global chat %q: %w", oldPath, err)
		}
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			return pub_models.Chat{}, diag.Errorf("ensure conversations dir: %w", err)
		}
		if err := utils.WriteFileAtomic(newPath, b, 0o644); err != nil {
			return pub_models.Chat{}, diag.Errorf("write migrated global chat %q: %w", newPath, err)
		}
		if err := os.Remove(oldPath); err != nil {
			return pub_models.Chat{}, diag.Errorf("remove legacy global chat %q: %w", oldPath, err)
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pub_models.Chat{}, diag.Errorf("stat legacy global chat %q: %w", oldPath, err)
	}

	c, err := loadConversation(ctx, confDir, newPath)
	traceChatf(ctx, "global scope msgs: %v, queries: %v", len(c.Messages), len(c.Queries))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			traceChatf(ctx, "load global scope no previous query at path=%q", newPath)
			runenv.Warnf(ctx, runenv.SubsystemChat, "no previous query found")
			return pub_models.Chat{}, nil
		}
		return pub_models.Chat{}, diag.Errorf("read global scope chat %q: %w", newPath, err)
	}
	traceChatf(ctx, "load global scope loaded path=%q chat_id=%q messages=%d", newPath, c.ID, len(c.Messages))

	if c.ID != globalScopeChatID {
		traceChatf(ctx, "load global scope normalizing chat id from=%q to=%q", c.ID, globalScopeChatID)
		c.ID = globalScopeChatID
		if err := Save(ctx, convDir, c); err != nil {
			return pub_models.Chat{}, diag.Errorf("rewrite global scope chat with normalized id: %w", err)
		}
	}

	return c, nil
}
