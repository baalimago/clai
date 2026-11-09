package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"

	// Blank import: internal/audio's init wires the audio_transcribe
	// tool engine (mode-as-tool bridge) for library consumers of pkg/agent.
	_ "github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/cli/process"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/diagnostic"
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

	// usageRecorder receives one CompletedModelCall per model step of every
	// query; toolCallRecorder receives one ToolCall per tool invocation. Nil
	// keeps clai's noop paths (worklog 26-08-11-clai-prometheus-metrics).
	usageRecorder    models.CallUsageRecorder
	toolCallRecorder models.ToolCallRecorder

	// outputIsTerminal feeds Configurations.OutputIsTerminal, which decides
	// D22's mid-run-authorization default (worklog
	// 2026-10-02-mcp-connection-cost, phase 6, R2-09). nil (the default)
	// leaves it unset, which resolves to the conservative fail-fast
	// posture: asInternalConfig hardcodes Out to io.Discard, so the
	// querier's own terminal check can never be true for this agent.
	outputIsTerminal *bool

	// logger receives one slog record per completed message (assistant,
	// reasoning, tool_call, tool_result, final_answer), truncated to
	// slogRuneLimit runes. Nil (the default) disables the channel. Library
	// mode stays silent on stdout regardless: asInternalConfig hardcodes
	// Out to io.Discard, so the logger is the sole embedded output channel
	// (worklog 2026-08-15-agent-slog-output, D4).
	logger *slog.Logger
	// slogLevel is the single caller-set level for every logged message; the
	// kind attribute is how a caller filters finer (worklog 2026-08-15-agent-slog-output, D3). Default Debug.
	slogLevel slog.Level
	// slogRuneLimit caps every logged text to this many runes via a rune-safe
	// head/tail split around the single-rune marker "…"; <= 0 means no cap
	// (worklog 2026-08-15-agent-slog-output, D2, D5). Default 200.
	slogRuneLimit int

	// diagnosticReporter receives agent diagnostics, the internal ones of
	// the run included; nil keeps the agent silent (phase 8 of worklog
	// 2026-11-02-diagnostics, F3). diagnosticLevels decide which detail the
	// run's producers build for it.
	diagnosticReporter diagnostic.Reporter
	diagnosticLevels   diag.ResolvedLevels

	querierCreator func(ctx context.Context, conf text.Configurations) (priv_models.Querier, error)

	// stdin and stdinIsTerminal are the process input a run reads when its
	// caller supplies no run environment: audio_record stops on Enter there.
	stdin           io.Reader
	stdinIsTerminal bool

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
	querierCreator: text.CreateQuerier,
	slogLevel:      slog.LevelDebug,
	slogRuneLimit:  200,
}

type Option func(*Agent)

func New(options ...Option) Agent {
	conf := defaultConf
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "."
	}
	conf.cfgDir = path.Join(home, ".config", "clai")
	conf.stdin, conf.stdinIsTerminal = process.Stdin()

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

// WithTools offers tools to the model. Without WithToolGlobs they are the
// only tools of the run; an agent given neither option offers every built-in
// tool (cmd, write_file and apply_patch among them) and every MCP tool. The
// model can call only what it was offered: a call naming any other tool is
// answered with "ERROR: unknown tool call: <name>" and runs nothing.
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

// WithLogger attaches a *slog.Logger to the agent. The logger receives one
// record per completed message (assistant, reasoning, tool_call,
// tool_result, final_answer), truncated to WithSlogRuneLimit runes. nil (the
// default) disables the channel. Library mode stays silent on stdout
// regardless of this option: the querier's terminal display is discarded and
// the logger is the sole embedded output channel.
func WithLogger(l *slog.Logger) Option {
	return func(a *Agent) {
		a.logger = l
	}
}

// WithSlogLevel sets the single level used for every logged message. The
// default is slog.LevelDebug; the message kind is carried as the "kind"
// attribute so callers can filter finer without per-kind levels.
func WithSlogLevel(level slog.Level) Option {
	return func(a *Agent) {
		a.slogLevel = level
	}
}

// WithSlogRuneLimit caps every logged message text to n runes with a rune-safe
// head/tail split around the single-rune marker "…". The default is 200; a
// value <= 0 disables the cap (the full text is logged unchanged).
func WithSlogRuneLimit(n int) Option {
	return func(a *Agent) {
		a.slogRuneLimit = n
	}
}

// WithToolGlobs sets freetext glob patterns to filter which tools are registered.
// Supports wildcards like "mcp_*", "mcp_playwright_*", or exact names like "cat".
// Only the matched tools, and those of WithTools, are offered to the model
// and callable by it.
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
// The policy is carried on each tool-call context of the agent's own
// querier, so agents with distinct lists can run concurrently in one process.
// There is no package-level policy: a tool called outside an agent query is
// permissive.
func WithCmdBanList(entries ...string) Option {
	return func(a *Agent) {
		a.cmdBan = entries
	}
}

