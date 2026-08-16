package text

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

const (
	RateLimitRetries     = 3
	FallbackWaitDuration = 20 * time.Second

	// maxReasoningBuf caps the reasoning text accumulated per stream. A
	// looping model can stream reasoning tokens forever; without the cap the
	// strings.Builder here grows unboundedly (O(n) per append, doubling the
	// backing array) until the process OOMs (kinoview production incident,
	// 2026-08-11: 2.53 GB heap). Keeping the tail preserves the reasoning
	// context that matters for tool calls and the final answer.
	maxReasoningBuf = 1 << 20 // 1 MiB
)

type Querier[C models.StreamCompleter] struct {
	Raw              bool
	structuredOutput bool
	outputModeKnown  bool
	outputIsTerminal bool
	chat             pub_models.Chat
	callStackLevel   int
	username         string
	// dims is the one terminal-dimensions snapshot of this interactive output
	// session. It is resolved once in NewQuerier from the session writer's fd;
	// every width-aware render path of the querier reads this value. The
	// snapshot is always usable: a failed read yields dimensions.Fallback.
	dims                    dimensions.Dimensions
	lineCount               int
	line                    string
	fullMsg                 string
	configDir               string
	debug                   bool
	debugTextQuerierPrinted bool
	shouldSaveReply         bool
	// replyMode gates always-on history recording: reply queries (-re/-dre) fork a
	// fresh promoted id, so recording them would pollute the directory history.
	replyMode bool
	// dirReplyMode marks a directory-scoped reply (-dre). It continues the bound
	// conversation in place (no fork), so unlike a plain -re it DOES record into the
	// directory history; the finalizer gates recording on !replyMode || dirReplyMode.
	dirReplyMode bool
	// useLookback enables internal dispatch of the search/inspect/read tools.
	useLookback bool
	// lookbackCWD is the canonical session working directory, the default anchor
	// for search_conversations.
	lookbackCWD           string
	hasPrinted            bool
	Model                 C
	toolOutputRuneLimit   int
	rateLimitLastAmTokens int

	// systemPrompt is the configured system prompt, injected into every
	// TextQuery call that does not already carry a system message.
	systemPrompt string

	// reasoningBuf preserves source reasoning for the session and model. The
	// activity viewport is a separate, bounded terminal-only copy.
	reasoningBuf     strings.Builder
	reasoningActive  bool
	activityViewport *utils.ActivityViewport
	// mcpSink buffers MCP server stderr lines for the rolling window. It is
	// created in NewQuerier before the MCP clients spawn and drained on the
	// serialized session loop.
	mcpSink *mcpLogSink
	// finalAnswerPopPending marks that the trailing assistant-prose block was
	// removed from the viewport state at stream end. The window redraws (and
	// the answer prints below it) only when the finalizer prints the answer,
	// so the terminal paints the pop and the reprint as one transition.
	finalAnswerPopPending bool

	// Output of the querier. This is used mostly when Querier is invoked as an agent
	out io.Writer

	// agentSettings carries the agent-only runtime settings (slog logger,
	// level, rune cap, recorder hooks) into the querier. nil (the CLI and
	// pkg/text paths) keeps every channel disabled.
	agentSettings *AgentSettings

	// isLikelyGemini3Preview is set to true if it's likely that the current underlying model
	// is the gemini 3 preview which suffers from an issue where it insists on crashing if there
	// is no "though_signature" within extra content, while also sending requests which lack "though_signature"
	//
	// Maybe one day this hack can be removed.
	isLikelyGemini3Preview bool

	maxToolCalls *int
	amToolCalls  int

	// stoploss is the token stoploss policy carried from the user config; the
	// stoploss controller consumes it in the session runner (Phase 3).
	stoploss *Stoploss

	costManager       CostManager
	costMgrRdyChan    <-chan struct{}
	costMgrErrChan    <-chan error
	callUsageRecorder CallUsageRecorder
	// toolCallRecorder receives one ToolCall per tool invocation of the
	// session. Nil keeps the noop path (worklog 26-08-11-clai-prometheus-metrics).
	toolCallRecorder ToolCallRecorder
	skillLoader      SkillLoader
	baseTools        map[string]pub_models.LLMTool
	registeredTools  map[string]struct{}
}

