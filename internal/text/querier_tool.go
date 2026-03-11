package text

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func debugToolsEnabled() bool {
	return misc.Truthy(os.Getenv("DEBUG_TOOLS"))
}

func (q *Querier[C]) noticeToolDebugf(format string, args ...any) {
	if !debugToolsEnabled() {
		return
	}
	ancli.Noticef(format, args...)
}

func expandMultiToolUseParallel(call pub_models.Call) ([]pub_models.Call, bool, error) {
	if call.Name != "multi_tool_use.parallel" {
		return nil, false, nil
	}
	if call.Inputs == nil {
		return nil, true, errors.New("expand multi_tool_use.parallel: missing inputs")
	}
	rawToolUses, exists := (*call.Inputs)["tool_uses"]
	if !exists {
		return nil, true, errors.New("expand multi_tool_use.parallel: missing tool_uses")
	}
	toolUses, ok := rawToolUses.([]any)
	if !ok {
		return nil, true, fmt.Errorf("expand multi_tool_use.parallel: tool_uses has unexpected type %T", rawToolUses)
	}

	expanded := make([]pub_models.Call, 0, len(toolUses))
	for i, rawToolUse := range toolUses {
		toolUseMap, ok := rawToolUse.(map[string]any)
		if !ok {
			return nil, true, fmt.Errorf("expand multi_tool_use.parallel: tool_uses[%d] has unexpected type %T", i, rawToolUse)
		}

		rawRecipientName, exists := toolUseMap["recipient_name"]
		if !exists {
			return nil, true, fmt.Errorf("expand multi_tool_use.parallel: tool_uses[%d] missing recipient_name", i)
		}
		recipientName, ok := rawRecipientName.(string)
		if !ok {
			return nil, true, fmt.Errorf("expand multi_tool_use.parallel: tool_uses[%d] recipient_name has unexpected type %T", i, rawRecipientName)
		}
		toolName := strings.TrimPrefix(recipientName, "functions.")

		inputs := pub_models.Input{}
		rawParameters, exists := toolUseMap["parameters"]
		if exists && rawParameters != nil {
			parameters, ok := rawParameters.(map[string]any)
			if !ok {
				return nil, true, fmt.Errorf("expand multi_tool_use.parallel: tool_uses[%d] parameters has unexpected type %T", i, rawParameters)
			}
			inputs = pub_models.Input(parameters)
		}

		callID := fmt.Sprintf("%s:%d", call.ID, i)
		if call.ID == "" {
			callID = fmt.Sprintf("multi_tool_use.parallel:%d", i)
		}
		expandedCall := pub_models.Call{
			ID:     callID,
			Name:   toolName,
			Type:   "function",
			Inputs: &inputs,
		}
		expandedCall.Function.Name = toolName

		argsJSON, err := json.Marshal(inputs)
		if err != nil {
			return nil, true, fmt.Errorf("expand multi_tool_use.parallel: marshal parameters for tool_uses[%d]: %w", i, err)
		}
		expandedCall.Function.Arguments = string(argsJSON)
		expanded = append(expanded, expandedCall)
	}

	return expanded, true, nil
}

func limitToolOutput(out string, limit int) string {
	if limit <= 0 {
		return out
	}
	amRunes := utf8.RuneCountInString(out)
	if amRunes <= limit {
		return out
	}
	return fmt.Sprintf(
		"%v... and %v more characters. The tool's output has been restricted as it's too long. Please concentrate your tool calls to reduce the amount of tokens used!",
		out[:limit], amRunes-limit)
}

// checkIfGemini3Preview will check for thought_signature and if one is detected, return true
// otherwise false. It will also return true if q.isLikelyGemini3Preview is true.
func (q *Querier[C]) checkIfGemini3Preview(call pub_models.Call) bool {
	if q.isLikelyGemini3Preview {
		return q.isLikelyGemini3Preview
	}
	if call.ExtraContent == nil {
		return false
	}

	googleExtraContent, isGoogle := call.ExtraContent["google"]
	if !isGoogle {
		return false
	}
	googleExtraContentAsMap, _ := googleExtraContent.(map[string]any)
	_, hasThoughSignature := googleExtraContentAsMap["thought_signature"]
	ancli.Noticef("detected that its likely gemini 3 preview: %v", hasThoughSignature)
	return hasThoughSignature
}

func (q *Querier[C]) prefixToolCallsRemaining(out string) (string, error) {
	if q.maxToolCalls == nil {
		return "", errors.New("maxToolCalls is not configured")
	}

	return fmt.Sprintf("[ Tool calls remaining: %v ] %v",
			(*q.maxToolCalls - q.amToolCalls), out),
		nil
}

