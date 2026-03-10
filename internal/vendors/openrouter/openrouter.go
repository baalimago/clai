package openrouter

import (
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const ChatURL = "https://openrouter.ai/api/v1/chat/completions"

var Default = OpenRouter{
	Model:       "openai/gpt-4.1-mini",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type OpenRouter struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"`
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`
}

func (o *OpenRouter) Setup() error {
	err := o.StreamCompleter.Setup("OPENROUTER_API_KEY", ChatURL, "DEBUG_OPENROUTER")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}

	modelName := strings.TrimPrefix(o.Model, "or:")
	o.StreamCompleter.Model = modelName
	o.StreamCompleter.FrequencyPenalty = &o.FrequencyPenalty
	o.StreamCompleter.MaxTokens = o.MaxTokens
	o.StreamCompleter.PresencePenalty = &o.PresencePenalty
	o.StreamCompleter.Temperature = &o.Temperature
	o.StreamCompleter.TopP = &o.TopP
	o.StreamCompleter.ExtraHeaders = map[string]string{
		"HTTP-Referer":       "clai",
		"X-OpenRouter-Title": "clai",
	}
	toolChoice := "auto"
	o.ToolChoice = &toolChoice
	return nil
}

func (o *OpenRouter) RegisterTool(tool pub_models.LLMTool) {
	o.InternalRegisterTool(tool)
}
