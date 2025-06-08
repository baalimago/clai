package anthropic

import (
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

type Claude struct {
	Model              string               `json:"model"`
	MaxTokens          int                  `json:"max_tokens"`
	Url                string               `json:"url"`
	AnthropicVersion   string               `json:"anthropic-version"`
	AnthropicBeta      string               `json:"anthropic-beta"`
	Temperature        float64              `json:"temperature"`
	TopP               float64              `json:"top_p"`
	TopK               int                  `json:"top_k"`
	StopSequences      []string             `json:"stop_sequences"`
	client             *http.Client         `json:"-"`
	apiKey             string               `json:"-"`
	debug              bool                 `json:"-"`
	debugFullStreamMsg string               `json:"-"`
	tools              []tools.UserFunction `json:"-"`
	functionName       string               `json:"-"`
	functionJson       string               `json:"-"`
	contentBlockType   string               `json:"-"`
}

var ClaudeDefault = Claude{
	Model:            "claude-4-sonnet-latest",
	Url:              ClaudeURL,
	AnthropicVersion: "2023-06-01",
	AnthropicBeta:    "",
	Temperature:      0.7,
	MaxTokens:        1024,
	TopP:             -1,
	TopK:             -1,
	StopSequences:    make([]string, 0),
}

type claudeReqMessage struct {
	Role    string          `json:"role"`
	Content []ClaudeMessage `json:"content"`
}

type claudeReq struct {
	Model         string               `json:"model"`
	Messages      []claudeReqMessage   `json:"messages"`
	MaxTokens     int                  `json:"max_tokens"`
	Stream        bool                 `json:"stream"`
	System        string               `json:"system"`
	Temperature   float64              `json:"temperature"`
	TopP          float64              `json:"top_p"`
	TopK          int                  `json:"top_k"`
	StopSequences []string             `json:"stop_sequences"`
	Tools         []tools.UserFunction `json:"tools,omitempty"`
}

// claudifyMessages converts from 'normal' openai chat format into a format which claud prefers
func claudifyMessages(msgs []models.Message) []claudeReqMessage {
	claudeMsgs := make([]claudeReqMessage, 0, len(msgs))
	// Remove any additional fields from the messages
	for _, msg := range msgs {
		claudeMsgs = append(claudeMsgs, claudeReqMessage{
			Role: msg.Role,
			Content: []ClaudeMessage{{
				Type: "text",
				Text: msg.Content,
			}},
		})
	}

	// If the first message is a system one, assume it's the system prompt and pop it
	if claudeMsgs[0].Role == "system" {
		claudeMsgs = claudeMsgs[1:]
	}

	// Convert system messages from 'system' to 'assistant'
	for i, v := range claudeMsgs {
		if v.Role == "system" {
			claudeMsgs[i].Role = "assistant"
		}
	}

	for i, v := range claudeMsgs {
		if v.Role == "tool" {
			claudeMsgs[i].Role = "user"
		}
	}

	// Merge consecutive assistant messages into the first one
	for i := 1; i < len(claudeMsgs); {
		if claudeMsgs[i].Role == "assistant" && claudeMsgs[i-1].Role == "assistant" {
			claudeMsgs[i-1].Content = append(claudeMsgs[i-1].Content, claudeMsgs[i].Content...)
			claudeMsgs = append(claudeMsgs[:i], claudeMsgs[i+1:]...)
		} else {
			i++
		}
	}

	// Merge consecutive user messages into the last one
	for i := len(claudeMsgs) - 2; i >= 0; i-- {
		if claudeMsgs[i].Role == "user" && claudeMsgs[i+1].Role == "user" {
			claudeMsgs[i+1].Content = append(claudeMsgs[i].Content, claudeMsgs[i+1].Content...)
			claudeMsgs = append(claudeMsgs[:i], claudeMsgs[i+1:]...)
		}
	}

	// If the first message is from an assistant, keep it as is
	// (no need to merge it into the upcoming user message)

	// If the last message is from an assistant, remove it
	if len(claudeMsgs) > 0 && claudeMsgs[len(claudeMsgs)-1].Role == "assistant" {
		claudeMsgs = claudeMsgs[:len(claudeMsgs)-1]
	}

	return claudeMsgs
}
