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

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// LoopToolCall is one tool call a loop iteration made.
type LoopToolCall struct {
	Name   string
	Failed bool
}

// LoopEnd classifies how a whole loop run ended.
type LoopEnd uint8

const (
	LoopEndAchieved LoopEnd = iota
	LoopEndBlocked
	LoopEndExhausted
	// LoopEndStopped means the run ended on an error or a cancellation.
	LoopEndStopped
)

func (e LoopEnd) String() string {
	switch e {
	case LoopEndAchieved:
		return "achieved"
	case LoopEndBlocked:
		return "blocked"
	case LoopEndExhausted:
		return "exhausted"
	case LoopEndStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// LoopSummary is the end-of-run report of a loop: how it ended, what it cost
// and which tools it used, summed over every iteration.
type LoopSummary struct {
	End           LoopEnd
	Iterations    int
	MaxIterations int // 0 means unbounded
	Duration      time.Duration
	Usage         pub_models.Usage
	CostUSD       float64
	// HasCost is false when no iteration produced a cost estimate, e.g. when
	// no price is known for the model.
	HasCost   bool
	ToolCalls []LoopToolCall
}

const loopToolsSeparator = " · "

// loopToolCalls lists the tool calls in messages in the order their results
// arrived. A result whose call is not in messages is named "unknown".
func loopToolCalls(messages []pub_models.Message) []LoopToolCall {
	names := map[string]string{}
	var calls []LoopToolCall
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
		calls = append(calls, newLoopToolCall(name, m))
	}
	return calls
}

func newLoopToolCall(name string, result pub_models.Message) LoopToolCall {
	return LoopToolCall{Name: name, Failed: strings.HasPrefix(toolResultText(result), "ERROR: ")}
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

// loopToolsLine renders calls in order, folding consecutive repeats:
// "go ✗ · cat ×2 · apply_patch · go".
func loopToolsLine(calls []LoopToolCall) string {
	var parts []string
	for i := 0; i < len(calls); {
		j := i + 1
		for j < len(calls) && calls[j] == calls[i] {
			j++
		}
		part := calls[i].Name
		if calls[i].Failed {
			part += " ✗"
		}
		if n := j - i; n > 1 {
			part += " ×" + strconv.Itoa(n)
		}
		parts = append(parts, part)
		i = j
	}
	return clipRunes(strings.Join(parts, loopToolsSeparator), models.LoopPhaseSummaryRunes)
}

// Lines renders the summary as one title row and indented detail rows.
func (s LoopSummary) Lines() []string {
	bound := "∞"
	if s.MaxIterations > 0 {
		bound = strconv.Itoa(s.MaxIterations)
	}
	lines := []string{fmt.Sprintf("▸ loop %v after %d/%s iterations in %v", s.End, s.Iterations, bound, s.Duration.Truncate(time.Second))}

	tokens := groupDigits(s.Usage.PromptTokens) + " in"
	if cached := s.Usage.PromptTokensDetails.CachedTokens; cached > 0 {
		tokens += " (" + groupDigits(cached) + " cached)"
	}
	tokens += loopToolsSeparator + groupDigits(s.Usage.CompletionTokens) + " out"
	lines = append(lines, "  tokens  "+tokens)

	if s.HasCost {
		lines = append(lines, fmt.Sprintf("  cost    ~$%.2f", s.CostUSD))
	} else {
		lines = append(lines, "  cost    unknown (no price for this model)")
	}

	lines = append(lines, "  tools   "+loopToolTotals(s.ToolCalls))
	return lines
}

// loopToolTotals counts calls per tool, most used first.
func loopToolTotals(calls []LoopToolCall) string {
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
	return head + ": " + strings.Join(parts, loopToolsSeparator)
}

func groupDigits(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// ReportLoopSummary prints the end-of-run summary. A terminal session prints
// it below the loop's output; raw, structured and redirected output send it
// to stderr so stdout stays machine-readable.
func (q *Querier[C]) ReportLoopSummary(summary LoopSummary) error {
	lines := summary.Lines()
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
		if _, err := io.WriteString(w, out.String()); err != nil {
			return fmt.Errorf("print loop summary: %w", err)
		}
		return nil
	}
	w := q.errOut
	if w == nil {
		w = os.Stderr
	}
	if _, err := io.WriteString(w, strings.Join(lines, "\n")+"\n"); err != nil {
		return fmt.Errorf("print loop summary: %w", err)
	}
	return nil
}
