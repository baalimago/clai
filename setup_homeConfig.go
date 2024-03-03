package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func setUpDotfileDirectory() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to find home dir: %w", err)
	}

	goaiDir := filepath.Join(homeDir, ".goai")
	conversationsDir := filepath.Join(goaiDir, "conversations")

	// Create the .goai directory.
	if err := os.MkdirAll(conversationsDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create .goai + .goai/conversations directory: %w", err)
	}

	// Define the path for the default_prompts.yml file inside .goai.
	promptsConfigPath := filepath.Join(goaiDir, "prompts.json")

	// Create the default_prompts.yml file.
	file, err := os.Create(promptsConfigPath)
	if err != nil {
		return fmt.Errorf("failed to create .goai/prompts.json: %w", err)
	}
	defer file.Close()

	// Optionally, write the initial content to the default_prompts.yml file.
	initialContent := `{
  "photo": "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
  "query": "You are an assistent for a CLI interface. Answer concisely and informatively. Prefer markdown if possible."
}`
	if _, err := file.WriteString(initialContent); err != nil {
		return err
	}

	return nil
}

func setPromptsFromConfig(homeDir string, cmq *chatModelQuerier, pq *photoQuerier) error {
	dirPath := fmt.Sprintf("%v/.goai", homeDir)
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		err := setUpDotfileDirectory()
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to setup config dotfile: %v\n", err))
		}
		ancli.PrintOK("created .goai directory and default prompts.json file\n")
	}

	promptPath := dirPath + "/prompts.json"
	if _, err := os.Stat(promptPath); os.IsNotExist(err) {
		ancli.PrintWarn(fmt.Sprintf("failed to find prompts file: %v\n", err))
	} else {
		chatPrompt, photoPrompt, err := parsePrompts(promptPath)
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to parse prompts: %v\n", err))
		} else {
			cmq.systemPrompt = chatPrompt
			pq.promptFormat = photoPrompt
		}
	}
	return nil
}

func homedirConfig(cmq *chatModelQuerier, pq *photoQuerier) {
	homeDir, err := os.UserHomeDir()
	if err == nil {
		err = setPromptsFromConfig(homeDir, cmq, pq)
		if err != nil {
			ancli.PrintWarn(fmt.Sprintf("failed to set prompts from config: %v\n", err))
		}
	} else {
		ancli.PrintWarn(fmt.Sprintf("failed to find home dir: %v\n", err))
	}
}
