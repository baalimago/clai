package mistral

import (
	"net/http"
)

const MistralURL = "https://api.mistral.ai/v1/chat/completions"

var MINSTRAL_DEFAULT = Mistral{
	Model:       "mistral-large-latest",
	Temperature: 0.7,
	TopP:        1.0,
	Url:         MistralURL,
	MaxTokens:   100000,
}

type Mistral struct {
	Model       string             `json:"model"`
	Url         string             `json:"url"`
	TopP        float64            `json:"top_p"`
	Temperature float64            `json:"temperature"`
	SafePrompt  bool               `json:"safe_prompt"`
	MaxTokens   int                `json:"max_tokens"`
	RandomSeed  int                `json:"random_seed"`
	client      *http.Client       `json:"-"`
	apiKey      string             `json:"-"`
	debug       bool               `json:"-"`
	tools       []MistralToolSuper `json:"-"`
}
