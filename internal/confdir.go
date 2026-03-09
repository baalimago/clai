package internal

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
)

func printConfDir(_ context.Context, args []string) (models.Querier, error) {
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get clai config dir: %w", err)
	}

	resolved, err := utils.ResolveConfigDirPath(configDir, args[1:])
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config dir path: %w", err)
	}

	fmt.Println(resolved)
	return nil, utils.ErrUserInitiatedExit
}
