package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type config struct {
	name     string
	filePath string
}

type action uint8

const (
	unset action = iota
	conf
	del
	newaction
	pasteConfig
	confWithEditor
	promptEditWithEditor
	unescapedFieldEditWithEditor
	back
)

// choiceToAction is a comma-separated string of choice strings which should trigger
// the action
var choiceToAction = map[string]action{
	"b,back":                 back,
	"c,configure":            conf,
	"d,delete":               del,
	"n,new":                  newaction,
	"p,paste":                pasteConfig,
	"e,configureWithEditor":  confWithEditor,
	"pr,promptWithEditor":    promptEditWithEditor,
	"ufe,unescapedFieldEdit": unescapedFieldEditWithEditor,
}

var actionToTableAction = map[action]utils.TableAction{
	back: {
		Short:  "b",
		Long:   "back",
		Format: "[b]ack",
		Action: func() error { return utils.ErrBack },
	},
	newaction: {
		Short:  "n",
		Long:   "new",
		Format: "[n]ew",
	},
	pasteConfig: {
		Short:  "p",
		Long:   "paste",
		Format: "[p]aste config",
	},
	confWithEditor: {
		Short:  "e",
		Long:   "configureWithEditor",
		Format: "conf with [e]ditor",
	},
	promptEditWithEditor: {
		Short:  "pr",
		Long:   "promptWithEditor",
		Format: "[pr]ompt edit with editor",
	},
	unescapedFieldEditWithEditor: {
		Short:  "ufe",
		Long:   "unescapedFieldEdit",
		Format: "(u)nescaped (f)ield (e)dit [ufe]",
	},
}

type setupCategory struct {
	name              string
	subdirPath        string
	defaultConfig     any
	load              func(string) ([]config, error)
	itemSelectActions []action
	itemActions       []action
}

const stage0Raw = "Setup categories"

func (a action) String() string {
	switch a {
	case unset:
		return "unset"
	case conf:
		return "[c]onfigure"
	case del:
		return "[d]el"
	case newaction:
		return "create [n]ew"
	case confWithEditor:
		return "configure with [e]ditor"
	case promptEditWithEditor:
		return "[pr]ompt edit with editor"
	case unescapedFieldEditWithEditor:
		return "(u)nescaped (f)ield (e)dit [ufe]"
	case pasteConfig:
		return "[p]aste config"
	case back:
		return "[b]ack"
	default:
		return "unset"
	}
}

func executeConfigAction(cfg config, a action) error {
	switch a {
	case conf:
		return actionReconfigure(cfg)
	case confWithEditor:
		return actionReconfigureWithEditor(cfg)
	case promptEditWithEditor:
		return actionReconfigurePromptWithEditor(cfg)
	case unescapedFieldEditWithEditor:
		return actionReconfigureStringFieldWithEditor(cfg, "")
	case del:
		return actionRemove(cfg)
	case back:
		return utils.ErrBack
	default:
		return fmt.Errorf("invalid action for config %q: %v", cfg.name, a)
	}
}

// InitCmd the setup to configure the different files
func InitCmd() error {
	claiDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	categories := []setupCategory{
		{
			name: "mode-files",
			load: func(dir string) ([]config, error) {
				return getConfigs(filepath.Join(dir, "*Config.json"), []string{})
			},
			itemSelectActions: nil,
			itemActions:       []action{conf, confWithEditor},
		},
		{
			name: "model files",
			load: func(dir string) ([]config, error) {
				return getConfigs(filepath.Join(dir, "*.json"), []string{"textConfig", "photoConfig", "videoConfig"})
			},
			itemSelectActions: nil,
			itemActions:       []action{conf, confWithEditor},
		},
		{
			name: "text generation profiles",
			load: func(dir string) ([]config, error) {
				profilesDir := filepath.Join(dir, "profiles")
				cfgs, err := getConfigs(filepath.Join(profilesDir, "*.json"), []string{})
				if err != nil {
					return nil, fmt.Errorf("failed to get profile configs: %w", err)
				}
				return cfgs, nil
			},
			itemSelectActions: []action{newaction},
			itemActions:       []action{conf, del, confWithEditor, promptEditWithEditor, unescapedFieldEditWithEditor},
			subdirPath:        "./profiles",
			defaultConfig:     text.DefaultProfile,
		},
		{
			name: "MCP server configuration",
			load: func(dir string) ([]config, error) {
				mcpServersDir := filepath.Join(dir, "mcpServers")
				if _, statErr := os.Stat(mcpServersDir); os.IsNotExist(statErr) {
					if err := utils.CreateFile(filepath.Join(mcpServersDir, "everything.json"), &defaultMcpServer); err != nil {
						return nil, fmt.Errorf("failed to create default mcp server config: %w", err)
					}
				}
				cfgs, err := getConfigs(filepath.Join(mcpServersDir, "*.json"), []string{})
				if err != nil {
					return nil, fmt.Errorf("failed to get mcp configs: %w", err)
				}
				return cfgs, nil
			},
			itemSelectActions: []action{newaction, pasteConfig},
			itemActions:       []action{conf, del, confWithEditor},
			subdirPath:        "./mcpServers",
			defaultConfig:     defaultMcpServer,
		},
		shellContextSetupCategory(),
	}

	for {
		category, err := selectCategory(categories)
		if err != nil {
			if errors.Is(err, utils.ErrUserInitiatedExit) {
				return utils.ErrUserInitiatedExit
			}
			return fmt.Errorf("failed to select category: %w", err)
		}
		if err := runCategory(claiDir, category); err != nil {
			if errors.Is(err, utils.ErrBack) {
				continue
			}
			if errors.Is(err, utils.ErrUserInitiatedExit) {
				return utils.ErrUserInitiatedExit
			}
			return fmt.Errorf("failed to run category %q: %w", category.name, err)
		}
	}
}

