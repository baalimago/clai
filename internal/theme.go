package internal

import (
	"fmt"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// PrepTheme resolves the clai config dir and loads theme.json into the global
// theme, returning the config dir. Every command that renders conversation
// content or reaches the completion bell needs it: config-touching commands
// get it via setup.ConfigRunPrep, the replay commands and the read-only chat
// subcommands call it directly since they run no config migration. A broken
// theme.json degrades to the built-in theme instead of failing the command.
func PrepTheme() (string, error) {
	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to find config dir: %v", err)
	}
	if err := utils.LoadTheme(claiConfDir); err != nil {
		ancli.Warnf("failed to load theme, using defaults: %v\n", err)
	}
	return claiConfDir, nil
}
