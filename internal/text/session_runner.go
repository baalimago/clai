package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

type ModelStepResult struct {
	AssistantText string
	ToolCalls     []pub_models.Call
	Usage         *pub_models.Usage
	StopRequested bool
	EndedNormally bool
}

type sessionRunner[C models.StreamCompleter] struct {
	querier        *Querier[C]
	recorder       CallUsageRecorder
	toolExecutor   toolExecutor[C]
	finalizer      sessionFinalizerer
	stoploss       *stoploss
	currentRetries int
	// resizeEvents delivers fresh terminal dimensions after SIGWINCH. It is
	// the dimensions.Viewer's Events channel in production and is nil for
	// non-rolling, non-terminal, and non-file sessions, which keep the
	// one-shot q.dims read. The runner consumes it in the serialized session
	// loop, so a resize and its redraw can never race a token or tool write.
	resizeEvents <-chan dimensions.Dimensions
}

type sessionFinalizerer interface {
	Finalize(*QuerySession)
}

func (r *sessionRunner[C]) Run(ctx context.Context, session *QuerySession) (runErr error) {
	if session == nil {
		return errors.New("run session: session is nil")
	}
	if r.recorder == nil {
		r.recorder = noopCallUsageRecorder{}
	}
	if r.stoploss == nil {
		r.stoploss = r.querier.newStoploss()
	}
	session.StartedAt = time.Now()
	defer func() {
		session.FinishedAt = time.Now()
		session.Failed = runErr != nil
		r.finalizer.Finalize(session)
	}()
	// Final MCP log flush before the finalizer prints the answer: deferred
	// functions run LIFO, so buffered server errors elevate below the window
	// and above the answer text.
	defer func() {
		if err := r.querier.drainMcpLogs(); err != nil {
			ancli.Warnf("failed to flush mcp server logs: %v", err)
		}
	}()

	for stepIndex := 0; ; {
		stepStartedAt := time.Now()
		stepResult, err := r.runStepWithRetry(ctx, session)
		if err != nil {
			if session.PendingTextString() != "" && session.FinalAssistantText == "" {
				session.FlushPendingTextToFinal()
			}
			return fmt.Errorf("run step %d: %w", stepIndex, err)
		}

		completedCall := CompletedModelCall{
			StepIndex:      stepIndex,
			Model:          r.modelName(),
			StartedAt:      stepStartedAt,
			FinishedAt:     time.Now(),
			Usage:          stepResult.Usage,
			EndedWithTool:  len(stepResult.ToolCalls) > 0,
			EndedWithReply: len(stepResult.ToolCalls) == 0 && stepResult.EndedNormally && !stepResult.StopRequested,
			EndedWithStop:  stepResult.StopRequested,
		}
		session.CompletedCalls = append(session.CompletedCalls, completedCall)
		if err := r.recorder.Record(ctx, completedCall); err != nil {
			ancli.Warnf("failed to record completed model call: %v", err)
		}

		if len(stepResult.ToolCalls) > 0 {
			if err := r.toolExecutor.ExecuteBatch(ctx, session, stepResult.ToolCalls); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return fmt.Errorf("execute tool step %d: %w", stepIndex, err)
			}
			// Token check AFTER the batch so the chat order stays
			// [assistant tool-call] [tool results] [handover user msg].
			if _, err := r.stoploss.CheckContextBudget(ctx, r.querier.Model, session, stepResult.Usage); err != nil {
				return fmt.Errorf("stoploss check step %d: %w", stepIndex, err)
			}
			stepIndex++
			continue
		}

		if stepResult.AssistantText != "" {
			session.FinalAssistantText = stepResult.AssistantText
			session.FinalReasoningText = session.PendingReasoning.String()
		}
		session.FinalUsage = stepResult.Usage
		if stepResult.StopRequested || stepResult.EndedNormally {
			return nil
		}
		stepIndex++
	}
}

func (r *sessionRunner[C]) runStepWithRetry(ctx context.Context, session *QuerySession) (ModelStepResult, error) {
	r.currentRetries = 0
	for {
		result, err := r.executeModelStep(ctx, session)
		if err == nil {
			return result, nil
		}
		var rateLimitErr *models.ErrRateLimit
		if !errors.As(err, &rateLimitErr) {
			return ModelStepResult{}, err
		}
		r.currentRetries++
		if r.currentRetries > RateLimitRetries {
			return ModelStepResult{}, fmt.Errorf("rate limit retry limit exceeded (%v), giving up", RateLimitRetries)
		}
		if err := r.waitForRateLimitReset(ctx, session.Chat, *rateLimitErr); err != nil {
			return ModelStepResult{}, fmt.Errorf("wait for rate limit reset: %w", err)
		}
	}
}

