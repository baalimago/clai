package openai

// Minimal subset of the OpenAI Responses API request/streaming types needed by clai.
//
// We intentionally model only the fields we need for:
//   - text streaming deltas
//   - function tool calling (name + JSON args)
//
// The wire format for Responses emits events with a "type" discriminator.

type responsesRequest struct {
	Model             string               `json:"model"`
	Input             []responsesInputItem `json:"input"`
	Stream            bool                 `json:"stream"`
	Tools             []responsesTool      `json:"tools,omitempty"`
	ToolChoice        any                  `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                `json:"parallel_tool_calls,omitempty"`
	Text              *responsesText       `json:"text,omitempty"`
	Reasoning         *responsesReasoning  `json:"reasoning,omitempty"`
	Temperature       *float64             `json:"temperature,omitempty"`
	TopP              *float64             `json:"top_p,omitempty"`
	MaxOutputTokens   *int                 `json:"max_output_tokens,omitempty"`
	// Store controls server-side retention of the response. clai is stateless
	// (it resends the full input[] each turn and never uses previous_response_id),
	// so it always sends store=false to match the Chat Completions privacy posture.
	Store *bool `json:"store,omitempty"`
	// Include opts into extra output data. clai requests
	// "reasoning.encrypted_content" for reasoning models so the (otherwise hidden)
	// reasoning items come back sealed and can be replayed on the next turn — the
	// only way to keep reasoning continuity while remaining store=false.
	Include []string `json:"include,omitempty"`
}

// responsesReasoning opts into reasoning behaviour on the Responses API. Summary
// must be non-empty (e.g. "auto") for the API to stream reasoning summary deltas;
// Effort controls how much the model reasons. Only reasoning models accept it.
type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`  // "minimal" | "low" | "medium" | "high"
	Summary string `json:"summary,omitempty"` // "auto" | "concise" | "detailed"
}

// responsesText carries the structured-output configuration for the Responses API.
// Unlike Chat Completions (which nests the schema under json_schema), Responses puts
// the schema fields directly on the format object.
type responsesText struct {
	Format responsesTextFormat `json:"format"`
}

type responsesTextFormat struct {
	Type        string         `json:"type"` // "text" | "json_object" | "json_schema"
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

// NOTE: For Responses, input[] is a *union* keyed by "type".
// We currently send:
//   - message (normal chat turns)
//   - function_call (assistant tool call items)
//   - function_call_output (tool results)
//
// See openai-go responses.ResponseInputItemUnionParam for the canonical union.

type responsesInputItem struct {
	Type string `json:"type"` // "message" | "function_call" | "function_call_output" | "reasoning"

	// message shape
	Role    string                  `json:"role,omitempty"`
	Content []responsesInputContent `json:"content,omitempty"`

	// function_call shape
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// function_call_output shape
	Output any `json:"output,omitempty"`

	// reasoning shape: an opaque reasoning item replayed to preserve continuity.
	// ID and EncryptedContent come straight back from a prior response's output.
	ID               string `json:"id,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
	// Summary is a pointer so a reasoning item can always emit it (as [] when we
	// captured no summary text) while every other input-item shape omits it. The
	// Responses API rejects a reasoning input item that is missing `summary`
	// ("Missing required parameter: input[N].summary"), and omitempty on a plain
	// slice would drop an empty one.
	Summary *[]responsesSummaryPart `json:"summary,omitempty"`
}

// responsesSummaryPart is one entry of a reasoning item's summary array
// ({type:"summary_text", text:"..."}).
type responsesSummaryPart struct {
	Type string `json:"type"` // "summary_text"
	Text string `json:"text"`
}

type responsesInputContent struct {
	Type string `json:"type"` // "input_text" | "input_image"
	Text string `json:"text,omitempty"`
	// ImageURL carries the image for an input_image part. Unlike Chat Completions
	// (where image_url is an object), the Responses API expects a plain data-URL string.
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"` // "auto" | "low" | "high"
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
	// ItemID and OutputIndex identify which output item a function-call event
	// belongs to. Responses may interleave argument deltas from parallel function
	// calls, so these discriminate them (item_id on delta/done events, item.id on
	// output_item.added; output_index is the fallback). See toolCallState keying.
	ItemID      string `json:"item_id,omitempty"`
	OutputIndex *int   `json:"output_index,omitempty"`
	// Message and Code are carried at the top level by the terminal "error" stream
	// event (distinct from response.failed, whose detail is nested under Error).
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

type responsesStreamErrBody struct {
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

type responsesResponse struct {
	// ID is the response identifier, present from response.created onward. It keys
	// the reasoning-item sidecar for the assistant turn.
	ID    string          `json:"id,omitempty"`
	Usage *responsesUsage `json:"usage,omitempty"`
	// Error carries the failure detail on a response.failed event (nested under
	// response.error, unlike the top-level "error" stream frame).
	Error *responsesStreamErrBody `json:"error,omitempty"`
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
	Type   string                       `json:"type"` // e.g. "function_call" | "reasoning"
	ID     string                       `json:"id,omitempty"`
	Name   string                       `json:"name,omitempty"`
	CallID string                       `json:"call_id,omitempty"`
	Output []responsesOutputItemContent `json:"output,omitempty"`
	// reasoning item fields (present on type=="reasoning" when the request opts in
	// via include=["reasoning.encrypted_content"]).
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	Summary          []responsesSummaryPart `json:"summary,omitempty"`
}

type responsesOutputItemContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
