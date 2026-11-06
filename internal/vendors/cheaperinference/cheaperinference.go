package cheaperinference

import (
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var Default = CheaperInference{
	Model:       "gpt-5.4-mini",
	Temperature: 1.0,
	TopP:        1.0,
	URL:         ChatURL,
}

type CheaperInference struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	URL              string  `json:"url"`
}

const ChatURL = "https://api.cheaperinference.com/v1/chat/completions"

func (g *CheaperInference) Setup() error {
	err := g.StreamCompleter.Setup("CHEAPER_INFERENCE_API_KEY", ChatURL, "CHEAPER_INFERENCE_DEBUG")
	if err != nil {
		return fmt.Errorf("failed to setup stream completer: %w", err)
	}

	modelName := strings.TrimPrefix(g.Model, "ci:")
	g.StreamCompleter.Model = modelName
	g.StreamCompleter.FrequencyPenalty = &g.FrequencyPenalty
	g.StreamCompleter.MaxTokens = g.MaxTokens
	g.StreamCompleter.Temperature = &g.Temperature
	g.StreamCompleter.TopP = &g.TopP
	toolChoice := "auto"
	g.ToolChoice = &toolChoice
	g.StreamCompleter.Vendor = "cheaperinference"
	g.StreamCompleter.Images = g.ImageCapabilities()
	return nil
}

func (g *CheaperInference) RegisterTool(tool pub_models.LLMTool) {
	g.InternalRegisterTool(tool)
}

// ImageCapabilities reports inline image input; image tool results fall back to text.
func (g *CheaperInference) ImageCapabilities() models.ImageCapabilities {
	return models.ImageCapabilities{
		Input:      models.ImageInputInline,
		ToolResult: models.ToolResultFallback,
		MIMEs:      []string{"image/png", "image/jpeg", "image/webp"},
	}
}
