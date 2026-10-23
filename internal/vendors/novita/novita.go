package novita

import (
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
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
	if err := g.StreamCompleter.SetupOpenAICompatible(g.Model, "NOVITA_API_KEY", "novita", ChatURL, "NOVITA_DEBUG", "novita:", g.FrequencyPenalty, g.Temperature, g.TopP, g.MaxTokens); err != nil {
		return err
	}
	g.StreamCompleter.Vendor = "novita"
	g.StreamCompleter.Images = g.ImageCapabilities()
	return nil
}

func (g *Novita) RegisterTool(tool pub_models.LLMTool) {
	g.InternalRegisterTool(tool)
}

// ImageCapabilities is the novita row of Local fact P5-1 (worklog 2026-10-21-image-support).
func (g *Novita) ImageCapabilities() models.ImageCapabilities {
	return models.ImageCapabilities{
		Input:      models.ImageInputInline,
		ToolResult: models.ToolResultNative,
		MIMEs:      []string{"image/png", "image/jpeg", "image/webp"},
	}
}
