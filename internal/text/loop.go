package text

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/chatid"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	loopHandoverPlaceholder = "(Previous iteration ended without a summary.)"

	loopInstructions = `You are running inside clai loop, an autonomous multi-iteration session. Work steadily toward the user's goal.
Call the complete_task tool when the goal is achieved or when you are blocked:
- status "achieved" when the goal is complete. A configured verification command must exit 0 for this call to be accepted; if it rejects the call, continue working and call complete_task again once the command passes.
- status "blocked" when you cannot continue without user input.
If you must stop before completing the goal, end your reply with a concise summary of what you did and what remains; the next iteration receives it as handover.`
)

// ErrLoopBlocked and ErrMaxIterations are the terminal sentinels returned by
// LoopRunner.Query when a loop stops without achieving its goal.
// (worklog 2026-10-21-clai-loop, README F3)
var (
	ErrLoopBlocked   = errors.New("loop task blocked")
	ErrMaxIterations = errors.New("loop max iterations reached")
)

// LoopBlockedError reports that the model called complete_task with blocked status.
// (worklog 2026-10-21-clai-loop, README F3)
type LoopBlockedError struct {
	Iteration int
	Summary   string
}

func (e *LoopBlockedError) Error() string {
	return fmt.Sprintf("loop task blocked at iteration %d: %s", e.Iteration, e.Summary)
}

func (e *LoopBlockedError) Unwrap() error {
	return ErrLoopBlocked
}

// MaxIterationsError reports that the loop exhausted MaxIterations without
// achieving the goal. (worklog 2026-10-21-clai-loop, README F3)
type MaxIterationsError struct {
	MaxIterations int
	LastHandover  string
}

func (e *MaxIterationsError) Error() string {
	return fmt.Sprintf("loop reached the -max-iterations limit (%d) without completing the goal", e.MaxIterations)
}

func (e *MaxIterationsError) Unwrap() error {
	return ErrMaxIterations
}

// LoopConfig holds the seed chat and iteration bounds for one LoopRunner.
// (worklog 2026-10-21-clai-loop, README F3, F5, F7)
type LoopConfig struct {
	SeedChat      pub_models.Chat
	GoalPrompt    string
	MaxIterations int
}

// LoopQuerier is the querier a LoopRunner drives: a ChatQuerier that also
// accepts one additional tool beside the user's selected catalog.
// *Querier[C] satisfies it. (worklog 2026-10-21-clai-loop, README F6)
type LoopQuerier interface {
	models.ChatQuerier

	// RegisterExtraTool makes tool callable on every following TextQuery,
	// beside every tool already selected, whether -t is unset, a glob, or an
	// explicit list; it never removes or narrows an existing tool.
	RegisterExtraTool(tool pub_models.LLMTool)

	// SetLoopProgress records the iteration that the following TextQuery runs,
	// so the rolling window pins its header from the first activity.
	SetLoopProgress(progress LoopProgress)

	// FinishLoopIteration applies the rolling-window rule after one iteration.
	// The window collapsed to its pinned header at stream end. An achieved
	// outcome prints its answer below that header; a continuing iteration leaves
	// it as its progress record; blocked and exhausted runs leave it and report
	// through the error. Without a drawn window the iteration already printed
	// its answer, so only the progress state is cleared. Calling it without
	// progress from SetLoopProgress is an error.
	FinishLoopIteration(outcome LoopOutcome, answer pub_models.Message) error

	// ReportLoopSummary prints the end-of-run summary once the loop stops,
	// whatever the reason.
	ReportLoopSummary(summary LoopSummary) error
}

// RegisterExtraTool adds tool to the querier's dispatch and selectable
// catalogs and to the model's tool box, so a following TextQuery offers it
// beside every already-selected tool. It is called between queries and never
// during one. (worklog 2026-10-21-clai-loop, README F6)
func (q *Querier[C]) RegisterExtraTool(tool pub_models.LLMTool) {
	name := tool.Specification().Name
	if q.tooling.run == nil {
		q.tooling.run = map[string]pub_models.LLMTool{}
	}
	q.tooling.run[name] = tool
	if q.tooling.base == nil {
		q.tooling.base = map[string]pub_models.LLMTool{}
	}
	q.tooling.base[name] = tool

	toolBox, ok := any(q.Model).(models.ToolBox)
	if !ok {
		return
	}
	if q.tooling.registered == nil {
		q.tooling.registered = map[string]struct{}{}
	}
	if _, registered := q.tooling.registered[name]; registered {
		return
	}
	toolBox.RegisterTool(tool)
	q.tooling.registered[name] = struct{}{}
}

