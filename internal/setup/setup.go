package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type config struct {
	name     string
	filePath string
}

// Run the setup to configure the different files
func Run() error {
	var input string
	fmt.Print("Do you wish to configure:\n\t0. mode-files (example: textConfig.json/photoConfig.json)\n\t1. model files (example: openai-gpt-4o.json, anthropic-claude-opus.json)\n[0/1]: ")
	fmt.Scanln(&input)
	var configs []config
	switch input {
	case "0":
		t, err := modeConfigs()
		if err != nil {
			return fmt.Errorf("failed to get config files: %v", err)
		}
		configs = t
	case "1":
		t, err := modelConfigs()
		if err != nil {
			return fmt.Errorf("failed to get model configs: %v", err)
		}
		configs = t
	default:
		return fmt.Errorf("unrecognized selection: %v", input)
	}

	return configure(configs)
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

func configure(cfgs []config) error {
	fmt.Println("Found config files: ")
	for i, cfg := range cfgs {
		fmt.Printf("\t%v: %v\n", i, cfg.name)
	}

	var input string
	fmt.Println("Please pick index: ")
	fmt.Scanln(&input)
	index, err := strconv.Atoi(input)
	if err != nil {
		return fmt.Errorf("invalid response, failed to convert choice: %v, to integer: %v", input, err)
	}
	if index < 0 || index >= len(cfgs) {
		return fmt.Errorf("invalid index: %v, must be between 0 and %v", index, len(cfgs))
	}
	return reconfigure(cfgs[index])
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

func interractiveReconfigure(cfg config, b []byte) error {
	var jzon map[string]any
	err := json.Unmarshal(b, &jzon)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %v, error: %w", cfg.name, err)
	}
	fmt.Printf("Current config:\n%s\n---\n", b)
	newConfig, err := buildNewConfig(jzon)

	newB, err := json.Marshal(newConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal new config: %v", err)
	}
	err = os.WriteFile(cfg.filePath, newB, 0644)
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
			}
		}
		newConfig[k] = newValue
	}
	return newConfig, nil
}
