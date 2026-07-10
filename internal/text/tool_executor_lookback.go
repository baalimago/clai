package text

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func isLookbackTool(name string) bool {
	switch name {
	case string(pub_models.SearchConversationsTool),
		string(pub_models.InspectConversationTool),
		string(pub_models.ReadMessageTool):
		return true
	default:
		return false
	}
}

// executeLookbackTool dispatches the internally-handled lookback tools. Like
// load_skill it appends a model-safe assistant tool call plus the tool result to
// the chat, pretty-prints both, and bounds the output by the standard rune limit.
// Tool-level errors are returned as an "ERROR: ..." tool result so the run
// continues rather than aborting.
func (e toolExecutor[C]) executeLookbackTool(session *QuerySession, call pub_models.Call, emitAssistant bool) error {
	q := e.querier
	var out string
	if !q.useLookback {
		out = fmt.Sprintf("ERROR: %s requested but lookback is disabled for this run (enable with -lb/-lookback)", call.Name)
	} else if res, err := e.runLookbackTool(call); err != nil {
		out = "ERROR: " + err.Error()
	} else {
		out = res
	}

	if emitAssistant {
		if err := e.emitAssistantToolCall(session, call); err != nil {
			return err
		}
	}
	out, budgetErr := e.applyToolCallBudget(session, out)
	if budgetErr != nil {
		return budgetErr
	}
	return e.emitToolResult(session, call, out)
}

func (e toolExecutor[C]) runLookbackTool(call pub_models.Call) (string, error) {
	q := e.querier
	var inputs pub_models.Input
	if call.Inputs != nil {
		inputs = *call.Inputs
	}
	switch call.Name {
	case string(pub_models.SearchConversationsTool):
		directory := stringInput(inputs, "directory")
		if strings.TrimSpace(directory) == "" {
			directory = q.lookbackCWD
		}
		req := chat.SearchRequest{
			Query:     stringInput(inputs, "query"),
			Directory: directory,
			Subtree:   boolInput(inputs, "subtree", true),
			Page:      intInput(inputs, "page", 0),
			PageSize:  intInput(inputs, "page_size", 0),
		}
		res, err := chat.NewConversationSearcher(q.configDir).Search(req)
		if err != nil {
			return "", err
		}
		return chat.FormatSearchResult(res), nil
	case string(pub_models.InspectConversationTool):
		return chat.InspectConversation(
			q.configDir,
			stringInput(inputs, "chat_id"),
			intInput(inputs, "page", 0),
			intInput(inputs, "page_size", 0),
			stringInput(inputs, "role"),
			stringInput(inputs, "match"),
		)
	case string(pub_models.ReadMessageTool):
		content, path, err := chat.ReadMessage(
			q.configDir,
			stringInput(inputs, "chat_id"),
			intInput(inputs, "message_index", -1),
		)
		if err != nil {
			return "", err
		}
		if q.toolOutputRuneLimit > 0 && utf8.RuneCountInString(content) > q.toolOutputRuneLimit {
			return fmt.Sprintf(
				"message is %d runes, exceeding the %d-rune tool-output limit. Read it directly from the conversation file: %s",
				utf8.RuneCountInString(content), q.toolOutputRuneLimit, path,
			), nil
		}
		return content, nil
	default:
		return "", fmt.Errorf("unknown lookback tool %q", call.Name)
	}
}

func stringInput(in pub_models.Input, key string) string {
	if in == nil {
		return ""
	}
	if v, ok := in[key].(string); ok {
		return v
	}
	return ""
}

func boolInput(in pub_models.Input, key string, dfault bool) bool {
	if in == nil {
		return dfault
	}
	switch v := in[key].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return dfault
}

func intInput(in pub_models.Input, key string, dfault int) int {
	if in == nil {
		return dfault
	}
	switch v := in[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n
		}
	}
	return dfault
}