// LoopRunner executes outer-loop iterations over a LoopQuerier and a
// CompletionRecorder. (worklog 2026-10-21-clai-loop, README F3, F4, F5, F7)
type LoopRunner interface {
	models.Querier

	// Query runs iterations starting at 1 until the goal is achieved, blocked,
	// cancelled, or MaxIterations is exhausted.
	//
	// A negative MaxIterations returns an error naming -max-iterations before
	// any TextQuery. Each iteration resets the recorder and runs on a child
	// context whose cancel is stored under utils.ContextCancelKey, so a
	// StopEvent cannot cancel the caller. Iteration 2 and later use a fresh
	// chat ID that preserves the seed messages through the first user message,
	// then the prior handover and a continuation prompt.
	Query(ctx context.Context) error
}

type loopRunner struct {
	querier  LoopQuerier
	recorder CompletionRecorder
	cfg      LoopConfig
	// started is the highest loop iteration that began a query, so Produced
	// can answer without asking the querier about an earlier iteration.
	started int
}

// NewLoopRunner constructs a LoopRunner over cq and recorder with cfg. It
// registers recorder on cq exactly once, before any iteration, so every
// iteration can call the completion tool F1 names.
// (worklog 2026-10-21-clai-loop, README F1, F6)
func NewLoopRunner(cq LoopQuerier, recorder CompletionRecorder, cfg LoopConfig) LoopRunner {
	cq.RegisterExtraTool(recorder)
	return &loopRunner{querier: cq, recorder: recorder, cfg: cfg}
}

func (r *loopRunner) Query(ctx context.Context) error {
	if r.cfg.MaxIterations < 0 {
		return fmt.Errorf("loop: -max-iterations must be zero (unlimited) or positive, got %d", r.cfg.MaxIterations)
	}
	start := time.Now()
	summary := LoopSummary{MaxIterations: r.cfg.MaxIterations}
	err := r.run(ctx, &summary)
	if summary.Iterations == 0 || (err != nil && !r.Produced() && claierr.ModelUnavailable(err)) {
		return err
	}
	summary.End = loopEndOf(err)
	summary.Duration = time.Since(start)
	if reportErr := r.querier.ReportLoopSummary(summary); reportErr != nil {
		return errors.Join(err, fmt.Errorf("report loop summary: %w", reportErr))
	}
	return err
}

// Produced reports whether the loop already emitted output. Only iteration 1
// can be replayed: any later failure means the autonomous run began, and
// replaying it would repeat work and side effects
// (architecture/model-aliases.md).
func (r *loopRunner) Produced() bool {
	if r.started > 1 {
		return true
	}
	reporter, ok := any(r.querier).(interface{ Produced() bool })
	if !ok {
		return true
	}
	return reporter.Produced()
}

func loopEndOf(err error) LoopEnd {
	var blocked *LoopBlockedError
	var exhausted *MaxIterationsError
	switch {
	case err == nil:
		return LoopEndAchieved
	case errors.As(err, &blocked):
		return LoopEndBlocked
	case errors.As(err, &exhausted):
		return LoopEndExhausted
	default:
		return LoopEndStopped
	}
}

func (r *loopRunner) run(ctx context.Context, summary *LoopSummary) error {
	// Iteration 1 has no handover; the summarizer derives its phase from the
	// initial prompt. Every later iteration inherits what the previous handover
	// reported, and the loop-phase summarizer may refine it while the iteration
	// streams.
	phase := ""
	var handover string
	for iteration := 1; ; iteration++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.recorder.Reset()
		r.querier.SetLoopProgress(LoopProgress{
			Iteration:     iteration,
			MaxIterations: r.cfg.MaxIterations,
			Phase:         phase,
		})
		chat, err := r.iterationChat(iteration, handover)
		if err != nil {
			return err
		}
		summary.Iterations = iteration
		r.started = iteration
		result, err := r.textQuery(ctx, chat)
		if err != nil {
			var partial *PartialRunError
			if errors.As(err, &partial) {
				summary.addTurn(chat, partial.Chat)
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("loop iteration %d: %w", iteration, err)
		}
		summary.addTurn(chat, result)
		answer := pub_models.Message{Role: "assistant", Content: lastAssistantText(result)}

		if submission, ok := r.recorder.Submission(); ok {
			switch submission.Status {
			case LoopStatusAchieved:
				if answer.Content == "" {
					answer.Content = submission.Summary
				}
				return r.finishDisplay(iteration, LoopOutcomeAchieved, answer)
			case LoopStatusBlocked:
				domain := &LoopBlockedError{Iteration: iteration, Summary: submission.Summary}
				return r.stopWithDisplay(iteration, domain, LoopOutcomeBlocked, answer)
			default:
				return fmt.Errorf("loop iteration %d: unsupported recorded status %q", iteration, submission.Status)
			}
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		handover = lastAssistantHandover(result)
		phase = loopHandoverProgress(handover)
		if r.cfg.MaxIterations > 0 && iteration >= r.cfg.MaxIterations {
			domain := &MaxIterationsError{MaxIterations: r.cfg.MaxIterations, LastHandover: handover}
			return r.stopWithDisplay(iteration, domain, LoopOutcomeExhausted, answer)
		}
		if err := r.finishDisplay(iteration, LoopOutcomeContinue, answer); err != nil {
			return err
		}
	}
}

// finishDisplay runs the iteration's terminal display step, naming the
// iteration when it fails.
func (r *loopRunner) finishDisplay(iteration int, outcome LoopOutcome, answer pub_models.Message) error {
	if err := r.querier.FinishLoopIteration(outcome, answer); err != nil {
		return fmt.Errorf("display loop iteration %d: %w", iteration, err)
	}
	return nil
}

// stopWithDisplay returns domain unchanged when the display succeeded, so the
// terminal result stays type-assertable; a failed display is joined beside it.
func (r *loopRunner) stopWithDisplay(iteration int, domain error, outcome LoopOutcome, answer pub_models.Message) error {
	displayErr := r.finishDisplay(iteration, outcome, answer)
	if displayErr == nil {
		return domain
	}
	return errors.Join(domain, displayErr)
}

// textQuery isolates one iteration's cancellation: a StopEvent cancels only
// this child context, never the caller's. (worklog 2026-10-21-clai-loop, README F4)
func (r *loopRunner) textQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	iterationCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	iterationCtx = context.WithValue(iterationCtx, utils.ContextCancelKey, cancel)
	return r.querier.TextQuery(iterationCtx, chat)
}

