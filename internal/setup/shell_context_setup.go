package setup

import (
	"fmt"
	"path/filepath"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
)

func shellContextSetupCategory() setupCategory {
	return setupCategory{
		name: "shell context",
		load: func(dir string) ([]config, error) {
			if err := utils.CreateConfigDir(dir); err != nil {
				return nil, fmt.Errorf("failed to ensure shell context config dir: %w", err)
			}
			cfgs, err := getConfigs(filepath.Join(dir, "shellContexts", "*.json"), []string{})
			if err != nil {
				return nil, fmt.Errorf("failed to get shell context configs: %w", err)
			}
			return cfgs, nil
		},
		itemSelectActions: []action{newaction},
		itemActions:       []action{conf, del, confWithEditor, unescapedFieldEditWithEditor},
		subdirPath:        "./shellContexts",
		defaultConfig:     text.ShellContextDefinition{},
	}
}
