package text

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/quantity"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/runtally"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// RunToolCall is one tool call a run made.
type RunToolCall struct {
	Name   string
	Failed bool
}

// RunTally is what a run spent and which tools it used, summed over each
// turn it took.
type RunTally struct {
	Usage   pub_models.Usage
	CostUSD float64
	// HasCost is false when no turn produced a cost estimate, e.g. when no
	// price is known for the model.
	HasCost   bool
	ToolCalls []RunToolCall
	// Subagents is what the clai runs this run started spent, reported
	// through internal/runtally.
	Subagents runtally.Totals
}

// totals is the run's own spend in the form a parent collects.
func (t RunTally) totals() runtally.Totals {
	own := runtally.Totals{
		Runs:             1,
		PromptTokens:     t.Usage.PromptTokens,
		CachedTokens:     t.Usage.PromptTokensDetails.CachedTokens,
		CompletionTokens: t.Usage.CompletionTokens,
		CostUSD:          t.CostUSD,
	}
	if !t.HasCost {
		own.UnpricedRuns = 1
	}
	return own
}

// QuerySummary is the end-of-run report of a plain query.
type QuerySummary struct {
	Outcome  QueryOutcome
	Duration time.Duration
	RunTally
}

// QueryOutcome classifies how a plain query ended.
type QueryOutcome uint8

const (
	QueryDone QueryOutcome = iota
	// QueryStopped means the query ended on an error or a cancellation.
	QueryStopped
)