func (q *Querier[C]) SuppressCompletionNotification() bool {
	return q.structuredOutput || (q.outputModeKnown && !q.outputIsTerminal)
}

type SkillLoader interface {
	LoadSkill(context.Context, string, string, map[string]pub_models.LLMTool) (LoadedSkillRuntime, error)
}

type LoadedSkillRuntime struct {
	Name            string
	SourceClass     string
	RenderedBody    string
	UserVisibleBody string
	Description     string
	Warnings        []string
	EnabledTools    []string
	ActiveTools     map[string]pub_models.LLMTool
	ActivationErr   string
	RawArgs         string
}

func (q *Querier[C]) postProcessOutput(ctx context.Context, newSysMsg pub_models.Message) {
	// Agent logging is unconditional: the final answer record fires before any
	// display branch so raw, structured, rolling, and terminal paths all emit
	// it (worklog 2026-08-15-agent-slog-output, Phase 3).
	q.logMessage(ctx, "final_answer", newSysMsg.Content, "")
	// The token should already have been printed while streamed
	if q.rawDisplay() {
		if q.debug {
			w := q.out
			if w == nil {
				w = os.Stdout
			}
			fmt.Fprintln(w, newSysMsg.Content)
		}
		return
	}
	if q.activityViewport != nil {
		// Rolling window: the final answer was removed from the window at stream
		// end. Redraw the window (without the answer) and print the answer below
		// it back-to-back, so the terminal shows one transition. When no pop is
		// pending, the assistant text already lives inside the window
		// (intermediate prose before tool calls) and must never be re-printed.
		if q.finalAnswerPopPending {
			if err := q.activityViewport.Render(q.out); err != nil {
				ancli.Warnf("failed to redraw activity viewport before final answer: %v", err)
			}
			utils.AttemptPrettyPrint(q.out, newSysMsg, q.username, q.Raw)
		}
		return
	}
	if q.dims.Width > 0 {
		utils.UpdateMessageTerminalMetadata(newSysMsg.Content, &q.line, &q.lineCount, q.dims.Width)
		// Write the details of q to the file determined by the environment variable DEBUG_OUTPUT_FILE
		if debugOutputFile := debugflags.OutputFile(); debugOutputFile != "" {
			file, err := os.Create(debugOutputFile)
			if err != nil {
				ancli.PrintErr(fmt.Sprintf("failed to create debug output file: %v\n", err))
			} else {
				defer file.Close()
				_, err = file.WriteString(debug.IndentedJsonFmt(struct {
					FullMessage string
					Line        string
					LineCount   int
					TermWidth   int
				}{
					FullMessage: q.fullMsg,
					Line:        q.line,
					LineCount:   q.lineCount,
					TermWidth:   q.dims.Width,
				}))
				if err != nil {
					ancli.PrintErr(fmt.Sprintf("failed to write to debug output file: %v\n", err))
				}
			}
		}
		table.ClearTermTo(q.out, q.lineCount-1)
	} else {
		fmt.Println()
	}
	utils.AttemptPrettyPrint(q.out, newSysMsg, q.username, q.Raw)
}

func (q *Querier[C]) postProcess() {
	session := &QuerySession{
		Chat:               q.chat,
		ShouldSaveReply:    q.shouldSaveReply,
		Raw:                q.rawDisplay(),
		FinalAssistantText: q.fullMsg,
		FinalUsage:         q.chat.TokenUsage,
		Finalized:          q.hasPrinted,
		Line:               q.line,
		LineCount:          q.lineCount,
	}
	sessionFinalizer[C]{querier: q}.Finalize(context.Background(), session)
	q.chat = session.Chat
	q.fullMsg = session.FinalAssistantText
	q.line = session.Line
	q.lineCount = session.LineCount
	q.hasPrinted = session.Finalized
}

