package text

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type recordingLoopQuerier struct {
	events    []string
	chats     []pub_models.Chat
	tools     []pub_models.LLMTool
	progress  []LoopProgress
	finished  []loopDisplayCall
	finishErr error
	handle    func(ctx context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error)
}

// loopDisplayCall records one FinishLoopIteration call.
type loopDisplayCall struct {
	outcome LoopOutcome
	answer  pub_models.Message
}

func (f *recordingLoopQuerier) Query(context.Context) error {
	return nil
}

func (f *recordingLoopQuerier) TextQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	f.chats = append(f.chats, chat)
	iteration := len(f.chats)
	f.events = append(f.events, fmt.Sprintf("query:%d", iteration))
	return f.handle(ctx, iteration, chat)
}

func (f *recordingLoopQuerier) RegisterExtraTool(tool pub_models.LLMTool) {
	f.tools = append(f.tools, tool)
	f.events = append(f.events, "register:"+tool.Specification().Name)
}

func (f *recordingLoopQuerier) SetLoopProgress(progress LoopProgress) {
	f.progress = append(f.progress, progress)
	f.events = append(f.events, fmt.Sprintf("progress:%d", progress.Iteration))
}

func (f *recordingLoopQuerier) FinishLoopIteration(outcome LoopOutcome, answer pub_models.Message) error {
	f.finished = append(f.finished, loopDisplayCall{outcome: outcome, answer: answer})
	f.events = append(f.events, fmt.Sprintf("finish:%d", outcome))
	return f.finishErr
}

func loopSeedChat() pub_models.Chat {
	return pub_models.Chat{
		ID:      "seed-chat-id",
		Created: time.Unix(1, 0),
		Profile: "seed-profile",
		Messages: []pub_models.Message{
			{Role: "system", Content: "seed system prompt"},
			{Role: "user", Content: "achieve the goal tool_complete_task"},
		},
	}
}

func appendedSeedSystem() string {
	return appendLoopInstructions(loopSeedChat().Messages[0].Content)
}

func TestLoopRunner_AchievedFirstIteration(t *testing.T) {
	recorder := NewCompletionRecorder("")
	fake := &recordingLoopQuerier{handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
		if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "done")); err != nil {
			return pub_models.Chat{}, err
		}
		chat.Messages = append(chat.Messages, pub_models.Message{Role: "assistant", Content: "completed"})
		return chat, nil
	}}
	seed := loopSeedChat()
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: seed, GoalPrompt: "the goal", MaxIterations: 10})

	if err := runner.Query(t.Context()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if seed.Messages[0].Content != "seed system prompt" {
		t.Fatalf("seed system message = %q after Query, want the seed left unmutated", seed.Messages[0].Content)
	}
	if len(fake.chats) != 1 {
		t.Fatalf("TextQuery calls = %d, want 1", len(fake.chats))
	}
	first := fake.chats[0]
	if first.ID != loopSeedChat().ID {
		t.Fatalf("iteration 1 chat id = %q, want the seed id", first.ID)
	}
	if first.Profile != loopSeedChat().Profile {
		t.Fatalf("iteration 1 chat profile = %q, want the seed profile", first.Profile)
	}
	if got := first.Messages[0].Content; got != appendedSeedSystem() {
		t.Fatalf("iteration 1 system message = %q, want the seed system with loop instructions", got)
	}
	if got := first.Messages[1]; got.Role != "user" || got.Content != loopSeedChat().Messages[1].Content {
		t.Fatalf("iteration 1 user message = %+v, want the seed user message unchanged", got)
	}
}

func TestLoopRunner_PrependsLoopInstructions(t *testing.T) {
	recorder := NewCompletionRecorder("")
	fake := &recordingLoopQuerier{handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
		if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "done")); err != nil {
			return pub_models.Chat{}, err
		}
		return chat, nil
	}}
	seed := pub_models.Chat{ID: "seed", Messages: []pub_models.Message{{Role: "user", Content: "achieve it"}}}
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: seed, MaxIterations: 5})

	if err := runner.Query(t.Context()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(fake.chats) != 1 {
		t.Fatalf("TextQuery calls = %d, want 1", len(fake.chats))
	}
	messages := fake.chats[0].Messages
	if len(messages) != 2 {
		t.Fatalf("iteration 1 messages = %d, want a prepended system message and the seed user message", len(messages))
	}
	if messages[0].Role != "system" || messages[0].Content != loopInstructions {
		t.Fatalf("iteration 1 system message = %+v, want the loop instructions", messages[0])
	}
	if messages[1].Role != "user" || messages[1].Content != "achieve it" {
		t.Fatalf("iteration 1 user message = %+v, want the seed message", messages[1])
	}
}

