package setup

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
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
)

var ErrUserExit = errors.New("user exit")

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
	default:
		return "unset"
	}
}

const stage_0 = `Do you wish to configure:
  0. mode-files (example: <config>/.clai/textConfig.json- or photoConfig.json)
  1. model files (example: <config>/.clai/openai-gpt-4o.json, <config>/.clai/anthropic-claude-opus.json)
  2. text generation profiles (see: "clai [h]elp [p]rofile" for additional info) 
[0/1/2]: `

// Run the setup to configure the different files
func Run() error {
	var input string
	fmt.Print(stage_0)
	fmt.Scanln(&input)
	var configs []config
	var a action
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %v", err)
	}
	claiDir := filepath.Join(configDir, ".clai")
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
	var profileName string
	fmt.Print("Enter profile name: ")
	fmt.Scanln(&profileName)
	newProfilePath := path.Join(profilePath, fmt.Sprintf("%v.json", profileName))
	err := utils.CreateFile(newProfilePath, &text.DEFAULT_PROFILE)
	if err != nil {
		return config{}, err
	}
	return config{
		name:     profileName,
		filePath: newProfilePath,
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