func (q *Querier[C]) resetTransientState() {
	q.fullMsg = ""
	q.line = ""
	q.lineCount = 0
	q.hasPrinted = false
	q.reasoningBuf.Reset()
	q.reasoningActive = false
	q.activityViewport = nil
	q.finalAnswerPopPending = false
}

func (q *Querier[C]) handleToken(token string) {
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	q.fullMsg += token
	if !q.debug {
		fmt.Fprint(w, token)
	}
}

func (q *Querier[C]) handleTokenForSession(session *QuerySession, token string) error {
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	session.AppendPendingText(token)
	q.fullMsg = session.PendingTextString()
	if !q.debug && !q.structuredOutput {
		if q.usesActivityViewport() && q.activityViewport != nil {
			q.activityViewport.AppendText(token)
			if err := q.activityViewport.Render(w); err != nil {
				return fmt.Errorf("render activity viewport: %w", err)
			}
			return nil
		}
		fmt.Fprint(w, token)
	}
	return nil
}

// appendReasoning accumulates one reasoning token into reasoningBuf, keeping
// only the last maxReasoningBuf bytes. An endless reasoning stream (a looping
// model) otherwise grows the builder without bound — O(n) per append with
// doubling backing arrays — until the process OOMs. The tail is what survives
// into tool calls and the final answer.
func (q *Querier[C]) appendReasoning(content string) {
	q.reasoningBuf.WriteString(content)
	if q.reasoningBuf.Len() > maxReasoningBuf {
		tail := q.reasoningBuf.String()
		q.reasoningBuf.Reset()
		q.reasoningBuf.WriteString(tail[len(tail)-maxReasoningBuf:])
	}
}

func (q *Querier[C]) usesActivityViewport() bool {
	return utils.RollingOutputEnabled() && !q.rawDisplay() && !q.debug && !q.structuredOutput
}

// ensureActivityViewport lazily creates the rolling activity window at the
// session's current dimensions snapshot. A resize can arrive before the first
// reasoning or tool event; the snapshot already carries the fresh size then
// (applyResize updates q.dims first), so the first render uses the new
// dimensions immediately. The initial effective height is bound to
// min(configured cap, terminal height) at creation, so the first frame never
// exceeds the terminal even without a resize (R5-01).
func (q *Querier[C]) ensureActivityViewport() *utils.ActivityViewport {
	if q.activityViewport == nil {
		q.activityViewport = utils.NewActivityViewport(q.dims.Width, utils.RollingOutputWindowCellHeight(), q.dims.Height)
	}
	return q.activityViewport
}

// drainMcpLogs moves buffered MCP server stderr lines into the rolling window.
// Normal lines coalesce per server into one bounded block; error lines and
// exit tails elevate below the window so they stay in the scrollback. It runs
// on the serialized session loop, the only place the viewport mutates, and is
// a cheap no-op when nothing is buffered.
func (q *Querier[C]) drainMcpLogs() error {
	if q.mcpSink == nil {
		return nil
	}
	entries := q.mcpSink.Drain()
	if len(entries) == 0 {
		return nil
	}
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	normal := make(map[string]*strings.Builder)
	var normalOrder []string
	for _, entry := range entries {
		if entry.isError {
			lines := entry.lines
			if !entry.exit {
				lines = []string{entry.line}
			}
			if err := q.elevateMcpError(w, entry.server, lines); err != nil {
				return err
			}
			continue
		}
		builder, ok := normal[entry.server]
		if !ok {
			builder = &strings.Builder{}
			normal[entry.server] = builder
			normalOrder = append(normalOrder, entry.server)
		}
		builder.WriteString(entry.line)
		builder.WriteString("\n")
	}
	for _, server := range normalOrder {
		q.ensureActivityViewport().AppendMcpLogBlock(server, normal[server].String(), utils.ToolOutputRows())
	}
	if len(normal) > 0 {
		return q.activityViewport.Render(w)
	}
	return nil
}

