package internal

import (
	"context"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
)

// PrepTheme resolves the clai config dir and loads theme.json into the global
// theme, returning the config dir. Every command that renders conversation
// content or reaches the completion bell needs it: config-touching commands
// get it via setup.ConfigRunPrep, the replay commands and the read-only chat
// subcommands call it directly since they run no config migration. A broken
// theme.json degrades to the built-in theme instead of failing the command.
func PrepTheme(ctx context.Context) (string, error) {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return "", diag.Errorf("failed to find config dir: %w", err)
	}
	if err := utils.LoadThemeFor(ctx, claiConfDir); err != nil {
		runenv.Warnf(ctx, runenv.SubsystemConfig, "failed to load theme, using defaults: %v\n", err)
	}
	return claiConfDir, nil
}