// WithUsageRecorder registers a CallUsageRecorder that receives one
// CompletedModelCall per model step of every query. Nil (the default)
// keeps clai's noop path: no recording, no behavior change. A Record error
// is logged by the session runner and never aborts the run.
func WithUsageRecorder(rec models.CallUsageRecorder) Option {
	return func(a *Agent) {
		a.usageRecorder = rec
	}
}

// WithToolCallRecorder registers a ToolCallRecorder that receives one
// ToolCall per tool invocation of every query. Nil (the default) keeps
// the noop path. A RecordToolCall error is logged and never aborts the
// run.
func WithToolCallRecorder(rec models.ToolCallRecorder) Option {
	return func(a *Agent) {
		a.toolCallRecorder = rec
	}
}

// WithResponseFormat configures structured output for the agent.
// Supports "json_object" and "json_schema" types.
func WithResponseFormat(rf models.ResponseFormat) Option {
	return func(a *Agent) {
		a.responseFormat = &rf
	}
}

// WithOutputIsTerminal tells the agent whether a human is attending this
// run, which decides D22's mid-run-authorization default: bounded when
// true, fail-fast when false (worklog 2026-10-02-mcp-connection-cost,
// phase 6, R2-09). Library mode's writer is always io.Discard, which
// carries no terminal signal of its own, so an agent that wants anything
// other than the conservative fail-fast default must set this explicitly.
func WithOutputIsTerminal(interactive bool) Option {
	return func(a *Agent) {
		a.outputIsTerminal = &interactive
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
		// Library mode is silent on stdout: embedded use never writes raw
		// terminal output. The slog logger (AgentSettings) is the sole
		// embedded output channel (worklog 2026-08-15-agent-slog-output, D4).
		Out: io.Discard,
		// nil unless WithOutputIsTerminal was used; NewQuerier then derives
		// the conservative fail-fast default from Out above rather than
		// overwriting an explicit value (R2-09).
		OutputIsTerminal: a.outputIsTerminal,
	}
	// Agent-only settings ride one pointer (worklog 2026-08-15-agent-slog-output, D7): the slog logger, its level,
	// the rune cap, and both recorder hooks. NewQuerier reads the recorders
	// from this pointer; Configurations carries no loose recorder fields.
	conf.AgentSettings = &text.AgentSettings{
		Logger:           a.logger,
		Level:            a.slogLevel,
		RuneLimit:        a.slogRuneLimit,
		UsageRecorder:    a.usageRecorder,
		ToolCallRecorder: a.toolCallRecorder,
		// Servers registered via WithMcpServers are load-bearing: a startup
		// failure fails Setup instead of degrading (worklog
		// 2026-09-05-error-propagation, D13).
		StrictMcpStartup: len(a.mcpServers) > 0,
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

	ctx = a.runContext(ctx)
	for _, dir := range []string{a.cfgDir, path.Join(a.cfgDir, "mcpServers"), path.Join(a.cfgDir, "conversations")} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			if err := os.Mkdir(dir, 0o755); err != nil {
				a.report(ctx, diagnosticConfigDirUnavailable, "agent configuration directory could not be created",
					slog.String("path", dir), slog.String("error", err.Error()))
			}
		}
	}

	querier, err := a.querierCreator(ctx, a.asInternalConfig())
	if err != nil {
		return diag.Errorf("publicQuerier.Setup failed to CreateTextQuerier: %w", err)
	}
	tq, isChatQuerier := querier.(priv_models.ChatQuerier)
	if !isChatQuerier {
		return fmt.Errorf("failed to cast Querier using model: '%v' to TextQuerier, cannot proceed", a.model)
	}
	a.querier = tq
	a.report(ctx, diagnosticSetupCompleted, "agent setup completed", slog.String("model", a.model))
	return nil
}

// runContext is ctx carrying a run environment. The caller's own is kept;
// without one the run reads the process input with its terminal state and
// writes nothing. Its internal diagnostics reach the caller's Reporter, and
// without one it reports nothing, so the library stays silent (F3).
func (a *Agent) runContext(ctx context.Context) context.Context {
	if _, ok := runenv.Lookup(ctx); ok {
		return ctx
	}
	stdin := a.stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	config := runenv.Config{Streams: stdio.New(stdio.Config{
		Format:        jsonl.FormatText,
		Stdin:         stdin,
		Stdout:        io.Discard,
		Stderr:        io.Discard,
		StdinTerminal: a.stdinIsTerminal && a.stdin != nil,
	})}
	if a.diagnosticReporter != nil {
		config.Reporter, config.Levels = a.diagnosticReporter, a.diagnosticLevels
	}
	return runenv.With(ctx, runenv.New(config))
}
