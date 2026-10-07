package generic

import (
	"encoding/json"
	"fmt"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// wireMessage is the chat-completions shape of a message. It is separate from
// pub_models.Message, whose JSON form is the on-disk persistence format.
type wireMessage struct {
	Role             string         `json:"role"`
	ToolCalls        []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Content          any            `json:"content"`
}

type wireToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function wireFunction   `json:"function"`
	Extra    map[string]any `json:"extra_content,omitempty"`
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func toWireMessages(msgs []pub_models.Message) ([]wireMessage, error) {
	out := make([]wireMessage, len(msgs))
	for i, m := range msgs {
		wm := wireMessage{
			Role:             m.Role,
			ToolCallID:       m.ToolCallID,
			ReasoningContent: m.ReasoningContent,
		}
		if len(m.ContentParts) > 0 {
			wm.Content = m.ContentParts
		} else {
			wm.Content = m.Content
		}
		if len(m.ToolCalls) > 0 {
			wm.ToolCalls = make([]wireToolCall, len(m.ToolCalls))
			for j, c := range m.ToolCalls {
				wc, err := toWireToolCall(c)
				if err != nil {
					return nil, fmt.Errorf("message %d tool call %d: %w", i, j, err)
				}
				wm.ToolCalls[j] = wc
			}
		}
		out[i] = wm
	}
	return out, nil
}

func toWireToolCall(c pub_models.Call) (wireToolCall, error) {
	typ := c.Type
	if typ == "" {
		typ = "function"
	}
	name := c.Function.Name
	if name == "" {
		name = c.Name
	}
	if name == "" {
		name = "EMPTY-STRING"
	}
	args := c.Function.Arguments
	if args == "" {
		inputs := pub_models.Input{}
		if c.Inputs != nil {
			inputs = *c.Inputs
		}
		b, err := json.Marshal(inputs)
		if err != nil {
			return wireToolCall{}, fmt.Errorf("marshal arguments of %q: %w", name, err)
		}
		args = string(b)
	}
	return wireToolCall{
		ID:       c.ID,
		Type:     typ,
		Function: wireFunction{Name: name, Arguments: args},
		Extra:    c.ExtraContent,
	}, nil
}
