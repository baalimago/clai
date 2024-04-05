package models

import (
	"context"
	"errors"
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
	// Will be called asynchronously. Should return error if setup fails
	Setup() error

	// StreamCompletions and return a channel which sends CompletionsEvents.
	// The CompletionEvents should either be a string or an error. If there is
	// a catastrophic error, return the error and close the channel.
	StreamCompletions(context.Context, Chat) (chan CompletionEvent, error)
}

type CompletionEvent any

type Chat struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SystemMessage returns the first encountered Message with role 'system'
func (c *Chat) SystemMessage() (Message, error) {
	for _, msg := range c.Messages {
		if msg.Role == "system" {
			return msg, nil
		}
	}
	return Message{}, errors.New("failed to find any system message")
}
