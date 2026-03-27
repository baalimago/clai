package text

import (
	"context"
	"fmt"
	"os"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func limitToolOutput(out string, limit int) string {
	if limit <= 0 {
		return out
	}
	totalRunes := utf8.RuneCountInString(out)
	if totalRunes <= limit {
		return out
	}
	f, err := os.CreateTemp("", "clai-tool-output-*.txt")
	if err != nil {
		return fmt.Sprintf("tool output too large (%d runes); failed to save full output to a temporary file: %v", totalRunes, err)
	}
	defer func() {
		_ = f.Close()
	}()
	if _, err := f.WriteString(out); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Sprintf("tool output too large (%d runes); failed to write full output to temporary file %q: %v", totalRunes, f.Name(), err)
	}
	if err := f.Sync(); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Sprintf("tool output too large (%d runes); failed to sync temporary file %q: %v", totalRunes, f.Name(), err)
	}
	return fmt.Sprintf(
		"[tool output too large: %d runes; full output saved to temp file: %s]",
		totalRunes,
		f.Name(),
	)
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
