// Package text exposes a high-level, public API for interacting with
// large language models (LLMs) using chat-style conversations.
//
// The package is intentionally small and focused. It wraps the internal
// clai text engine and re-exports only the pieces that are expected to
// be stable for external consumers.
//
// Typical usage is to construct a FullResponse querier and issue a chat
// style request:
//
//	ctx := context.Background()
//	q := text.NewFullResponseQuerier()
//
//	chat := models.Chat{ /* populate chat with messages */ }
//	reply, err := q.Query(ctx, chat)
//	if err != nil {
//	    // handle error
//	}
//	_ = reply
//
// The concrete configuration of the underlying engine (model name,
// system prompt, config directory, tool usage, etc.) is managed inside
// this package and can evolve without breaking callers. Advanced
// integrations can inject additional LLM tools or MCP servers via
// functional options when constructing a FullResponse instance.
package text
