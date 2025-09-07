package models

import (
	"context"
	"fmt"
	"time"

	"github.com/baalimago/clai/internal/tools"

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

// ToolBox can register tools which later on will be added to the chat completion queries
type ToolBox interface {
	// RegisterTool registers a tool to the ToolBox
	RegisterTool(tools.LLMTool)
}

type CompletionEvent any

type NoopEvent struct{}

type ErrRateLimit struct {
	ResetAt         time.Time
	TokensRemaining int
	MaxInputTokens  int
}

func (erl *ErrRateLimit) Error() string {
	return fmt.Sprintf("reset at: '%v', input tokens used at time of rate limit: '%v'", erl.ResetAt, erl.TokensRemaining)
}

func NewRateLimitError(resetAt time.Time, maxInputTokens int, tokensRemaining int) error {
	return &ErrRateLimit{
		ResetAt:         resetAt,
		MaxInputTokens:  maxInputTokens,
		TokensRemaining: tokensRemaining,
	}
}
