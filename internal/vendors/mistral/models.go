package mistral

import (
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

type MistralToolSuper struct {
	Function MistralTool `json:"function"`
	Type     string      `json:"type"`
}

type MistralTool struct {
	Description string            `json:"description"`
	Name        string            `json:"name"`
	Parameters  tools.InputSchema `json:"parameters"`
}

type Request struct {
	MaxTokens   int                `json:"max_tokens,omitempty"`
	Messages    []models.Message   `json:"messages,omitempty"`
	Model       string             `json:"model,omitempty"`
	RandomSeed  int                `json:"random_seed,omitempty"`
	SafePrompt  bool               `json:"safe_prompt,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
	ToolChoice  string             `json:"tool_choice,omitempty"`
	Tools       []MistralToolSuper `json:"tools,omitempty"`
	TopP        float64            `json:"top_p,omitempty"`
}

type Response struct {
	FinishReason string   `json:"finish_reason"`
	Choices      []Choice `json:"choices"`
	Created      int      `json:"created"`
	ID           string   `json:"id"`
	Model        string   `json:"model"`
	Object       string   `json:"object"`
	Usage        Usage    `json:"usage"`
}

type Choice struct {
	Index int     `json:"index"`
	Delta Message `json:"delta"`
}

type Message struct {
	Content   string `json:"content"`
	Role      string `json:"role"`
	ToolCalls []struct {
		Call Call `json:"function"`
	} `json:"tool_calls"`
}

type Call struct {
	Arguments string `json:"arguments"`
	Name      string `json:"name"`
}

type Usage struct {
	CompletionTokens int `json:"completion_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