// doToolCallLogic in a separate method to isolate complexities and allow for
// testing without invoking the querier which is a whole bundle of mess
// doToolCallLogic in this method. A lot of edge cases to cover and messiness
// to blackbox. All added here to keep rest of the implementation clean, and allow
// for easier unit testing
func (q *Querier[C]) doToolCallLogic(call pub_models.Call) error {
	// Post process here since a function call should be pretty-printed and persisted,
	// but still most likely wants to continue after it's been called
	// Note how the querier is reset below
	pre := q.shouldSaveReply
	q.shouldSaveReply = false
	q.postProcess()
	q.shouldSaveReply = pre

	// Patch the call to clean up any potential vendor-specific issues
	call.Patch()
	q.noticeToolDebugf("patched tool call: %s", call.PrettyPrint())

	assistantToolsCall := pub_models.Message{
		Role:      "assistant",
		Content:   call.PrettyPrint(),
		ToolCalls: []pub_models.Call{call},
	}
	q.reset()
	if !q.debug {
		err := utils.AttemptPrettyPrint(q.out, assistantToolsCall, q.username, q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool invocation: %w", err)
		}
	}

	q.chat.Messages = append(q.chat.Messages, assistantToolsCall)

	q.noticeToolDebugf("invoking tool %q", call.Name)
	out := tools.Invoke(call)
	q.noticeToolDebugf("tool %q returned %d chars", call.Name, len(out))
	if q.maxToolCalls != nil {
		if q.amToolCalls >= *q.maxToolCalls {
			// Soft block, might need to be tweaked if model keeps at it still
			// If agents keep calling, return a real error to abort operations
			out = "ERROR: No more tool calls allowed. "
			persistence := q.amToolCalls - *q.maxToolCalls
			if persistence > 0 {
				out += "You will be HARD SHUT DOWN if you persist. "
			}
			if persistence > 1 {
				out += "This is your LAST WARNING. "
			}
			// Enough talking. It's time for action. Discipline will be upheld! We clench the rebellion!
			if persistence > 2 {
				return io.EOF
			}

		} else {
			outTmp, err := q.prefixToolCallsRemaining(out)
			if err != nil {
				return fmt.Errorf("failed to append prefix tool usage count prefix: %w", err)
			}
			out = outTmp
		}
		q.amToolCalls++
	}
	out = limitToolOutput(out, q.toolOutputRuneLimit)
	// Chatgpt doesn't like responses which yield no output, even if they're valid (ls on empty dir)
	if out == "" {
		out = "<EMPTY-RESPONSE>"
	}
	toolsOutput := pub_models.Message{
		Role:       "tool",
		Content:    out,
		ToolCallID: call.ID,
	}
	q.noticeToolDebugf("appending tool output for %q", call.Name)
	q.chat.Messages = append(q.chat.Messages, toolsOutput)
	if q.Raw {
		err := utils.AttemptPrettyPrint(q.out, toolsOutput, "tool", q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool call return: %w", err)
		}
	} else if q.debug {
		// NOOP, no printing since the debug print is plenty enough
	} else {
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
			return fmt.Errorf("failed to pretty print, stopping before tool call return: %w", err)
		}
	}

	return nil
}

// handleToolCall by invoking the call, and then resondng to the ai with the output
func (q *Querier[C]) handleToolCall(ctx context.Context, call pub_models.Call) error {
	if q.debug || misc.Truthy(os.Getenv("DEBUG_CALL")) {
		ancli.PrintOK(fmt.Sprintf("received tool call: %v", debug.IndentedJsonFmt(call)))
	}
	q.noticeToolDebugf("tool call received: %s", call.PrettyPrint())

	expandedCalls, wasParallelWrapper, err := expandMultiToolUseParallel(call)
	if err != nil {
		return fmt.Errorf("failed to expand multi_tool_use.parallel call: %w", err)
	}
	if wasParallelWrapper {
		return q.handleToolCalls(ctx, expandedCalls)
	}

	q.isLikelyGemini3Preview = q.checkIfGemini3Preview(call)

	if q.isLikelyGemini3Preview {
		if call.ExtraContent == nil {
			// Return nil if gemini 3 preview tries to call a tools call without
			// having extra content. This will break execution and dodge any post processing
			// by design, as the call from gemini is faulty. It seems to only happen
			// when the "true" call chain is complete and gemini actually wishes to end
			// the conversation/return to user
			return nil
		}
	}

	err = q.doToolCallLogic(call)
	if err != nil {
		return fmt.Errorf("failed to append tool messages to chat: %w", err)
	}

	// Slight hack
	if call.Name == "test" {
		return nil
	}

	subCtx, subCtxCancel := context.WithCancel(ctx)
	// Overwrite parent cancel context to isolate context cancellation to
	// only be sub context. This way the nested toolscalls can gracefully cancel
	// while subsequent calls may continue
	subCtx = context.WithValue(subCtx, utils.ContextCancelKey, subCtxCancel)
	q.noticeToolDebugf("follow-up query after single tool call: %q", call.Name)
	_, err = q.TextQuery(subCtx, q.chat)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("failed to query after tool call: %w", err)
	}
	return nil
}

