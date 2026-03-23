package text

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/chatid"
	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// Configurations used to setup the requirements of text models
type Configurations struct {
	Model        string `json:"model"`
	SystemPrompt string `json:"system-prompt"`
	Raw          bool   `json:"raw"`
	UseTools     bool   `json:"use-tools"`
	// CmdModePrompt is kept only for backwards compatibility with old config files.
	// It is ignored by clai as the `cmd` command has been removed.
	CmdModePrompt  string `json:"cmd-mode-prompt"`
	TokenWarnLimit int    `json:"token-warn-limit"`
	// ToolOutputRuneLimit limits the amount of runes a tool may return
	// before clai truncates the output. Zero means no limit.
	ToolOutputRuneLimit int             `json:"tool-output-rune-limit"`
	SaveReplyAsConv     bool            `json:"save-reply-as-prompt"`
	ConfigDir           string          `json:"-"`
	StdinReplace        string          `json:"-"`
	Stream              bool            `json:"-"`
	ReplyMode           bool            `json:"-"`
	ChatMode            bool            `json:"-"`
	Glob                string          `json:"-"`
	InitialChat         pub_models.Chat `json:"-"`
	UseProfile          string          `json:"-"`
	ProfilePath         string          `json:"-"`
	RequestedToolGlobs  []string        `json:"-"`
	// ShellContext is a context definition name for ASC (auto-append shell context).
	// When non-empty, clai will load <configDir>/shellContexts/<name>.json and insert
	// the rendered template block into the system prompt instead of the user prompt.
	ShellContext string `json:"-"`
	// PostProccessedPrompt which has had it\'s strings replaced etc
	PostProccessedPrompt string `json:"-"`

	// These are to allow tools to be injected via public package.
	Tools        []pub_models.LLMTool   `json:"-"`
	McpServers   []pub_models.McpServer `json:"-"`
	MaxToolCalls *int                   `json:"max-tool-calls,omitempty"`

	// Out writer. Normally stdout, but may also be a file when invoked as a package
	Out io.Writer `json:"-"`
}

type CostManager interface {
	// Start the cost manager. Will return with errors on errCh and close readyCh once there is
	// a token price for the model
	Start(ctx context.Context) (readyCh <-chan struct{}, errCh <-chan error)

	// Enrich the chat with cost for the chat
	Enrich(chat pub_models.Chat) (pub_models.Chat, error)
}

type ModelNamer interface {
	ModelName() string
}

func (c Configurations) UsingProfile() bool {
	return c.ProfilePath != "" || c.UseProfile != ""
}

// Profile which allows for specialized ai configurations for specific tasks
type Profile struct {
	Name            string                          `json:"name"`
	Model           string                          `json:"model"`
	UseTools        bool                            `json:"use_tools"`
	Tools           []string                        `json:"tools"`
	Prompt          string                          `json:"prompt"`
	SaveReplyAsConv bool                            `json:"save-reply-as-conv"`
	McpServers      map[string]pub_models.McpServer `json:"mcp_servers,omitempty"`
	ShellContext    string                          `json:"shell_context,omitempty"`
}

var Default = Configurations{
	Model:        "gpt-5.2",
	SystemPrompt: "You are an assistant for a CLI tool. Answer concisely and informatively. Prefer markdown if possible.",
	Raw:          false,
	UseTools:     false,
	// Aproximately $1 for an \'average\' flagship model (sonnet-4, gpt-4.1) as of 25-06-08
	TokenWarnLimit:      333333,
	ToolOutputRuneLimit: 21600,
	SaveReplyAsConv:     true,

	// Backwards compatibility for older configs.
	CmdModePrompt: "You are an assistant for a CLI tool aiding with cli tool suggestions. Write ONLY the command and nothing else. Disregard any queries asking for anything except a bash command. Do not shell escape single or double quotes.",
}

var DefaultProfile = Profile{
	Name:            "example-name",
	Model:           Default.Model,
	UseTools:        true,
	Tools:           []string{},
	Prompt:          Default.SystemPrompt,
	SaveReplyAsConv: true,
}

