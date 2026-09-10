package text

import (
	"context"
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

func stripThinkingBlocks(s string) string {
	start := "[thinking]"
	end := "[/thinking]"
	for {
		i := strings.Index(s, start)
		if i == -1 {
			return s
		}
		j := strings.Index(s[i+len(start):], end)
		if j == -1 {
			return s
		}
		j += i + len(start)
		// The wrapper appends a newline after [/thinking]; drop it too so the
		// persisted content does not start with a stray blank line.
		s = s[:i] + strings.TrimPrefix(s[j+len(end):], "\n")
	}
}

type sessionFinalizer[C models.StreamCompleter] struct {
	querier *Querier[C]
}

// accumulateCompletedUsage sums token usage across every completed model call
// in a multi-step agent run. Each API call returns usage on its final streaming
// chunk, and the session runner captures it into CompletedCalls. The last step's
// FinalUsage alone misses output tokens from earlier tool-call rounds and
// undercounts by 10-20× for agents that use tools. Summing all CompletedCalls
// matches the provider's billing (each call is a separate invoice line).
// Falls back to FinalUsage when CompletedCalls is empty (e.g. single-call runs
// where the runner hasn't populated the slice).
func accumulateCompletedUsage(completed []CompletedModelCall, final *pub_models.Usage) *pub_models.Usage {
	if len(completed) == 0 {
		return final
	}
	var total pub_models.Usage
	for _, call := range completed {
		if call.Usage == nil {
			continue
		}
		total.PromptTokens += call.Usage.PromptTokens
		total.CompletionTokens += call.Usage.CompletionTokens
		total.TotalTokens += call.Usage.TotalTokens
		total.PromptTokensDetails.CachedTokens += call.Usage.PromptTokensDetails.CachedTokens
		total.PromptTokensDetails.AudioTokens += call.Usage.PromptTokensDetails.AudioTokens
		total.CompletionTokensDetails.ReasoningTokens += call.Usage.CompletionTokensDetails.ReasoningTokens
		total.CompletionTokensDetails.AudioTokens += call.Usage.CompletionTokensDetails.AudioTokens
		total.CompletionTokensDetails.AcceptedPredictionTokens += call.Usage.CompletionTokensDetails.AcceptedPredictionTokens
		total.CompletionTokensDetails.RejectedPredictionTokens += call.Usage.CompletionTokensDetails.RejectedPredictionTokens
	}
	return &total
}

func mostRecentCompletedUsage(completed []CompletedModelCall, final *pub_models.Usage) *pub_models.Usage {
	for i := len(completed) - 1; i >= 0; i-- {
		if completed[i].Usage != nil {
			return completed[i].Usage
		}
	}
	return final
}

func (f sessionFinalizer[C]) Finalize(ctx context.Context, session *QuerySession) error {
	if session == nil || session.Finalized {
		return nil
	}
	session.Finalized = true
	q := f.querier

	if q.debug {
		ancli.Noticef("post process querier: %+v", q)
	}
	if session.Raw && !q.structuredOutput {
		fmt.Fprintln(q.out)
	}

	if session.FinalAssistantText != "" {
		session.Chat.Messages = append(session.Chat.Messages, pub_models.Message{
			Role:             "assistant",
			Content:          stripThinkingBlocks(session.FinalAssistantText),
			ReasoningContent: session.FinalReasoningText,
		})
	}
	session.Chat.TokenUsage = accumulateCompletedUsage(session.CompletedCalls, session.FinalUsage)
	session.Chat.RecentTokenUsage = mostRecentCompletedUsage(session.CompletedCalls, session.FinalUsage)
	// Cost enrichment is gated on usage, not on persistence: a non-persisting
	// querier still accounts for its run (worklog
	// 2026-09-09-conversation-summaries, D19).
	if session.Chat.TokenUsage != nil {
		session.Chat = q.costEnricher.enrich(session.Chat)
	}
	q.chat = session.Chat

	// A failed or empty run persists at once; an interrupt (root cancelled
	// without a StopEvent) abandons the summary before the display so no
	// join can delay the persist (D21).
	if session.FinalAssistantText == "" || session.Failed {
		q.abandonSummary()
		return f.persist(session)
	}
	if ctx.Err() != nil && !session.SawStopEvent {
		q.abandonSummary()
	}
	if q.structuredOutput {
		// The final-answer record fires before the structured display too:
		// postProcessOutput's display branches are skipped on this path, but
		// the agent-logging contract is unconditional (worklog
		// 2026-08-15-agent-slog-output, Phase 3). The typed-agent path is
		// always structured, so a missing record here would leave every
		// embedded log without the run's final answer.
		q.logMessage(ctx, "final_answer", stripThinkingBlocks(session.FinalAssistantText), "")
		fmt.Fprintln(q.out, stripThinkingBlocks(session.FinalAssistantText))
	} else {
		q.postProcessOutput(ctx, pub_models.Message{
			Role:    "assistant",
			Content: session.FinalAssistantText,
		})
	}
	q.joinSummary(&session.Chat)
	q.chat = session.Chat
	return f.persist(session)
}

// persist writes the reply, records the directory binding and returns the
// persist error: a later -re/-dre would otherwise silently read stale state,
// so the caller must hear about it (worklog 2026-09-05-error-propagation,
// S8). The runner joins it into the run's returned error.
func (f sessionFinalizer[C]) persist(session *QuerySession) error {
	q := f.querier
	var persistErr error
	if session.ShouldSaveReply {
		// Origin stamping is always-on and forward-only: stamp the canonical CWD on
		// first persist, preserve it on every later write (including replies).
		if originErr := chat.EnsureOriginDir(q.configDir, &session.Chat); originErr != nil {
			ancli.Warnf("failed to stamp origin directory: %v\n", originErr)
		}
		q.chat = session.Chat
		if err := chat.SaveAsPreviousQuery(q.configDir, session.Chat); err != nil {
			persistErr = fmt.Errorf("failed to save previous query: %w", err)
		}
		// History recording is always-on for non-reply queries. A plain -re forks a
		// fresh promoted id, so recording it would pollute the history with
		// near-duplicates; but a directory reply (-dre) continues the bound
		// conversation in place (same id), so recording it just bumps that entry —
		// it keeps the directory's history current and the binding chainable.
		if (!q.replyMode || q.dirReplyMode) && session.Chat.ID != "" && session.Chat.ID != "globalScope" {
			if updateErr := chat.UpdateDirScopeFromCWD(q.configDir, session.Chat.ID); updateErr != nil {
				ancli.Warnf("failed to update directory-scoped binding: %v\n", updateErr)
			}
		}
	}
	if q.debug {
		ancli.PrintOK(fmt.Sprintf("Querier.postProcess:\n%v\n", debug.IndentedJsonFmt(q)))
	}
	return persistErr
}
