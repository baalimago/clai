package openai

import (
	"fmt"
	"net/http"
	"os"
	"path"

	"context"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type ChatGPT struct {
	Model            string  `json:"model"`
	FrequencyPenalty float32 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float32 `json:"presence_penalty"`
	Temperature      float32 `json:"temperature"`
	TopP             float32 `json:"top_p"`
	Url              string  `json:"url"`
	client           *http.Client
	raw              bool
	chat             models.Chat
	apiKey           string
	username         string
	debug            bool
}

// Query performs a streamCompletion and appends the returned message to it's internal chat.
// Then it stores the internal chat as prevQuery.json, so that it may be used n upcoming queries
func (q *ChatGPT) Query(ctx context.Context) error {
	nextMsg, err := q.streamCompletions(ctx, q.apiKey, q.chat.Messages)
	if err != nil {
		return fmt.Errorf("failed to stream completions: %w", err)
	}
	q.chat.Messages = append(q.chat.Messages, nextMsg)
	home, _ := os.UserHomeDir()
	err = tools.WriteFile(path.Join(home, ".clai/conversations/prevQuery.json"), &q.chat)
	if err != nil {
		return fmt.Errorf("failed to write prevQuery: %w", err)
	}
	return nil
}

// TextQuery performs a streamCompletion and appends the returned message to it's internal chat.
// It therefore does not store it to prevQuery.json, and assumes that the calee will deal with
// storing the chat.
func (q *ChatGPT) TextQuery(ctx context.Context, userInput string) error {
	q.chat.Messages = append(q.chat.Messages, models.Message{Role: "user", Content: userInput})
	nextMsg, err := q.streamCompletions(ctx, q.apiKey, q.chat.Messages)
	if err != nil {
		return fmt.Errorf("failed to stream completions: %w", err)
	}
	q.chat.Messages = append(q.chat.Messages, nextMsg)
	return nil
}

func (q *ChatGPT) Chat() models.Chat {
	return q.chat
}

func (q *ChatGPT) SetChat(chat models.Chat) {
	q.chat = chat
}

func loadQuerier(model string) (*ChatGPT, error) {
	home, _ := os.UserHomeDir()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable 'OPENAI_API_KEY' not set")
	}
	defaultCpy := defaultGpt
	defaultCpy.Model = model
	defaultCpy.Url = ChatURL
	// Load config based on model, allowing for different configs for each model
	gptQuerier, err := tools.LoadConfigFromFile[ChatGPT](home, fmt.Sprintf("openai_gpt_%v.json", model), nil, &defaultCpy)
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
	querier, err := loadQuerier(conf.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to load querier of model: %v, error: %w", conf.Model, err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		querier.debug = true
	}
	querier.chat = conf.InitialPrompt
	return querier, nil
}
