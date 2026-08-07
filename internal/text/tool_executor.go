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
	"github.com/baalimago/go_away_boilerplate/pkg/table"
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
	return e.ExecuteBatch(ctx, session, []pub_models.Call{call})
}

// ExecuteBatch records all calls from one model turn before producing any tool
// outputs. Responses may emit several function calls in one response; preserving
// that grouping is required when the full stateless input is replayed.
//
// Every call is preflighted against the stoploss controller before any tool
// side effect runs (R3-02): allowed calls are invoked only after the whole
// batch has been decided, and one tool result is emitted per call in original
// order, including refusals that return io.EOF (R2-02).
func (e toolExecutor[C]) ExecuteBatch(ctx context.Context, session *QuerySession, calls []pub_models.Call) error {
	if len(calls) == 0 {
		return nil
	}
	q := e.querier
	planned := make([]pub_models.Call, 0, len(calls))
	for _, call := range calls {
		if q.debug || misc.Truthy(os.Getenv("DEBUG_CALL")) {
			ancli.PrintOK(fmt.Sprintf("received tool call: %v", debug.IndentedJsonFmt(call)))
		}
		decision := q.decideToolCall(session, call)
		if decision.TreatAsReturnToUser || decision.SkipExecution {
			session.FinalAssistantText = session.PendingTextString()
			session.ResetPendingText()
			continue
		}
		planned = append(planned, decision.PatchedCall)
	}
	if len(planned) == 0 {
		return nil
	}
	plans := q.newStoploss().PreflightToolCallBudget(session, planned)
	if err := e.finalizeAssistantTextBeforeToolCall(session, planned[0]); err != nil {
		return fmt.Errorf("finalize assistant text before tool call: %w", err)
	}
	// load_skill self-emits its assistant tool-call at execution time (the
	// trust prompt and the load error must precede the call echo, and a
	// failed load must not leave a dangling assistant call in the chat). To
	// keep immediate assistant→tool pairing in the model's emission order,
	// the batch is split into segments at each load_skill call: consecutive
	// non-skill calls keep the grouped emission, and each load_skill runs as
	// its own pair (R9-01). A hardStop plan must not end the batch early: the
	// assistant message already declared every call of the segment, so the
	// remaining plans must still emit their tool results before io.EOF is
	// returned, otherwise the persisted transcript carries a dangling
	// tool_call (R11-01). The io.EOF is therefore deferred until every plan
	// in the batch has emitted its result.
	pendingEOF := false
	for start := 0; start < len(plans); {
		if plans[start].call.Name == string(pub_models.LoadSkillTool) {
			if err := e.runPlannedCall(ctx, session, plans[start]); err != nil {
				return err
			}
			if plans[start].hardStop {
				pendingEOF = true
			}
			start++
			continue
		}
		end := start + 1
		for end < len(plans) && plans[end].call.Name != string(pub_models.LoadSkillTool) {
			end++
		}
		nonSkill := make([]pub_models.Call, 0, end-start)
		for _, plan := range plans[start:end] {
			nonSkill = append(nonSkill, plan.call)
		}
		if err := e.emitAssistantToolCalls(session, nonSkill); err != nil {
			return err
		}
		for _, plan := range plans[start:end] {
			if err := e.runPlannedCall(ctx, session, plan); err != nil {
				return err
			}
			if plan.hardStop {
				pendingEOF = true
			}
		}
		start = end
	}
	if pendingEOF {
		return io.EOF
	}
	return nil
}

// runPlannedCall executes one preflighted tool call and emits its tool result.
// Refused calls never reach their implementation: the ladder text is emitted
// as the tool result instead (R2-01). A refused load_skill still emits its
// assistant tool-call so the exchange stays valid (R2-02).
func (e toolExecutor[C]) runPlannedCall(ctx context.Context, session *QuerySession, plan toolCallBudgetPlan) error {
	if !plan.allowed {
		if plan.call.Name == string(pub_models.LoadSkillTool) {
			if err := e.emitAssistantToolCall(session, plan.call); err != nil {
				return err
			}
		}
		return e.emitToolResult(session, plan.call, plan.out)
	}
	if plan.call.Name == string(pub_models.LoadSkillTool) {
		return e.executeLoadSkill(ctx, session, plan.call)
	}
	q := e.querier
	out := ""
	if isLookbackTool(plan.call.Name) {
		if !q.useLookback {
			out = fmt.Sprintf("ERROR: %s requested but lookback is disabled for this run (enable with -lb/-lookback)", plan.call.Name)
		} else if res, err := e.runLookbackTool(plan.call); err != nil {
			out = "ERROR: " + err.Error()
		} else {
			out = res
		}
	} else {
		out = tools.Invoke(ctx, plan.call)
	}
	return e.emitToolResult(session, plan.call, plan.prefix+out)
}

