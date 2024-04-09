package photo

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func (c *Configurations) SetupPrompts() error {
	args := flag.Args()
	if c.ReplyMode {
		confDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("failed to get config dir: %w", err)
		}
		iP, err := reply.Load(confDir)
		if err != nil {
			return fmt.Errorf("failed to load previous query: %w", err)
		}
		if len(iP.Messages) > 0 {
			replyMessages := "You will be given a serie of messages from different roles, then a prompt descibing what to do with these messages. "
			replyMessages += "Between the messages and the prompt, there will be this line: '-------------'."
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
	prompt, err := tools.Prompt(c.StdinReplace, args)
	if err != nil {
		return fmt.Errorf("failed to setup prompt from stdin: %w", err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("format: '%v', prompt: '%v'\n", c.PromptFormat, prompt))
	}
	c.Prompt += fmt.Sprintf(c.PromptFormat, prompt)
	return nil
}
