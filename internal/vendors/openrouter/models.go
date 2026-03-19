package openrouter

import "encoding/json"

type Model struct {
	ID                  string                     `json:"id"`
	Name                string                     `json:"name"`
	Pricing             ModelPricing               `json:"pricing"`
	TopProvider         TopProvider                `json:"top_provider"`
	Architecture        Architecture               `json:"architecture"`
	SupportedParameters []string                   `json:"supported_parameters"`
	ContextLength       int                        `json:"context_length,omitempty"`
	PerRequestLimits    map[string]any             `json:"per_request_limits,omitempty"`
	Raw                 map[string]json.RawMessage `json:"-"`
}

type ModelPricing struct {
	Prompt            string `json:"prompt"`
	Completion        string `json:"completion"`
	Request           string `json:"request"`
	Image             string `json:"image"`
	WebSearch         string `json:"web_search"`
	InternalReasoning string `json:"internal_reasoning"`
	InputCacheRead    string `json:"input_cache_read"`
	InputCacheWrite   string `json:"input_cache_write"`
}

type TopProvider struct {
	ContextLength       int `json:"context_length"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

type Architecture struct {
	Modality     string `json:"modality"`
	Tokenizer    string `json:"tokenizer"`
	InstructType string `json:"instruct_type"`
}