func (o QueryOutcome) String() string {
	switch o {
	case QueryDone:
		return "done"
	case QueryStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// Lines renders the summary as one title row and indented detail rows.
func (s QuerySummary) Lines() []string {
	return append([]string{"▸ " + s.message()}, s.detailLines()...)
}

func (s QuerySummary) message() string {
	return fmt.Sprintf("query %v in %v", s.Outcome, s.Duration.Truncate(time.Second))
}

// facts is the summary as the facts of its JSON diagnostic record.
func (s QuerySummary) facts() []slog.Attr {
	return append([]slog.Attr{slog.String("outcome", s.Outcome.String())}, s.tallyFacts(s.Duration)...)
}

// addTurn folds one finished turn's usage, cost rows and tool calls into the
// tally. Only what the turn added to chat is counted, so a seed chat resumed
// from an earlier run does not count twice.
func (t *RunTally) addTurn(chat, result pub_models.Chat) {
	if result.TokenUsage != nil {
		u := result.TokenUsage
		t.Usage.PromptTokens += u.PromptTokens
		t.Usage.CompletionTokens += u.CompletionTokens
		t.Usage.TotalTokens += u.TotalTokens
		t.Usage.PromptTokensDetails.CachedTokens += u.PromptTokensDetails.CachedTokens
	}
	if len(result.Queries) > len(chat.Queries) {
		t.HasCost = true
		for _, q := range result.Queries[len(chat.Queries):] {
			t.CostUSD += q.CostUSD
		}
	}
	if len(result.Messages) > len(chat.Messages) {
		t.ToolCalls = append(t.ToolCalls, runToolCalls(result.Messages[len(chat.Messages):])...)
	}
}

func (t RunTally) detailLines() []string {
	tokens := quantity.Quantity(t.Usage.PromptTokens).SI() + " in"
	if cached := t.Usage.PromptTokensDetails.CachedTokens; cached > 0 {
		tokens += " (" + quantity.Quantity(cached).SI() + " cached)"
	}
	tokens += summarySeparator + quantity.Quantity(t.Usage.CompletionTokens).SI() + " out"
	lines := []string{"  tokens  " + tokens}

	if t.HasCost {
		lines = append(lines, fmt.Sprintf("  cost    ~$%.2f", t.CostUSD))
	} else {
		lines = append(lines, "  cost    unknown (no price for this model)")
	}

	lines = append(lines, "  tools   "+toolTotals(t.ToolCalls))
	if t.Subagents.Runs > 0 {
		lines = append(lines, "  subagents "+t.Subagents.Line())
	}
	return lines
}

// tallyFacts is the tally as diagnostic facts: what detailLines shows, as
// fields instead of aligned rows.
func (t RunTally) tallyFacts(duration time.Duration) []slog.Attr {
	facts := []slog.Attr{
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.Int("tokens_in", t.Usage.PromptTokens),
		slog.Int("tokens_cached", t.Usage.PromptTokensDetails.CachedTokens),
		slog.Int("tokens_out", t.Usage.CompletionTokens),
	}
	if t.HasCost {
		facts = append(facts, slog.Float64("cost_usd", t.CostUSD))
	}
	failed := 0
	var perTool []slog.Attr
	index := map[string]int{}
	for _, c := range t.ToolCalls {
		if c.Failed {
			failed++
		}
		i, ok := index[c.Name]
		if !ok {
			i = len(perTool)
			index[c.Name] = i
			perTool = append(perTool, slog.Int(c.Name, 0))
		}
		perTool[i].Value = slog.IntValue(int(perTool[i].Value.Int64()) + 1)
	}
	facts = append(facts,
		slog.Int("tool_calls", len(t.ToolCalls)),
		slog.Int("tool_failures", failed),
		slog.Attr{Key: "tools", Value: slog.GroupValue(perTool...)},
	)
	if s := t.Subagents; s.Runs > 0 {
		subagents := []slog.Attr{
			slog.Int("runs", s.Runs),
			slog.Int("tokens_in", s.PromptTokens),
			slog.Int("tokens_cached", s.CachedTokens),
			slog.Int("tokens_out", s.CompletionTokens),
			slog.Int("unpriced_runs", s.UnpricedRuns),
		}
		if s.UnpricedRuns < s.Runs {
			subagents = append(subagents, slog.Float64("cost_usd", s.CostUSD))
		}
		facts = append(facts, slog.Attr{Key: "subagents", Value: slog.GroupValue(subagents...)})
	}
	return facts
}

const summarySeparator = " · "

// runToolCalls lists the tool calls in messages in the order their results
// arrived. A result whose call is not in messages is named "unknown".
func runToolCalls(messages []pub_models.Message) []RunToolCall {
	names := map[string]string{}
	var calls []RunToolCall
	for _, m := range messages {
		for _, c := range m.ToolCalls {
			names[c.ID] = c.Name
		}
		if m.Role != "tool" {
			continue
		}
		name, ok := names[m.ToolCallID]
		if !ok || name == "" {
			name = "unknown"
		}
		calls = append(calls, newRunToolCall(name, m))
	}
	return calls
}

func newRunToolCall(name string, result pub_models.Message) RunToolCall {
	return RunToolCall{Name: name, Failed: strings.HasPrefix(toolResultText(result), "ERROR: ")}
}

func toolResultText(m pub_models.Message) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	for _, p := range m.Parts {
		if text, ok := p.(pub_models.TextPart); ok {
			return text.Text
		}
	}
	return ""
}

// toolTotals counts calls per tool, most used first.
func toolTotals(calls []RunToolCall) string {
	if len(calls) == 0 {
		return "none"
	}
	type total struct {
		name  string
		count int
	}
	index := map[string]int{}
	var totals []total
	failed := 0
	for _, c := range calls {
		if c.Failed {
			failed++
		}
		i, ok := index[c.Name]
		if !ok {
			i = len(totals)
			index[c.Name] = i
			totals = append(totals, total{name: c.Name})
		}
		totals[i].count++
	}
	slices.SortStableFunc(totals, func(a, b total) int { return cmp.Compare(b.count, a.count) })
	parts := make([]string, 0, len(totals))
	for _, t := range totals {
		part := t.name
		if t.count > 1 {
			part += " ×" + strconv.Itoa(t.count)
		}
		parts = append(parts, part)
	}
	head := fmt.Sprintf("%d calls", len(calls))
	if len(calls) == 1 {
		head = "1 call"
	}
	if failed > 0 {
		head += fmt.Sprintf(", %d failed", failed)
	}
	return head + ": " + strings.Join(parts, summarySeparator)
}

// SendTranscriptToStderr keeps stdout for the reply: raw tool calls, tool
// output and reasoning print to stderr instead.
func (q *Querier[C]) SendTranscriptToStderr() {
	q.transcriptToStderr = true
}

// ShowProgressOn keeps a redirected stdout for the reply alone and draws the
// session's progress on progress, a terminal, the way a terminal stdout
// would show it: clai -reply-only q ... > reply.md shows the rolling window
// on stderr. It does nothing unless stdout is redirected, progress is a
// terminal, and the output is neither raw, structured nor debug.
func (q *Querier[C]) ShowProgressOn(progress io.Writer) {
	out := q.out
	if out == nil {
		out = runenv.From(q.diagContext()).Stdout()
	}
	if q.Raw || q.structuredOutput || q.debug || utils.IsTerminalWriter(out) || !utils.IsTerminalWriter(progress) {
		return
	}
	q.replyOut = out
	q.out = progress
	q.outputIsTerminal = true
	q.dims = utils.SessionDimensions(progress)
}

// handOffReply clears what the session drew of the reply on the progress
// terminal, writes the reply to the redirected stdout and leaves one line
// saying where it went.
func (q *Querier[C]) handOffReply(reply string) error {
	if q.activityViewport != nil {
		if err := q.clearActivityWindow(q.out); err != nil {
			return err
		}
	} else if q.dims.Width > 0 {
		utils.UpdateMessageTerminalMetadata(reply, &q.line, &q.lineCount, q.dims.Width)
		if err := table.ClearTermTo(q.out, q.lineCount-1); err != nil {
			return diag.Errorf("clear streamed reply: %w", err)
		}
	}
	reply = strings.TrimRight(stripThinkingBlocks(reply), "\n") + "\n"
	if _, err := io.WriteString(q.replyOut, reply); err != nil {
		return diag.Errorf("write reply: %w", err)
	}
	lines := strings.Count(reply, "\n")
	note := fmt.Sprintf("▸ reply written to stdout (%d lines)", lines)
	if lines == 1 {
		note = "▸ reply written to stdout (1 line)"
	}
	_, err := io.WriteString(q.out, table.Colorize(utils.LoopTitleColor(), note)+"\n")
	return err
}

// transcriptOut is where the raw transcript (everything but the reply)
// prints.
func (q *Querier[C]) transcriptOut() io.Writer {
	if q.transcriptToStderr {
		if q.errOut != nil {
			return q.errOut
		}
		return runenv.From(q.diagContext()).Stderr()
	}
	if q.out != nil {
		return q.out
	}
	return runenv.From(q.diagContext()).Stdout()
}

// holdsStepText is -reply-only on a non-terminal stdout: a step's prose is
// held until the step ends, because only then is it known to be the reply
// rather than narration before a tool call.
func (q *Querier[C]) holdsStepText() bool {
	return q.transcriptToStderr && q.rawDisplay() && !q.debug && !q.structuredOutput
}

func (q *Querier[C]) releaseStepText(text string, endedWithTool bool) error {
	text = stripThinkingBlocks(text)
	if !q.holdsStepText() || text == "" {
		return nil
	}
	if q.json.active() {
		// The final step's text is the reply record; narration before a tool
		// call is transcript, on the destination the text form uses.
		if !endedWithTool {
			return nil
		}
		if err := q.jsonTranscript().ReplyDelta(text); err != nil {
			return diag.Errorf("write step text record: %w", err)
		}
		return nil
	}
	w := q.out
	if w == nil {
		w = runenv.From(q.diagContext()).Stdout()
	}
	if endedWithTool {
		w = q.transcriptOut()
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
	}
	if _, err := io.WriteString(w, text); err != nil {
		return diag.Errorf("print step text: %w", err)
	}
	return nil
}

// EnableQuerySummary makes each plain Query print a QuerySummary when it
// ends. Loop iterations never print one; the loop reports its own.
func (q *Querier[C]) EnableQuerySummary() {
	q.querySummary = true
}

// SetRunTally makes the run count the subagents it starts into its summary
// and report its own totals to the clai that started it.
func (q *Querier[C]) SetRunTally(scope *runtally.Scope) {
	q.runTally = scope
}

// finishQuery settles a plain query's tally and, with -qs, prints its
// summary.
func (q *Querier[C]) finishQuery(start time.Time, chat, result pub_models.Chat, runErr error) error {
	summary := QuerySummary{Duration: time.Since(start)}
	if runErr != nil {
		summary.Outcome = QueryStopped
	}
	summary.addTurn(chat, result)
	settleErr := q.settleRunTally(&summary.RunTally)
	if !q.querySummary {
		return settleErr
	}
	if q.json.active() {
		q.reportSummaryRecord(summary.message(), summary.facts())
		return settleErr
	}
	if err := q.reportSummary(summary.Lines()); err != nil {
		return errors.Join(settleErr, diag.Errorf("print query summary: %w", err))
	}
	return settleErr
}

// reportSummaryRecord is the JSON format's summary: a diagnostic record with
// the summary as facts, delivered by the run's Reporter (F12, F14). A failed
// Report changes nothing for the run (F6).
func (q *Querier[C]) reportSummaryRecord(message string, facts []slog.Attr) {
	ctx := q.diagContext()
	runenv.From(ctx).Report(ctx, runenv.TypeRunSummary, message, facts...)
}

// settleRunTally folds in what the run's subagents reported and hands the
// sum, the run's own spend included, to its parent.
func (q *Querier[C]) settleRunTally(t *RunTally) error {
	if q.runTally == nil {
		return nil
	}
	subagents, collectErr := q.runTally.Collect()
	t.Subagents = subagents
	if _, err := q.runTally.Report(t.totals().Add(subagents)); err != nil {
		return errors.Join(collectErr, diag.Errorf("report to parent run: %w", err))
	}
	return collectErr
}

// reportSummary prints summary rows. A terminal session prints them below
// the output; raw, structured and redirected output send them to stderr so
// stdout stays machine-readable.
func (q *Querier[C]) reportSummary(lines []string) error {
	if q.usesActivityViewport() {
		w := q.out
		if w == nil {
			w = runenv.From(q.diagContext()).Stdout()
		}
		var out strings.Builder
		out.WriteString(table.Colorize(utils.LoopTitleColor(), lines[0]) + "\n")
		for _, l := range lines[1:] {
			out.WriteString(table.Colorize(utils.LoopPhaseColor(), l) + "\n")
		}
		_, err := io.WriteString(w, out.String())
		return err
	}
	w := q.errOut
	if w == nil {
		w = runenv.From(q.diagContext()).Stderr()
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}
