package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Chat struct {
	Created  time.Time `json:"created"`
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail"`
	// RawB64 is here for the vendors who does not encode
	// their mimetype and data into the "URL" field
	RawB64   string `json:"-"`
	MIMEType string `json:"-"`
}

type ImageOrTextInputTypes string

const (
	Image ImageOrTextInputTypes = "image_url"
)

type ImageOrTextInput struct {
	Text string `json:"text,omitempty"`
	Type string `json:"type,omitempty"`
	// This is openai's billion dollar API design
	// The "image_url" here may be b64 string
	ImageB64 *ImageURL `json:"image_url,omitempty"`
}

type Message struct {
	Role       string `json:"role"`
	ToolCalls  []Call `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Content and ContentParts is like this since
	// making Mesage generic would cause changes in 70+ places.
	//
	// This way we can use ContentParts (ImageInput) without
	// updating all places
	Content      string             `json:"-"`
	ContentParts []ImageOrTextInput `json:"-"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	obj := map[string]any{
		"role": m.Role,
	}
	if len(m.ToolCalls) > 0 {
		obj["tool_calls"] = m.ToolCalls
	}
	if m.ToolCallID != "" {
		obj["tool_call_id"] = m.ToolCallID
	}
	if len(m.ContentParts) > 0 {
		obj["content"] = m.ContentParts
	} else if m.Content != "" {
		obj["content"] = m.Content
	}
	return json.Marshal(obj)
}

func (m *Message) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["role"]; ok {
		if err := json.Unmarshal(v, &m.Role); err != nil {
			return err
		}
	}
	if v, ok := raw["tool_calls"]; ok {
		if err := json.Unmarshal(v, &m.ToolCalls); err != nil {
			return err
		}
	}
	if v, ok := raw["tool_call_id"]; ok {
		if err := json.Unmarshal(v, &m.ToolCallID); err != nil {
			return err
		}
	}
	if v, ok := raw["content"]; ok {
		t := bytes.TrimSpace(v)
		if len(t) > 0 && t[0] == '[' {
			var arr []ImageOrTextInput
			if err := json.Unmarshal(v, &arr); err != nil {
				return err
			}
			m.ContentParts = arr
			m.Content = ""
		} else {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			m.Content = s
			m.ContentParts = nil
		}
	}
	return nil
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
