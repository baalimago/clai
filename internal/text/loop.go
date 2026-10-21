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

	handover := ""
	for iteration := 1; ; iteration++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.recorder.Reset()
		chat, err := r.iterationChat(iteration, handover)
		if err != nil {
			return err
		}
		result, err := r.textQuery(ctx, chat)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("loop iteration %d: %w", iteration, err)
		}

		if submission, ok := r.recorder.Submission(); ok {
			switch submission.Status {
			case LoopStatusAchieved:
				return nil
			case LoopStatusBlocked:
				return &LoopBlockedError{Iteration: iteration, Summary: submission.Summary}
			default:
				return fmt.Errorf("loop iteration %d: unsupported recorded status %q", iteration, submission.Status)
			}
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		handover = lastAssistantHandover(result)
		if r.cfg.MaxIterations > 0 && iteration >= r.cfg.MaxIterations {
			return &MaxIterationsError{MaxIterations: r.cfg.MaxIterations, LastHandover: handover}
		}
	}
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
	for i := len(chat.Messages) - 1; i >= 0; i-- {
		if chat.Messages[i].Role != "assistant" {
			continue
		}
		if content := strings.TrimSpace(chat.Messages[i].Content); content != "" {
			return content
		}
		return loopHandoverPlaceholder
	}
	return loopHandoverPlaceholder
}
