package models

import (
	"errors"
	"fmt"
	"time"
)

type Chat struct {
	Created  time.Time `json:"created,omitempty"`
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content,omitempty"`
	ToolCalls  []Call `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
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

func (c *Chat) LastOfRole(role string) (Message, int, error) {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		msg := c.Messages[i]
		if msg.Role == role {
			return msg, i, nil
		}
	}
	return Message{}, -1, fmt.Errorf("failed to find any %v message", role)
}
