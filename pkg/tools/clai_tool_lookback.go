package tools

import (
	"errors"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// The lookback tools are no-op markers: their Specification is advertised to the
// model, but execution is dispatched internally by the tool executor (which holds
// the resolving config dir and session CWD), exactly like load_skill.

var (
	SearchConversations = &searchConversationsTool{}
	InspectConversation = &inspectConversationTool{}
	ReadMessage         = &readMessageTool{}
)

type searchConversationsTool struct{}

func (t *searchConversationsTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        string(pub_models.SearchConversationsTool),
		Description: "Search this directory's past conversations by keyword. All tokens must match (AND); wrap a phrase in double quotes to match it contiguously. Anchored to a directory (defaults to the session working directory) and subtree-inclusive by default. Results are ranked with a transparent hit count and carry a real snippet so you can judge fit yourself.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"query"},
			Properties: map[string]pub_models.ParameterObject{
				"query":     {Type: "string", Description: "AND keywords, plus \"quoted phrases\" matched as contiguous substrings."},
				"directory": {Type: "string", Description: "Canonical path to anchor the search. Defaults to the session working directory; pass another path to investigate a different codebase."},
				"subtree":   {Type: "boolean", Description: "Match origin_dir at directory AND nested beneath it (default true). Set false to restrict to an exact directory match."},
				"page":      {Type: "integer", Description: "0-based page index (default 0)."},
				"page_size": {Type: "integer", Description: "Rows per page (default 10, capped)."},
			},
		},
	}
}

func (t *searchConversationsTool) Call(pub_models.Input) (string, error) {
	return "", errors.New("search_conversations is handled internally by clai")
}

type inspectConversationTool struct{}

func (t *inspectConversationTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        string(pub_models.InspectConversationTool),
		Description: "List a conversation's messages as a paginated per-message metadata outline (index, role, length, preview) without dumping bodies. Indices are storage-true and map 1:1 to read_message. Filter by role or a content substring.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"chat_id"},
			Properties: map[string]pub_models.ParameterObject{
				"chat_id":   {Type: "string", Description: "The conversation to inspect (as surfaced by search_conversations or the descriptor)."},
				"page":      {Type: "integer", Description: "0-based page index (default 0)."},
				"page_size": {Type: "integer", Description: "Messages per page (default 20, capped)."},
				"role":      {Type: "string", Description: "Restrict to a single role: user, assistant, tool, or system."},
				"match":     {Type: "string", Description: "List only messages whose content contains this substring."},
			},
		},
	}
}

func (t *inspectConversationTool) Call(pub_models.Input) (string, error) {
	return "", errors.New("inspect_conversation is handled internally by clai")
}

type readMessageTool struct{}

func (t *readMessageTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        string(pub_models.ReadMessageTool),
		Description: "Read the full content of a single message by its index from inspect_conversation. The output is role-tagged and truncated only by the standard tool-output limit (the conversation's on-disk path is surfaced on truncation).",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"chat_id", "message_index"},
			Properties: map[string]pub_models.ParameterObject{
				"chat_id":       {Type: "string", Description: "The conversation to read from."},
				"message_index": {Type: "integer", Description: "The index from inspect_conversation."},
			},
		},
	}
}

func (t *readMessageTool) Call(pub_models.Input) (string, error) {
	return "", errors.New("read_message is handled internally by clai")
}
