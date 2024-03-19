package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

var defaultPhotoConfig = photoQuerier{
	Model:         "dall-e-3",
	PictureDir:    fmt.Sprintf("%v/Pictures", os.Getenv("HOME")),
	PicturePrefix: "clai",
	PromptFormat:  "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
}

var defaultChatConfig = chatModelQuerier{
	Model:        "gpt-4-turbo-preview",
	SystemPrompt: "You are an assistent for a CLI interface. Answer concisely and informatively. Prefer markdown if possible.",
	Url:          "https://api.openai.com/v1/chat/completions",
	Temperature:  1.0,
	TopP:         1.0,
	Raw:          false,
}

func writeConfigFile[T chatModelQuerier | photoQuerier](configPath string, config *T) error {
	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if _, err := file.Write(b); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func unmarshalConfg[T chatModelQuerier | photoQuerier](chatConfigPath string, config *T) error {
	if _, err := os.Stat(chatConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("failed to find photo config file: %w", err)
	}
	file, err := os.Open(chatConfigPath)
	if err != nil {
		return fmt.Errorf("failed to open chat config file: %w", err)
	}
	defer file.Close()
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("failed to read chat config file: %w", err)
	}
	err = json.Unmarshal(fileBytes, config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal chat config file: %w", err)
	}

	return nil
}

func setUpDotfileDirectory() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to find home dir: %w", err)
	}

	claiDir := filepath.Join(homeDir, ".clai")
	conversationsDir := filepath.Join(claiDir, "conversations")

	// Create the .clai directory.
	if err := os.MkdirAll(conversationsDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create .clai + .clai/conversations directory: %w", err)
	}

	err = writeConfigFile(filepath.Join(claiDir, "photoConfig.json"), &defaultPhotoConfig)
	if err != nil {
		return err
	}
	err = writeConfigFile(filepath.Join(claiDir, "chatConfig.json"), &defaultChatConfig)
	if err != nil {
		return err
	}
	return nil
}

func setPromptsFromConfig(homeDir string, cmq *chatModelQuerier, pq *photoQuerier) error {
	dirPath := fmt.Sprintf("%v/.clai", homeDir)
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		err := setUpDotfileDirectory()
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to setup config dotfile: %v\n", err))
		}
		ancli.PrintOK("created .clai directory and default prompts.json file\n")
	}

	photoConfig := dirPath + "/photoConfig.json"
	err := unmarshalConfg(photoConfig, pq)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to unmarshal photo config: %v\n", err))
	}
	chatConfig := dirPath + "/chatConfig.json"
	err = unmarshalConfg(chatConfig, cmq)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to unmarshal photo config: %v\n", err))
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
