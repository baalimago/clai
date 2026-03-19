package text

import (
	"context"
	"fmt"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
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
	return hasThoughSignature
}

func (q *Querier[C]) doToolCallLogic(call pub_models.Call) error {
	session := &QuerySession{
		Chat:            q.chat,
		ShouldSaveReply: q.shouldSaveReply,
		Raw:             q.Raw,
		ToolCallsUsed:   q.amToolCalls,
	}
	err := toolExecutor[C]{querier: q}.Execute(context.Background(), session, call)
	q.chat = session.Chat
	q.amToolCalls = session.ToolCallsUsed
	return err
}
