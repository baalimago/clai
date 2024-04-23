package openai

import (
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

type ChatGPT struct {
	Model            string         `json:"model"`
	FrequencyPenalty float32        `json:"frequency_penalty"`
	MaxTokens        *int           `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float32        `json:"presence_penalty"`
	Temperature      float32        `json:"temperature"`
	TopP             float32        `json:"top_p"`
	Url              string         `json:"url"`
	tools            []GptToolSuper `json:"-"`
	toolsCallName    string         `json:"-"`
	// Argument string exists since the arguments for function calls is streamed token by token... yeah... great idea
	toolsCallArgsString string       `json:"-"`
	client              *http.Client `json:"-"`
	apiKey              string       `json:"-"`
	debug               bool         `json:"-"`
}

type GptTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Inputs      tools.InputSchema `json:"parameters"`
}

type GptToolSuper struct {
	Type     string  `json:"type"`
	Function GptTool `json:"function"`
}

var GPT_DEFAULT = ChatGPT{
	Model:       "gpt-4-turbo",
	Temperature: 1.0,
	TopP:        1.0,
	Url:         ChatURL,
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
	Tools            []GptToolSuper   `json:"tools,omitempty"`
}
