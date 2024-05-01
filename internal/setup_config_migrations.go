package internal

import (
	"fmt"
	"os"
	"path"

	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type oldChatConfig struct {
	Model            string  `json:"model"`
	SystemPrompt     string  `json:"system_prompt"`
	Raw              bool    `json:"raw"`
	URL              string  `json:"url"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
}

type oldPhotoConfig struct {
	Model         string `json:"model"`
	PictureDir    string `json:"photo-dir"`
	PicturePrefix string `json:"photo-prefix"`
	PromptFormat  string `json:"prompt-format"`
}

// migrateOldChatConfig by first checking if file chatConfig exists, then
// reading + copying the fields to the new text.Configrations struct. Then write the
// file as textConfig. For the remaining fields, create vendor specific gpt4TurboPreview
// struct and write that to gpt4TurboPreview.json.
func migrateOldChatConfig(configDirPath string) error {
	oldChatConfigPath := fmt.Sprintf("%v/chatConfig.json", configDirPath)
	if _, err := os.Stat(oldChatConfigPath); os.IsNotExist(err) {
		// Nothing to migrate
		return nil
	}
	var oldConf oldChatConfig
	err := utils.ReadAndUnmarshal(oldChatConfigPath, &oldConf)
	if err != nil {
		return fmt.Errorf("failed to unmarshal old photo config: %w", err)
	}
	ancli.PrintOK("migrating old chat config to new format in textConfg.json\n")
	migratedTextConfig := text.Configurations{
		Model:        oldConf.Model,
		SystemPrompt: oldConf.SystemPrompt,
	}

	err = os.Remove(oldChatConfigPath)
	if err != nil {
		return fmt.Errorf("failed to remove old chatConfig: %w", err)
	}
	err = utils.CreateFile(fmt.Sprintf("%v/textConfig.json", configDirPath), &migratedTextConfig)
	if err != nil {
		return fmt.Errorf("failed to write new text config: %w", err)
	}

	migratedChatgptConfig := openai.ChatGPT{
		FrequencyPenalty: oldConf.FrequencyPenalty,
		MaxTokens:        oldConf.MaxTokens,
		PresencePenalty:  oldConf.PresencePenalty,
		Temperature:      oldConf.Temperature,
		TopP:             oldConf.TopP,
		Model:            oldConf.Model,
		Url:              oldConf.URL,
	}

	err = utils.CreateFile(fmt.Sprintf("%v/openai_gpt_%v.json", configDirPath, oldConf.Model), &migratedChatgptConfig)
	if err != nil {
		return fmt.Errorf("failed to write gpt4 turbo preview config: %w", err)
	}
	return nil
}

// migrateOldPhotoConfig by attempting to read and unmarshal the photoConfig.json file
// and transferring the fields which are applicable to the new photo.Configurations struct.
// Then writes the new photoConfig.json file.
func migrateOldPhotoConfig(configDirPath string) error {
	oldPhotoConfigPath := fmt.Sprintf("%v/photoConfig.json", configDirPath)
	if _, err := os.Stat(oldPhotoConfigPath); os.IsNotExist(err) {
		// Nothing to migrate, return
		return nil
	}
	var oldConf oldPhotoConfig
	err := utils.ReadAndUnmarshal(oldPhotoConfigPath, &oldConf)
	if err != nil {
		return fmt.Errorf("failed to unmarshal old photo config: %w", err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("oldConf: %+v\n", oldConf))
	}
	if oldConf.PictureDir == "" {
		// Field is empty only if the photoConfig already has been migrated. Super hacky dodge, but good enough for now
		return nil
	}
	newFilePath := path.Join(configDirPath, "photoConfig.json")
	ancli.PrintOK(fmt.Sprintf("migrating old photo config to new format saved to: '%v'\n", newFilePath))
	migratedPhotoConfig := photo.Configurations{
		Model:        oldConf.Model,
		PromptFormat: oldConf.PromptFormat,
		Output: photo.Output{
			Type:   photo.LOCAL,
			Dir:    oldConf.PictureDir,
			Prefix: oldConf.PicturePrefix,
		},
	}
	err = os.Remove(oldPhotoConfigPath)
	if err != nil {
		return fmt.Errorf("failed to remove old photoConfig: %w", err)
	}
	err = utils.CreateFile(newFilePath, &migratedPhotoConfig)
	if err != nil {
		return fmt.Errorf("failed to write new chat config: %w", err)
	}

	return nil
}