// elevateMcpError moves an MCP server error block out of the rolling window
// into the scrollback. The window region is cleared, the styled error block
// prints below it, and the window redraws underneath, so the error is never
// evicted. When no window exists yet, the block prints directly and the first
// window frame renders below it.
func (q *Querier[C]) elevateMcpError(w io.Writer, server string, lines []string) error {
	if q.activityViewport == nil {
		return utils.PrintMcpErrorBlock(w, server, lines, q.dims.Width)
	}
	rows := q.activityViewport.DetachRenderedRegion()
	if rows > 0 {
		if err := table.ClearTermTo(w, rows); err != nil {
			return fmt.Errorf("clear window region before elevating mcp error: %w", err)
		}
	}
	if err := utils.PrintMcpErrorBlock(w, server, lines, q.dims.Width); err != nil {
		return fmt.Errorf("print elevated mcp error: %w", err)
	}
	return q.activityViewport.Render(w)
}

// startResizeWatcher starts one dimensions watcher for terminal
// rolling-output sessions. It binds to the session writer's fd, the file clai
// actually writes to, so the observed size matches the output target. The
// watcher starts exactly when usesActivityViewport holds and the writer is an
// *os.File; non-rolling, non-terminal, and non-file sessions get a nil
// channel and a no-op stop and keep today's one-shot q.dims read. The stop
// function is idempotent and must be called once per started watcher; it
// releases the process-wide SIGWINCH registration.
func (q *Querier[C]) startResizeWatcher(ctx context.Context) (<-chan dimensions.Dimensions, func()) {
	if !q.usesActivityViewport() {
		return nil, func() {}
	}
	f, ok := q.out.(*os.File)
	if !ok {
		return nil, func() {}
	}
	viewer := dimensions.New(ctx, f.Fd())
	return viewer.Events(), viewer.Stop
}

// rawDisplay makes output to pipes, files, and other non-terminal writers
// identical to explicit raw mode.
func (q *Querier[C]) rawDisplay() bool {
	return q.Raw || (q.outputModeKnown && !q.outputIsTerminal)
}

// closeReasoningIfOpen finishes the display viewport and persists the complete
// source reasoning separately from assistant text.
func (q *Querier[C]) closeReasoningIfOpen(ctx context.Context, session *QuerySession) {
	if !q.reasoningActive {
		return
	}
	q.reasoningActive = false
	// One reasoning record per block, not per token: the complete buffer is
	// captured before any branch resets it. An empty buffer (a spurious close)
	// emits no record.
	if reasoning := q.reasoningBuf.String(); reasoning != "" {
		q.logMessage(ctx, "reasoning", reasoning, "")
	}
	if q.usesActivityViewport() {
		session.PendingReasoning.WriteString(q.reasoningBuf.String())
		q.fullMsg = session.PendingTextString()
		q.reasoningBuf.Reset()
		q.activityViewport.FinishReasoning()
		return
	}
	if !q.debug && !q.structuredOutput {
		w := q.out
		if w == nil {
			w = os.Stdout
		}
		if q.rawDisplay() {
			fmt.Fprint(w, "\n[/thinking]\n")
		} else {
			fmt.Fprint(w, table.Colorize(utils.RoleColor("reasoning"), "\n[/thinking]\n"))
		}
	}
	if q.structuredOutput {
		session.PendingReasoning.WriteString(q.reasoningBuf.String())
		q.fullMsg = session.PendingTextString()
		q.reasoningBuf.Reset()
		return
	}
	if !utils.RollingOutputEnabled() && !q.rawDisplay() && !q.debug {
		session.PendingReasoning.WriteString(q.reasoningBuf.String())
		q.fullMsg = session.PendingTextString()
		q.reasoningBuf.Reset()
		return
	}
	reasoningWrapped := "[thinking]" + q.reasoningBuf.String() + "\n[/thinking]\n"
	if session.PendingTextString() == "" {
		session.PendingText.WriteString(reasoningWrapped)
		session.PendingReasoning.WriteString(q.reasoningBuf.String())
	} else {
		existing := session.PendingText.String()
		session.PendingText.Reset()
		session.PendingText.WriteString(reasoningWrapped)
		session.PendingText.WriteString(existing)
		session.PendingReasoning.WriteString(q.reasoningBuf.String())
	}
	q.fullMsg = session.PendingTextString()
	q.reasoningBuf.Reset()
}

