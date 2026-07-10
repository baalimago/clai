package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

var responsesDataPrefix = []byte("data: ")

type responsesStreamer struct {
	apiKey      string
	url         string
	model       string
	debug       bool
	client      *http.Client
	usageSetter func(*pub_models.Usage) error

	tools []responsesTool

	// responseFormat, when set, configures structured output via text.format.
	responseFormat *generic.ResponseFormat
	// temperature and topP are forwarded only when non-nil. Reasoning models reject
	// them, so callers omit them for those models.
	temperature *float64
	topP        *float64
	// maxOutputTokens maps to the Responses max_output_tokens field when non-nil.
	maxOutputTokens *int
	// reasoningEffort configures reasoning.effort for reasoning models. Empty means
	// "let the API pick" (default medium); it is ignored for non-reasoning models.
	reasoningEffort string
}

type toolCallState struct {
	callID      string
	toolName    string
	argsBuf     bytes.Buffer
	callEmitted bool
}

func (st *toolCallState) reset() {
	st.callID = ""
	st.toolName = ""
	st.argsBuf.Reset()
	st.callEmitted = false
}

func (st *toolCallState) beginFromItem(item *responsesOutputItem) {
	st.reset()
	if item == nil {
		return
	}
	if item.CallID != "" {
		st.callID = item.CallID
	} else if item.ID != "" {
		st.callID = item.ID
	}
	if item.Name != "" {
		st.toolName = item.Name
	}
}

func (st *toolCallState) appendArgs(delta string) error {
	if delta == "" {
		return nil
	}
	if _, err := st.argsBuf.WriteString(delta); err != nil {
		return fmt.Errorf("write args delta: %w", err)
	}
	return nil
}

// emitCall sends the normalized tool call. Preceding reasoning items ride on the
// Call so the assistant message can persist them for stateless continuity.
func (st *toolCallState) emitCall(out chan<- models.CompletionEvent, reasoning []pub_models.ReasoningItem) error {
	if st.callEmitted {
		return nil
	}
	if st.toolName == "" {
		return fmt.Errorf("missing tool name for call_id=%q", st.callID)
	}
	if st.callID == "" {
		return fmt.Errorf("missing call id for tool=%q", st.toolName)
	}

	// A zero-argument tool call may arrive with no argument deltas at all; treat an
	// empty buffer as an empty JSON object rather than failing to unmarshal "".
	argsStr := st.argsBuf.String()
	if len(bytes.TrimSpace(st.argsBuf.Bytes())) == 0 {
		argsStr = "{}"
	}

	var input pub_models.Input
	if err := json.Unmarshal([]byte(argsStr), &input); err != nil {
		return fmt.Errorf("unmarshal tool args for tool=%q call_id=%q: %w", st.toolName, st.callID, err)
	}

	// The emitted call's identity is the tool name the model chose off the wire, and
	// the model can only choose from the profile-filtered set advertised to it
	// (mapResponsesTools over the querier's toolBox — that IS where profile filtering
	// is enforced). ToolFromName is only a spec enrichment against the global
	// registry, exactly as the generic Chat Completions path does (doToolsCall); it is
	// NOT a profile gate. Lookback tools (search_conversations/inspect_conversation/
	// read_message) are dispatched internally by name and never registered globally,
	// so the lookup misses by design (load_skill is likewise dispatched by name and is
	// absent from the global registry unless -t tools ran tools.Init). Match every other
	// vendor and never abort on a miss: keep the wire name (Call.Patch back-fills
	// Function.Name) and let the executor dispatch by Call.Name. An unadvertised or
	// hallucinated name then degrades to a recoverable "ERROR: unknown tool call"
	// result, never a dead stream.
	userFunc := tools.ToolFromName(st.toolName)
	if userFunc.Name == "" {
		userFunc.Name = st.toolName
	}
	userFunc.Arguments = argsStr

	out <- pub_models.Call{
		ID:             st.callID,
		Name:           st.toolName,
		Inputs:         &input,
		Type:           "function",
		Function:       userFunc,
		ReasoningItems: reasoning,
	}
	st.callEmitted = true
	st.reset()
	return nil
}

// toolCallTracker keeps per-item tool-call state keyed by the stream's item
// discriminator. Responses may interleave argument deltas from several parallel
// function calls, so a single shared state would let one call's output_item.added
// reset another's buffered arguments and mis-attribute later deltas.
//
// It also accumulates reasoning items so that an emitted tool call can carry them
// forward for stateless reasoning continuity.
type toolCallTracker struct {
	states         map[string]*toolCallState
	reasoningItems []pub_models.ReasoningItem
}

