package models

import (
	"context"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type Querier interface {
	Query(ctx context.Context) error
}

type ChatQuerier interface {
	Querier
	TextQuery(context.Context, pub_models.Chat) (pub_models.Chat, error)
}

type StreamCompleter interface {
	// Setup the stream completer, do things like init http.Client/websocket etc
	// Will be called synchronously. Should return error if setup fails
	Setup() error

	// StreamCompletions and return a channel which sends CompletionsEvents.
	// The CompletionEvents should be a string, an error, NoopEvent or a models.Call. If there is
	// a catastrophic error, return the error and close the channel.
	StreamCompletions(context.Context, pub_models.Chat) (chan CompletionEvent, error)
}

// InputTokenCounter can return the amount of input tokens for a chat.
type InputTokenCounter interface {
	CountInputTokens(context.Context, pub_models.Chat) (int, error)
}

// UsageTokenCounter outputs the tokens that it has counterd
type UsageTokenCounter interface {
	TokenUsage() *pub_models.Usage
}

// ToolBox can register tools which later on will be added to the chat completion queries
type ToolBox interface {
	// RegisterTool registers a tool to the ToolBox
	RegisterTool(pub_models.LLMTool)
}

// CompletionEvent is the value type carried on a vendor's completions
// channel. It is deliberately left as any — changing it would be a breaking
// change across every producer loop for no gain, since the runner's existing
// %w wrap already preserves a typed error end to end.
//
// Channel contract (worklog 2026-09-05-error-propagation, D8):
//   - An error value on the channel is terminal: the runner ends the step and
//     returns it, unless it satisfies errors.Is(err, io.EOF) or
//     errors.Is(err, context.Canceled), which end the step normally.
//   - A producer that detects a provider error state mid-stream must send a
//     vocabulary error, never a NoopEvent.
//   - A producer that sends a terminal error must stop reading right after the
//     send, on every producer loop: the runner ends the step on every channel
//     error and never reads the channel again, so a producer that continued
//     would block on its next send (worklog 2026-09-05-error-propagation, D18).
//   - The runner wraps the terminal error with %w and must not flatten it, so
//     errors.Is/errors.As keep matching the vocabulary through the wrap.
type CompletionEvent any

type NoopEvent struct{}

// ReasoningEvent carries a single reasoning/thinking token from the model.
// It is handled like content but displayed dimmed so users can distinguish
// chain-of-thought from the final answer.
type ReasoningEvent struct {
	Content string
}

type StopEvent struct{}
