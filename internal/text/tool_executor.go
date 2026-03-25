package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type ToolDecision struct {
	PatchedCall         pub_models.Call
	SkipExecution       bool
	TreatAsReturnToUser bool
}

type toolExecutor[C models.StreamCompleter] struct {
	querier *Querier[C]
}

func (e toolExecutor[C]) Execute(ctx context.Context, session *QuerySession, call pub_models.Call) error {
	_ = ctx
	q := e.querier
	if q.debug || misc.Truthy(os.Getenv("DEBUG_CALL")) {
		ancli.PrintOK(fmt.Sprintf("received tool call: %v", debug.IndentedJsonFmt(call)))
	}

	decision := q.decideToolCall(session, call)
	if decision.TreatAsReturnToUser || decision.SkipExecution {
		session.FinalAssistantText = session.PendingTextString()
		session.ResetPendingText()
		return nil
	}
	if err := e.finalizeAssistantTextBeforeToolCall(session, call); err != nil {
		return fmt.Errorf("finalize assistant text before tool call: %w", err)
	}
	call = decision.PatchedCall

	assistantToolsCall := pub_models.Message{
		Role:      "assistant",
		Content:   call.PrettyPrint(),
		ToolCalls: []pub_models.Call{call},
	}
	if !q.debug {
		err := utils.AttemptPrettyPrint(q.out, assistantToolsCall, q.username, q.Raw)
		if err != nil {
			return fmt.Errorf("pretty print assistant tool call: %w", err)
		}
	}
	session.Chat.Messages = append(session.Chat.Messages, assistantToolsCall)

	out := tools.Invoke(call)
	if q.maxToolCalls != nil {
		if session.ToolCallsUsed >= *q.maxToolCalls {
			out = "ERROR: No more tool calls allowed. "
			persistence := session.ToolCallsUsed - *q.maxToolCalls
			if persistence > 0 {
				out += "You will be HARD SHUT DOWN if you persist. "
			}
			if persistence > 1 {
				out += "This is your LAST WARNING. "
			}
			if persistence > 2 {
				return io.EOF
			}
		} else {
			outTmp, err := q.prefixToolCallsRemainingWithCount(out, session.ToolCallsUsed)
			if err != nil {
				return fmt.Errorf("prefix tool calls remaining: %w", err)
			}
			out = outTmp
		}
		session.ToolCallsUsed++
	}
	out = limitToolOutput(out, q.toolOutputRuneLimit)
	if out == "" {
		out = "<EMPTY-RESPONSE>"
	}
	toolsOutput := pub_models.Message{
		Role:       "tool",
		Content:    out,
		ToolCallID: call.ID,
	}
	session.Chat.Messages = append(session.Chat.Messages, toolsOutput)
	if q.Raw {
		err := utils.AttemptPrettyPrint(q.out, toolsOutput, "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("pretty print raw tool output: %w", err)
		}
	} else if !q.debug {
		toolPrintContent := out
		if !strings.Contains(toolPrintContent, "mcp_") {
			toolPrintContent = utils.ShortenedOutput(out, MaxShortenedNewlines)
		}
		smallOutputMsg := pub_models.Message{
			Role:    "tool",
			Content: toolPrintContent,
		}
		err := utils.AttemptPrettyPrint(q.out, smallOutputMsg, "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("pretty print tool output: %w", err)
		}
	}
	session.ResetPendingText()
	return nil
}

func (e toolExecutor[C]) finalizeAssistantTextBeforeToolCall(session *QuerySession, call pub_models.Call) error {
	if session == nil {
		return errors.New("session is nil")
	}
	pending := session.PendingTextString()
	if pending == "" {
		return nil
	}
	q := e.querier
	if !q.Raw && q.termWidth > 0 {
		utils.UpdateMessageTerminalMetadata(pending, &session.Line, &session.LineCount, q.termWidth)
		if err := utils.ClearTermTo(q.out, q.termWidth, session.LineCount-1); err != nil {
			return fmt.Errorf("clear streamed assistant text before tool call: %w", err)
		}
	}
	if isEchoedToolCallText(pending, call) {
		session.ResetPendingText()
		q.fullMsg = ""
		q.line = ""
		q.lineCount = 0
		return nil
	}
	session.ResetPendingText()
	session.FinalAssistantText = pending
	q.fullMsg = pending
	q.line = ""
	q.lineCount = 0
	q.postProcessOutput(pub_models.Message{
		Role:    "assistant",
		Content: pending,
	})
	session.Line = q.line
	session.LineCount = q.lineCount
	return nil
}

func isEchoedToolCallText(pending string, call pub_models.Call) bool {
	return strings.TrimSpace(pending) == strings.TrimSpace(call.PrettyPrint())
}

func (q *Querier[C]) decideToolCall(session *QuerySession, call pub_models.Call) ToolDecision {
	if session.LikelyGeminiPreview || q.checkIfGemini3Preview(call) {
		session.LikelyGeminiPreview = true
		if call.ExtraContent == nil {
			return ToolDecision{
				PatchedCall:         call,
				SkipExecution:       true,
				TreatAsReturnToUser: true,
			}
		}
	}
	call.Patch()
	return ToolDecision{PatchedCall: call}
}

func (q *Querier[C]) prefixToolCallsRemainingWithCount(out string, used int) (string, error) {
	if q.maxToolCalls == nil {
		return "", errors.New("maxToolCalls is not configured")
	}
	return fmt.Sprintf("[ Tool calls remaining: %v ] %v", (*q.maxToolCalls - used), out), nil
}
