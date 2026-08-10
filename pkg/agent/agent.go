package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/chat"
	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/text/models"
)

type Agent struct {
	name           string
	model          string
	prompt         string
	tools          []models.LLMTool
	mcpServers     []models.McpServer
	cfgDir         string
	toolGlobs      []string
	cmdBan         []string
	maxToolCalls   *int
	stoploss       Stoploss
	responseFormat *models.ResponseFormat

	querierCreator func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error)

	out io.Writer

	querier priv_models.ChatQuerier
}

// skipIndexOnce disables the chat index exactly once. Agent.Setup may run for
// multiple agents concurrently (worklog 2026-08-02-cmd-ban-list, phase 5, R2-01
// enforcement requires concurrent Setups); an unsynchronized write to
// chat.SkipIndex from every Setup would race under -race.
var skipIndexOnce sync.Once

var defaultConf = Agent{
	model:          "gpt-5.2",
	prompt:         "Uh-oh. Something is not quite right. Please ask the user to overlook his agentic setup, and to update the prompt.",
	tools:          make([]models.LLMTool, 0),
	mcpServers:     make([]models.McpServer, 0),
	querierCreator: internal.CreateTextQuerier,
	out:            os.Stdout,
}

type Option func(*Agent)

func New(options ...Option) Agent {
	conf := defaultConf
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	conf.cfgDir = path.Join(home, ".config", "clai")

	for _, o := range options {
		o(&conf)
	}
	return conf
}

func WithConfigDir(cfgDir string) Option {
	return func(a *Agent) {
		if !strings.HasSuffix(cfgDir, "clai") {
			cfgDir = path.Join(cfgDir, "clai")
		}
		a.cfgDir = cfgDir
	}
}

// Stoploss configures the token stoploss policy for an agent run. MaxTokens
// <= 0 disables the stoploss. MaxTokensHandoverMsg is the user message
// injected into the chat when the limit is crossed; empty means the default
// message (text.DefaultHandoverInstructions). MaxToolCallsAfterHandover is
// the wrap-up tool-call budget for the post-handover phase: <= 0 means
// unlimited tool calls after the handover fires. It is inert without
// MaxTokens > 0: the internal config drops the whole Stoploss object
// otherwise, so the agent stays unlimited.
type Stoploss struct {
	MaxTokens                 int
	MaxTokensHandoverMsg      string
	MaxToolCallsAfterHandover int
}

// WithStoploss configures the token stoploss policy for the agent. A
// zero-value Stoploss disables the stoploss: the agent default stays
// unlimited.
func WithStoploss(s Stoploss) Option {
	return func(a *Agent) {
		a.stoploss = s
	}
}

// WithMaxToolCalls sets the maximum number of tool calls for the run.
// 0 means no limit.
func WithMaxToolCalls(am int) Option {
	return func(a *Agent) {
		a.maxToolCalls = &am
	}
}

func WithModel(model string) Option {
	return func(a *Agent) {
		a.model = model
	}
}

func WithPrompt(prompt string) Option {
	return func(a *Agent) {
		a.prompt = prompt
	}
}

func WithTools(tools []models.LLMTool) Option {
	return func(a *Agent) {
		a.tools = tools
	}
}

func WithMcpServers(mcpServers []models.McpServer) Option {
	return func(a *Agent) {
		a.mcpServers = mcpServers
	}
}

func WithOutputTo(out io.Writer) Option {
	return func(a *Agent) {
		a.out = out
	}
}

// WithToolGlobs sets freetext glob patterns to filter which tools are registered.
// Supports wildcards like "mcp_*", "mcp_playwright_*", or exact names like "cat".
func WithToolGlobs(globs ...string) Option {
	return func(a *Agent) {
		a.toolGlobs = globs
	}
}

// WithCmdBanList sets the per-run command ban list for the freetext command
// tools (cmd, async_cmd; legacy aliases freetext_command, async_cmd_run).
// Commands matching an entry
// are refused before spawn and the refusal names the matched entry. The
// default is empty, which keeps all tools fully permissive.
//
// The policy is carried through each query context, so agents with distinct
// lists can run concurrently in one process. Direct tool calls outside an
// agent query use the package-level fallback policy.
func WithCmdBanList(entries ...string) Option {
	return func(a *Agent) {
		a.cmdBan = entries
	}
}

// WithResponseFormat configures structured output for the agent.
// Supports "json_object" and "json_schema" types.
func WithResponseFormat(rf models.ResponseFormat) Option {
	return func(a *Agent) {
		a.responseFormat = &rf
	}
}

func (a *Agent) asInternalConfig() text.Configurations {
	conf := text.Configurations{
		Model:              a.model,
		SystemPrompt:       a.prompt,
		ConfigDir:          a.cfgDir,
		UseTools:           true,
		SaveReplyAsConv:    true,
		McpServers:         a.mcpServers,
		Tools:              a.tools,
		MaxToolCalls:       a.maxToolCalls,
		RequestedToolGlobs: a.toolGlobs,
		CmdBan:             a.cmdBan,
		ResponseFormat:     a.responseFormat,
		Out:                a.out,
	}
	// A zero-value Stoploss must not create a non-nil internal pointer: the
	// agent default stays unlimited (MaxTokens <= 0 disables the stoploss).
	if a.stoploss.MaxTokens > 0 {
		conf.Stoploss = &text.Stoploss{
			MaxTokens:                 a.stoploss.MaxTokens,
			MaxTokensHandoverMsg:      a.stoploss.MaxTokensHandoverMsg,
			MaxToolCallsAfterHandover: a.stoploss.MaxToolCallsAfterHandover,
		}
	}
	return conf
}

func (a *Agent) Setup(ctx context.Context) error {
	// Embedded consumers (kinoview, etc.) never use CLI list/search/dirscope
	// features; the chat index is pure overhead that causes OOM on 32-bit ARM
	// when the conversation directory is large.
	skipIndexOnce.Do(func() { chat.SkipIndex = true })

	if _, err := os.Stat(a.cfgDir); os.IsNotExist(err) {
		os.Mkdir(a.cfgDir, 0o755)
	}
	mcpServersDir := path.Join(a.cfgDir, "mcpServers")
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		os.Mkdir(mcpServersDir, 0o755)
	}
	conversationsDir := path.Join(a.cfgDir, "conversations")
	if _, err := os.Stat(conversationsDir); os.IsNotExist(err) {
		os.Mkdir(conversationsDir, 0o755)
	}

	querier, err := a.querierCreator(ctx, a.asInternalConfig())
	if err != nil {
		return fmt.Errorf("publicQuerier.Setup failed to CreateTextQuerier: %v", err)
	}
	tq, isChatQuerier := querier.(priv_models.ChatQuerier)
	if !isChatQuerier {
		return fmt.Errorf("failed to cast Querier using model: '%v' to TextQuerier, cannot proceed", a.model)
	}
	a.querier = tq
	return nil
}
