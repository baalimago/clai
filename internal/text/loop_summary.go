package text

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/models"
)

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
	RunTally
}

// loopToolsLine renders calls in order, folding consecutive repeats:
// "go ✗ · cat ×2 · apply_patch · go".
func loopToolsLine(calls []RunToolCall) string {
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
	return clipRunes(strings.Join(parts, summarySeparator), models.LoopPhaseSummaryRunes)
}

// Lines renders the summary as one title row and indented detail rows.
func (s LoopSummary) Lines() []string {
	bound := "∞"
	if s.MaxIterations > 0 {
		bound = strconv.Itoa(s.MaxIterations)
	}
	title := fmt.Sprintf("▸ loop %v after %d/%s iterations in %v", s.End, s.Iterations, bound, s.Duration.Truncate(time.Second))
	return append([]string{title}, s.detailLines()...)
}

// ReportLoopSummary prints the end-of-run summary. A terminal session prints
// it below the loop's output; raw, structured and redirected output send it
// to stderr so stdout stays machine-readable.
func (q *Querier[C]) ReportLoopSummary(summary LoopSummary) error {
	if err := q.reportSummary(summary.Lines()); err != nil {
		return fmt.Errorf("print loop summary: %w", err)
	}
	return nil
}
