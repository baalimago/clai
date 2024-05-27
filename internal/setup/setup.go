package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
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
)

// Run the setup to configure the different files
func Run() error {
	var input string
	fmt.Print("Do you wish to configure:\n\t0. mode-files (example: textConfig.json/photoConfig.json)\n\t1. model files (example: openai-gpt-4o.json, anthropic-claude-opus.json)\n[0/1]: ")
	fmt.Scanln(&input)
	var configs []config
	var a action
	switch input {
	case "0":
		t, err := modeConfigs()
		if err != nil {
			return fmt.Errorf("failed to get config files: %v", err)
		}
		configs = t
		a = conf
	case "1":
		t, err := modelConfigs()
		if err != nil {
			return fmt.Errorf("failed to get model configs: %v", err)
		}

		configs = t
	default:
		return fmt.Errorf("unrecognized selection: %v", input)
	}
	return configure(configs, a)
}

// modelConfigs gets the model configuration files using pattern os.UserConfigDir()/.clai/*.json
func modelConfigs() ([]config, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %v", err)
	}

	pattern := filepath.Join(configDir, ".clai", "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob pattern %v: %v", pattern, err)
	}

	var configs []config
	for _, file := range files {
		if filepath.Base(file) == "textConfig.json" || filepath.Base(file) == "photoConfig.json" {
			continue
		}
		configs = append(configs, config{
			name:     filepath.Base(file),
			filePath: file,
		})
	}

	return configs, nil
}

// modeConfigs gets the mode configuration files using pattern os.UserConfigDir()/.clai/*Config.json
func modeConfigs() ([]config, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %v", err)
	}

	pattern := filepath.Join(configDir, ".clai", "*Config.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob pattern %v: %v", pattern, err)
	}

	var configs []config
	for _, file := range files {
		configs = append(configs, config{
			name:     filepath.Base(file),
			filePath: file,
		})
	}

	return configs, nil
}

func configure(cfgs []config, a action) error {
	fmt.Println("Found config files: ")
	for i, cfg := range cfgs {
		fmt.Printf("\t%v: %v\n", i, cfg.name)
	}

	var input string
	fmt.Print("Please pick index: ")
	fmt.Scanln(&input)
	index, err := strconv.Atoi(input)
	if err != nil {
		return fmt.Errorf("invalid response, failed to convert choice: %v, to integer: %v", input, err)
	}
	if index < 0 || index >= len(cfgs) {
		return fmt.Errorf("invalid index: %v, must be between 0 and %v", index, len(cfgs))
	}
	if a == unset {
		fmt.Print("Do you wish to [c]onfigure or [d]elete?\n[c/d]: ")
		fmt.Scanln(&input)
		switch input {
		case "c", "configure":
			a = conf
		case "d", "delete":
			a = del
		default:
			return fmt.Errorf("invalid choice: %v", input)
		}
	}

	switch a {
	case conf:
		return reconfigure(cfgs[index])
	case del:
		return remove(cfgs[index])
	default:
		return fmt.Errorf("invalid action, expected conf or del: %v", input)
	}
}

func reconfigure(cfg config) error {
	f, err := os.Open(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", cfg.filePath, err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %v", cfg.filePath, err)
	}
	return interractiveReconfigure(cfg, b)
}

func remove(cfg config) error {
	fmt.Printf("Are you sure you want to delete: '%v'?\n[y/n]: ", cfg.filePath)
	var input string
	fmt.Scanln(&input)
	if input != "y" {
		return fmt.Errorf("aborting deletion")
	}
	err := os.Remove(cfg.filePath)
	if err != nil {
		return fmt.Errorf("failed to delete file: '%v', error: %v", cfg.filePath, err)
	}
	ancli.PrintOK(fmt.Sprintf("deleted file: '%v'\n", cfg.filePath))
	return nil
}

func interractiveReconfigure(cfg config, b []byte) error {
	var jzon map[string]any
	err := json.Unmarshal(b, &jzon)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %v, error: %w", cfg.name, err)
	}
	fmt.Printf("Current config:\n%s\n---\n", b)
	newConfig, err := buildNewConfig(jzon)
	if err != nil {
		return fmt.Errorf("failed to build new config: %v", err)
	}

	newB, err := json.MarshalIndent(newConfig, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal new config: %v", err)
	}
	err = os.WriteFile(cfg.filePath, newB, 0o644)
	if err != nil {
		return fmt.Errorf("failed to write new config at: '%v', error: %v", cfg.filePath, err)
	}
	ancli.PrintOK(fmt.Sprintf("wrote new config to: '%v'\n", cfg.filePath))
	return nil
}

func buildNewConfig(jzon map[string]any) (map[string]any, error) {
	newConfig := make(map[string]any)
	for k, v := range jzon {
		var input string
		var newValue any
		maplike, isMap := v.(map[string]any)
		if isMap {
			m, err := buildNewConfig(maplike)
			if err != nil {
				return nil, fmt.Errorf("failed to parse nested map-like: %v", err)
			}
			newValue = m
		} else {
			fmt.Printf("Key: '%v', current: '%v'\nPlease enter new value, or leave empty to keep: ", k, v)
			fmt.Scanln(&input)
			if input == "" {
				newValue = v
			} else {
				newValue = input
				newValue = castPrimitive(newValue)
			}
		}
		newConfig[k] = newValue
	}
	return newConfig, nil
}

func castPrimitive(v any) any {
	if misc.Truthy(v) {
		return true
	}

	if misc.Falsy(v) {
		return false
	}

	s, isString := v.(string)
	if !isString {
		// We don't really know what unholy value this might be, but let's just return it and hope it's benign
		return v
	}
	i, err := strconv.Atoi(s)
	if err == nil {
		return i
	}
	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return f
	}
	return s
}
