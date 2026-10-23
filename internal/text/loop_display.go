package text

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// LoopProgress is the display state of one outer-loop iteration: which
// iteration runs, how the run is bounded, and the phase summary that crossed
// into it.
type LoopProgress struct {
	Iteration     int
	MaxIterations int // 0 means unbounded
	// Phase is the loop-phase summary: what the previous iteration achieved and
	// what remains toward the goal. Iteration 1 has no handover, so its phase is
	// the plan derived from the initial prompt. It is empty until a summary or a
	// handover fallback supplies it.
	Phase string
	// PhasePending says the loop-phase summarizer for this iteration is still
	// running. The header shows a placeholder instead of Phase until it lands,
	// so the first frame names the work in progress.
	PhasePending bool
}

// loopSummarizingPlaceholder is the phase shown after the iteration marker
// while the loop-phase summarizer for this iteration runs.
const loopSummarizingPlaceholder = "summarizing…"

// loopPhaseSeparator joins the iteration marker to the phase summary on the
// pinned header row.
const loopPhaseSeparator = " - "

// Header returns the pinned header row for the iteration. The iteration marker
// and the phase summary share one row: `▸ loop 2/10 - what remains`. Only the
// marker carries the loop accent; the phase uses the muted activity colour. A
// pending phase shows the summarizing placeholder; a phase that is still
// unknown leaves the marker alone.
func (p LoopProgress) Header() []utils.HeaderLine {
	spans := []utils.HeaderSpan{{Text: p.iterationLabel(), Color: utils.LoopTitleColor()}}
	phase := p.Phase
	if p.PhasePending {
		phase = loopSummarizingPlaceholder
	}
	if phase = loopProgressLine(phase); phase != "" {
		spans = append(spans, utils.HeaderSpan{Text: loopPhaseSeparator + phase, Color: utils.LoopPhaseColor()})
	}
	return []utils.HeaderLine{{Spans: spans}}
}

func (p LoopProgress) iterationLabel() string {
	var label strings.Builder
	label.WriteString("▸ loop ")
	label.WriteString(strconv.Itoa(p.Iteration))
	label.WriteString("/")
	if p.MaxIterations > 0 {
		label.WriteString(strconv.Itoa(p.MaxIterations))
	} else {
		label.WriteString("∞")
	}
	return label.String()
}

// LoopOutcome classifies how one finished iteration left the outer loop. It
// drives the rolling-window rule: a continuing iteration leaves its pinned
// header rows, and a terminal outcome keeps them as the progress record.
type LoopOutcome uint8

const (
	// LoopOutcomeContinue means the iteration recorded no accepted completion
	// and the loop starts another iteration.
	LoopOutcomeContinue LoopOutcome = iota
	// LoopOutcomeAchieved means the iteration recorded an accepted completion.
	LoopOutcomeAchieved
	// LoopOutcomeBlocked means the iteration recorded a blocker.
	LoopOutcomeBlocked
	// LoopOutcomeExhausted means the run reached its iteration bound.
	LoopOutcomeExhausted
)

// valid reports whether the value names a defined LoopOutcome.
func (o LoopOutcome) valid() bool {
	return o <= LoopOutcomeExhausted
}

// loopProgressLine collapses a phase summary to one bounded logical line. The
// viewport wraps it at the terminal width; the bound is the summarizer's own.
func loopProgressLine(phase string) string {
	phase = strings.Join(strings.Fields(phase), " ")
	return clipRunes(phase, models.LoopPhaseSummaryRunes)
}

