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
	prefix := ""
	if c.ReplyMode {
		confDir, err := utils.GetClaiConfigDir()
		if err != nil {
			return fmt.Errorf("get config dir: %w", err)
		}
		iP, err := chat.LoadPrevQuery(confDir)
		if err != nil {
			return fmt.Errorf("load previous query: %w", err)
		}
		if len(iP.Messages) > 0 {
			replyMessages := "You will be given a serie of messages from different roles, then a prompt descibing what to do with these messages. "
			replyMessages += "Between the messages and the prompt, there will be this line: '-------------'. "
			replyMessages += "The format is json with the structure {\"role\": \"<role>\", \"content\": \"<content>\"}. "
			replyMessages += "The roles are 'system' and 'user'. "
			b, err := json.Marshal(iP.Messages)
			if err != nil {
				return fmt.Errorf("encode reply JSON: %w", err)
			}
			replyMessages = fmt.Sprintf("%vMessages:\n%v\n-------------\n", replyMessages, string(b))
			prefix = replyMessages
		}
	}

	prompt, err := utils.Prompt(c.StdinReplace, args)
	if err != nil {
		return fmt.Errorf("setup prompt from stdin/args: %w", err)
	}

	msgs, err := chat.PromptToImageMessage(prompt)
	if err != nil {
		return fmt.Errorf("convert prompt to image message: %w", err)
	}

	// Default to the full prompt; if PromptToImageMessage extracted a text part,
	// we use that, but we must not end up with an empty prompt for normal text input.
	plainPrompt := prompt

	isImagePrompt := false
	for _, m := range msgs {
		for _, cp := range m.ContentParts {
			switch cp.Type {
			case "image_url":
				isImagePrompt = true
				c.PromptImageB64 = cp.ImageB64.RawB64
			case "text":
				if cp.Text != "" {
					plainPrompt = cp.Text
				}
			}
		}
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("format: '%v', prompt: '%v'\n", c.PromptFormat, prompt))
	}

	// For image prompts we don't add reply prefixes or formatting.
	if isImagePrompt {
		c.Prompt = plainPrompt
		return nil
	}

	if strings.Contains(c.PromptFormat, "%v") {
		plainPrompt = fmt.Sprintf(c.PromptFormat, plainPrompt)
	}

	c.Prompt = prefix + plainPrompt
	return nil
}
