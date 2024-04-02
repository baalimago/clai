package openai

import (
	"context"
	"fmt"
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
)

type ChatGPT struct {
	Model            string       `json:"model"`
	FrequencyPenalty float32      `json:"frequency_penalty"`
	MaxTokens        *int         `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float32      `json:"presence_penalty"`
	Temperature      float32      `json:"temperature"`
	TopP             float32      `json:"top_p"`
	Url              string       `json:"url"`
	Raw              bool         `json:"raw"`
	client           *http.Client `json:"-"`
	chat             models.Chat  `json:"-"`
	apiKey           string       `json:"-"`
	username         string       `json:"-"`
	debug            bool         `json:"-"`
}

type gptReq struct {
	Model            string           `json:"model"`
	ResponseFormat   responseFormat   `json:"response_format"`
	Messages         []models.Message `json:"messages"`
	Stream           bool             `json:"stream"`
	FrequencyPenalty float32          `json:"frequency_penalty"`
	MaxTokens        *int             `json:"max_tokens"`
	PresencePenalty  float32          `json:"presence_penalty"`
	Temperature      float32          `json:"temperature"`
	TopP             float32          `json:"top_p"`
}

// Query performs a streamCompletion and appends the returned message to it's internal chat.
// Then it stores the internal chat as prevQuery.json, so that it may be used n upcoming queries
func (q *ChatGPT) Query(ctx context.Context) error {
	nextMsg, err := q.streamCompletions(ctx, q.apiKey, q.chat.Messages)
	if err != nil {
		return fmt.Errorf("failed to stream completions: %w", err)
	}
	q.chat.Messages = append(q.chat.Messages, nextMsg)
	err = reply.SaveAsPreviousQuery(q.chat.Messages)
	if err != nil {
		return fmt.Errorf("failed to save as previous query: %w", err)
	}
	return nil
}

// TextQuery performs a streamCompletion and appends the returned message to it's internal chat.
// It therefore does not store it to prevQuery.json, and assumes that the calee will deal with
// storing the chat.
func (q *ChatGPT) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	nextMsg, err := q.streamCompletions(ctx, q.apiKey, chat.Messages)
	if err != nil {
		return chat, fmt.Errorf("failed to stream completions: %w", err)
	}
	chat.Messages = append(chat.Messages, nextMsg)
	return chat, nil
}
