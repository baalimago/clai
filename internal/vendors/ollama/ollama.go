package ollama

import (
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
	return g.StreamCompleter.SetupOpenAICompatible(g.Model, "OLLAMA_API_KEY", "ollama", ChatURL, "OLLAMA_DEBUG", "ollama:", g.FrequencyPenalty, g.Temperature, g.TopP, g.MaxTokens)
}

func (g *Ollama) RegisterTool(tool pub_models.LLMTool) {
	g.InternalRegisterTool(tool)
}