func newToolCallTracker() *toolCallTracker {
	return &toolCallTracker{states: make(map[string]*toolCallState)}
}

// state returns the tool-call state for key, creating a fresh one if absent.
func (t *toolCallTracker) state(key string) *toolCallState {
	st, ok := t.states[key]
	if !ok {
		st = &toolCallState{}
		t.states[key] = st
	}
	return st
}

// drop discards the state for key once its call has been emitted.
func (t *toolCallTracker) drop(key string) {
	delete(t.states, key)
}

// callKey returns the discriminator identifying which function call an event
// belongs to. Responses references items by item_id on delta/done events and by
// item.id on output_item.added; output_index is a fallback when neither id is set.
func (evt responsesStreamEvent) callKey() string {
	if evt.ItemID != "" {
		return evt.ItemID
	}
	if evt.Item != nil && evt.Item.ID != "" {
		return evt.Item.ID
	}
	if evt.OutputIndex != nil {
		return fmt.Sprintf("idx:%d", *evt.OutputIndex)
	}
	return ""
}

func (s *responsesStreamer) stream(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	req, err := s.createRequest(ctx, chat)
	if err != nil {
		return nil, fmt.Errorf("openai responses: create request: %w", err)
	}

	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai responses: do request: %w", err)
	}

	if err := validateResponsesHTTPResponse(res); err != nil {
		return nil, fmt.Errorf("openai responses: validate response: %w", err)
	}

	out := make(chan models.CompletionEvent)
	go s.readResponsesStream(ctx, res.Body, out)
	return out, nil
}

func validateResponsesHTTPResponse(res *http.Response) error {
	if res.StatusCode == http.StatusOK {
		return nil
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("read error body: %w", err)
	}
	return fmt.Errorf("unexpected status code %v, body: %s", res.Status, string(body))
}

func (s *responsesStreamer) readResponsesStream(ctx context.Context, body io.ReadCloser, out chan models.CompletionEvent) {
	defer func() {
		_ = body.Close()
		close(out)
	}()

	br := bufio.NewReader(body)
	tracker := newToolCallTracker()

	for {
		if ctx.Err() != nil {
			return
		}

		line, err := br.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				out <- fmt.Errorf("openai responses: read stream line: %w", err)
			}
			return
		}

		evt, ok, err := s.parseStreamLine(line)
		if err != nil {
			out <- fmt.Errorf("openai responses: parse stream event: %w", err)
			return
		}
		if !ok {
			continue
		}

		done, err := handleResponsesStreamEvent(out, tracker, evt, s.usageSetter)
		if err != nil {
			out <- fmt.Errorf("openai responses: handle event %q: %w", evt.Type, err)
			return
		}
		if done {
			// Terminal event handled; stop reading so a trailing [DONE] (or any
			// further frames) cannot trigger a second, blocking StopEvent send.
			return
		}
	}
}

func (s *responsesStreamer) parseStreamLine(line []byte) (responsesStreamEvent, bool, error) {
	if s.debug {
		ancli.Okf("got: '%s'", line)
	}

	evt, ok, err := parseResponsesLine(line)
	if err != nil {
		return responsesStreamEvent{}, false, fmt.Errorf("parse: %w", err)
	}
	return evt, ok, nil
}

