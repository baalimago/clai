package openai

// Minimal subset of the OpenAI Responses API request/streaming types needed by clai.
//
// We intentionally model only the fields we need for:
//   - text streaming deltas
//   - function tool calling (name + JSON args)
//
// The wire format for Responses emits events with a "type" discriminator.

type responsesRequest struct {
	Model      string               `json:"model"`
	Input      []responsesInputItem `json:"input"`
	Stream     bool                 `json:"stream"`
	Tools      []responsesTool      `json:"tools,omitempty"`
	ToolChoice any                  `json:"tool_choice,omitempty"`
}

// NOTE: For Responses, input[] is a *union* keyed by "type".
// We currently send:
//   - message (normal chat turns)
//   - function_call (assistant tool call items)
//   - function_call_output (tool results)
//
// See openai-go responses.ResponseInputItemUnionParam for the canonical union.

type responsesInputItem struct {
	Type string `json:"type"` // "message" | "function_call" | "function_call_output"

	// message shape
	Role    string                  `json:"role,omitempty"`
	Content []responsesInputContent `json:"content,omitempty"`

	// function_call shape
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// function_call_output shape
	Output any `json:"output,omitempty"`
}

type responsesInputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesTool struct {
	Type        string `json:"type"` // "function"
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type responsesStreamEvent struct {
	Type     string                  `json:"type"`
	Delta    string                  `json:"delta,omitempty"`
	Item     *responsesOutputItem    `json:"item,omitempty"`
	Error    *responsesStreamErrBody `json:"error,omitempty"`
	Response *responsesResponse      `json:"response,omitempty"`
}

type responsesStreamErrBody struct {
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
}

type responsesResponse struct {
	Usage *responsesUsage `json:"usage,omitempty"`
}

type responsesUsage struct {
	InputTokens         int                           `json:"input_tokens"`
	OutputTokens        int                           `json:"output_tokens"`
	TotalTokens         int                           `json:"total_tokens"`
	InputTokensDetails  *responsesInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *responsesOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type responsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
	AudioTokens  int `json:"audio_tokens"`
}

type responsesOutputTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens"`
	AudioTokens              int `json:"audio_tokens"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
}

type responsesOutputItem struct {
	Type   string                       `json:"type"` // e.g. "function_call"
	ID     string                       `json:"id,omitempty"`
	Name   string                       `json:"name,omitempty"`
	CallID string                       `json:"call_id,omitempty"`
	Output []responsesOutputItemContent `json:"output,omitempty"`
}

type responsesOutputItemContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
