package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

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
	if call.Name == string(pub_models.LoadSkillTool) {
		return e.executeLoadSkill(ctx, session, call)
	}
	if isLookbackTool(call.Name) {
		return e.executeLookbackTool(session, call)
	}

	// Build display message (has PrettyPrint in Content for user-facing output).
	// The model-safe message for chat history omits Content so the model does not
	// learn the "Call: ..." text format, which causes hallucinations.
	assistantToolsCall := pub_models.Message{
		Role:             "assistant",
		Content:          call.PrettyPrint(),
		ToolCalls:        []pub_models.Call{call},
		ReasoningContent: call.ReasoningContent,
	}
	modelSafeMsg := pub_models.Message{
		Role:             "assistant",
		ToolCalls:        []pub_models.Call{call},
		ReasoningContent: call.ReasoningContent,
	}
	if !q.debug {
		err := utils.AttemptPrettyPrint(q.out, assistantToolsCall, q.username, q.Raw)
		if err != nil {
			return fmt.Errorf("pretty print assistant tool call: %w", err)
		}
	}
	session.Chat.Messages = append(session.Chat.Messages, modelSafeMsg)

	out := tools.Invoke(ctx, call)
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
		out = fmt.Sprintf("<NO-OUTPUT> tool %s completed successfully but produced no stdout/stderr.", call.Name)
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
		err := utils.AttemptPrettyPrint(q.out, utils.PrepareDisplayMessage(toolsOutput), "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("pretty print tool output: %w", err)
		}
	}
	session.ResetPendingText()
	return nil
}

func (e toolExecutor[C]) executeLoadSkill(ctx context.Context, session *QuerySession, call pub_models.Call) error {
	q := e.querier
	if q.skillLoader == nil {
		return fmt.Errorf("load_skill requested but skills are unavailable")
	}
	var skillName, rawArgs string
	if call.Inputs != nil {
		if v, ok := (*call.Inputs)["skill"].(string); ok {
			skillName = v
		}
		if v, ok := (*call.Inputs)["arguments"].(string); ok {
			rawArgs = v
		}
	}
	loaded, err := q.skillLoader.LoadSkill(ctx, skillName, rawArgs, q.baseTools)
	if err != nil {
		return err
	}
	if loaded.ActivationErr != "" {
		// Build display message (has PrettyPrint in Content for user-facing output).
		// The model-safe message for chat history omits Content so the model does not
		// learn the "Call: ..." text format, which causes hallucinations.
		assistantToolsCall := pub_models.Message{
			Role:      "assistant",
			Content:   call.PrettyPrint(),
			ToolCalls: []pub_models.Call{call},
		}
		modelSafeMsg := pub_models.Message{
			Role:      "assistant",
			ToolCalls: []pub_models.Call{call},
		}
		if !q.debug {
			if err := utils.AttemptPrettyPrint(q.out, assistantToolsCall, q.username, q.Raw); err != nil {
				return fmt.Errorf("pretty print assistant tool call: %w", err)
			}
		}
		session.Chat.Messages = append(session.Chat.Messages, modelSafeMsg)
		outMsg := pub_models.Message{Role: "tool", Content: "ERROR: " + loaded.ActivationErr, ToolCallID: call.ID}
		session.Chat.Messages = append(session.Chat.Messages, outMsg)
		if !q.debug {
			if err := utils.AttemptPrettyPrint(q.out, outMsg, "tool", q.Raw); err != nil {
				return fmt.Errorf("pretty print skill output: %w", err)
			}
		}
		session.ResetPendingText()
		return nil
	}
	if len(loaded.ActiveTools) > 0 {
		q.baseTools = loaded.ActiveTools
	}
	content := loaded.RenderedBody
	userVisibleContent := loaded.UserVisibleBody
	if strings.TrimSpace(userVisibleContent) == "" {
		userVisibleContent = loaded.RenderedBody
	}
	if !q.Raw {
		userVisibleContent = formatSkillOutputForDisplay(loaded)
	}
	if len(loaded.Warnings) > 0 {
		body := strings.TrimSpace(userVisibleContent)
		userVisibleContent = "Warnings:\n- " + strings.Join(loaded.Warnings, "\n- ")
		if body != "" {
			userVisibleContent = body + "\n\n" + userVisibleContent
		}
	}
	// Build display message (has PrettyPrint in Content for user-facing output).
	// The model-safe message for chat history omits Content so the model does not
	// learn the "Call: ..." text format, which causes hallucinations.
	assistantToolsCall := pub_models.Message{
		Role:      "assistant",
		Content:   call.PrettyPrint(),
		ToolCalls: []pub_models.Call{call},
	}
	modelSafeMsg := pub_models.Message{
		Role:      "assistant",
		ToolCalls: []pub_models.Call{call},
	}
	if !q.debug {
		if err := utils.AttemptPrettyPrint(q.out, assistantToolsCall, q.username, q.Raw); err != nil {
			return fmt.Errorf("pretty print assistant tool call: %w", err)
		}
	}
	session.Chat.Messages = append(session.Chat.Messages, modelSafeMsg)
	outMsg := pub_models.Message{Role: "tool", Content: content, ToolCallID: call.ID}
	session.Chat.Messages = append(session.Chat.Messages, outMsg)
	if !q.debug {
		printMsg := outMsg
		printMsg.Content = userVisibleContent
		if err := utils.AttemptPrettyPrint(q.out, printMsg, "tool", q.Raw); err != nil {
			return fmt.Errorf("pretty print skill output: %w", err)
		}
		summary := fmt.Sprintf("Loaded skill\n  Name: %s\n  Source: %s\nloaded skill %s [%s]", loaded.Name, loaded.SourceClass, loaded.Name, loaded.SourceClass)
		if desc := strings.TrimSpace(loaded.Description); desc != "" {
			summary += fmt.Sprintf("\n  Description: %s", desc)
		}
		content := strings.TrimSpace(loaded.RenderedBody)
		length := utf8.RuneCountInString(content)
		approxTokens := (length + 3) / 4
		summary += fmt.Sprintf("\n  Length: %d chars\n  Estimated tokens: ~%d", length, approxTokens)
		if strings.TrimSpace(loaded.RawArgs) != "" {
			summary += fmt.Sprintf("\n  Arguments: %q", loaded.RawArgs)
			summary += fmt.Sprintf("\nloaded skill %s [%s] args=%q", loaded.Name, loaded.SourceClass, loaded.RawArgs)
		}
		ancli.Noticef("%s", summary)
	}
	session.ResetPendingText()
	return nil
}

func formatSkillOutputForDisplay(loaded LoadedSkillRuntime) string {
	content := strings.TrimSpace(loaded.RenderedBody)
	length := utf8.RuneCountInString(content)
	approxTokens := (length + 3) / 4
	return fmt.Sprintf(
		"Name: %s\nDescription: %s\nLength: %d chars\nEstimated tokens: ~%d",
		loaded.Name,
		strings.TrimSpace(loaded.Description),
		length,
		approxTokens,
	)
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
		if err := utils.ClearTermTo(q.out, session.LineCount-1); err != nil {
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
	displayMsg := utils.PrepareDisplayMessage(pub_models.Message{
		Role:    "assistant",
		Content: pending,
	})
	if !q.Raw {
		if q.termWidth > 0 {
			utils.UpdateMessageTerminalMetadata(displayMsg.Content, &q.line, &q.lineCount, q.termWidth)
		} else {
			fmt.Fprintln(q.out)
		}
		utils.AttemptPrettyPrint(q.out, displayMsg, q.username, q.Raw)
	}
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