func TestAppendLoopInstructions(t *testing.T) {
	if got := appendLoopInstructions("  \n"); got != loopInstructions {
		t.Fatalf("appendLoopInstructions(blank) = %q, want only the loop instructions", got)
	}
	if got, want := appendLoopInstructions("base prompt\n\n"), "base prompt\n\n"+loopInstructions; got != want {
		t.Fatalf("appendLoopInstructions(base) = %q, want %q", got, want)
	}
	already := "base prompt\n\n" + loopInstructions
	if got := appendLoopInstructions(already + "\n"); got != already {
		t.Fatalf("appendLoopInstructions(already appended) = %q, want %q", got, already)
	}
}

func TestLoopRunner_IterationChatDoesNotDuplicateLoopInstructions(t *testing.T) {
	seed := loopSeedChat()
	seed.Messages[0].Content = appendLoopInstructions(seed.Messages[0].Content)
	runner := &loopRunner{cfg: LoopConfig{SeedChat: seed, GoalPrompt: "the goal"}}

	chat, err := runner.iterationChat(2, "handover")
	if err != nil {
		t.Fatalf("iterationChat: %v", err)
	}
	if got := strings.Count(chat.Messages[0].Content, loopInstructions); got != 1 {
		t.Fatalf("iteration 2 system message contains %d loop instruction blocks, want 1", got)
	}
}

