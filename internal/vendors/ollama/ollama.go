package ollama

import (
	"github.com/baalimago/clai/internal/text/generic"
)

var OLLAMA_DEFAULT = Ollama{
	Model:       "llama3",
	Temperature: 1.0,
	TopP:        1.0,
	Url:         ChatURL,
}

type Ollama struct {
	generic.StreamCompleter
	Model            string  `json:"model"`
	FrequencyPenalty float64 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"` // Use a pointer to allow null value
	PresencePenalty  float64 `json:"presence_penalty"`
	Temperature      float64 `json:"temperature"`
	TopP             float64 `json:"top_p"`
	Url              string  `json:"url"`
}
