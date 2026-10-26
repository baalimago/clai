package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

const (
	// maxReasoningBuf caps the reasoning text accumulated per stream. A
	// looping model can stream reasoning tokens forever; without the cap the
	// accumulator grows unboundedly until the process OOMs (kinoview production
	// incident, 2026-08-11: 2.53 GB heap). Keeping the tail preserves the reasoning
	// context that matters for tool calls and the final answer.
	maxReasoningBuf = 1 << 20 // 1 MiB
)

type Querier[C models.StreamCompleter] struct {
	Raw              bool
	errOut           io.Writer
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
	lookbackCWD string
	// cmdBan is this run's command ban policy, attached to every tool-call
	// context by the executor (D29). No package-level ban state exists.
	cmdBan     []string
	hasPrinted bool
	Model      C
	tooling    tooling

	// systemPrompt is the configured system prompt, injected into every
	// TextQuery call that does not already carry a system message.
	systemPrompt string

	// reasoningBuf preserves source reasoning for the session and model. The
	// activity viewport is a separate, bounded terminal-only copy.
	reasoningBuf     tailBuffer
	reasoningActive  bool
	activityViewport *utils.ActivityViewport
	// loopProgress is the display state of the loop iteration the current
	// TextQuery runs; nil outside a loop and after the iteration finishes.
	loopProgress *LoopProgress
	// loopFrameSettled marks that the iteration's pinned header was collapsed
	// and left in the scrollback. The next iteration starts a fresh frame below
	// it instead of rewriting it in place.
	loopFrameSettled bool
	// mcpSink buffers MCP server stderr lines for the rolling window. It is
	// created in NewQuerier before the MCP clients spawn and drained on the
	// serialized session loop.
	mcpSink *mcpLogSink
	// finalAnswerPopPending marks that the trailing assistant-prose block was
	// removed from the viewport state at stream end. The finalizer clears the
	// whole drawn window and prints the answer in its place only when it has an
	// answer, so the terminal paints the clear and the reprint as one
	// transition.
	finalAnswerPopPending bool

	// Output of the querier. This is used mostly when Querier is invoked as an agent
	out io.Writer

	// imageTurn is the turn's image wiring; nil means the default for configDir.
	imageTurn            *ImageTurn
	defaultImageProgress ImageProgress
	// imageProgressSince is when the step's first ImageProgressEvent arrived.
	imageProgressSince time.Time
	// imageDetails holds what generate_image recorded per image handle until shown.
	imageDetails map[string]present.Details

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

	// stoploss is the token stoploss policy carried from the user config; the
	// stoploss controller consumes it in the session runner (Phase 3).
	stoploss *Stoploss

	// costEnricher waits for the model price catalog and stamps cost
	// estimates onto the chat at finalization.
	costEnricher      costEnricher
	callUsageRecorder CallUsageRecorder

	// runModel is the configured model name, the last rung of the summary
	// model ladder on the query path (D22).
	runModel string
	// vendor and modelVersion name the route vendorType resolved.
	vendor       string
	modelVersion string
	// summarizer is attached by the text query command (plain or loop mode); nil
	// means no in-flight label (pkg/agent, tests, the summarizer's own
	// querier).
	summarizer             models.Summarizer
	querySummary           bool
	summarizeConversations bool
	summaryModel           string
	summaryJoinTimeout     time.Duration
	// summaryInterrupt lets the join give up early; nil wires SIGINT/SIGTERM
	// at launch time.
	summaryInterrupt <-chan struct{}
	summaryRun       *summaryRun
	// summaryReady holds the result the session loop took while the run was
	// still streaming; the join consumes it instead of waiting.
	summaryReady *summaryResult
	// summaryDeferred holds a plain query's label launch until its first
	// tool step: a one-step answer never waits, so its label never lands.
	summaryDeferred bool
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
		// The loop window collapsed to its pinned header at stream end. The loop
		// runner prints an achieved answer below that header in
		// FinishLoopIteration, once it knows the outcome. Nothing prints from
		// here (worklog 2026-10-21-clai-loop).
		if q.loopProgress != nil {
			return
		}
		// Rolling window: the final answer replaces the whole window. The answer
		// block was dropped from the viewport state at stream end, so clear every
		// drawn row and print the answer where the window started. When no pop is
		// pending, the assistant text already lives inside the window
		// (intermediate prose before tool calls) and must never be re-printed.
		if q.finalAnswerPopPending {
			if err := q.printAnswerInActivityWindow(q.out, newSysMsg, q.Raw); err != nil {
				ancli.Warnf("failed to print final answer in activity viewport: %v", err)
			}
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
		if !q.debug {
			table.ClearTermTo(q.out, q.lineCount-1)
		}
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
	// Legacy display-only finalize path (tests): the runner's Run joins the
	// persist error into its returned error instead (S8).
	_ = sessionFinalizer[C]{querier: q}.Finalize(context.Background(), session)
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
	q.loopFrameSettled = false
	q.resetActivityViewport()
	q.finalAnswerPopPending = false
}

// resetActivityViewport starts every query without activity. A loop iteration
// keeps the viewport but not the previous frame: the previous iteration's
// header is already in the scrollback, and the next frame is drawn below it.
func (q *Querier[C]) resetActivityViewport() {
	if q.loopProgress == nil {
		q.activityViewport = nil
		return
	}
	if q.activityViewport == nil {
		return
	}
	q.activityViewport.CollapseToHeader()
	q.activityViewport.SetHeader(q.loopProgress.Header())
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

// appendReasoning accumulates one reasoning token into reasoningBuf. The
// bounded circular buffer keeps the newest maxReasoningBuf bytes without
// repeatedly copying the full retained tail.
func (q *Querier[C]) appendReasoning(content string) {
	q.reasoningBuf.WriteString(content)
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
		if q.loopProgress != nil {
			q.activityViewport.SetHeader(q.loopProgress.Header())
		}
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
	q.stopImageProgress()
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
	if err := q.clearActivityWindow(w); err != nil {
		return fmt.Errorf("clear window region before elevating mcp error: %w", err)
	}
	if err := utils.PrintMcpErrorBlock(w, server, lines, q.dims.Width); err != nil {
		return fmt.Errorf("print elevated mcp error: %w", err)
	}
	return q.activityViewport.RenderPinned(w)
}

// printAnswerInActivityWindow clears the whole drawn activity window and prints
// answer where the window started. A session without a drawn window prints the
// answer at the current cursor.
func (q *Querier[C]) printAnswerInActivityWindow(w io.Writer, answer pub_models.Message, raw bool) error {
	if err := q.clearActivityWindow(w); err != nil {
		return err
	}
	if err := utils.AttemptPrettyPrint(w, answer, q.username, raw); err != nil {
		return fmt.Errorf("print answer in activity window: %w", err)
	}
	return nil
}

// clearActivityWindow erases the rolling window's drawn region and leaves the
// cursor where the window started, so the next write starts there.
func (q *Querier[C]) clearActivityWindow(w io.Writer) error {
	if q.activityViewport == nil {
		return nil
	}
	rows := q.activityViewport.DetachRenderedRegion()
	if rows == 0 {
		return nil
	}
	if err := table.ClearTermTo(w, rows); err != nil {
		return fmt.Errorf("clear activity window: %w", err)
	}
	return nil
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

// warnf reports a user-facing warning without desyncing a drawn rolling frame.
// A live frame is erased, the warning printed where the frame started, and the
// frame redrawn below it; without a frame the warning prints the way ancli
// prints it.
func (q *Querier[C]) warnf(format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if q.activityViewport == nil || !q.usesActivityViewport() {
		ancli.Warnf("%s", message)
		return nil
	}
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	if err := q.activityViewport.PrintAbove(w, "warning: "+message); err != nil {
		return fmt.Errorf("print warning above activity window: %w", err)
	}
	return nil
}

// prepareFinalAnswerPop removes the trailing assistant-prose block from the
// rolling window state at stream end without redrawing. In loop mode it settles
// the pinned header while the cursor still belongs to the window: the loop
// runner's terminal display step runs after the finalizer's cost and cache
// output, so a later collapse would move the cursor up over unrelated lines.
func (q *Querier[C]) prepareFinalAnswerPop() error {
	if q.activityViewport == nil || !q.usesActivityViewport() {
		return nil
	}
	if q.activityViewport.RemoveTextBlock() > 0 {
		q.finalAnswerPopPending = true
	}
	if q.loopProgress == nil {
		return nil
	}
	return q.settleLoopHeader()
}

// Query using the underlying model to stream completions and then print the output
// from the model to stdout. Blocking operation.
func (q *Querier[C]) Query(ctx context.Context) error {
	// Catch-all in the csae that stdout isn't set
	if q.out == nil {
		q.out = os.Stdout
	}
	// A loop iteration pins its header from the first frame, so the window
	// exists and shows which iteration runs before any activity arrives. The
	// pending placeholder is decided here, where the iteration's chat is known
	// and launchSummary makes the same decision.
	q.markLoopPhasePending()
	if q.loopProgress != nil && q.usesActivityViewport() {
		q.ensureActivityViewport()
		if err := q.activityViewport.RenderPinned(q.out); err != nil {
			return fmt.Errorf("render loop header: %w", err)
		}
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
	q.startSummary(ctx)
	defer func() { q.summaryDeferred = false }()
	// The finalizer stores the result chat on q while Run is in flight.
	before := q.chat
	start := time.Now()
	err := runner.Run(ctx, session)
	if q.querySummary && q.loopProgress == nil {
		err = errors.Join(err, q.reportQuerySummary(start, before, session.Chat, err))
	}
	q.chat = session.Chat
	q.fullMsg = session.FinalAssistantText
	q.line = session.Line
	q.lineCount = session.LineCount
	q.hasPrinted = session.Finalized
	q.tooling.calls = session.ToolCallsUsed
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
		return pub_models.Chat{}, &PartialRunError{Chat: q.chat, Err: err}
	}
	if q.debug && !q.debugTextQuerierPrinted {
		q.debugTextQuerierPrinted = true
		ancli.PrintOK(fmt.Sprintf("Querier.TextQuery:\n%v", debug.IndentedJsonFmt(q)))
	}

	return q.chat, nil
}

// PartialRunError is a failed or cancelled TextQuery. Chat holds what the
// run finalized before it stopped: completed tool results, usage and cost.
type PartialRunError struct {
	Chat pub_models.Chat
	Err  error
}

func (e *PartialRunError) Error() string {
	return "TextQuery: " + e.Err.Error()
}

func (e *PartialRunError) Unwrap() error {
	return e.Err
}

func (q *Querier[C]) SetChatID(chatID string) {
	q.chat.ID = chatID
}

// SetSummaryJoinTimeout overrides how long the finalizer waits for the label.
func (q *Querier[C]) SetSummaryJoinTimeout(d time.Duration) {
	q.summaryJoinTimeout = d
}

// SetSummarizer attaches the in-flight conversation summarizer.
func (q *Querier[C]) SetSummarizer(s models.Summarizer) {
	q.summarizer = s
}

// setImageTurn replaces the turn's image wiring; nil restores the default for the config dir.
func (q *Querier[C]) setImageTurn(t *ImageTurn) {
	q.imageTurn = t
}
