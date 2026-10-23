package generic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/baalimago/clai/internal/models"
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

type wireTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wireImagePart struct {
	Type     string       `json:"type"`
	ImageURL wireImageURL `json:"image_url"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

// ImageDataURL encodes an image as the base64 data URL that chat-completions
// style routes carry it in.
func ImageDataURL(p pub_models.ImagePart) string {
	return "data:" + p.MIME + ";base64," + base64.StdEncoding.EncodeToString(p.Data)
}

// toWireMessages expects msgs to have passed models.CheckImageParts against caps.
func toWireMessages(msgs []pub_models.Message, caps models.ImageCapabilities) ([]wireMessage, error) {
	out := make([]wireMessage, len(msgs))
	for i, m := range msgs {
		wm := wireMessage{
			Role:             m.Role,
			ToolCallID:       m.ToolCallID,
			ReasoningContent: m.ReasoningContent,
		}
		switch {
		case len(m.Parts) > 0 && partsAsArray(m.Role, caps):
			wm.Content = toWireParts(m.Parts)
		case len(m.Parts) > 0:
			wm.Content = m.Describe()
		default:
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

// partsAsArray is false where no image may travel; text then goes as a plain string.
func partsAsArray(role string, caps models.ImageCapabilities) bool {
	switch {
	case caps.Input == models.ImageInputNone, role == "assistant":
		return false
	case role == "tool":
		return caps.ToolResult == models.ToolResultNative
	default:
		return true
	}
}

func toWireParts(parts []pub_models.Part) []any {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		switch part := p.(type) {
		case pub_models.TextPart:
			out = append(out, wireTextPart{Type: "text", Text: part.Text})
		case pub_models.ImagePart:
			out = append(out, wireImagePart{Type: "image_url", ImageURL: wireImageURL{URL: ImageDataURL(part)}})
		}
	}
	return out
}