func TestLoopRunner_HandoverAndStopEventIsolation(t *testing.T) {
	t.Run("stop event cancels only the iteration", func(t *testing.T) {
		rootCtx, rootCancel := context.WithCancel(t.Context())
		defer rootCancel()
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{handle: func(ctx context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error) {
			if iteration == 1 {
				cancel, ok := ctx.Value(utils.ContextCancelKey).(context.CancelFunc)
				if !ok {
					return pub_models.Chat{}, errors.New("iteration context has no cancel func")
				}
				cancel()
				if ctx.Err() == nil {
					return pub_models.Chat{}, errors.New("iteration cancel did not cancel the iteration context")
				}
				chat.Messages = append(chat.Messages, pub_models.Message{
					Role:      "assistant",
					Content:   "iteration one handover",
					ToolCalls: []pub_models.Call{{ID: "ignored-call", Name: "ignored_tool"}},
				})
				return chat, nil
			}
			if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "done")); err != nil {
				return pub_models.Chat{}, err
			}
			return chat, nil
		}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), GoalPrompt: "the goal text", MaxIterations: 5})

		if err := runner.Query(rootCtx); err != nil {
			t.Fatalf("Query: %v", err)
		}
		if rootCtx.Err() != nil {
			t.Fatalf("caller context = %v after the iteration StopEvent, want it untouched", rootCtx.Err())
		}
		if len(fake.chats) != 2 {
			t.Fatalf("TextQuery calls = %d, want 2", len(fake.chats))
		}

		second := fake.chats[1]
		if second.ID == "" || second.ID == loopSeedChat().ID {
			t.Fatalf("iteration 2 chat id = %q, want a fresh id", second.ID)
		}
		if second.Profile != loopSeedChat().Profile {
			t.Fatalf("iteration 2 chat profile = %q, want the seed profile", second.Profile)
		}
		if got := second.Messages[0].Content; got != appendedSeedSystem() {
			t.Fatalf("iteration 2 system message = %q, want the seed system with loop instructions", got)
		}
		if got := second.Messages[1]; got.Role != "user" || got.Content != loopSeedChat().Messages[1].Content {
			t.Fatalf("iteration 2 first user message = %+v, want it identical to the seed", got)
		}
		if first, next := chat.ComputeGroupKey(fake.chats[0]), chat.ComputeGroupKey(second); first == "" || first != next {
			t.Fatalf("GroupKey = (%q, %q), want a non-empty shared key across iterations", first, next)
		}
		if len(second.Messages) != 4 {
			t.Fatalf("iteration 2 messages = %d, want seed-through-first-user + handover + continuation", len(second.Messages))
		}
		handover := second.Messages[2]
		if handover.Role != "assistant" || handover.Content != "iteration one handover" {
			t.Fatalf("iteration 2 handover = %+v, want the prior assistant text", handover)
		}
		if len(handover.ToolCalls) != 0 {
			t.Fatalf("iteration 2 handover tool calls = %+v, want them dropped", handover.ToolCalls)
		}
		continuation := second.Messages[3]
		if continuation.Role != "user" || !strings.Contains(continuation.Content, "the goal text") {
			t.Fatalf("iteration 2 continuation = %+v, want a user message containing the goal", continuation)
		}
	})

	for _, tc := range []struct {
		name        string
		appendReply pub_models.Message
	}{
		{"missing assistant text uses the placeholder", pub_models.Message{}},
		{"empty assistant text uses the placeholder", pub_models.Message{Role: "assistant", Content: "  \n\t "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := NewCompletionRecorder("")
			fake := &recordingLoopQuerier{handle: func(ctx context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error) {
				if iteration == 1 && tc.appendReply.Role != "" {
					chat.Messages = append(chat.Messages, tc.appendReply)
				}
				if iteration == 2 {
					if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "done")); err != nil {
						return pub_models.Chat{}, err
					}
				}
				return chat, nil
			}}
			runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), GoalPrompt: "the goal", MaxIterations: 5})

			if err := runner.Query(t.Context()); err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(fake.chats) != 2 {
				t.Fatalf("TextQuery calls = %d, want 2", len(fake.chats))
			}
			handover := fake.chats[1].Messages[2]
			if handover.Role != "assistant" || handover.Content != loopHandoverPlaceholder {
				t.Fatalf("handover = %+v, want the placeholder assistant message", handover)
			}
		})
	}

	t.Run("caller cancellation before the first iteration starts nothing", func(t *testing.T) {
		rootCtx, rootCancel := context.WithCancel(t.Context())
		rootCancel()
		fake := &recordingLoopQuerier{handle: func(context.Context, int, pub_models.Chat) (pub_models.Chat, error) {
			return pub_models.Chat{}, errors.New("must not run")
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 5})

		if err := runner.Query(rootCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Query error = %v, want context cancellation", err)
		}
		if len(fake.chats) != 0 {
			t.Fatalf("TextQuery calls = %d, want 0", len(fake.chats))
		}
	})

	t.Run("caller cancellation while TextQuery fails returns the caller error", func(t *testing.T) {
		rootCtx, rootCancel := context.WithCancel(t.Context())
		fake := &recordingLoopQuerier{handle: func(context.Context, int, pub_models.Chat) (pub_models.Chat, error) {
			rootCancel()
			return pub_models.Chat{}, errors.New("stream failed")
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 5})

		if err := runner.Query(rootCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Query error = %v, want the caller cancellation to win", err)
		}
		if len(fake.chats) != 1 {
			t.Fatalf("TextQuery calls = %d, want 1", len(fake.chats))
		}
	})

	t.Run("caller cancellation stops before the next iteration", func(t *testing.T) {
		rootCtx, rootCancel := context.WithCancel(t.Context())
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{handle: func(_ context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			rootCancel()
			return chat, nil
		}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), GoalPrompt: "the goal", MaxIterations: 5})

		err := runner.Query(rootCtx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Query error = %v, want context cancellation", err)
		}
		if len(fake.chats) != 1 {
			t.Fatalf("TextQuery calls = %d, want no iteration after cancellation", len(fake.chats))
		}
	})

	t.Run("text query error returns immediately", func(t *testing.T) {
		boom := errors.New("boom")
		fake := &recordingLoopQuerier{handle: func(context.Context, int, pub_models.Chat) (pub_models.Chat, error) {
			return pub_models.Chat{}, boom
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 5})

		err := runner.Query(t.Context())
		if !errors.Is(err, boom) {
			t.Fatalf("Query error = %v, want the wrapped TextQuery error", err)
		}
		if !strings.Contains(err.Error(), "iteration 1") {
			t.Fatalf("Query error = %q, want it to name the iteration", err)
		}
		if len(fake.chats) != 1 {
			t.Fatalf("TextQuery calls = %d, want 1", len(fake.chats))
		}
	})

	t.Run("seed without a user message is rejected", func(t *testing.T) {
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			return chat, nil
		}}
		seed := pub_models.Chat{ID: "seed", Messages: []pub_models.Message{{Role: "system", Content: "only a system message"}}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: seed, MaxIterations: 5})

		err := runner.Query(t.Context())
		if err == nil || !strings.Contains(err.Error(), "no user message") {
			t.Fatalf("Query error = %v, want a missing-user-message error", err)
		}
		if len(fake.chats) != 1 {
			t.Fatalf("TextQuery calls = %d, want the first iteration to run", len(fake.chats))
		}
	})
}