func (r *loopRunner) iterationChat(iteration int, handover string) (pub_models.Chat, error) {
	if iteration == 1 {
		chat := cloneChat(r.cfg.SeedChat)
		chat.Messages = withLoopInstructions(chat.Messages)
		return chat, nil
	}

	id, err := chatid.New()
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("generate loop iteration chat id: %w", err)
	}
	seedMessages, ok := messagesThroughFirstUser(r.cfg.SeedChat.Messages)
	if !ok {
		return pub_models.Chat{}, errors.New("loop seed chat has no user message to continue from")
	}
	chat := pub_models.Chat{
		ID:       id,
		Created:  time.Now(),
		Profile:  r.cfg.SeedChat.Profile,
		Messages: withLoopInstructions(seedMessages),
	}
	chat.Messages = append(chat.Messages,
		pub_models.Message{Role: "assistant", Content: handover},
		pub_models.Message{Role: "user", Content: continuationPrompt(r.cfg.GoalPrompt)},
	)
	return chat, nil
}

func cloneChat(chat pub_models.Chat) pub_models.Chat {
	clone := chat
	clone.Messages = append([]pub_models.Message(nil), chat.Messages...)
	return clone
}

func withLoopInstructions(messages []pub_models.Message) []pub_models.Message {
	out := append([]pub_models.Message(nil), messages...)
	for i := range out {
		if out[i].Role == "system" {
			out[i].Content = appendLoopInstructions(out[i].Content)
			return out
		}
	}
	return append([]pub_models.Message{{Role: "system", Content: loopInstructions}}, out...)
}

func appendLoopInstructions(systemPrompt string) string {
	systemPrompt = strings.TrimRight(systemPrompt, " \t\r\n")
	switch {
	case systemPrompt == "":
		return loopInstructions
	case strings.HasSuffix(systemPrompt, loopInstructions):
		// A seed loaded from a previous loop run (for example via -re) already
		// carries the instructions; appending them again would duplicate them.
		return systemPrompt
	default:
		return systemPrompt + "\n\n" + loopInstructions
	}
}

func messagesThroughFirstUser(messages []pub_models.Message) ([]pub_models.Message, bool) {
	for i, message := range messages {
		if message.Role == "user" {
			return append([]pub_models.Message(nil), messages[:i+1]...), true
		}
	}
	return nil, false
}

func continuationPrompt(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return "Continue working toward the goal. The previous iteration's closing message is the handover above."
	}
	return fmt.Sprintf("Continue working toward the goal:\n\n%s\n\nThe previous iteration's closing message is the handover above.", goal)
}

func lastAssistantHandover(chat pub_models.Chat) string {
	if content := lastAssistantText(chat); content != "" {
		return content
	}
	return loopHandoverPlaceholder
}

// lastAssistantText returns the trimmed content of the last assistant message,
// or "" when the chat has no non-empty assistant reply.
func lastAssistantText(chat pub_models.Chat) string {
	for i := len(chat.Messages) - 1; i >= 0; i-- {
		if chat.Messages[i].Role != "assistant" {
			continue
		}
		return strings.TrimSpace(chat.Messages[i].Content)
	}
	return ""
}
