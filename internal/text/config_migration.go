package text

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
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

// MigrateOldChatConfig by first checking if file chatConfig exists, then
// reading + copying the fields to the new text.Configrations struct. Then write the
// file as textConfig. For the remaining fields, create vendor specific gpt4TurboPreview
// struct and write that to gpt4TurboPreview.json.
func MigrateOldChatConfig(configDirPath string) error {
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
	migratedTextConfig := Configurations{
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
		URL:              oldConf.URL,
	}

	err = utils.CreateFile(fmt.Sprintf("%v/openai_gpt_%v.json", configDirPath, oldConf.Model), &migratedChatgptConfig)
	if err != nil {
		return fmt.Errorf("failed to write gpt4 turbo preview config: %w", err)
	}
	return nil
}
