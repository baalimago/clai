package text

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/chatid"
	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/text/generic"
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
	UseSkills    bool   `json:"-"`
	// CmdModePrompt is kept only for backwards compatibility with old config files.
	// It is ignored by clai as the `cmd` command has been removed.
	CmdModePrompt  string `json:"cmd-mode-prompt"`
	TokenWarnLimit int    `json:"token-warn-limit"`
	// ToolOutputRuneLimit limits the amount of runes a tool may return
	// before clai truncates the output. Zero means no limit.
	ToolOutputRuneLimit int    `json:"tool-output-rune-limit"`
	SaveReplyAsConv     bool   `json:"save-reply-as-prompt"`
	ConfigDir           string `json:"-"`
	StdinReplace        string `json:"-"`
	Stream              bool   `json:"-"`
	ReplyMode           bool   `json:"-"`
	// DirReplyMode marks a directory-scoped reply (-dre). Unlike a plain -re (which
	// forks a fresh promoted id and must not record), -dre continues the bound
	// conversation in place, so it DOES upsert the directory history (see finalizer).
	DirReplyMode        bool            `json:"-"`
	ChatMode            bool            `json:"-"`
	Glob                string          `json:"-"`
	InitialChat         pub_models.Chat `json:"-"`
	UseProfile          string          `json:"-"`
	ProfilePath         string          `json:"-"`
	ProfileUseSkillsSet bool            `json:"-"`
	RequestedToolGlobs  []string        `json:"-"`
	// ShellContext is a context definition name for ASC (auto-append shell context).
	// When non-empty, clai will load <configDir>/shellContexts/<name>.json and insert
	// the rendered template block into the system prompt instead of the user prompt.
	ShellContext string `json:"-"`
	// PostProccessedPrompt which has had it\'s strings replaced etc
	PostProccessedPrompt string `json:"-"`

	// These are to allow tools to be injected via public package.
	Tools        []pub_models.LLMTool          `json:"-"`
	McpServers   []pub_models.McpServer        `json:"-"`
	BaseTools    map[string]pub_models.LLMTool `json:"-"`
	MaxToolCalls *int                          `json:"max-tool-calls,omitempty"`

	// Out writer. Normally stdout, but may also be a file when invoked as a package
	Out io.Writer `json:"-"`

	// ResponseFormat configures structured output (json_object, json_schema).
	// When nil, no response_format is sent (defaults to text).
	ResponseFormat   *pub_models.ResponseFormat `json:"-"`
	SkillsDescriptor string                     `json:"-"`
	SkillLoader      SkillLoader                `json:"-"`

	// UseLookback gates the opt-in conversation lookback: the recent-conversations
	// descriptor plus the search/inspect/read tools. Resolved as
	// CLI (-lb/-lookback) > profile (use_lookback) > default (false), and is only
	// effectively true when the CWD binding has recorded history.
	UseLookback bool `json:"use_lookback"`
	// LookbackDescriptor is the dir-scoped recent-conversations block injected into
	// the system prompt when the lookback is active.
	LookbackDescriptor string `json:"-"`
	// LookbackCWD is the canonical session working directory captured at setup,
	// used as the default search anchor for search_conversations.
	LookbackCWD string `json:"-"`
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
	UseSkills       *bool                           `json:"use_skills,omitempty"`
	Tools           []string                        `json:"tools"`
	Prompt          string                          `json:"prompt"`
	SaveReplyAsConv *bool                           `json:"save-reply-as-conv,omitempty"`
	McpServers      map[string]pub_models.McpServer `json:"mcp_servers,omitempty"`
	ShellContext    string                          `json:"shell_context,omitempty"`
	UseLookback     *bool                           `json:"use_lookback,omitempty"`
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
	UseLookback:         false,

	// Backwards compatibility for older configs.
	CmdModePrompt: "You are an assistant for a CLI tool aiding with cli tool suggestions. Write ONLY the command and nothing else. Disregard any queries asking for anything except a bash command. Do not shell escape single or double quotes.",
}

var DefaultProfile = Profile{
	Name:            "example-name",
	Model:           Default.Model,
	UseTools:        true,
	UseSkills:       nil,
	Tools:           []string{},
	Prompt:          Default.SystemPrompt,
	SaveReplyAsConv: new(true),
}

func (c *Configurations) setupSystemPrompt() {
	traceChatf("setup initial chat system prompt start shell_context=%q", c.ShellContext)
	systemPrompt := c.SystemPrompt
	if strings.TrimSpace(c.SkillsDescriptor) != "" {
		systemPrompt += "\n\n" + c.SkillsDescriptor
	}
	if strings.TrimSpace(c.LookbackDescriptor) != "" {
		systemPrompt += "\n\n" + c.LookbackDescriptor
	}
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
		if c.InitialChat.ID == "" && iP.ID != "" && iP.ID != "globalScope" {
			c.InitialChat.ID = iP.ID
			traceChatf("setup initial chat adopted previous query chat id=%q", c.InitialChat.ID)
		}
		if c.InitialChat.Created.IsZero() && !iP.Created.IsZero() {
			c.InitialChat.Created = iP.Created
			traceChatf("setup initial chat adopted previous query created=%q", c.InitialChat.Created.Format(time.RFC3339Nano))
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
	if c.InitialChat.Created.IsZero() {
		c.InitialChat.Created = time.Now()
		traceChatf("setup initial chat set created timestamp created=%q", c.InitialChat.Created.Format(time.RFC3339Nano))
	}
	traceChatf("setup initial chat done chat_id=%q total_messages=%d", c.InitialChat.ID, len(c.InitialChat.Messages))
	return nil
}

// toGenericResponseFormat converts the public ResponseFormat to the internal type
// used by generic.StreamCompleter.
func toGenericResponseFormat(rf *pub_models.ResponseFormat) *generic.ResponseFormat {
	if rf == nil {
		return nil
	}
	gf := &generic.ResponseFormat{
		Type: rf.Type,
	}
	if rf.Schema != nil {
		s := rf.Schema
		gf.JSONSchema = &generic.JSONSchemaSpec{
			Name:        s.Name,
			Description: s.Description,
			Strict:      s.Strict,
			Schema:      s.Schema,
		}
	}
	return gf
}

// responseFormatFromGeneric converts the internal generic.ResponseFormat (used for
// JSON deserialization from files) to the public ResponseFormat.
func responseFormatFromGeneric(gf *generic.ResponseFormat) *pub_models.ResponseFormat {
	if gf == nil {
		return nil
	}
	rf := &pub_models.ResponseFormat{
		Type: gf.Type,
	}
	if gf.JSONSchema != nil {
		rf.Schema = &pub_models.JSONSchema{
			Name:        gf.JSONSchema.Name,
			Description: gf.JSONSchema.Description,
			Strict:      gf.JSONSchema.Strict,
			Schema:      gf.JSONSchema.Schema,
		}
	}
	return rf
}

// LoadResponseFormat loads a response_format JSON file from disk and sets it
// on the configuration. The file must follow the OpenAI response_format schema.
func (c *Configurations) LoadResponseFormat(path string) error {
	var gf generic.ResponseFormat
	if err := utils.ReadAndUnmarshal(path, &gf); err != nil {
		return fmt.Errorf("failed to load response format from %q: %w", path, err)
	}
	c.ResponseFormat = responseFormatFromGeneric(&gf)
	return nil
}
