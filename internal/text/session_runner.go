package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type ModelStepResult struct {
	AssistantText string
	ToolCall      *pub_models.Call
	Usage         *pub_models.Usage
	StopRequested bool
	EndedNormally bool
}

type sessionRunner[C models.StreamCompleter] struct {
	querier        *Querier[C]
	recorder       CallUsageRecorder
	toolExecutor   toolExecutor[C]
	finalizer      sessionFinalizerer
	currentRetries int
}

type sessionFinalizerer interface {
	Finalize(*QuerySession)
}

func (r *sessionRunner[C]) Run(ctx context.Context, session *QuerySession) error {
	if session == nil {
		return errors.New("run session: session is nil")
	}
	if r.recorder == nil {
		r.recorder = noopCallUsageRecorder{}
	}
	session.StartedAt = time.Now()
	defer func() {
		session.FinishedAt = time.Now()
		r.finalizer.Finalize(session)
	}()

	if err := r.querier.tokenLengthWarning(); err != nil {
		return fmt.Errorf("run token warning: %w", err)
	}

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
			EndedWithTool:  stepResult.ToolCall != nil,
			EndedWithReply: stepResult.ToolCall == nil && stepResult.EndedNormally && !stepResult.StopRequested,
			EndedWithStop:  stepResult.StopRequested,
		}
		session.CompletedCalls = append(session.CompletedCalls, completedCall)
		if err := r.recorder.Record(ctx, completedCall); err != nil {
			ancli.Warnf("failed to record completed model call: %v", err)
		}

		if stepResult.ToolCall != nil {
			if err := r.toolExecutor.Execute(ctx, session, *stepResult.ToolCall); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return fmt.Errorf("execute tool step %d: %w", stepIndex, err)
			}
			stepIndex++
			continue
		}

		if stepResult.AssistantText != "" {
			session.FinalAssistantText = stepResult.AssistantText
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

	var result ModelStepResult
	for {
		select {
		case completion, ok := <-completionsChan:
			if !ok {
				result.EndedNormally = true
				result.AssistantText = session.PendingTextString()
				result.Usage = q.currentTokenUsage()
				return result, nil
			}
			switch cast := completion.(type) {
			case pub_models.Call:
				result.ToolCall = &cast
				result.AssistantText = session.PendingTextString()
				result.Usage = q.currentTokenUsage()
				return result, nil
			case string:
				q.handleTokenForSession(session, cast)
			case error:
				if errors.Is(cast, context.Canceled) || errors.Is(cast, io.EOF) {
					result.EndedNormally = true
					result.AssistantText = session.PendingTextString()
					result.Usage = q.currentTokenUsage()
					return result, nil
				}
				return ModelStepResult{}, fmt.Errorf("completion stream error: %w", cast)
			case models.NoopEvent:
			case models.StopEvent:
				contextCancel := ctx.Value(utils.ContextCancelKey)
				castContextCancel, ok := contextCancel.(context.CancelFunc)
				if ok {
					castContextCancel()
				}
				session.SawStopEvent = true
				result.StopRequested = true
				result.AssistantText = session.PendingTextString()
				result.Usage = q.currentTokenUsage()
				return result, nil
			case nil:
				if q.debug {
					ancli.PrintWarn("received nil completion event, which is slightly weird, but not necessarily an error")
				}
			default:
				return ModelStepResult{}, fmt.Errorf("unknown completion type: %v", completion)
			}
		case <-ctx.Done():
			result.StopRequested = true
			result.AssistantText = session.PendingTextString()
			result.Usage = q.currentTokenUsage()
			return result, nil
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
