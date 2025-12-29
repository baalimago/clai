package text

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

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

	out := tools.Invoke(call)
	if q.maxToolCalls != nil {
		if q.amToolCalls >= *q.maxToolCalls {
			// Soft block, might need to be tweaked if model keeps at it still
			// If agents keep calling, return a real error to abort operations
			out = "ERROR: No more tool calls allowed"
		} else {
			outTmp, err := q.prefixToolCallsRemaining(out)
			if err != nil {
				return fmt.Errorf("failed to append prefix tool usage count prefix: %w", err)
			}
			out = outTmp

			q.amToolCalls++
		}
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
	if q.cmdMode {
		return errors.New("cant call tools in cmd mode")
	}

	if q.debug || misc.Truthy(os.Getenv("DEBUG_CALL")) {
		ancli.PrintOK(fmt.Sprintf("received tool call: %v", debug.IndentedJsonFmt(call)))
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

	err := q.doToolCallLogic(call)
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
	_, err = q.TextQuery(subCtx, q.chat)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("failed to query after tool call: %w", err)
	}
	return nil
}
