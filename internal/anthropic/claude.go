package anthropic

import (
	"context"
	"fmt"
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
)

type Claude struct {
	Model            string `json:"model"`
	MaxTokens        int    `json:"max_tokens"`
	Url              string `json:"url"`
	AnthropicVersion string `json:"anthropic-version"`
	AnthropicBeta    string `json:"anthropic-beta"`
	client           *http.Client
	raw              bool
	chat             models.Chat
	apiKey           string
	username         string
	debug            bool
}

type ClaudeReq struct {
	Model     string           `json:"model"`
	Messages  []models.Message `json:"messages"`
	MaxTokens int              `json:"max_tokens"`
	Stream    bool             `json:"stream"`
	System    string           `json:"system"`
}

// Query performs a streamCompletion and appends the returned message to it's internal chat.
// Then it stores the internal chat as prevQuery.json, so that it may be used n upcoming queries
func (c *Claude) Query(ctx context.Context) error {
	nextMsg, err := c.streamCompletions(ctx, c.chat)
	if err != nil {
		return fmt.Errorf("failed to stream completions: %w", err)
	}
	c.chat.Messages = append(c.chat.Messages, nextMsg)
	err = reply.SaveAsPreviousQuery(c.chat.Messages)
	if err != nil {
		return fmt.Errorf("failed to save as previous query: %w", err)
	}
	return nil
}

// TextQuery performs a streamCompletion and appends the returned message to it's internal chat.
// It therefore does not store it to prevQuery.json, and assumes that the calee will deal with
// storing the chat.
func (c *Claude) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	nextMsg, err := c.streamCompletions(ctx, chat)
	if err != nil {
		return chat, fmt.Errorf("failed to stream completions: %w", err)
	}
	chat.Messages = append(chat.Messages, nextMsg)
	return chat, nil
}

// claudifyMessages converts from 'normal' openai chat format into a format which claud prefers
func claudifyMessages(msgs []models.Message) []models.Message {
	// the 'system' role only is accepted as a separate parameter in json
	// If the first message is a system one, assume it's the system prompt and pop it
	// This system message has already been added into the initial query
	if msgs[0].Role == "system" {
		msgs = msgs[1:]
	}
	// Convert system messages from 'system' to 'assistant', since
	for i, v := range msgs {
		v := v
		if v.Role == "system" {
			v.Role = "assistant"
		}
		msgs[i] = v
	}
	return msgs
}
