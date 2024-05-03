package generic

import (
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

// StreamCompleter is a struct which follows the model for both OpenAI and Mistral
type StreamCompleter struct {
	Model            string
	FrequencyPenalty *float64
	MaxTokens        *int
	PresencePenalty  *float64
	Temperature      *float64
	TopP             *float64
	ToolChoice       *string
	Clean            func([]models.Message) []models.Message
	url              string
	tools            []ToolSuper
	toolsCallName    string
	// Argument string exists since the arguments for function calls is streamed token by token... yeah... great idea
	toolsCallArgsString string
	toolsCallID         string
	client              *http.Client
	apiKey              string
	debug               bool
}

type ToolSuper struct {
	Type     string `json:"type"`
	Function Tool   `json:"function"`
}

type Tool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Inputs      tools.InputSchema `json:"parameters"`
}

type chatCompletionChunk struct {
	Id                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int      `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint"`
	Choices           []Choice `json:"choices"`
}

type Choice struct {
	Index        int         `json:"index"`
	Delta        Delta       `json:"delta"`
	Logprobs     interface{} `json:"logprobs"` // null or complex object, hence interface{}
	FinishReason string      `json:"finish_reason"`
}

type Delta struct {
	Content   any         `json:"content"`
	Role      string      `json:"role"`
	ToolCalls []ToolsCall `json:"tool_calls"`
}

type ToolsCall struct {
	Function Func   `json:"function"`
	ID       string `json:"id"`
	Index    int    `json:"index"`
	Type     string `json:"type"`
}

type Func struct {
	Arguments string `json:"arguments"`
	Name      string `json:"name"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type req struct {
	Model            string           `json:"model,omitempty"`
	ResponseFormat   responseFormat   `json:"response_format,omitempty"`
	Messages         []models.Message `json:"messages,omitempty"`
	Stream           bool             `json:"stream,omitempty"`
	FrequencyPenalty *float64         `json:"frequency_penalty,omitempty"`
	MaxTokens        *int             `json:"max_tokens,omitempty"`
	PresencePenalty  *float64         `json:"presence_penalty,omitempty"`
	Temperature      *float64         `json:"temperature,omitempty"`
	TopP             *float64         `json:"top_p,omitempty"`
	ToolChoice       *string          `json:"tool_choice,omitempty"`
	Tools            []ToolSuper      `json:"tools,omitempty"`
}
