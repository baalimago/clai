package deepseek

import "github.com/baalimago/clai/internal/tools"

// Copy from novita, and openai
type ChatCompletion struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
	SystemFingerprint string   `json:"system_fingerprint"`
}

type Choice struct {
	Index        int         `json:"index"`
	Delta        Delta       `json:"delta"`
	Logprobs     interface{} `json:"logprobs"` // null or complex object, hence interface{}
	FinishReason string      `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Delta struct {
	Content   any         `json:"content"`
	Role      string      `json:"role"`
	ToolCalls []ToolsCall `json:"tool_calls"`
}

type ToolsCall struct {
	Function GptFunc `json:"function"`
	ID       string  `json:"id"`
	Index    int     `json:"index"`
	Type     string  `json:"type"`
}

type GptFunc struct {
	Arguments string `json:"arguments"`
	Name      string `json:"name"`
}

type GptTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Inputs      tools.InputSchema `json:"parameters"`
}

type GptToolSuper struct {
	Type     string  `json:"type"`
	Function GptTool `json:"function"`
}
