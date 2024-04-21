package anthropic

import (
	"github.com/baalimago/clai/internal/tools"
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
	ID    string      `json:"id,omitempty"`
	Input tools.Input `json:"input,omitempty"`
	Name  string      `json:"name,omitempty"`
	Text  string      `json:"text,omitempty"`
	Type  string      `json:"type"`
}

type TokenInfo struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
