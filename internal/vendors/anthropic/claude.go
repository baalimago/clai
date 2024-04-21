package anthropic

import (
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

type Claude struct {
	Model            string               `json:"model"`
	MaxTokens        int                  `json:"max_tokens"`
	Url              string               `json:"url"`
	AnthropicVersion string               `json:"anthropic-version"`
	AnthropicBeta    string               `json:"anthropic-beta"`
	client           *http.Client         `json:"-"`
	apiKey           string               `json:"-"`
	debug            bool                 `json:"-"`
	tools            []tools.UserFunction `json:"-"`
}

var CLAUDE_DEFAULT = Claude{
	Model:            "claude-3-opus-20240229",
	Url:              ClaudeURL,
	AnthropicVersion: "2023-06-01",
	AnthropicBeta:    "tools-2024-04-04",
	MaxTokens:        1024,
}

type claudeReq struct {
	Model     string               `json:"model"`
	Messages  []models.Message     `json:"messages"`
	MaxTokens int                  `json:"max_tokens"`
	Stream    bool                 `json:"stream"`
	System    string               `json:"system"`
	Tools     []tools.UserFunction `json:"tools,omitempty"`
}

// claudifyMessages converts from 'normal' openai chat format into a format which claud prefers
func claudifyMessages(msgs []models.Message) []models.Message {
	// If the first message is a system one, assume it's the system prompt and pop it
	if msgs[0].Role == "system" {
		msgs = msgs[1:]
	}

	// Convert system messages from 'system' to 'assistant'
	for i, v := range msgs {
		if v.Role == "system" {
			msgs[i].Role = "assistant"
		}
	}

	for i, v := range msgs {
		if v.Role == "tool" {
			msgs[i].Role = "user"
		}
	}

	// Merge consecutive assistant messages into the first one
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == "assistant" && msgs[i-1].Role == "assistant" {
			msgs[i-1].Content += "\n" + msgs[i].Content
			msgs = append(msgs[:i], msgs[i+1:]...)
			i--
		}
	}

	// Merge consecutive user messages into the last one
	for i := len(msgs) - 2; i >= 0; i-- {
		if msgs[i].Role == "user" && msgs[i+1].Role == "user" {
			msgs[i+1].Content = msgs[i].Content + "\n" + msgs[i+1].Content
			msgs = append(msgs[:i], msgs[i+1:]...)
		}
	}

	// If the first message is from an assistant, keep it as is
	// (no need to merge it into the upcoming user message)

	// If the last message is from an assistant, remove it
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == "assistant" {
		msgs = msgs[:len(msgs)-1]
	}

	return msgs
}