func TestLoopRunner_BlockedReturnsTypedError(t *testing.T) {
	recorder := NewCompletionRecorder("")
	fake := &recordingLoopQuerier{handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
		if _, err := recorder.CallWithContext(ctx, loopInput("blocked", "Need credentials.")); err != nil {
			return pub_models.Chat{}, err
		}
		return chat, nil
	}}
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 10})

	err := runner.Query(t.Context())
	if !errors.Is(err, ErrLoopBlocked) {
		t.Fatalf("Query error = %v, want ErrLoopBlocked", err)
	}
	var blocked *LoopBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Query error = %T, want *LoopBlockedError", err)
	}
	if blocked.Iteration != 1 || blocked.Summary != "Need credentials." {
		t.Fatalf("LoopBlockedError = %+v, want iteration 1 and the summary", blocked)
	}
	if message := blocked.Error(); !strings.Contains(message, "iteration 1") || !strings.Contains(message, "Need credentials.") {
		t.Fatalf("LoopBlockedError.Error() = %q, want the iteration and summary", message)
	}
	if len(fake.chats) != 1 {
		t.Fatalf("TextQuery calls = %d, want 1", len(fake.chats))
	}
}

func TestLoopRunner_TerminalErrorKeepsDomainType(t *testing.T) {
	t.Run("blocked returns the typed error", func(t *testing.T) {
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			if _, err := recorder.CallWithContext(ctx, loopInput("blocked", "Need credentials.")); err != nil {
				return pub_models.Chat{}, err
			}
			return chat, nil
		}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 3})

		err := runner.Query(t.Context())
		if _, ok := err.(*LoopBlockedError); !ok {
			t.Fatalf("Query error = %T (%v), want *LoopBlockedError", err, err)
		}
	})

	t.Run("exhausted returns the typed error", func(t *testing.T) {
		fake := &recordingLoopQuerier{handle: func(_ context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			return chat, nil
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 1})

		err := runner.Query(t.Context())
		if _, ok := err.(*MaxIterationsError); !ok {
			t.Fatalf("Query error = %T (%v), want *MaxIterationsError", err, err)
		}
	})

	terminal := []struct {
		name          string
		maxIterations int
		submit        func(ctx context.Context, recorder CompletionRecorder) error
	}{
		{name: "blocked", maxIterations: 3, submit: func(ctx context.Context, recorder CompletionRecorder) error {
			_, err := recorder.CallWithContext(ctx, loopInput("blocked", "Need credentials."))
			return err
		}},
		{name: "exhausted", maxIterations: 1, submit: func(context.Context, CompletionRecorder) error { return nil }},
	}
	for _, tc := range terminal {
		t.Run("display failure joins and names the iteration/"+tc.name, func(t *testing.T) {
			displayErr := errors.New("display failed")
			recorder := NewCompletionRecorder("")
			fake := &recordingLoopQuerier{
				finishErr: displayErr,
				handle: func(ctx context.Context, _ int, _ pub_models.Chat) (pub_models.Chat, error) {
					return pub_models.Chat{}, tc.submit(ctx, recorder)
				},
			}
			runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: tc.maxIterations})

			err := runner.Query(t.Context())
			if !errors.Is(err, displayErr) {
				t.Fatalf("Query error = %v, want the display error", err)
			}
			if message := err.Error(); !strings.Contains(message, "loop iteration 1") {
				t.Fatalf("Query error = %q, want the failed iteration named", message)
			}
		})
	}
}

