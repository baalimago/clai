package ollama

import (
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const ChatURL = "http://localhost:11434/v1/chat/completions"

var Default = Ollama{
	Model:       "llama3",
	Temperature: 1.0,
	TopP:        1.0,
}

type Ollama struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
}

func (g *Ollama) Setup() error {
	if err := g.StreamCompleter.SetupOpenAICompatible(g.Model, "OLLAMA_API_KEY", "ollama", ChatURL, "OLLAMA_DEBUG", "ollama:", g.FrequencyPenalty, g.Temperature, g.TopP, g.MaxTokens); err != nil {
		return err
	}
	g.StreamCompleter.Vendor = "ollama"
	g.StreamCompleter.Images = g.ImageCapabilities()
	return nil
}

func (g *Ollama) RegisterTool(tool pub_models.LLMTool) {
	g.InternalRegisterTool(tool)
}

// ImageCapabilities is the ollama row of Local fact P5-1 (worklog 2026-10-21-image-support).
func (g *Ollama) ImageCapabilities() models.ImageCapabilities {
	return models.ImageCapabilities{
		Input:      models.ImageInputInline,
		ToolResult: models.ToolResultFallback,
		MIMEs:      []string{"image/png", "image/jpeg", "image/webp"},
	}
}
