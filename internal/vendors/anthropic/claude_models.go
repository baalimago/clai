package anthropic

import (
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type ClaudeResponse struct {
	Content      []ClaudeMessage `json:"content"`
	ID           string          `json:"id"`
	Model        string          `json:"model"`
	Role         string          `json:"role"`
	StopReason   string          `json:"stop_reason"`
	StopSequence any             `json:"stop_sequence"`
	Type         string          `json:"type"`
	Usage        TokenInfo       `json:"usage"`
}

type ClaudeMessage struct {
	ID    string            `json:"id,omitempty"`
	Input *pub_models.Input `json:"input,omitempty"`
	Name  string            `json:"name,omitempty"`
	Text  string            `json:"text,omitempty"`
	Type  string            `json:"type"`
}

type TokenInfo struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Delta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type ContentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta Delta  `json:"delta"`
}

type ContentBlockSuper struct {
	Type             string              `json:"type"`
	Index            int                 `json:"index"`
	ToolContentBlock ToolUseContentBlock `json:"content_block"`
}

type ToolUseContentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input *map[string]any `json:"input,omitempty"`
}

type ToolResultContentBlock struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	ToolUseID string `json:"tool_use_id"`
}

type TextContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Root struct {
	Type         string              `json:"type"`
	Index        int                 `json:"index"`
	ContentBlock ToolUseContentBlock `json:"content_block"`
}

type ClaudeConvMessage struct {
	Role string `json:"role"`
	// Content may be either ToolContentBlock or TextContentBlock
	Content []any `json:"content"`
}