// loopHandoverProgress derives the fallback phase summary from a handover. A
// handover typically states what changed and what remains, so the first and
// last non-empty lines carry the progression; a single-line handover is used
// as it is. The fallback is shown until the loop-phase summarizer answers.
func loopHandoverProgress(handover string) string {
	var lines []string
	for line := range strings.SplitSeq(handover, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	switch len(lines) {
	case 0:
		return ""
	case 1:
		return clipRunes(lines[0], models.LoopPhaseSummaryRunes)
	default:
		first, last := lines[0], lines[len(lines)-1]
		if first == last {
			return clipRunes(first, models.LoopPhaseSummaryRunes)
		}
		return loopProgressLine(first + " · " + last)
	}
}

// clipRunes bounds text to limit runes, marking a cut with an ellipsis.
func clipRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// showLoopPhase displays phase on the pinned header and clears the pending
// placeholder. A summary that lands before the window exists stays on the
// progress state, so the first frame is born with it.
func (q *Querier[C]) showLoopPhase(phase string) error {
	if q.loopProgress == nil {
		return nil
	}
	q.loopProgress.Phase = phase
	q.loopProgress.PhasePending = false
	if q.activityViewport == nil {
		return nil
	}
	q.activityViewport.SetHeader(q.loopProgress.Header())
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	if err := q.activityViewport.Render(w); err != nil {
		return fmt.Errorf("render loop phase: %w", err)
	}
	return nil
}

// settleLoopHeader collapses the rolling window to its pinned header and hands
// the drawn rows to the scrollback, where they stay as the iteration's progress
// record. It gives an in-flight summarizer the join bound to reach the header,
// then runs at stream end before the finalizer's cost and cache output moves the
// cursor. It is idempotent: the runner's continue step calls it again after that
// output.
func (q *Querier[C]) settleLoopHeader() error {
	if q.loopFrameSettled {
		return nil
	}
	q.loopFrameSettled = true
	if q.activityViewport == nil {
		return nil
	}
	// A summary that lands after the stream would otherwise miss the header:
	// the join below runs after the collapse, when the header already belongs
	// to the scrollback. Wait for it here, under the same bound the finalizer's
	// join uses, so a slow summarizer is not guaranteed to lose to the stream.
	if err := q.awaitLoopPhaseBeforeSettle(); err != nil {
		return err
	}
	if q.loopProgress != nil {
		// The iteration is over: a summary that has not landed by now never
		// reaches this header, so the fallback phase takes its place.
		q.loopProgress.PhasePending = false
		q.activityViewport.SetHeader(q.loopProgress.Header())
	}
	q.activityViewport.CollapseToHeader()
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	if err := q.activityViewport.RenderPinned(w); err != nil {
		return fmt.Errorf("collapse loop window: %w", err)
	}
	q.activityViewport.DetachRenderedRegion()
	return nil
}

// awaitLoopPhaseBeforeSettle takes an in-flight summarizer's result into the
// header before the window freezes. A result the session loop already took is
// left alone. On timeout or interrupt the run is abandoned, so the finalizer's
// join cannot wait a second time.
func (q *Querier[C]) awaitLoopPhaseBeforeSettle() error {
	if q.loopProgress == nil || !q.loopProgress.PhasePending || q.summaryRun == nil || q.summaryReady != nil {
		return nil
	}
	res, wait := q.waitForSummary(q.summaryRun)
	switch wait {
	case summaryWaitResult:
		return q.awaitSummaryResult(res)
	case summaryWaitTimeout:
		traceSummaryf("loop-phase summary abandoned before settle after %v", q.summaryJoinBound())
	case summaryWaitInterrupt:
		traceSummaryf("loop-phase summary abandoned before settle on interrupt")
	}
	q.abandonSummary()
	return nil
}

// SetLoopProgress tells the querier which loop iteration the next TextQuery
// runs, so its rolling window can pin the iteration header. The runner sets it
// before every iteration, when the iteration's chat does not exist yet, so the
// pending placeholder is decided later by markLoopPhasePending.
func (q *Querier[C]) SetLoopProgress(progress LoopProgress) {
	q.loopProgress = &progress
}

// markLoopPhasePending marks the iteration's phase pending exactly when the
// summarizer that fills it will launch, so a placeholder never outlives a run
// that shows no placeholder and never misses one that does. Query calls it once
// the iteration's chat is known, beside launchSummary's own decision.
func (q *Querier[C]) markLoopPhasePending() {
	if q.loopProgress == nil {
		return
	}
	q.loopProgress.PhasePending = q.shouldLaunchSummary()
}

// FinishLoopIteration applies the rolling-window rule for one finished
// iteration. The window normally collapses to the pinned header rows at stream
// end, before the finalizer's cost and cache output can move the cursor. This
// method is the runner's terminal step: an achieved iteration prints its answer
// below the header, which stays as the iteration's progress record; blocked and
// exhausted runs leave the header and report through the error. Without a drawn
// window the iteration already printed its own answer, so only the progress
// state is cleared. A caller that skipped SetLoopProgress gets an error.
func (q *Querier[C]) FinishLoopIteration(outcome LoopOutcome, answer pub_models.Message) error {
	if q.loopProgress == nil {
		return errors.New("finish loop iteration: no loop progress was set")
	}
	// The outcome is validated before the display gate: a caller mistake must
	// surface even when no window was drawn.
	if !outcome.valid() {
		return fmt.Errorf("finish loop iteration: unknown outcome %d", outcome)
	}
	if q.activityViewport == nil {
		q.loopProgress = nil
		return nil
	}
	w := q.out
	if w == nil {
		w = os.Stdout
	}
	switch outcome {
	case LoopOutcomeContinue:
		// The stream-end settle already left this header in the scrollback. The
		// fallback covers a viewport that never saw a stream end.
		if err := q.settleLoopHeader(); err != nil {
			return err
		}
	case LoopOutcomeAchieved:
		q.loopProgress = nil
		if err := utils.AttemptPrettyPrint(w, answer, q.username, false); err != nil {
			return fmt.Errorf("print achieved loop answer: %w", err)
		}
		return nil
	case LoopOutcomeBlocked, LoopOutcomeExhausted:
	}
	q.loopProgress = nil
	return nil
}
