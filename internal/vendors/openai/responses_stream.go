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
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

var responsesDataPrefix = []byte("data: ")

type responsesStreamer struct {
	apiKey       string
	url          string
	model        string
	debug        bool
	client       *http.Client
	usageSetter  func(*pub_models.Usage) error

	tools []responsesTool
}

type toolCallState struct {
	callID     string
	toolName   string
	argsBuf    bytes.Buffer
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

func (st *toolCallState) emitCall(out chan<- models.CompletionEvent) error {
	if st.callEmitted {
		return nil
	}
	if st.toolName == "" {
		return fmt.Errorf("missing tool name for call_id=%q", st.callID)
	}
	if st.callID == "" {
		return fmt.Errorf("missing call id for tool=%q", st.toolName)
	}

	var input pub_models.Input
	if err := json.Unmarshal(st.argsBuf.Bytes(), &input); err != nil {
		return fmt.Errorf("unmarshal tool args for tool=%q call_id=%q: %w", st.toolName, st.callID, err)
	}

	userFunc := tools.ToolFromName(st.toolName)
	if userFunc.Name == "" {
		return fmt.Errorf("resolve tool from name %q: %w", st.toolName, fmt.Errorf("tool not found"))
	}
	userFunc.Arguments = st.argsBuf.String()

	out <- pub_models.Call{
		ID:       st.callID,
		Name:     st.toolName,
		Inputs:   &input,
		Type:     "function",
		Function: userFunc,
	}
	st.callEmitted = true
	st.reset()
	return nil
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
	var st toolCallState

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

		if err := handleResponsesStreamEvent(out, &st, evt, s.usageSetter); err != nil {
			out <- fmt.Errorf("openai responses: handle event %q: %w", evt.Type, err)
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

func handleResponsesStreamEvent(out chan<- models.CompletionEvent, st *toolCallState, evt responsesStreamEvent, usageSetter func(*pub_models.Usage) error) error {
	switch evt.Type {
	case "response.output_text.delta":
		return emitTextDelta(out, evt.Delta)

	case "response.output_item.added":
		return handleOutputItemAdded(out, st, evt.Item)

	case "response.function_call_arguments.delta":
		return handleFunctionCallArgumentsDelta(out, st, evt.Delta)

	case "response.function_call_arguments.done":
		return st.emitCall(out)

	case "response.completed":
		if err := maybeSetUsage(evt, usageSetter); err != nil {
			return fmt.Errorf("set usage: %w", err)
		}
		out <- models.StopEvent{}
		return nil

	case "response.failed":
		msg := "response failed"
		if evt.Error != nil && evt.Error.Message != "" {
			msg = evt.Error.Message
		}
		return fmt.Errorf("%s", msg)

	default:
		out <- models.NoopEvent{}
		return nil
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

func handleOutputItemAdded(out chan<- models.CompletionEvent, st *toolCallState, item *responsesOutputItem) error {
	if item == nil {
		out <- models.NoopEvent{}
		return nil
	}
	if item.Type != "function_call" {
		out <- models.NoopEvent{}
		return nil
	}

	st.beginFromItem(item)
	out <- models.NoopEvent{}
	return nil
}

func handleFunctionCallArgumentsDelta(out chan<- models.CompletionEvent, st *toolCallState, delta string) error {
	if delta == "" {
		out <- models.NoopEvent{}
		return nil
	}

	if err := st.appendArgs(delta); err != nil {
		return fmt.Errorf("buffer tool args: %w", err)
	}
	return nil
}

func (s *responsesStreamer) createRequest(ctx context.Context, chat pub_models.Chat) (*http.Request, error) {
	input, err := mapChatToResponsesInput(chat)
	if err != nil {
		return nil, fmt.Errorf("map chat to responses input: %w", err)
	}

	reqBody := responsesRequest{
		Model:  s.model,
		Input:  input,
		Stream: true,
	}
	if len(s.tools) > 0 {
		reqBody.Tools = s.tools
		reqBody.ToolChoice = "auto"
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

func mapChatToResponsesInput(chat pub_models.Chat) ([]responsesInputItem, error) {
	out := make([]responsesInputItem, 0, len(chat.Messages))
	for _, msg := range chat.Messages {
		// NOTE: Unlike the legacy ChatCompletions API, Responses expects the full
		// conversation in `input`, including assistant messages that contain tool calls.
		// If we drop assistant tool calls, subsequent `function_call_output` items will
		// fail with: "No tool call found for function call output with call_id ...".
		items, err := mapMessageToResponsesInputItems(msg)
		if err != nil {
			return nil, fmt.Errorf("map message role %q: %w", msg.Role, err)
		}
		out = append(out, items...)
	}
	return out, nil
}

func mapMessageToResponsesInputItems(msg pub_models.Message) ([]responsesInputItem, error) {
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
		out := make([]responsesInputItem, 0, 1+len(msg.ToolCalls))

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
	contentType := "input_text"

	if len(msg.ContentParts) == 0 {
		return []responsesInputContent{{Type: contentType, Text: msg.Content}}, nil
	}

	out := make([]responsesInputContent, 0, len(msg.ContentParts))
	for _, cp := range msg.ContentParts {
		if cp.Text != "" {
			out = append(out, responsesInputContent{Type: contentType, Text: cp.Text})
			continue
		}
		return nil, fmt.Errorf("unsupported content part: type=%q", cp.Type)
	}
	if len(out) == 0 {
		return []responsesInputContent{{Type: contentType, Text: ""}}, nil
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