func TestLoopRunner_MaxIterationsAndValidation(t *testing.T) {
	t.Run("negative limit fails before any query", func(t *testing.T) {
		fake := &recordingLoopQuerier{handle: func(context.Context, int, pub_models.Chat) (pub_models.Chat, error) {
			return pub_models.Chat{}, errors.New("must not run")
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: -1})

		err := runner.Query(t.Context())
		if err == nil || !strings.Contains(err.Error(), "max-iterations") {
			t.Fatalf("Query error = %v, want a max-iterations validation error", err)
		}
		if len(fake.chats) != 0 {
			t.Fatalf("TextQuery calls = %d, want 0", len(fake.chats))
		}
	})

	t.Run("zero limit is unlimited", func(t *testing.T) {
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{handle: func(ctx context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error) {
			if iteration == 3 {
				if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "done")); err != nil {
					return pub_models.Chat{}, err
				}
				return chat, nil
			}
			chat.Messages = append(chat.Messages, pub_models.Message{Role: "assistant", Content: fmt.Sprintf("handover %d", iteration)})
			return chat, nil
		}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), GoalPrompt: "the goal", MaxIterations: 0})

		if err := runner.Query(t.Context()); err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(fake.chats) != 3 {
			t.Fatalf("TextQuery calls = %d, want 3", len(fake.chats))
		}
	})

	t.Run("exhausted limit returns the last handover", func(t *testing.T) {
		fake := &recordingLoopQuerier{handle: func(_ context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error) {
			chat.Messages = append(chat.Messages, pub_models.Message{Role: "assistant", Content: fmt.Sprintf("handover %d", iteration)})
			return chat, nil
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 2})

		err := runner.Query(t.Context())
		if !errors.Is(err, ErrMaxIterations) {
			t.Fatalf("Query error = %v, want ErrMaxIterations", err)
		}
		var exhausted *MaxIterationsError
		if !errors.As(err, &exhausted) {
			t.Fatalf("Query error = %T, want *MaxIterationsError", err)
		}
		if exhausted.MaxIterations != 2 || exhausted.LastHandover != "handover 2" {
			t.Fatalf("MaxIterationsError = %+v, want limit 2 and the final handover", exhausted)
		}
		if message := exhausted.Error(); !strings.Contains(message, "max-iterations") {
			t.Fatalf("MaxIterationsError.Error() = %q, want the limit named", message)
		}
		if len(fake.chats) != 2 {
			t.Fatalf("TextQuery calls = %d, want 2", len(fake.chats))
		}
	})
}

type fixedLoopRecorder struct {
	submission LoopSubmission
	recorded   bool
}

func (r *fixedLoopRecorder) Call(pub_models.Input) (string, error) {
	return "recorded", nil
}

func (r *fixedLoopRecorder) CallWithContext(context.Context, pub_models.Input) (string, error) {
	return "recorded", nil
}

func (r *fixedLoopRecorder) Specification() pub_models.Specification {
	return pub_models.Specification{Name: "complete_task"}
}

func (r *fixedLoopRecorder) Submission() (LoopSubmission, bool) {
	return r.submission, r.recorded
}

func (r *fixedLoopRecorder) Reset() {}

func TestLoopRunner_RejectsUnknownRecordedStatus(t *testing.T) {
	recorder := &fixedLoopRecorder{
		submission: LoopSubmission{Status: LoopStatusNone, Summary: "unexpected"},
		recorded:   true,
	}
	fake := &recordingLoopQuerier{handle: func(_ context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
		return chat, nil
	}}
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 5})

	err := runner.Query(t.Context())
	if err == nil || !strings.Contains(err.Error(), "unsupported recorded status") {
		t.Fatalf("Query error = %v, want an unsupported-status error", err)
	}
	if len(fake.chats) != 1 {
		t.Fatalf("TextQuery calls = %d, want 1", len(fake.chats))
	}
}

func TestLoopRunner_RegistersRecorderOnce(t *testing.T) {
	recorder := NewCompletionRecorder("")
	fake := &recordingLoopQuerier{handle: func(_ context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
		chat.Messages = append(chat.Messages, pub_models.Message{Role: "assistant", Content: "still working"})
		return chat, nil
	}}
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 3})

	if err := runner.Query(t.Context()); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Query error = %v, want ErrMaxIterations", err)
	}
	if len(fake.tools) != 1 {
		t.Fatalf("RegisterExtraTool calls = %d, want 1", len(fake.tools))
	}
	if got := fake.tools[0].Specification().Name; got != "complete_task" {
		t.Fatalf("registered tool = %q, want complete_task", got)
	}
	if len(fake.events) < 3 || fake.events[0] != "register:complete_task" || fake.events[1] != "progress:1" || fake.events[2] != "query:1" {
		t.Fatalf("events = %v, want one registration and the first progress before the first query", fake.events)
	}
	for _, event := range fake.events[1:] {
		if strings.HasPrefix(event, "register:") {
			t.Fatalf("events = %v, want the tool registered only once", fake.events)
		}
	}
	if len(fake.chats) != 3 {
		t.Fatalf("TextQuery calls = %d, want 3", len(fake.chats))
	}
}