func selectCategory(categories []setupCategory) (setupCategory, error) {
	choicesFormat := "Select category <num>, [q]uit: "
	selected, err := utils.SelectFromTable(
		stage0Raw,
		utils.SlicePaginator(categories),
		choicesFormat,
		func(i int, category setupCategory) (string, error) {
			return fmt.Sprintf("%d. %s", i, category.name), nil
		},
		10,
		true,
		[]utils.TableAction{},
		os.Stdout,
	)
	if err != nil {
		return setupCategory{}, fmt.Errorf("failed to select setup category: %w", err)
	}
	index := selected[0]
	if index < 0 || index >= len(categories) {
		return setupCategory{}, fmt.Errorf("selected category index %d out of range", index)
	}
	return categories[index], nil
}

func runCategory(claiDir string, category setupCategory) error {
	for {
		cfgs, err := category.load(claiDir)
		if err != nil {
			return fmt.Errorf("failed to load category configs: %w", err)
		}
		err = selectConfigItem(category, cfgs)
		if err != nil {
			if errors.Is(err, utils.ErrBack) {
				return fmt.Errorf("user returned to category selection: %w", err)
			}
			if errors.Is(err, utils.ErrUserInitiatedExit) {
				return utils.ErrUserInitiatedExit
			}
			return fmt.Errorf("failed to select config item: %w", err)
		}
	}
}

// getConfigs using a glob, and then exclude files using strings.Contains()
func getConfigs(includeGlob string, excludeContains []string) ([]config, error) {
	files, err := filepath.Glob(includeGlob)
	if err != nil {
		return nil, fmt.Errorf("failed to glob pattern %v: %w", includeGlob, err)
	}
	var configs []config
OUTER:
	for _, file := range files {
		for _, e := range excludeContains {
			if strings.Contains(filepath.Base(file), e) {
				continue OUTER
			}
		}
		configs = append(configs, config{
			name:     filepath.Base(file),
			filePath: file,
		})
	}

	return configs, nil
}

func setupCustomTableActions(category setupCategory) []utils.TableAction {
	ret := []utils.TableAction{
		actionToTableAction[back],
	}
	for _, a := range category.itemSelectActions {
		cta, found := actionToTableAction[a]
		if !found {
			ancli.Warnf("custom table action not found: %q, this is a bit odd", a)
			continue
		}
		switch a {
		case newaction:
			cta.Action = func() error {
				cfg, err := createConfigFile(category.subdirPath, category.name, category.defaultConfig)
				if err != nil {
					return fmt.Errorf("failed to create config file: %w", err)
				}
				return actionReconfigure(cfg)
			}
		case pasteConfig:
			cta.Action = func() error {
				return actionPasteMcpServer(category.subdirPath)
			}
		}
		ret = append(ret, cta)
	}
	return ret
}

func selectConfigItem(category setupCategory, cfgs []config) error {
	if len(cfgs) == 0 {
		return fmt.Errorf("found no configuration files for category %q", category.name)
	}

	customTableActions := setupCustomTableActions(category)
	selectedIndices, err := utils.SelectFromTable(
		fmt.Sprintf("Configs in %s", category.name),
		utils.SlicePaginator(cfgs),
		"Select config <num>: ",
		func(i int, cfg config) (string, error) {
			return fmt.Sprintf("%d. %s", i, cfg.name), nil
		},
		10,
		true,
		customTableActions,
		os.Stdout,
	)
	if err != nil {
		return fmt.Errorf("failed to select config item: %w", err)
	}

	selectedIndex := selectedIndices[0]
	if selectedIndex < 0 || selectedIndex >= len(cfgs) {
		return fmt.Errorf("selected config index %d out of range", selectedIndex)
	}

	selectedCfg := cfgs[selectedIndex]
	if err := previewConfigItem(selectedCfg); err != nil {
		return fmt.Errorf("failed to preview selected config item %q: %w", selectedCfg.name, err)
	}

	err = actOnConfigItem(category, selectedCfg)
	// Allow the user to go one step back on back actions
	// Will fill callstack, but veeeeeery slowly
	if err != nil && errors.Is(err, utils.ErrBack) {
		return selectConfigItem(category, cfgs)
	}
	return err
}