func (r *sessionRunner[C]) waitForRateLimitReset(ctx context.Context, chat pub_models.Chat, rateLimitErr models.ErrRateLimit) error {
	counter, ok := any(r.querier.Model).(models.InputTokenCounter)
	if ok {
		inCount, err := counter.CountInputTokens(ctx, chat)
		if err != nil {
			return fmt.Errorf("count input tokens: %w", err)
		}
		waitDur := time.Until(rateLimitErr.ResetAt)
		if waitDur < time.Second {
			ancli.Warnf("rate limit wait duration less than 1 second, setting to %v", FallbackWaitDuration)
			waitDur = FallbackWaitDuration
		}
		if inCount < int(float64(r.querier.rateLimitLastAmTokens)*0.8) {
			waitDur *= 2
			ancli.Warnf(
				"am of input tokens is: %v, which is: %v lower than last. Exp-increasing sleep to: %v",
				inCount,
				r.querier.rateLimitLastAmTokens-inCount,
				waitDur,
			)
		}
		if err := sleepContext(ctx, waitDur); err != nil {
			return fmt.Errorf("sleep during rate limit backoff: %w", err)
		}
		r.querier.rateLimitLastAmTokens = inCount
		return nil
	}

	ancli.Warnf("detected rate limit at: %v tokens, will sleep until: %v\n", rateLimitErr.TokensRemaining, rateLimitErr.ResetAt)
	if err := sleepContext(ctx, time.Until(rateLimitErr.ResetAt.Add(10*time.Second))); err != nil {
		return fmt.Errorf("sleep during fallback rate limit backoff: %w", err)
	}
	return nil
}

func (r *sessionRunner[C]) executeModelStep(ctx context.Context, session *QuerySession) (ModelStepResult, error) {
	q := r.querier
	traceChatf("query start chat_id=%q messages=%d raw=%t should_save_reply=%t", session.Chat.ID, len(session.Chat.Messages), q.Raw, session.ShouldSaveReply)
	traceChatf("query sending chat to stream completions chat_id=%q messages=%d", session.Chat.ID, len(session.Chat.Messages))
	session.ResetPendingText()

	completionsChan, err := q.Model.StreamCompletions(ctx, session.Chat)
	if err != nil {
		return ModelStepResult{}, fmt.Errorf("stream completions: %w", err)
	}

	// mcpNotify wakes the serialized loop when the MCP sink buffers an error
	// entry. It is nil (select case disabled) when no MCP sink exists.
	var mcpNotify <-chan struct{}
	if q.mcpSink != nil {
		mcpNotify = q.mcpSink.Notify()
	}

	var result ModelStepResult
	for {
		// Drain buffered MCP server logs before this event renders, so the log
		// block joins the same frame as the reasoning or tool activity.
		if err := q.drainMcpLogs(); err != nil {
			return ModelStepResult{}, fmt.Errorf("drain mcp logs: %w", err)
		}
		select {
		case completion, ok := <-completionsChan:
			if !ok {
				q.closeReasoningIfOpen(session)
				if len(result.ToolCalls) == 0 {
					q.prepareFinalAnswerPop()
				}
				result.EndedNormally = len(result.ToolCalls) == 0
				result.AssistantText = session.PendingTextString()
				result.Usage = q.currentTokenUsage()
				return result, nil
			}
			// A resize that arrived before this event must win over the render:
			// the select may observe the resize and the event as simultaneously
			// ready and pick either, so drain every buffered resize before any
			// event renders. This keeps the first frame (and every later frame)
			// at the freshest dimensions; a stale-width row never appears.
			if err := r.drainPendingResizes(); err != nil {
				return ModelStepResult{}, err
			}
			switch cast := completion.(type) {
			case pub_models.Call:
				if q.reasoningActive && cast.ReasoningContent == "" {
					cast.ReasoningContent = q.reasoningBuf.String()
				}
				q.closeReasoningIfOpen(session)
				result.ToolCalls = append(result.ToolCalls, cast)
			case string:
				q.closeReasoningIfOpen(session)
				if err := q.handleTokenForSession(session, cast); err != nil {
					return ModelStepResult{}, err
				}
			case error:
				if errors.Is(cast, context.Canceled) || errors.Is(cast, io.EOF) {
					q.closeReasoningIfOpen(session)
					result.EndedNormally = true
					result.AssistantText = session.PendingTextString()
					result.Usage = q.currentTokenUsage()
					return result, nil
				}
				return ModelStepResult{}, fmt.Errorf("completion stream error: %w", cast)
			case models.NoopEvent:
			case models.ReasoningEvent:
				if !q.reasoningActive {
					if q.usesActivityViewport() {
						q.ensureActivityViewport()
					} else if !q.debug && !q.structuredOutput {
						w := q.out
						if w == nil {
							w = os.Stdout
						}
						if q.rawDisplay() {
							fmt.Fprint(w, "[thinking]")
						} else {
							fmt.Fprint(w, table.Colorize(utils.RoleColor("reasoning"), "[thinking]"))
						}
					}
					q.reasoningActive = true
				}
				if q.usesActivityViewport() {
					q.activityViewport.AppendReasoning(cast.Content)
					if err := q.activityViewport.Render(q.out); err != nil {
						return ModelStepResult{}, fmt.Errorf("render activity viewport: %w", err)
					}
				} else if !q.debug && !q.structuredOutput {
					w := q.out
					if w == nil {
						w = os.Stdout
					}
					if q.rawDisplay() {
						fmt.Fprint(w, cast.Content)
					} else {
						fmt.Fprint(w, table.Colorize(utils.RoleColor("reasoning"), cast.Content))
					}
				}
				q.appendReasoning(cast.Content)
			case models.StopEvent:
				q.closeReasoningIfOpen(session)
				result.AssistantText = session.PendingTextString()
				result.Usage = q.currentTokenUsage()
				if len(result.ToolCalls) > 0 {
					return result, nil
				}
				q.prepareFinalAnswerPop()
				contextCancel := ctx.Value(utils.ContextCancelKey)
				castContextCancel, ok := contextCancel.(context.CancelFunc)
				if ok {
					castContextCancel()
				}
				session.SawStopEvent = true
				result.StopRequested = true
				return result, nil
			case nil:
				if q.debug {
					ancli.PrintWarn("received nil completion event, which is slightly weird, but not necessarily an error")
				}
			default:
				return ModelStepResult{}, fmt.Errorf("unknown completion type: %v", completion)
			}
		case d, ok := <-r.resizeEvents:
			if !ok {
				// The watcher stopped (session teardown or source close); no
				// further resize applies. Nilling the channel keeps the select
				// from spinning on the closed channel.
				r.resizeEvents = nil
				continue
			}
			if err := r.applyResize(d); err != nil {
				return ModelStepResult{}, err
			}
		case <-mcpNotify:
			// An MCP server wrote an error while the stream was waiting.
			// Drain it now so the elevated block renders live instead of
			// waiting for the next session event.
			if err := q.drainMcpLogs(); err != nil {
				return ModelStepResult{}, fmt.Errorf("drain mcp logs: %w", err)
			}
		case <-ctx.Done():
			q.closeReasoningIfOpen(session)
			result.StopRequested = true
			result.AssistantText = session.PendingTextString()
			result.Usage = q.currentTokenUsage()
			q.prepareFinalAnswerPop()
			return result, nil
		}
	}
}

