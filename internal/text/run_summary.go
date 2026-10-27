package text

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/quantity"
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
	title := fmt.Sprintf("▸ query %v in %v", s.Outcome, s.Duration.Truncate(time.Second))
	return append([]string{title}, s.detailLines()...)
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

	return append(lines, "  tools   "+toolTotals(t.ToolCalls))
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

// transcriptOut is where the raw transcript (everything but the reply)
// prints.
func (q *Querier[C]) transcriptOut() io.Writer {
	if q.transcriptToStderr {
		if q.errOut != nil {
			return q.errOut
		}
		return os.Stderr
	}
	if q.out != nil {
		return q.out
	}
	return os.Stdout
}

// EnableQuerySummary makes each plain Query print a QuerySummary when it
// ends. Loop iterations never print one; the loop reports its own.
func (q *Querier[C]) EnableQuerySummary() {
	q.querySummary = true
}

func (q *Querier[C]) reportQuerySummary(start time.Time, chat, result pub_models.Chat, runErr error) error {
	summary := QuerySummary{Duration: time.Since(start)}
	if runErr != nil {
		summary.Outcome = QueryStopped
	}
	summary.addTurn(chat, result)
	if err := q.reportSummary(summary.Lines()); err != nil {
		return fmt.Errorf("print query summary: %w", err)
	}
	return nil
}

// reportSummary prints summary rows. A terminal session prints them below
// the output; raw, structured and redirected output send them to stderr so
// stdout stays machine-readable.
func (q *Querier[C]) reportSummary(lines []string) error {
	if q.usesActivityViewport() {
		w := q.out
		if w == nil {
			w = os.Stdout
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
		w = os.Stderr
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}