// emitAssistantToolCall prints the user-facing assistant tool-call turn and
// appends the model-safe version to the chat history. The model-safe message
// omits the PrettyPrint content so the model does not learn the "Call: ..."
// text format, which causes hallucinations.
func (e toolExecutor[C]) emitAssistantToolCall(session *QuerySession, call pub_models.Call) error {
	return e.emitAssistantToolCalls(session, []pub_models.Call{call})
}

func (e toolExecutor[C]) emitAssistantToolCalls(session *QuerySession, calls []pub_models.Call) error {
	q := e.querier
	if len(calls) == 0 {
		return nil
	}
	modelSafe := pub_models.Message{
		Role:      "assistant",
		ToolCalls: calls,
	}
	for _, call := range calls {
		if modelSafe.ReasoningContent == "" {
			modelSafe.ReasoningContent = call.ReasoningContent
		}
		if len(modelSafe.ReasoningItems) == 0 && len(call.ReasoningItems) > 0 {
			// Carry sealed reasoning continuity from the normalized call onto the
			// assistant turn. Empty for other vendors.
			modelSafe.ReasoningItems = call.ReasoningItems
		}
		if !q.debug && !q.structuredOutput {
			if q.rawDisplay() {
				display := pub_models.Message{Role: "assistant", Content: call.PrettyPrint(), ToolCalls: []pub_models.Call{call}, ReasoningContent: call.ReasoningContent}
				if err := utils.AttemptPrettyPrint(q.out, display, q.username, q.rawDisplay()); err != nil {
					return fmt.Errorf("pretty print raw assistant tool call: %w", err)
				}
			}
		}
	}
	session.Chat.Messages = append(session.Chat.Messages, modelSafe)
	return nil
}

