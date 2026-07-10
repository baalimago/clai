package generic

import (
	"net/http"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// StreamCompleter is a struct which follows the model for both OpenAI and Mistral
type StreamCompleter struct {
	Model            string   `json:"-"`
	FrequencyPenalty *float64 `json:"-"`
	MaxTokens        *int     `json:"-"`
	PresencePenalty  *float64 `json:"-"`
	Temperature      *float64 `json:"-"`
	TopP             *float64 `json:"-"`
	// ReasoningEffort maps to the Chat Completions reasoning_effort field. Only set
	// for reasoning models (others reject it); empty omits it. Vendor-agnostic:
	// non-OpenAI callers leave it empty.
	ReasoningEffort string                                          `json:"-"`
	ToolChoice      *string                                         `json:"-"`
	Clean           func([]pub_models.Message) []pub_models.Message `json:"-"`
	URL             string
	ExtraHeaders    map[string]string `json:"-"`
	tools           []ToolSuper
	toolsCallName   string
	// Argument string exists since the arguments for function calls is streamed token by token... yeah... great idea
	toolsCallArgsString string
	toolsCallID         string
	extraContent        map[string]any
	reasoningContent    string
	client              *http.Client
	apiKey              string
	debug               bool

	// ResponseFormat configures structured output. When nil, defaults to {type: "text"}.
	ResponseFormat *ResponseFormat `json:"-"`

	usage *pub_models.Usage
}

// SetResponseFormat configures the response format for structured output.
// Pass nil to reset to default (text).
func (s *StreamCompleter) SetResponseFormat(rf *ResponseFormat) {
	s.ResponseFormat = rf
}

// ResponseFormatSetter is implemented by types that can accept a response format.
type ResponseFormatSetter interface {
	SetResponseFormat(*ResponseFormat)
}

type ToolSuper struct {
	Type     string `json:"type"`
	Function Tool   `json:"function"`
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Inputs      pub_models.InputSchema `json:"parameters"`
}

type chatCompletionChunk struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	Created           int               `json:"created"`
	Model             string            `json:"model"`
	SystemFingerprint string            `json:"system_fingerprint"`
	Choices           []Choice          `json:"choices"`
	Usage             *pub_models.Usage `json:"usage,omitempty"`
}

type Choice struct {
	Index        int    `json:"index"`
	Delta        Delta  `json:"delta"`
	Logprobs     any    `json:"logprobs"` // null or complex object, hence interface{}
	FinishReason string `json:"finish_reason"`
}

type Delta struct {
	Content          any         `json:"content"`
	ReasoningContent string      `json:"reasoning_content"`
	Role             string      `json:"role"`
	ToolCalls        []ToolsCall `json:"tool_calls"`
}

type ExtraContent map[string]map[string]any

type ToolsCall struct {
	Function Func   `json:"function"`
	ID       string `json:"id"`
	Index    int    `json:"index"`
	Type     string `json:"type"`

	// ExtraContent for initially google thought_signature
	ExtraContent map[string]any `json:"extra_content,omitempty"`
}

type Func struct {
	Arguments string `json:"arguments"`
	Name      string `json:"name"`
}

// ResponseFormat follows the OpenAI chat completions response_format schema.
// Supported types: "text", "json_object", "json_schema".
type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *JSONSchemaSpec `json:"json_schema,omitempty"`
}

// JSONSchemaSpec defines the schema when response_format type is "json_schema".
type JSONSchemaSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
	Schema      map[string]any `json:"schema"`
}

type req struct {
	Model             string               `json:"model,omitempty"`
	ResponseFormat    ResponseFormat       `json:"response_format"`
	Messages          []pub_models.Message `json:"messages,omitempty"`
	Stream            bool                 `json:"stream,omitempty"`
	StreamOptions     map[string]any       `json:"stream_options"`
	FrequencyPenalty  *float64             `json:"frequency_penalty,omitempty"`
	MaxTokens         *int                 `json:"max_tokens,omitempty"`
	PresencePenalty   *float64             `json:"presence_penalty,omitempty"`
	Temperature       *float64             `json:"temperature,omitempty"`
	TopP              *float64             `json:"top_p,omitempty"`
	ReasoningEffort   string               `json:"reasoning_effort,omitempty"`
	ToolChoice        *string              `json:"tool_choice,omitempty"`
	Tools             []ToolSuper          `json:"tools,omitempty"`
	ParalellToolCalls bool                 `json:"parallel_tools_call,omitempty"`
}
