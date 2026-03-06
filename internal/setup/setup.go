package setup

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type config struct {
	name        string
	filePath    string
	kind        configKind
	isSynthetic bool
}

type configKind uint8

const (
	configKindNormal configKind = iota
	configKindCreateProfile
	configKindCreateMCPServer
	configKindPasteMCPConfig
)

type action uint8

const (
	unset action = iota
	conf
	del
	newaction
	confWithEditor
	pasteNew
	promptEditWithEditor
)

type setupCategory struct {
	name    string
	load    func(string) ([]config, error)
	actions []action
}

const stage0Raw = "Setup categories"

func stage0Colorized() string {
	return colorPrimary(stage0Raw)
}

var defaultMcpServer = pub_models.McpServer{
	Command: "npx",
	Args:    []string{"-y", "@modelcontextprotocol/server-everything"},
}

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
	case pasteNew:
		return "[p]aste new config"
	case promptEditWithEditor:
		return "[pr]ompt edit with editor"
	default:
		return "unset"
	}
}

// SubCmd the setup to configure the different files
func SubCmd() error {
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
			actions: []action{conf},
		},
		{
			name: "model files",
			load: func(dir string) ([]config, error) {
				return getConfigs(filepath.Join(dir, "*.json"), []string{"textConfig", "photoConfig", "videoConfig"})
			},
			actions: []action{conf, del, confWithEditor},
		},
		{
			name: "text generation profiles",
			load: func(dir string) ([]config, error) {
				profilesDir := filepath.Join(dir, "profiles")
				cfgs, err := getConfigs(filepath.Join(profilesDir, "*.json"), []string{})
				if err != nil {
					return nil, fmt.Errorf("failed to get profile configs: %w", err)
				}
				cfgs = append(cfgs, config{
					name:        "[create new profile]",
					filePath:    profilesDir,
					kind:        configKindCreateProfile,
					isSynthetic: true,
				})
				return cfgs, nil
			},
			actions: []action{conf, del, confWithEditor, promptEditWithEditor},
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
				cfgs = append(cfgs,
					config{
						name:        "[create new MCP server]",
						filePath:    mcpServersDir,
						kind:        configKindCreateMCPServer,
						isSynthetic: true,
					},
					config{
						name:        "[paste new MCP config]",
						filePath:    mcpServersDir,
						kind:        configKindPasteMCPConfig,
						isSynthetic: true,
					},
				)
				return cfgs, nil
			},
			actions: []action{conf, del, confWithEditor},
		},
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
	choicesFormat := "Select category, [p]rev, [q]uit: "
	selected, err := utils.SelectFromTable(
		stage0Colorized(),
		categories,
		choicesFormat,
		func(i int, category setupCategory) (string, error) {
			return fmt.Sprintf("%d. %s", i, category.name), nil
		},
		10,
		true,
		false,
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

// createProFile, as in create profile file. I'm a very funny person.
func createProFile(profilePath string) (config, error) {
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		if err := os.MkdirAll(profilePath, os.ModePerm); err != nil {
			return config{}, fmt.Errorf("failed to create profile directory: %w", err)
		}
	}
	fmt.Print(colorSecondary("Enter profile name: "))
	profileName, err := utils.ReadUserInput()
	if err != nil {
		return config{}, fmt.Errorf("read profile name: %w", err)
	}
	newProfilePath := path.Join(profilePath, fmt.Sprintf("%v.json", profileName))
	err = utils.CreateFile(newProfilePath, &text.DefaultProfile)
	if err != nil {
		return config{}, fmt.Errorf("create profile file: %w", err)
	}
	return config{
		name:     profileName,
		filePath: newProfilePath,
		kind:     configKindNormal,
	}, nil
}

// createMcpServerFile creates a new MCP server configuration file inside
// mcpServersPath using the provided server name and a default configuration.
func createMcpServerFile(mcpServersPath string) (config, error) {
	if _, err := os.Stat(mcpServersPath); os.IsNotExist(err) {
		if err := os.MkdirAll(mcpServersPath, os.ModePerm); err != nil {
			return config{}, fmt.Errorf("failed to create mcp server directory: %w", err)
		}
	}
	fmt.Print(colorSecondary("Enter server name: "))
	serverName, err := utils.ReadUserInput()
	if err != nil {
		return config{}, fmt.Errorf("read server name: %w", err)
	}
	newServerPath := path.Join(mcpServersPath, fmt.Sprintf("%v.json", serverName))
	err = utils.CreateFile(newServerPath, &defaultMcpServer)
	if err != nil {
		return config{}, fmt.Errorf("create mcp server file: %w", err)
	}
	return config{
		name:     serverName,
		filePath: newServerPath,
		kind:     configKindNormal,
	}, nil
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
			kind:     configKindNormal,
		})
	}

	return configs, nil
}

func pasteMcpServerConfig(mcpConfDir string) ([]config, error) {
	ancli.Noticef("Paste your MCP server configuration below.")
	ancli.Noticef("Press Ctrl+D when finished (or type 'EOF' on a new line):")

	var lines []string
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "EOF" {
			break
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading input: %w", err)
	}

	pastedConfig := strings.Join(lines, "\n")
	if strings.TrimSpace(pastedConfig) == "" {
		return nil, fmt.Errorf("no configuration provided")
	}

	serverNames, err := ParseAndAddMcpServer(mcpConfDir, pastedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse mcp server: %w", err)
	}

	ret := make([]config, 0, len(serverNames))
	for _, s := range serverNames {
		ret = append(ret, config{
			name:     s,
			filePath: filepath.Join(mcpConfDir, fmt.Sprintf("%v.json", s)),
			kind:     configKindNormal,
		})
	}

	return ret, nil
}