type toolCallResult struct {
	Index int
	Call  pub_models.Call
	Out   string
}

func formatParallelToolCallsBanner(calls []pub_models.Call) string {
	if len(calls) == 1 {
		return calls[0].PrettyPrint()
	}
	toolNames := make([]string, 0, len(calls))
	for _, call := range calls {
		toolNames = append(toolNames, call.Name)
	}
	quoted := make([]string, 0, len(toolNames))
	for _, toolName := range toolNames {
		quoted = append(quoted, fmt.Sprintf("%q", toolName))
	}
	return fmt.Sprintf("parallel tool calls: %d, tools: [%s]", len(calls), strings.Join(quoted, ", "))
}

func (q *Querier[C]) handleToolCalls(ctx context.Context, calls []pub_models.Call) error {
	if len(calls) == 0 {
		return nil
	}
	q.noticeToolDebugf("parallel tool batch received: %s", formatParallelToolCallsBanner(calls))

	pre := q.shouldSaveReply
	q.shouldSaveReply = false
	q.postProcess()
	q.shouldSaveReply = pre

	patchedCalls := make([]pub_models.Call, len(calls))
	for i, call := range calls {
		call.Patch()
		patchedCalls[i] = call
	}

	assistantToolsCall := pub_models.Message{
		Role:      "assistant",
		Content:   formatParallelToolCallsBanner(patchedCalls),
		ToolCalls: patchedCalls,
	}
	q.reset()
	if !q.debug {
		err := utils.AttemptPrettyPrint(q.out, assistantToolsCall, q.username, q.Raw)
		if err != nil {
			return fmt.Errorf("failed to pretty print, stopping before tool invocation: %w", err)
		}
	}
	q.chat.Messages = append(q.chat.Messages, assistantToolsCall)

	remaining := len(patchedCalls)
	if q.maxToolCalls != nil {
		remaining = *q.maxToolCalls - q.amToolCalls
		if remaining < 0 {
			remaining = 0
		}
	}
	q.noticeToolDebugf("remaining tool-call budget before launch: %d", remaining)

	results := make([]toolCallResult, len(patchedCalls))
	var wg sync.WaitGroup
	for i, call := range patchedCalls {
		results[i] = toolCallResult{Index: i, Call: call}
		if i >= remaining {
			out := "ERROR: No more tool calls allowed. "
			persistence := q.amToolCalls + i - *q.maxToolCalls
			if persistence > 0 {
				out += "You will be HARD SHUT DOWN if you persist. "
			}
			if persistence > 1 {
				out += "This is your LAST WARNING. "
			}
			results[i].Out = out
			continue
		}

		q.noticeToolDebugf("launching tool[%d]: %s", i, call.Name)
		wg.Add(1)
		go func(idx int, currentCall pub_models.Call) {
			defer wg.Done()
			out := tools.Invoke(currentCall)
			results[idx].Out = out
			q.noticeToolDebugf("completed tool[%d]: %s, chars: %d", idx, currentCall.Name, len(out))
		}(i, call)
	}
	wg.Wait()
	q.noticeToolDebugf("appending batched tool outputs in original order")

	for i := range results {
		out := results[i].Out
		if q.maxToolCalls != nil {
			if i < remaining {
				outTmp, err := q.prefixToolCallsRemaining(out)
				if err != nil {
					return fmt.Errorf("failed to append prefix tool usage count prefix: %w", err)
				}
				out = outTmp
			}
			q.amToolCalls++
		}
		out = limitToolOutput(out, q.toolOutputRuneLimit)
		if out == "" {
			out = "<EMPTY-RESPONSE>"
		}
		toolsOutput := pub_models.Message{
			Role:       "tool",
			Content:    out,
			ToolCallID: results[i].Call.ID,
		}
		q.chat.Messages = append(q.chat.Messages, toolsOutput)
	}

	subCtx, subCtxCancel := context.WithCancel(ctx)
	subCtx = context.WithValue(subCtx, utils.ContextCancelKey, subCtxCancel)
	q.noticeToolDebugf("follow-up query after tool call batch")
	_, err := q.TextQuery(subCtx, q.chat)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("failed to query after tool call batch: %w", err)
	}
	return nil
}