// emitToolResult bounds the output, appends it as the tool-result turn, prints
// it, and resets pending text — the shared tail of every tool-call exchange.
func (e toolExecutor[C]) emitToolResult(session *QuerySession, call pub_models.Call, out string) error {
	q := e.querier
	displayOut := out
	out = limitToolOutput(out, q.toolOutputRuneLimit)
	if out == "" {
		out = fmt.Sprintf("<NO-OUTPUT> tool %s completed successfully but produced no stdout/stderr.", call.Name)
		displayOut = out
	}
	outMsg := pub_models.Message{
		Role:       "tool",
		Content:    out,
		ToolCallID: call.ID,
	}
	session.Chat.Messages = append(session.Chat.Messages, outMsg)
	if !q.structuredOutput {
		if q.rawDisplay() {
			printMsg := outMsg
			printMsg.Content = displayOut
			if err := utils.AttemptPrettyPrint(q.out, printMsg, "tool", q.rawDisplay()); err != nil {
				return fmt.Errorf("pretty print raw tool output: %w", err)
			}
		} else if !q.debug {
			if err := q.appendToolActivity(call, outMsg.Content); err != nil {
				return fmt.Errorf("print tool output: %w", err)
			}
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
		if err := e.emitAssistantToolCall(session, call); err != nil {
			return err
		}
		return e.emitToolResult(session, call, "ERROR: "+loaded.ActivationErr)
	}
	if len(loaded.ActiveTools) > 0 {
		q.baseTools = loaded.ActiveTools
	}
	if len(loaded.EnabledTools) > 0 {
		toolBox, ok := any(q.Model).(models.ToolBox)
		if !ok {
			return fmt.Errorf("trusted skill enabled tools but the model has no tool box")
		}
		for _, name := range loaded.EnabledTools {
			tool, exists := loaded.ActiveTools[name]
			if !exists {
				continue
			}
			if q.registeredTools == nil {
				q.registeredTools = map[string]struct{}{}
			}
			if _, registered := q.registeredTools[name]; registered {
				continue
			}
			toolBox.RegisterTool(tool)
			q.registeredTools[name] = struct{}{}
		}
		loaded.Warnings = append(loaded.Warnings, "skill enabled local tools: "+strings.Join(loaded.EnabledTools, ", "))
	}
	content := loaded.RenderedBody
	userVisibleContent := loaded.UserVisibleBody
	if strings.TrimSpace(userVisibleContent) == "" {
		userVisibleContent = loaded.RenderedBody
	}
	if !q.rawDisplay() {
		userVisibleContent = formatSkillOutputForDisplay(loaded)
	}
	if len(loaded.Warnings) > 0 {
		body := strings.TrimSpace(userVisibleContent)
		userVisibleContent = "Warnings:\n- " + strings.Join(loaded.Warnings, "\n- ")
		if body != "" {
			userVisibleContent = body + "\n\n" + userVisibleContent
		}
	}
	if err := e.emitAssistantToolCall(session, call); err != nil {
		return err
	}
	// Skill bodies are persisted and displayed in full (no output limit, no
	// display shortening): the loaded skill IS the instruction set.
	outMsg := pub_models.Message{Role: "tool", Content: content, ToolCallID: call.ID}
	session.Chat.Messages = append(session.Chat.Messages, outMsg)
	if !q.debug && !q.structuredOutput {
		printMsg := outMsg
		printMsg.Content = userVisibleContent
		if q.rawDisplay() {
			if err := utils.AttemptPrettyPrint(q.out, printMsg, "tool", q.rawDisplay()); err != nil {
				return fmt.Errorf("pretty print raw skill output: %w", err)
			}
		} else if err := q.appendToolActivity(call, printMsg.Content); err != nil {
			return fmt.Errorf("print skill output: %w", err)
		}
	}
	session.ResetPendingText()
	return nil
}

func (q *Querier[C]) appendToolActivity(call pub_models.Call, content string) error {
	if !utils.RollingOutputEnabled() {
		if err := utils.PrintToolActivity(q.out, call, content, q.dims.Width, utils.ToolOutputRows()); err != nil {
			return fmt.Errorf("print tool activity: %w", err)
		}
		return nil
	}
	q.ensureActivityViewport().AppendTool(call, utils.SummarizeAsyncToolResult(call.Name, content), utils.ToolOutputRows())
	if err := q.activityViewport.Render(q.out); err != nil {
		return fmt.Errorf("render activity viewport: %w", err)
	}
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
	if q.usesActivityViewport() {
		return e.finalizeAssistantTextRolling(session, pending, call)
	}
	return e.finalizeAssistantTextPlain(session, pending, call)
}

// finalizeAssistantTextPlain finalizes assistant prose for non-rolling
// display: the streamed text is cleared and re-printed as one proper message
// above the upcoming tool activity. Echoed tool-call text is dropped.
func (e toolExecutor[C]) finalizeAssistantTextPlain(session *QuerySession, pending string, call pub_models.Call) error {
	q := e.querier
	if !q.rawDisplay() && !q.structuredOutput && q.dims.Width > 0 {
		utils.UpdateMessageTerminalMetadata(pending, &session.Line, &session.LineCount, q.dims.Width)
		rowsToClear := session.LineCount - 1
		if q.activityViewport != nil {
			rowsToClear += q.activityViewport.DetachRenderedRegion()
		}
		if err := table.ClearTermTo(q.out, rowsToClear); err != nil {
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
	if !q.rawDisplay() && !q.structuredOutput {
		if q.dims.Width > 0 {
			utils.UpdateMessageTerminalMetadata(displayMsg.Content, &q.line, &q.lineCount, q.dims.Width)
		} else {
			fmt.Fprintln(q.out)
		}
		utils.AttemptPrettyPrint(q.out, displayMsg, q.username, q.rawDisplay())
	}
	session.Line = q.line
	session.LineCount = q.lineCount
	return nil
}

// finalizeAssistantTextRolling finalizes assistant prose while the rolling
// window is active. The prose already lives inside the window (or is moved
// into it), so nothing is re-printed outside the window. Echoed tool-call text
// is dropped from the window and the transcript.
func (e toolExecutor[C]) finalizeAssistantTextRolling(session *QuerySession, pending string, call pub_models.Call) error {
	q := e.querier
	q.ensureActivityViewport()
	if isEchoedToolCallText(pending, call) {
		// The model echoed its own tool call as prose. Remove it from the
		// window when it was streamed there, or clear the direct print when
		// the window did not exist yet.
		if q.activityViewport.RemoveTextBlock() > 0 {
			if err := q.activityViewport.Render(q.out); err != nil {
				return fmt.Errorf("render activity viewport after dropping echoed tool call: %w", err)
			}
		} else if q.dims.Width > 0 {
			utils.UpdateMessageTerminalMetadata(pending, &session.Line, &session.LineCount, q.dims.Width)
			if err := table.ClearTermTo(q.out, session.LineCount-1); err != nil {
				return fmt.Errorf("clear echoed tool call text: %w", err)
			}
		}
		session.ResetPendingText()
		q.fullMsg = ""
		q.line = ""
		q.lineCount = 0
		return nil
	}
	if !q.activityViewport.TextBlockActive() {
		// The prose was streamed before any activity created the window. Move
		// it inside the window instead of leaving it outside it.
		if q.dims.Width > 0 {
			utils.UpdateMessageTerminalMetadata(pending, &session.Line, &session.LineCount, q.dims.Width)
			if err := table.ClearTermTo(q.out, session.LineCount-1); err != nil {
				return fmt.Errorf("clear streamed assistant text before tool call: %w", err)
			}
		}
		q.activityViewport.AppendText(pending)
		if err := q.activityViewport.Render(q.out); err != nil {
			return fmt.Errorf("render activity viewport: %w", err)
		}
	}
	session.ResetPendingText()
	session.FinalAssistantText = pending
	q.fullMsg = pending
	q.line = ""
	q.lineCount = 0
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
