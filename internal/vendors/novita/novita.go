package novita

import (
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/tools"
)

var Default = Novita{
	Model:       "gryphe/mythomax-l2-13b",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type Novita struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`
}

const ChatURL = "https://api.novita.ai/openai/v1/chat/completions"

func (g *Novita) Setup() error {
	if os.Getenv("NOVITA_API_KEY") == "" {
		os.Setenv("NOVITA_API_KEY", "novita")
	}
	err := g.StreamCompleter.Setup("NOVITA_API_KEY", ChatURL, "NOVITA_DEBUG")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}

	modelName := strings.TrimLeft(g.Model, "novita:")
	g.StreamCompleter.Model = modelName
	g.StreamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	return nil
}

func (g *Novita) RegisterTool(tool tools.LLMTool) {
	g.InternalRegisterTool(tool)
}
