package text

import (
	"flag"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/chat"
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
	ChatMode      bool
	Glob          string
	Raw           bool
	InitialPrompt models.Chat
	// PostProccessedPrompt which has had it's strings replaced etc
	PostProccessedPrompt string
}

var DEFAULT = Configurations{
	Model:        "gpt-4-turbo-preview",
	SystemPrompt: "You are an assistent for a CLI interface. Answer concisely and informatively. Prefer markdown if possible.",
}

func (c *Configurations) SetupPrompts() error {
	if c.Glob != "" && c.ReplyMode {
		ancli.PrintWarn("Using glob + reply modes together might yield strange results. The prevQuery will be appended after the glob messages.\n")
	}

	primed := false
	args := flag.Args()
	if c.Glob != "" {
		primed = true
		globChat, err := glob.CreateChat(c.Glob, c.SystemPrompt)
		if err != nil {
			return fmt.Errorf("failed to get glob chat: %w", err)
		}
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("glob messages: %v", globChat.Messages))
		}
		c.InitialPrompt = globChat
		args = args[1:]
	}

	if c.ReplyMode {
		primed = true
		iP, err := reply.Load()
		if err != nil {
			return fmt.Errorf("failed to load previous query: %w", err)
		}
		c.InitialPrompt.Messages = append(c.InitialPrompt.Messages, iP.Messages...)
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
	// If chatmode, the initial message will be handled by the chat querier
	if !c.ChatMode {
		c.InitialPrompt.Messages = append(c.InitialPrompt.Messages, models.Message{
			Role:    "user",
			Content: prompt,
		})
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("InitialPrompt: %+v\n", c.InitialPrompt))
	}
	c.PostProccessedPrompt = prompt
	if c.InitialPrompt.ID == "" {
		c.InitialPrompt.ID = chat.IdFromPrompt(prompt)
	}
	return nil
}
