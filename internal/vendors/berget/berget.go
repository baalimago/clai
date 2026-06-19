package berget

import (
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/text/generic"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var Default = Berget{
	Model:       "gemma-4-31B-it",
	Temperature: 0.7,
	TopP:        1.0,
	URL:         ChatURL,
}

type Berget struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`
}

const ChatURL = "https://api.berget.ai/v1/chat/completions"

func (g *Berget) Setup() error {
	err := g.StreamCompleter.Setup("BERGET_API_KEY", ChatURL, "BERGET_DEBUG")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}

	modelName := strings.TrimPrefix(g.Model, "berget:")
	g.StreamCompleter.Model = modelName
	g.StreamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	return nil
}

func (g *Berget) RegisterTool(tool pub_models.LLMTool) {
	g.InternalRegisterTool(tool)
}