// handleResponsesStreamEvent processes a single stream event, writing normalized
// events to out. It returns done=true once a terminal event (response.completed or
// a mapped [DONE]) has been handled so the reader can stop.
func handleResponsesStreamEvent(out chan<- models.CompletionEvent, tracker *toolCallTracker, evt responsesStreamEvent, usageSetter func(*pub_models.Usage) error) (bool, error) {
	switch evt.Type {
	case "response.output_text.delta":
		return false, emitTextDelta(out, evt.Delta)

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return false, emitReasoningDelta(out, evt.Delta)

	case "response.created", "response.in_progress":
		out <- models.NoopEvent{}
		return false, nil

	case "response.output_item.added":
		return false, handleOutputItemAdded(out, tracker, evt)

	case "response.output_item.done":
		return false, handleOutputItemDone(out, tracker, evt)

	case "response.function_call_arguments.delta":
		return false, handleFunctionCallArgumentsDelta(out, tracker, evt)

	case "response.function_call_arguments.done":
		return false, handleFunctionCallArgumentsDone(out, tracker, evt)

	case "response.completed", "response.incomplete":
		// response.incomplete is emitted instead of response.completed when the
		// output was truncated (e.g. max_output_tokens or content filter). It is
		// terminal and carries the final usage in the same shape.
		if err := maybeSetUsage(evt, usageSetter); err != nil {
			return false, fmt.Errorf("set usage: %w", err)
		}
		out <- models.StopEvent{}
		return true, nil

	case "response.failed":
		// The actionable API error is nested under response.error; the top-level
		// Error is only a compatibility fallback.
		msg := "response failed"
		switch {
		case evt.Response != nil && evt.Response.Error != nil && evt.Response.Error.Message != "":
			msg = evt.Response.Error.Message
		case evt.Error != nil && evt.Error.Message != "":
			msg = evt.Error.Message
		}
		return false, fmt.Errorf("%s", msg)

	case "error", "response.error":
		// Terminal top-level stream error. Its detail is carried at the top level
		// (message/code), unlike response.failed which nests it under Error. Surface
		// it as an error so the consumer aborts instead of ending on a silent EOF.
		msg := "openai responses stream error"
		switch {
		case evt.Message != "":
			msg = evt.Message
		case evt.Error != nil && evt.Error.Message != "":
			msg = evt.Error.Message
		}
		return false, fmt.Errorf("%s", msg)

	default:
		out <- models.NoopEvent{}
		return false, nil
	}
}

func maybeSetUsage(evt responsesStreamEvent, usageSetter func(*pub_models.Usage) error) error {
	if usageSetter == nil || evt.Response == nil || evt.Response.Usage == nil {
		return nil
	}
	mapped := mapUsage(evt.Response.Usage)
	if err := usageSetter(mapped); err != nil {
		return fmt.Errorf("usage setter: %w", err)
	}
	return nil
}

func mapUsage(u *responsesUsage) *pub_models.Usage {
	if u == nil {
		return nil
	}
	out := &pub_models.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.InputTokensDetails != nil {
		out.PromptTokensDetails = pub_models.PromptTokensDetails{
			CachedTokens: u.InputTokensDetails.CachedTokens,
			AudioTokens:  u.InputTokensDetails.AudioTokens,
		}
	}
	if u.OutputTokensDetails != nil {
		out.CompletionTokensDetails = pub_models.CompletionTokensDetails{
			ReasoningTokens:          u.OutputTokensDetails.ReasoningTokens,
			AudioTokens:              u.OutputTokensDetails.AudioTokens,
			AcceptedPredictionTokens: u.OutputTokensDetails.AcceptedPredictionTokens,
			RejectedPredictionTokens: u.OutputTokensDetails.RejectedPredictionTokens,
		}
	}
	return out
}

func emitTextDelta(out chan<- models.CompletionEvent, delta string) error {
	if delta != "" {
		out <- delta
		return nil
	}
	out <- models.NoopEvent{}
	return nil
}

func emitReasoningDelta(out chan<- models.CompletionEvent, delta string) error {
	if delta != "" {
		out <- models.ReasoningEvent{Content: delta}
		return nil
	}
	out <- models.NoopEvent{}
	return nil
}

func handleOutputItemAdded(out chan<- models.CompletionEvent, tracker *toolCallTracker, evt responsesStreamEvent) error {
	item := evt.Item
	if item == nil || item.Type != "function_call" {
		out <- models.NoopEvent{}
		return nil
	}

	tracker.state(evt.callKey()).beginFromItem(item)
	out <- models.NoopEvent{}
	return nil
}

func handleFunctionCallArgumentsDelta(out chan<- models.CompletionEvent, tracker *toolCallTracker, evt responsesStreamEvent) error {
	if evt.Delta == "" {
		out <- models.NoopEvent{}
		return nil
	}

	if err := tracker.state(evt.callKey()).appendArgs(evt.Delta); err != nil {
		return fmt.Errorf("buffer tool args: %w", err)
	}
	return nil
}

func handleFunctionCallArgumentsDone(out chan<- models.CompletionEvent, tracker *toolCallTracker, evt responsesStreamEvent) error {
	key := evt.callKey()
	if err := tracker.state(key).emitCall(out, tracker.reasoningItems); err != nil {
		return err
	}
	tracker.drop(key)
	return nil
}

