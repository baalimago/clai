package generic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

var dataPrefix = []byte("data: ")

// streamCompletions taking the messages as prompt conversation. Returns the messages from the chat model.
func (s *StreamCompleter) StreamCompletions(ctx context.Context, chat models.Chat) (chan models.CompletionEvent, error) {
	if s.Clean != nil {
		cpy := make([]models.Message, len(chat.Messages))
		copy(cpy, chat.Messages)
		chat.Messages = s.Clean(cpy)
	}
	req, err := s.createRequest(ctx, chat)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("unexpected status code: %v, body: %v", res.Status, string(body))
	}
	outChan, err := s.handleStreamResponse(ctx, res)
	if err != nil {
		return outChan, fmt.Errorf("failed to handle stream response: %w", err)
	}

	return outChan, nil
}

func (s *StreamCompleter) createRequest(ctx context.Context, chat models.Chat) (*http.Request, error) {
	reqData := req{
		Model:            s.Model,
		FrequencyPenalty: s.FrequencyPenalty,
		MaxTokens:        s.MaxTokens,
		PresencePenalty:  s.PresencePenalty,
		Temperature:      s.Temperature,
		TopP:             s.TopP,
		ResponseFormat:   responseFormat{Type: "text"},
		Messages:         chat.Messages,
		Stream:           true,
		// No support for this yet since it's limited usecase and high complexity
		ParalellToolCalls: false,
	}
	if s.debug {
		ancli.PrintOK(fmt.Sprintf("streamcompleter: %v\n", debug.IndentedJsonFmt(s)))
	}
	if len(s.tools) > 0 {
		reqData.Tools = s.tools
		reqData.ToolChoice = s.ToolChoice
	}
	if s.debug {
		ancli.PrintOK(fmt.Sprintf("generic streamcompleter request: %v\n", debug.IndentedJsonFmt(reqData)))
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", s.apiKey))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")
	return req, nil
}

func (s *StreamCompleter) handleStreamResponse(ctx context.Context, res *http.Response) (chan models.CompletionEvent, error) {
	outChan := make(chan models.CompletionEvent)
	go func() {
		br := bufio.NewReader(res.Body)
		defer func() {
			res.Body.Close()
			close(outChan)
		}()
		for {
			if ctx.Err() != nil {
				close(outChan)
				return
			}
			token, err := br.ReadBytes('\n')
			if err != nil {
				outChan <- fmt.Errorf("failed to read line: %w", err)
			}
			outChan <- s.handleStreamChunk(token)
		}
	}()

	return outChan, nil
}

func (s *StreamCompleter) handleStreamChunk(token []byte) models.CompletionEvent {
	token = bytes.TrimPrefix(token, dataPrefix)
	token = bytes.TrimSpace(token)
	if string(token) == "[DONE]" {
		return models.NoopEvent{}
	}

	if s.debug {
		ancli.PrintOK(fmt.Sprintf("token: %+v\n", string(token)))
	}
	var chunk chatCompletionChunk
	err := json.Unmarshal(token, &chunk)
	if err != nil {
		if misc.Truthy(os.Getenv("DEBUG")) {
			// Expect some failing unmarshalls, which seems to be fine
			ancli.PrintWarn(fmt.Sprintf("failed to unmarshal token: %v, err: %v\n", token, err))
			return models.NoopEvent{}
		}
	}
	if len(chunk.Choices) == 0 {
		return models.NoopEvent{}
	}

	var chosen models.CompletionEvent
	for _, choice := range chunk.Choices {
		compEvent := s.handleChoice(choice)
		switch compEvent.(type) {
		// Set chosen to the first error, string
		case error, string, models.NoopEvent:
			_, isNoopEvent := chosen.(models.NoopEvent)
			if chosen == nil || isNoopEvent {
				chosen = compEvent
			}
		case tools.Call:
			// Always prefer tools call, if possible
			chosen = compEvent
		}
	}

	if s.debug {
		ancli.PrintOK(fmt.Sprintf("chosen: %T -  %+v\n", chosen, chosen))
	}
	return chosen
}

func (s *StreamCompleter) handleChoice(choice Choice) models.CompletionEvent {
	// If there is no tools call, just handle it as a strings. This works for most cases
	if len(choice.Delta.ToolCalls) == 0 && choice.FinishReason != "tool_calls" {
		return choice.Delta.Content
	}

	// Function name is only shown in first chunk of a functions call
	// TODO: Implement support for parallel function calls, now we only handle first tools call in list
	var argChunk string
	if len(choice.Delta.ToolCalls) > 0 && choice.Delta.ToolCalls[0].Function.Name != "" {
		s.toolsCallName = choice.Delta.ToolCalls[0].Function.Name
		s.toolsCallID = choice.Delta.ToolCalls[0].ID
	}

	if len(choice.Delta.ToolCalls) > 0 {
		argChunk = choice.Delta.ToolCalls[0].Function.Arguments
		// The arguments is streamed as a stringified json for chatgpt, chunk by chunk, with no apparent structure
		s.toolsCallArgsString += argChunk

		if s.debug {
			ancli.PrintOK(fmt.Sprintf("toolsCallArgsString: %v\n", s.toolsCallArgsString))
		}
		var input tools.Input
		err := json.Unmarshal([]byte(s.toolsCallArgsString), &input)
		if err == nil {
			return s.doToolsCall()
		}
	}
	return models.NoopEvent{}
}

// doToolsCall by parsing the arguments
func (s *StreamCompleter) doToolsCall() models.CompletionEvent {
	defer func() {
		// Reset tools call construction strings to prepare for consequtive calls
		s.toolsCallName = ""
		s.toolsCallArgsString = ""
	}()
	var input tools.Input
	err := json.Unmarshal([]byte(s.toolsCallArgsString), &input)
	if err != nil {
		return fmt.Errorf("failed to unmarshal argument string: %w, argsString: %v", err, s.toolsCallArgsString)
	}

	userFunc := tools.ToolFromName(s.toolsCallName)
	userFunc.Arguments = s.toolsCallArgsString
	userFunc.Inputs = &tools.InputSchema{}

	return tools.Call{
		ID:       s.toolsCallID,
		Name:     s.toolsCallName,
		Inputs:   &input,
		Type:     "function",
		Function: userFunc,
	}
}

// heuristicTokenCountFactor is used to approximate token usage when
// the vendor does not expose an endpoint for counting tokens.
const heuristicTokenCountFactor = 1.1

// CountInputTokens estimates the amount of input tokens in the chat.
func (s *StreamCompleter) CountInputTokens(ctx context.Context, chat models.Chat) (int, error) {
	var count int
	for _, m := range chat.Messages {
		count += len(strings.Split(m.Content, " "))
	}
	return int(float64(count) * heuristicTokenCountFactor), nil
}