type loopTestTool struct {
	name string
	out  string
}

func (t loopTestTool) Call(pub_models.Input) (string, error) {
	return t.out, nil
}

func (t loopTestTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: t.name}
}

func newLoopTestQuerier(t *testing.T, conf Configurations) *Querier[*vendors.Mock] {
	t.Helper()
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	conf.Model = "test"
	conf.ConfigDir = t.TempDir()
	conf.Out = &strings.Builder{}
	conf.SkipAmbientMcpServers = true
	q, err := NewQuerier(t.Context(), conf, &vendors.Mock{})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	return &q
}

func loopToolNames(chat pub_models.Chat) map[string]bool {
	names := map[string]bool{}
	for _, message := range chat.Messages {
		if message.Role != "assistant" {
			continue
		}
		for _, call := range message.ToolCalls {
			names[call.Name] = true
		}
	}
	return names
}

func loopToolOutputs(chat pub_models.Chat) map[string]bool {
	outputs := map[string]bool{}
	for _, message := range chat.Messages {
		if message.Role == "tool" {
			outputs[message.Content] = true
		}
	}
	return outputs
}

type loopNoToolBoxModel struct{}

func (loopNoToolBoxModel) Setup() error {
	return nil
}

func (loopNoToolBoxModel) StreamCompletions(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
	return nil, errors.New("not used")
}

func TestQuerier_RegisterExtraTool_withoutToolBox(t *testing.T) {
	var q Querier[loopNoToolBoxModel]
	var lq LoopQuerier = &q
	lq.RegisterExtraTool(loopTestTool{name: "extra_tool", out: "extra output"})

	if q.tooling.run["extra_tool"] == nil || q.tooling.base["extra_tool"] == nil {
		t.Fatalf("extra_tool = (run %v, base %v), want it in both catalogs", q.tooling.run, q.tooling.base)
	}
	if len(q.tooling.registered) != 0 {
		t.Fatalf("registered tools = %v, want no model registration without a tool box", q.tooling.registered)
	}
}

func TestQuerier_RegisterExtraTool(t *testing.T) {
	var _ LoopQuerier = &Querier[*vendors.Mock]{}

	t.Run("empty catalog", func(t *testing.T) {
		q := newLoopTestQuerier(t, Configurations{})
		var lq LoopQuerier = q
		lq.RegisterExtraTool(loopTestTool{name: "extra_tool", out: "extra output"})
		lq.RegisterExtraTool(loopTestTool{name: "extra_tool", out: "extra output"})
		if len(q.tooling.registered) != 1 {
			t.Fatalf("registered tools = %v, want extra_tool registered once", q.tooling.registered)
		}

		if q.tooling.run["extra_tool"] == nil {
			t.Fatal("extra_tool is not in the dispatch catalog")
		}
		chat, err := lq.TextQuery(t.Context(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "tool_extra_tool"}}})
		if err != nil {
			t.Fatalf("TextQuery: %v", err)
		}
		if !loopToolNames(chat)["extra_tool"] {
			t.Fatalf("tool calls = %v, want extra_tool offered and called", loopToolNames(chat))
		}
		if !loopToolOutputs(chat)["extra output"] {
			t.Fatalf("tool outputs = %v, want extra_tool executed", loopToolOutputs(chat))
		}
	})

	t.Run("populated catalog keeps existing tools", func(t *testing.T) {
		q := newLoopTestQuerier(t, Configurations{
			UseTools: true,
			Tools:    []pub_models.LLMTool{loopTestTool{name: "existing_tool", out: "existing output"}},
		})
		var lq LoopQuerier = q
		lq.RegisterExtraTool(loopTestTool{name: "extra_tool", out: "extra output"})
		if _, ok := q.tooling.registered["existing_tool"]; !ok {
			t.Fatalf("registered tools = %v, want the pre-existing tool", q.tooling.registered)
		}

		chat, err := lq.TextQuery(t.Context(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "tool_existing_tool tool_extra_tool"}}})
		if err != nil {
			t.Fatalf("TextQuery: %v", err)
		}
		names := loopToolNames(chat)
		if !names["existing_tool"] || !names["extra_tool"] {
			t.Fatalf("tool calls = %v, want both the existing and the extra tool", names)
		}
		outputs := loopToolOutputs(chat)
		if !outputs["existing output"] || !outputs["extra output"] {
			t.Fatalf("tool outputs = %v, want both tools executed", outputs)
		}
	})
}
