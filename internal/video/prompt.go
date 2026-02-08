package video

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func (c *Configurations) SetupPrompts(args []string) error {
	if c.ReplyMode {
		confDir, err := utils.GetClaiConfigDir()
		if err != nil {
			return fmt.Errorf("failed to get config dir: %w", err)
		}
		iP, err := chat.LoadPrevQuery(confDir)
		if err != nil {
			return fmt.Errorf("failed to load previous query: %w", err)
		}
		if len(iP.Messages) > 0 {
			replyMessages := "You will be given a serie of messages from different roles, then a prompt descibing what to do with these messages. "
			replyMessages += "Between the messages and the prompt, there will be this line: '-------------'. "
			replyMessages += "The format is json with the structure {\"role\": \"<role>\", \"content\": \"<content>\"}. "
			replyMessages += "The roles are 'system' and 'user'. "
			b, err := json.Marshal(iP.Messages)
			if err != nil {
				return fmt.Errorf("failed to encode reply JSON: %w", err)
			}
			replyMessages = fmt.Sprintf("%vMessages:\n%v\n-------------\n", replyMessages, string(b))
			c.Prompt += replyMessages
		}
	}
	prompt, err := utils.Prompt(c.StdinReplace, args)
	if err != nil {
		return fmt.Errorf("failed to setup prompt from stdin: %w", err)
	}
	chat, err := chat.PromptToImageMessage(prompt)
	if err != nil {
		return fmt.Errorf("failed to convert to chat with image message")
	}
	isImagePrompt := false
	for _, m := range chat {
		for _, cp := range m.ContentParts {
			if cp.Type == "image_url" {
				isImagePrompt = true
				c.PromptImageB64 = cp.ImageB64.RawB64
			}
			if cp.Type == "text" {
				c.Prompt = cp.Text
			}
		}
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("format: '%v', prompt: '%v'\n", c.PromptFormat, prompt))
	}
	// Don't do additional weird stuff if it's an image prompt
	if isImagePrompt {
		return nil
	}
	// If prompt format has %v, formatting it, otherwise just appending
	if strings.Contains(c.PromptFormat, "%v") {
		c.Prompt += fmt.Sprintf(c.PromptFormat, prompt)
	} else {
		c.Prompt += prompt
	}
	return nil
}
