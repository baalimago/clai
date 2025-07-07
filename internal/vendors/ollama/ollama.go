package ollama

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/tools"
)

var Default = Ollama{
	Model:       "llama3",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type Ollama struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`
}

const ChatURL = "http://localhost:11434/v1/chat/completions"

func (g *Ollama) Setup() error {
	if os.Getenv("OLLAMA_API_KEY") == "" {
		os.Setenv("OLLAMA_API_KEY", "ollama")
	}
	err := g.StreamCompleter.Setup("OLLAMA_API_KEY", ChatURL, "OLLAMA_DEBUG")
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

func (g *Ollama) RegisterTool(tool tools.LLMTool) {
	g.InternalRegisterTool(tool)
}
