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
	apiKey string
	url    string
	model  string
	debug  bool
	client *http.Client

	tools []responsesTool
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

	if res.StatusCode != http.StatusOK {
		defer func() { _ = res.Body.Close() }()
		body, readErr := io.ReadAll(res.Body)
		if readErr != nil {
			return nil, fmt.Errorf("openai responses: read error body: %w", readErr)
		}
		return nil, fmt.Errorf("openai responses: unexpected status code %v, body: %s", res.Status, string(body))
	}

	out := make(chan models.CompletionEvent)
	go func() {
		defer func() {
			_ = res.Body.Close()
			close(out)
		}()

		br := bufio.NewReader(res.Body)

		var currentCallID string
		var currentToolName string
		var argsBuf bytes.Buffer
		var callEmitted bool

		emitCall := func() error {
			if callEmitted {
				return nil
			}
			if currentToolName == "" {
				return fmt.Errorf("missing tool name for call_id=%q", currentCallID)
			}
			if currentCallID == "" {
				return fmt.Errorf("missing call id for tool=%q", currentToolName)
			}

			var input pub_models.Input
			if err := json.Unmarshal(argsBuf.Bytes(), &input); err != nil {
				return fmt.Errorf("unmarshal tool args for tool=%q call_id=%q: %w", currentToolName, currentCallID, err)
			}

			userFunc := tools.ToolFromName(currentToolName)
			if userFunc.Name == "" {
				return fmt.Errorf("resolve tool from name %q: %w", currentToolName, fmt.Errorf("tool not found"))
			}
			userFunc.Arguments = argsBuf.String()

			out <- pub_models.Call{
				ID:       currentCallID,
				Name:     currentToolName,
				Inputs:   &input,
				Type:     "function",
				Function: userFunc,
			}
			callEmitted = true

			currentCallID = ""
			currentToolName = ""
			argsBuf.Reset()
			return nil
		}

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

			evt, ok, parseErr := parseResponsesLine(line)
			if parseErr != nil {
				out <- fmt.Errorf("openai responses: parse stream event: %w", parseErr)
				return
			}
			if !ok {
				continue
			}

			switch evt.Type {
			case "response.output_text.delta":
				if evt.Delta != "" {
					out <- evt.Delta
				} else {
					out <- models.NoopEvent{}
				}

			case "response.output_item.added":
				if evt.Item == nil {
					out <- models.NoopEvent{}
					continue
				}
				if evt.Item.Type != "function_call" {
					out <- models.NoopEvent{}
					continue
				}

				// A new tool call is starting. Reset any previous in-flight state.
				currentCallID = ""
				currentToolName = ""
				argsBuf.Reset()
				callEmitted = false

				if evt.Item.CallID != "" {
					currentCallID = evt.Item.CallID
				} else if evt.Item.ID != "" {
					currentCallID = evt.Item.ID
				}
				if evt.Item.Name != "" {
					currentToolName = evt.Item.Name
				}
				out <- models.NoopEvent{}

			case "response.function_call_arguments.delta":
				if evt.Delta == "" {
					out <- models.NoopEvent{}
					continue
				}
				if _, wErr := argsBuf.WriteString(evt.Delta); wErr != nil {
					out <- fmt.Errorf("openai responses: buffer tool args: %w", wErr)
					return
				}

			case "response.function_call_arguments.done":
				if emitErr := emitCall(); emitErr != nil {
					out <- fmt.Errorf("openai responses: emit tool call: %w", emitErr)
					return
				}

			case "response.completed":
				out <- models.StopEvent{}

			case "response.failed":
				msg := "response failed"
				if evt.Error != nil && evt.Error.Message != "" {
					msg = evt.Error.Message
				}
				out <- fmt.Errorf("openai responses: %s", msg)
				return

			default:
				out <- models.NoopEvent{}
			}
		}
	}()

	return out, nil
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
		noTools.Tools = nil
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