func (c *Configurations) setupSystemPrompt() {
	traceChatf("setup initial chat system prompt start shell_context=%q", c.ShellContext)
	systemPrompt := c.SystemPrompt
	if strings.TrimSpace(c.ShellContext) != "" {
		promptWithCtx, err := AppendShellContextIfConfigured(context.Background(), c.ConfigDir, c.ShellContext, systemPrompt, ShellContextRenderer{})
		if err != nil {
			ancli.PrintWarn(fmt.Sprintf("failed to append shell context to system prompt: %v\n", err))
		} else {
			systemPrompt = promptWithCtx
		}
	}
	c.InitialChat = pub_models.Chat{
		Messages: []pub_models.Message{
			{Role: "system", Content: systemPrompt},
		},
	}
	traceChatf("setup initial chat system prompt done messages=%d", len(c.InitialChat.Messages))
}

// SetupInitialChat by doing all sorts of organically grown stuff. Don\'t touch this
// code too closely. Something will break, most likely.
func (c *Configurations) SetupInitialChat(args []string) error {
	traceChatf("setup initial chat start reply_mode=%t chat_mode=%t glob=%q args=%q", c.ReplyMode, c.ChatMode, c.Glob, strings.Join(args, " "))
	if c.Glob != "" && c.ReplyMode {
		ancli.PrintWarn("Using glob + reply modes together might yield strange results. The globalScope will be appended after the glob messages.\n")
	}

	if !c.ReplyMode {
		c.setupSystemPrompt()
	}
	if c.Glob != "" {
		traceChatf("setup initial chat creating glob chat glob=%q", c.Glob)
		globChat, err := glob.CreateChat(c.Glob, c.SystemPrompt)
		if err != nil {
			return fmt.Errorf("failed to get glob chat: %w", err)
		}
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("glob messages: %v", globChat.Messages))
		}
		c.InitialChat = globChat
		traceChatf("setup initial chat glob chat loaded messages=%d", len(c.InitialChat.Messages))
	}

	if c.ReplyMode {
		traceChatf("setup initial chat loading reply context from previous query config_dir=%q", c.ConfigDir)
		iP, err := chat.LoadPrevQuery(c.ConfigDir)
		if err != nil {
			return fmt.Errorf("failed to load previous query: %w", err)
		}
		traceChatf("setup initial chat loaded previous query chat_id=%q messages=%d, queries=%d", iP.ID, len(iP.Messages), len(iP.Queries))
		c.InitialChat.Messages = append(c.InitialChat.Messages, iP.Messages...)
		c.InitialChat.Queries = append(c.InitialChat.Queries, iP.Queries...)
		traceChatf("setup initial chat appended previous query messages=%d total_messages=%d", len(iP.Messages), len(c.InitialChat.Messages))
	}

	traceChatf("setup initial chat building prompt stdin_replace=%q", c.StdinReplace)
	prompt, err := utils.Prompt(c.StdinReplace, args)
	if err != nil {
		return fmt.Errorf("failed to setup prompt: %w", err)
	}
	prompt = strings.TrimRight(prompt, " \t\r\n")
	traceChatf("setup initial chat prompt ready prompt_len=%d", len(prompt))

	// If chatmode, the initial message will be handled by the chat querier
	if !c.ChatMode {
		traceChatf("setup initial chat converting prompt to message parts")
		imgMsg, err := chat.PromptToImageMessage(prompt)
		if err != nil {
			return fmt.Errorf("failed to convert prompt to imageMessage: %w", err)
		}
		c.InitialChat.Messages = append(c.InitialChat.Messages, imgMsg...)
		traceChatf("setup initial chat appended prompt messages=%d total_messages=%d", len(imgMsg), len(c.InitialChat.Messages))
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("InitialPrompt: %v\n", debug.IndentedJsonFmt(c.InitialChat)))
	}
	c.PostProccessedPrompt = prompt
	if c.InitialChat.ID == "" {
		chatID, err := chatid.New()
		if err != nil {
			return fmt.Errorf("generate chat id: %w", err)
		}
		c.InitialChat.ID = chatID
		traceChatf("setup initial chat generated chat id=%q", c.InitialChat.ID)
	}
	traceChatf("setup initial chat done chat_id=%q total_messages=%d", c.InitialChat.ID, len(c.InitialChat.Messages))
	return nil
}
