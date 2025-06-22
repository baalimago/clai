package setup

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
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
	confWithEditor
	pasteNew
)

var defaultMcpServer = tools.McpServer{
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
	default:
		return "unset"
	}
}

const stage_0 = `Do you wish to configure:
  0. mode-files (example: <config>/.clai/textConfig.json- or photoConfig.json)
  1. model files (example: <config>/.clai/openai-gpt-4o.json, <config>/.clai/anthropic-claude-opus.json)
  2. text generation profiles (see: "clai [h]elp [p]rofile" for additional info)
  3. MCP server configuration (enables custom tools)
[0/1/2/3]: `

// Run the setup to configure the different files
func Run() error {
	fmt.Print(stage_0)

	input, err := utils.ReadUserInput()
	if err != nil {
		return fmt.Errorf("failed to read input while running: %w", err)
	}
	var configs []config
	var a action
	claiDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %v", err)
	}
	switch input {
	case "0":
		t, err := getConfigs(filepath.Join(claiDir, "*Config.json"), []string{})
		if err != nil {
			return fmt.Errorf("failed to get configs files: %w", err)
		}
		configs = t
		a = conf
	case "1":
		t, err := getConfigs(filepath.Join(claiDir, "*.json"), []string{"textConfig", "photoConfig"})
		if err != nil {
			return fmt.Errorf("failed to get configs files: %w", err)
		}
		configs = t
		qAct, err := queryForAction([]action{conf, del, confWithEditor})
		if err != nil {
			return fmt.Errorf("failed to find action: %w", err)
		}
		a = qAct
	case "2":
		profilesDir := filepath.Join(claiDir, "profiles")
		t, err := getConfigs(filepath.Join(profilesDir, "*.json"), []string{})
		if err != nil {
			return fmt.Errorf("failed to get configs files: %w", err)
		}
		configs = t
		qAct, err := queryForAction([]action{conf, del, newaction, confWithEditor})
		if err != nil {
			return fmt.Errorf("failed to find action: %w", err)
		}
		a = qAct
		if a == newaction {
			c, err := createProFile(profilesDir)
			if err != nil {
				return fmt.Errorf("failed to create profile file: %w", err)
			}
			// Reset config list as the user most likely only wants to edit the newly configured profile
			configs = make([]config, 0)
			configs = append(configs, c)
			// Once new file has potentially been created, potentially alter it
			a = conf
		}
	case "3":
		mcpServersDir := filepath.Join(claiDir, "mcpServers")
		if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
			if err := utils.CreateFile(filepath.Join(mcpServersDir, "everything.json"), &defaultMcpServer); err != nil {
				return fmt.Errorf("failed to create default mcp server config: %w", err)
			}
		}
		t, err := getConfigs(filepath.Join(mcpServersDir, "*.json"), []string{})
		if err != nil {
			return fmt.Errorf("failed to get configs files: %w", err)
		}
		configs = t
		qAct, err := queryForAction([]action{conf, del, newaction, confWithEditor, pasteNew})
		if err != nil {
			return fmt.Errorf("failed to find action: %w", err)
		}
		a = qAct
		if a == newaction {
			c, err := createMcpServerFile(mcpServersDir)
			if err != nil {
				return fmt.Errorf("failed to create mcp server file: %w", err)
			}
			configs = []config{c}
			a = conf
		}

		if a == pasteNew {
			pastedConfigs, err := pasteMcpServerConfig(mcpServersDir)
			if err != nil {
				return fmt.Errorf("failed to paste mcp server config: %w", err)
			}
			configs = pastedConfigs
			a = conf
		}
	case "q", "quit", "e", "exit":
		return utils.ErrUserInitiatedExit
	default:
		return fmt.Errorf("unrecognized selection: %v", input)
	}
	return configure(configs, a)
}

// createProFile, as in create profile file. I'm a very funny person.
func createProFile(profilePath string) (config, error) {
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		os.MkdirAll(profilePath, os.ModePerm)
	}
	fmt.Print("Enter profile name: ")
	profileName, err := utils.ReadUserInput()
	if err != nil {
		return config{}, err
	}
	newProfilePath := path.Join(profilePath, fmt.Sprintf("%v.json", profileName))
	err = utils.CreateFile(newProfilePath, &text.DEFAULT_PROFILE)
	if err != nil {
		return config{}, err
	}
	return config{
		name:     profileName,
		filePath: newProfilePath,
	}, nil
}

// createMcpServerFile creates a new MCP server configuration file inside
// mcpServersPath using the provided server name and a default configuration.
func createMcpServerFile(mcpServersPath string) (config, error) {
	if _, err := os.Stat(mcpServersPath); os.IsNotExist(err) {
		os.MkdirAll(mcpServersPath, os.ModePerm)
	}
	fmt.Print("Enter server name: ")
	serverName, err := utils.ReadUserInput()
	if err != nil {
		return config{}, err
	}
	newServerPath := path.Join(mcpServersPath, fmt.Sprintf("%v.json", serverName))
	err = utils.CreateFile(newServerPath, &defaultMcpServer)
	if err != nil {
		return config{}, err
	}
	return config{
		name:     serverName,
		filePath: newServerPath,
	}, nil
}

// getConfigs using a glob, and then exclude files using strings.Contains()
func getConfigs(includeGlob string, excludeContains []string) ([]config, error) {
	files, err := filepath.Glob(includeGlob)
	if err != nil {
		return nil, fmt.Errorf("failed to glob pattern %v: %v", includeGlob, err)
	}
	var configs []config
OUTER:
	for _, file := range files {
		// The moment this becomes a performance issue it's time to think about
		// maybe reducing the amount of config files
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

	ret := make([]config, 0)
	for _, s := range serverNames {
		ret = append(ret, config{
			name:     s,
			filePath: filepath.Join(mcpConfDir, fmt.Sprintf("%v.json", s)),
		})
	}

	return ret, nil
}
