package generic

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
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
		replyMessages, err := ReplyContext(iP.Messages)
		if err != nil {
			return err
		}
		c.Prompt += replyMessages
	}

	prompt, err := utils.Prompt(c.StdinReplace, args)
	if err != nil {
		return fmt.Errorf("failed to setup prompt from stdin: %w", err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("format: '%v', prompt: '%v'\n", c.PromptFormat, prompt))
	}
	c.Prompt += fmt.Sprintf(c.PromptFormat, prompt)
	return nil
}

// ReplyContext renders the previous messages as the -re prompt preamble, each
// image as its "[image <handle>]" label; no messages render as "".
func ReplyContext(messages []pub_models.Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}
	replyMessages := "You will be given a serie of messages from different roles, then a prompt descibing what to do with these messages. "
	replyMessages += "Between the messages and the prompt, there will be this line: '-------------'."
	replyMessages += "The format is json with the structure {\"role\": \"<role>\", \"content\": \"<content>\"}. "
	replyMessages += "The roles are 'system' and 'user'. "
	b, err := json.Marshal(chat.ImagesDescribed(messages))
	if err != nil {
		return "", fmt.Errorf("failed to encode reply JSON: %w", err)
	}
	return fmt.Sprintf("%vMessages:\n%v\n-------------\n", replyMessages, string(b)), nil
}
