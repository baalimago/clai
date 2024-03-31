package text

import (
	"flag"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// Configurations used to setup the requirements of text models
type Configurations struct {
	Model         string `json:"model"`
	SystemPrompt  string `json:"system-prompt"`
	StdinReplace  string
	Stream        bool
	ReplyMode     bool
	Glob          string
	Raw           bool
	InitialPrompt models.Chat
}

var DEFAULT = Configurations{
	Model:        "gpt-4-turbo-preview",
	SystemPrompt: "You are an assistent for a CLI interface. Answer concisely and informatively. Prefer markdown if possible.",
}

func (c *Configurations) SetupPrompts() error {
	if c.Glob != "" && c.ReplyMode {
		ancli.PrintWarn("Using glob + reply modes together might yield strange results. The prevQuery will be appended after the glob messages.")
	}

	primed := false
	args := flag.Args()
	if c.Glob != "" {
		globChat, err := glob.CreateChat(c.Glob, c.SystemPrompt)
		if err != nil {
			return fmt.Errorf("failed to get glob chat: %w", err)
		}
		c.InitialPrompt = globChat
		args = args[2:]
	}

	if c.ReplyMode {
		primed = true
		iP, err := reply.Load()
		if err != nil {
			return fmt.Errorf("failed to load previous query: %w", err)
		}
		c.InitialPrompt.Messages = append(c.InitialPrompt.Messages, iP.Messages...)
		args = args[1:]
	}
	if !primed {
		c.InitialPrompt.Messages = append(c.InitialPrompt.Messages, models.Message{
			Role:    "system",
			Content: c.SystemPrompt,
		})
	}
	prompt, err := tools.Prompt(c.StdinReplace, args)
	if err != nil {
		return fmt.Errorf("failed to setup prompt: %w", err)
	}
	c.InitialPrompt.Messages = append(c.InitialPrompt.Messages, models.Message{
		Role:    "user",
		Content: prompt,
	})
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("InitialPrompt: %+v\n", c.InitialPrompt))
	}
	return nil
}
