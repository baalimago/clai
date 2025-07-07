package openai

import (
	"fmt"

	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/tools"
)

var GptDefault = ChatGPT{
	Model:       "gpt-4.1-mini",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type ChatGPT struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`
}

func (g *ChatGPT) Setup() error {
	err := g.StreamCompleter.Setup("OPENAI_API_KEY", ChatURL, "DEBUG_OPENAI")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	g.StreamCompleter.Model = g.Model
	g.StreamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	return nil
}

func (g *ChatGPT) RegisterTool(tool tools.LLMTool) {
	g.InternalRegisterTool(tool)
}