// applyResize refreshes the session snapshot and the rolling window after a
// SIGWINCH. It runs on the serialized session loop, never in a signal
// callback, so the resize, its redraw, and token/tool writes cannot overlap.
// The viewer delivers only snapshots from successful reads; a failed refresh
// never reaches this method, so the last valid snapshot survives. A resize
// before the first reasoning/tool event updates q.dims only; the lazy
// viewport creation reads the fresh snapshot and its first render uses the
// new dimensions immediately. Render errors abort the step like any other
// viewport write error; a partial frame stays dirty and the next Render
// retries the full frame.
func (r *sessionRunner[C]) applyResize(d dimensions.Dimensions) error {
	q := r.querier
	q.dims = d
	if q.activityViewport != nil {
		if err := q.drainMcpLogs(); err != nil {
			return fmt.Errorf("drain mcp logs after resize: %w", err)
		}
		q.activityViewport.Resize(d.Width, d.Height)
		if err := q.activityViewport.Render(q.out); err != nil {
			return fmt.Errorf("render activity viewport after resize: %w", err)
		}
	}
	return nil
}

// drainPendingResizes applies every resize event buffered before the next
// completion event is rendered. The select may observe a resize and an event
// as simultaneously ready and pick either; draining here makes the resize
// that arrived first always win, so a frame never renders at a stale width
// (R2-03). It is a no-op for an empty channel; a closed channel nils the
// source so the main select stops watching it.
func (r *sessionRunner[C]) drainPendingResizes() error {
	for {
		select {
		case d, ok := <-r.resizeEvents:
			if !ok {
				r.resizeEvents = nil
				return nil
			}
			if err := r.applyResize(d); err != nil {
				return fmt.Errorf("drain resize: %w", err)
			}
		default:
			return nil
		}
	}
}

func (r *sessionRunner[C]) modelName() string {
	namer, ok := any(r.querier.Model).(ModelNamer)
	if !ok {
		return ""
	}
	return namer.ModelName()
}

func sleepContext(ctx context.Context, dur time.Duration) error {
	if dur <= 0 {
		return nil
	}
	timer := time.NewTimer(dur)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return fmt.Errorf("context done while sleeping: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
