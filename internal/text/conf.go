package text

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// Configurations used to setup the requirements of text models
type Configurations struct {
	Model          string      `json:"model"`
	SystemPrompt   string      `json:"system-prompt"`
	CmdModePrompt  string      `json:"cmd-mode-prompt"`
	Raw            bool        `json:"raw"`
	UseTools       bool        `json:"use-tools"`
	TokenWarnLimit int         `json:"token-warn-limit"`
	ConfigDir      string      `json:"-"`
	StdinReplace   string      `json:"-"`
	Stream         bool        `json:"-"`
	ReplyMode      bool        `json:"-"`
	ChatMode       bool        `json:"-"`
	CmdMode        bool        `json:"-"`
	Glob           string      `json:"-"`
	InitialPrompt  models.Chat `json:"-"`
	// PostProccessedPrompt which has had it's strings replaced etc
	PostProccessedPrompt string `json:"-"`
}

var DEFAULT = Configurations{
	Model:         "gpt-4o",
	SystemPrompt:  "You are an assistant for a CLI tool. Answer concisely and informatively. Prefer markdown if possible.",
	CmdModePrompt: "You are an assistant for a CLI tool aiding with cli tool suggestions. Write ONLY the command and nothing else.",
	Raw:           false,
	UseTools:      false,
	// Aproximately $1 for the worst input rates as of 2024-05
	TokenWarnLimit: 17000,
}

func (c *Configurations) SetupPrompts(args []string) error {
	if c.Glob != "" && c.ReplyMode {
		ancli.PrintWarn("Using glob + reply modes together might yield strange results. The prevQuery will be appended after the glob messages.\n")
	}

	// Allways replace system prompt on cmd mode. This somewhat corrupts the chat since it always will
	// be the command prompt. But it's better than not having it
	if !c.ReplyMode {
		c.InitialPrompt = models.Chat{
			Messages: []models.Message{
				{Role: "system", Content: c.SystemPrompt},
			},
		}
	}
	if c.Glob != "" {
		globChat, err := glob.CreateChat(c.Glob, c.SystemPrompt)
		if err != nil {
			return fmt.Errorf("failed to get glob chat: %w", err)
		}
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("glob messages: %v", globChat.Messages))
		}
		c.InitialPrompt = globChat
	}

	if c.ReplyMode {
		iP, err := reply.Load(c.ConfigDir)
		if err != nil {
			return fmt.Errorf("failed to load previous query: %w", err)
		}
		c.InitialPrompt.Messages = append(c.InitialPrompt.Messages, iP.Messages...)

		if c.CmdMode {
			// Replace the initial message with the cmd prompt. This sort of
			// destroys the history, but since the conversation might be long it's fine
			c.InitialPrompt.Messages[0].Content = c.SystemPrompt
		}
	}

	prompt, err := utils.Prompt(c.StdinReplace, args)
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
		ancli.PrintOK(fmt.Sprintf("InitialPrompt: %v\n", debug.IndentedJsonFmt(c.InitialPrompt)))
	}
	c.PostProccessedPrompt = prompt
	if c.InitialPrompt.ID == "" {
		c.InitialPrompt.ID = chat.IdFromPrompt(prompt)
	}
	return nil
}
