package models

import (
	"context"
	"errors"
	"time"

	"github.com/baalimago/clai/internal/tools"
)

type Querier interface {
	Query(ctx context.Context) error
}

type ChatQuerier interface {
	Querier
	TextQuery(context.Context, Chat) (Chat, error)
}

type StreamCompleter interface {
	// Setup the stream completer, do things like init http.Client/websocket etc
	// Will be called synchronously. Should return error if setup fails
	Setup() error

	// StreamCompletions and return a channel which sends CompletionsEvents.
	// The CompletionEvents should be a string, an error, NoopEvent or a models.Call. If there is
	// a catastrophic error, return the error and close the channel.
	StreamCompletions(context.Context, Chat) (chan CompletionEvent, error)
}

// ToolBox can register tools which later on will be added to the chat completion queries
type ToolBox interface {
	// RegisterTool registers a tool to the ToolBox
	RegisterTool(tools.LLMTool)
}

type CompletionEvent any

type NoopEvent struct{}

type Message struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []tools.Call `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type Chat struct {
	Created  time.Time `json:"created,omitempty"`
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

// FirstSystemMessage returns the first encountered Message with role 'system'
func (c *Chat) FirstSystemMessage() (Message, error) {
	for _, msg := range c.Messages {
		if msg.Role == "system" {
			return msg, nil
		}
	}
	return Message{}, errors.New("failed to find any system message")
}

func (c *Chat) FirstUserMessage() (Message, error) {
	for _, msg := range c.Messages {
		if msg.Role == "user" {
			return msg, nil
		}
	}
	return Message{}, errors.New("failed to find any user message")
}