// handleOutputItemDone captures completed reasoning items (their id and sealed
// encrypted_content) so they can be replayed on the next turn. Non-reasoning items
// are a no-op here.
func handleOutputItemDone(out chan<- models.CompletionEvent, tracker *toolCallTracker, evt responsesStreamEvent) error {
	item := evt.Item
	if item == nil || item.Type != "reasoning" {
		out <- models.NoopEvent{}
		return nil
	}
	// A reasoning item with no encrypted_content is unusable for stateless replay
	// (store=false requires it; without it, replay would omit the field and 400).
	// Skip it rather than persist a dead item.
	if item.EncryptedContent == "" {
		out <- models.NoopEvent{}
		return nil
	}
	ri := pub_models.ReasoningItem{ID: item.ID, EncryptedContent: item.EncryptedContent}
	for _, s := range item.Summary {
		if s.Text != "" {
			ri.Summary = append(ri.Summary, s.Text)
		}
	}
	tracker.reasoningItems = append(tracker.reasoningItems, ri)
	out <- models.NoopEvent{}
	return nil
}

func (s *responsesStreamer) createRequest(ctx context.Context, chat pub_models.Chat) (*http.Request, error) {
	reasoning := isReasoningModel(s.model)
	// Reasoning items are opaque and OpenAI-reasoning-model-only, so only replay
	// them for reasoning models; other models (and, structurally, the Chat
	// Completions path) never receive them.
	input, err := mapChatToResponsesInput(chat, reasoning)
	if err != nil {
		return nil, fmt.Errorf("map chat to responses input: %w", err)
	}

	storeFalse := false
	reqBody := responsesRequest{
		Model:           s.model,
		Input:           input,
		Stream:          true,
		Text:            mapResponseFormat(s.responseFormat),
		Temperature:     s.temperature,
		TopP:            s.topP,
		MaxOutputTokens: s.maxOutputTokens,
		// clai holds full conversation state client-side, so opt out of server-side
		// retention (the Responses API otherwise defaults store=true).
		Store: &storeFalse,
	}
	// Reasoning models stream a reasoning summary only when the request opts in via
	// reasoning.summary; request it so the deltas render as [thinking]. Effort is
	// forwarded when configured, otherwise the API default is used. Also request the
	// encrypted reasoning items so they can be replayed next turn (store=false means
	// the server keeps nothing, so continuity has to travel with us).
	if reasoning {
		reqBody.Reasoning = &responsesReasoning{Summary: "auto"}
		if s.reasoningEffort != "" {
			reqBody.Reasoning.Effort = s.reasoningEffort
		}
		reqBody.Include = []string{"reasoning.encrypted_content"}
	}
	if len(s.tools) > 0 {
		reqBody.Tools = s.tools
		reqBody.ToolChoice = "auto"
		parallelToolCalls := true
		reqBody.ParallelToolCalls = &parallelToolCalls
	}

	if s.debug {
		noTools := reqBody
		ancli.PrintOK(fmt.Sprintf("openai responses request (tools redacted):\nurl: %s\nbody: %s\n", s.url, debug.IndentedJsonFmt(noTools)))
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("encode json: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.apiKey))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")
	return req, nil
}

// mapResponseFormat converts the generic response format into the Responses
// text.format payload. It returns nil for the default (text) format so the field
// is omitted from the request.
func mapResponseFormat(rf *generic.ResponseFormat) *responsesText {
	if rf == nil || rf.Type == "" || rf.Type == "text" {
		return nil
	}
	format := responsesTextFormat{Type: rf.Type}
	if rf.Type == "json_schema" && rf.JSONSchema != nil {
		format.Name = rf.JSONSchema.Name
		format.Description = rf.JSONSchema.Description
		format.Schema = rf.JSONSchema.Schema
		format.Strict = rf.JSONSchema.Strict
	}
	return &responsesText{Format: format}
}

func mapChatToResponsesInput(chat pub_models.Chat, includeReasoning bool) ([]responsesInputItem, error) {
	out := make([]responsesInputItem, 0, len(chat.Messages))
	for _, msg := range chat.Messages {
		// NOTE: Unlike the legacy ChatCompletions API, Responses expects the full
		// conversation in `input`, including assistant messages that contain tool calls.
		// If we drop assistant tool calls, subsequent `function_call_output` items will
		// fail with: "No tool call found for function call output with call_id ...".
		items, err := mapMessageToResponsesInputItems(msg, includeReasoning)
		if err != nil {
			return nil, fmt.Errorf("map message role %q: %w", msg.Role, err)
		}
		out = append(out, items...)
	}
	return out, nil
}

func mapMessageToResponsesInputItems(msg pub_models.Message, includeReasoning bool) ([]responsesInputItem, error) {
	switch msg.Role {
	case "tool":
		if msg.ToolCallID == "" {
			return nil, fmt.Errorf("map tool message: missing tool_call_id")
		}
		return []responsesInputItem{{
			Type:   "function_call_output",
			CallID: msg.ToolCallID,
			// Responses expects `output` to be a string.
			Output: msg.Content,
		}}, nil

	case "assistant":
		out := make([]responsesInputItem, 0, 1+len(msg.ReasoningItems)+len(msg.ToolCalls))

		// Reasoning items must precede the function calls they produced. Replaying
		// them (with their sealed encrypted_content) lets a reasoning model resume its
		// own chain-of-thought across a stateless tool loop. Only for reasoning models
		// on the Responses path — the items are opaque and OpenAI-specific.
		if includeReasoning {
			for _, ri := range msg.ReasoningItems {
				// summary must always be present as an array on a reasoning input
				// item (empty when we captured none), so build a non-nil slice and
				// attach it by pointer.
				summary := make([]responsesSummaryPart, 0, len(ri.Summary))
				for _, s := range ri.Summary {
					summary = append(summary, responsesSummaryPart{Type: "summary_text", Text: s})
				}
				out = append(out, responsesInputItem{
					Type:             "reasoning",
					ID:               ri.ID,
					EncryptedContent: ri.EncryptedContent,
					Summary:          &summary,
				})
			}
		}

		// If the assistant is describing tool calls, we must include the function_call items
		// so that later function_call_output items can reference them by call_id.
		for _, tc := range msg.ToolCalls {
			callID := tc.ID
			if callID == "" {
				return nil, fmt.Errorf("map assistant tool call: missing id")
			}
			name := tc.Name
			if name == "" {
				return nil, fmt.Errorf("map assistant tool call %q: missing name", callID)
			}

			out = append(out, responsesInputItem{
				Type:      "function_call",
				CallID:    callID,
				Name:      name,
				Arguments: tc.Function.Arguments,
			})
		}

		if msg.Content != "" {
			out = append(out, responsesInputItem{
				Type: "message",
				Role: "assistant",
				Content: []responsesInputContent{{
					Type: "output_text",
					Text: msg.Content,
				}},
			})
		}

		if len(out) == 0 {
			out = append(out, responsesInputItem{
				Type:    "message",
				Role:    "assistant",
				Content: []responsesInputContent{{Type: "output_text", Text: ""}},
			})
		}
		return out, nil
	}

	parts, err := mapMessageToResponsesContent(msg)
	if err != nil {
		return nil, fmt.Errorf("map content: %w", err)
	}
	return []responsesInputItem{{Type: "message", Role: msg.Role, Content: parts}}, nil
}

func mapMessageToResponsesContent(msg pub_models.Message) ([]responsesInputContent, error) {
	const textType = "input_text"

	if len(msg.ContentParts) == 0 {
		return []responsesInputContent{{Type: textType, Text: msg.Content}}, nil
	}

	out := make([]responsesInputContent, 0, len(msg.ContentParts))
	for _, cp := range msg.ContentParts {
		// Image part: the Responses API takes a plain data-URL string on image_url,
		// mirroring the vision support of the Chat Completions path.
		if cp.ImageB64 != nil {
			out = append(out, responsesInputContent{
				Type:     "input_image",
				ImageURL: cp.ImageB64.URL,
				Detail:   cp.ImageB64.Detail,
			})
			continue
		}
		if cp.Text != "" {
			out = append(out, responsesInputContent{Type: textType, Text: cp.Text})
			continue
		}
		return nil, fmt.Errorf("unsupported content part: type=%q", cp.Type)
	}
	if len(out) == 0 {
		return []responsesInputContent{{Type: textType, Text: ""}}, nil
	}
	return out, nil
}

func parseResponsesLine(line []byte) (responsesStreamEvent, bool, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return responsesStreamEvent{}, false, nil
	}

	if !bytes.HasPrefix(line, responsesDataPrefix) {
		return responsesStreamEvent{}, false, nil
	}

	payload := bytes.TrimSpace(bytes.TrimPrefix(line, responsesDataPrefix))
	if bytes.Equal(payload, []byte("[DONE]")) {
		return responsesStreamEvent{Type: "response.completed"}, true, nil
	}
	if len(payload) == 0 {
		return responsesStreamEvent{}, false, nil
	}

	var evt responsesStreamEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return responsesStreamEvent{}, false, fmt.Errorf("unmarshal: %w", err)
	}
	if evt.Type == "" {
		return responsesStreamEvent{}, false, nil
	}
	return evt, true, nil
}
