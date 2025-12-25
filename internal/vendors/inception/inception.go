package inception

import (
	"fmt"

	"github.com/baalimago/clai/internal/text/generic"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var Default = Inception{
	Model: "murcury",
	URL:   ChatURL,
}

type Inception struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	URL              string  `json:"url"`
}

const ChatURL = "https://api.inceptionlabs.ai/v1/chat/completions"

func (g *Inception) Setup() error {
	err := g.StreamCompleter.Setup("INCEPTION_API_KEY", ChatURL, "INCEPTION_DEBUG")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}
	g.StreamCompleter.Model = g.Model
	g.StreamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	return nil
}

func (g *Inception) RegisterTool(tool pub_models.LLMTool) {
	g.InternalRegisterTool(tool)
}