func (q *Querier[C]) currentTokenUsage() *pub_models.Usage {
	tokenCounter, isModelCounter := any(q.Model).(models.UsageTokenCounter)
	if !isModelCounter {
		if q.debug {
			ancli.Okf("is not usage token counter")
		}
		return nil
	}
	if q.debug && tokenCounter.TokenUsage() != nil {
		ancli.Okf("token usage: %v", *tokenCounter.TokenUsage())
	}
	return tokenCounter.TokenUsage()
}

// prepareFinalAnswerPop removes the trailing assistant-prose block from the
// rolling window state at stream end without redrawing. The window still shows
// the answer until postProcessOutput redraws it immediately before the answer
// prints below, so the terminal paints the transition in one frame.
func (q *Querier[C]) prepareFinalAnswerPop() {
	if q.activityViewport == nil || !q.usesActivityViewport() {
		return
	}
	if q.activityViewport.RemoveTextBlock() > 0 {
		q.finalAnswerPopPending = true
	}
}

// Query using the underlying model to stream completions and then print the output
// from the model to stdout. Blocking operation.
func (q *Querier[C]) Query(ctx context.Context) error {
	// Catch-all in the csae that stdout isn't set
	if q.out == nil {
		q.out = os.Stdout
	}
	// One dimensions watcher for terminal rolling-output sessions. It observes
	// the same file the session writes to; the runner consumes fresh snapshots
	// in the serialized streaming loop. Non-rolling and non-terminal sessions
	// keep the one-shot q.dims read and never start a signal registration.
	resizeEvents, stopResizeWatcher := q.startResizeWatcher(ctx)
	defer stopResizeWatcher()
	session := &QuerySession{
		Chat:            q.chat,
		ShouldSaveReply: q.shouldSaveReply,
		Raw:             q.rawDisplay(),
		Line:            q.line,
		LineCount:       q.lineCount,
	}
	runner := sessionRunner[C]{
		querier:      q,
		recorder:     q.callUsageRecorder,
		toolExecutor: toolExecutor[C]{querier: q},
		finalizer:    sessionFinalizer[C]{querier: q},
		stoploss:     q.newStoploss(),
		resizeEvents: resizeEvents,
	}
	err := runner.Run(ctx, session)
	q.chat = session.Chat
	q.fullMsg = session.FinalAssistantText
	q.line = session.Line
	q.lineCount = session.LineCount
	q.hasPrinted = session.Finalized
	q.amToolCalls = session.ToolCallsUsed
	q.isLikelyGemini3Preview = session.LikelyGeminiPreview
	// The final answer pop is consumed by the finalizer's postProcessOutput
	// inside Run; it must not leak into the next query.
	q.finalAnswerPopPending = false
	return err
}

func (q *Querier[C]) TextQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	q.resetTransientState()
	// Inject the configured system prompt when the incoming chat
	// does not already carry one.  This ensures system prompts
	// configured via Configurations.SystemPrompt (e.g. through
	// agent.WithPrompt) reach the model even when the caller
	// bypasses SetupInitialChat (the CLI path).
	if q.systemPrompt != "" {
		hasSystem := false
		for _, m := range chat.Messages {
			if m.Role == "system" {
				hasSystem = true
				break
			}
		}
		if !hasSystem {
			chat.Messages = append(
				[]pub_models.Message{{Role: "system", Content: q.systemPrompt}},
				chat.Messages...,
			)
		}
	}
	q.chat = chat
	// Query will update the chat with the latest system message
	err := q.Query(ctx)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("TextQuery: %w", err)
	}
	if q.debug && !q.debugTextQuerierPrinted {
		q.debugTextQuerierPrinted = true
		ancli.PrintOK(fmt.Sprintf("Querier.TextQuery:\n%v", debug.IndentedJsonFmt(q)))
	}

	return q.chat, nil
}

func (q *Querier[C]) SetChatID(chatID string) {
	q.chat.ID = chatID
}
