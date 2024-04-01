package openai

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
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

func loadQuerier(loadFrom, model string) (*ChatGPT, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable 'OPENAI_API_KEY' not set")
	}
	defaultCpy := defaultGpt
	defaultCpy.Model = model
	defaultCpy.Url = ChatURL
	// Load config based on model, allowing for different configs for each model
	gptQuerier, err := tools.LoadConfigFromFile[ChatGPT](loadFrom, fmt.Sprintf("openai_gpt_%v.json", model), nil, &defaultCpy)
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("ChatGPT config: %+v\n", gptQuerier))
	}
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to load config for model: %v, error: %v\n", model, err))
	}
	gptQuerier.client = &http.Client{}
	gptQuerier.apiKey = apiKey
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return &gptQuerier, nil
}

func NewTextQuerier(conf text.Configurations) (models.ChatQuerier, error) {
	home, _ := os.UserConfigDir()
	querier, err := loadQuerier(home, conf.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to load querier of model: %v, error: %w", conf.Model, err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		querier.debug = true
	}
	querier.chat = conf.InitialPrompt
	querier.Raw = conf.Raw
	return querier, nil
}
