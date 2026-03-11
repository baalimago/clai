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
	"slices"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

var dataPrefix = []byte("data: ")

// StreamCompletions taking the messages as prompt conversation. Returns the messages from the chat model.
func (s *StreamCompleter) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	if s.Clean != nil {
		cpy := make([]pub_models.Message, len(chat.Messages))
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
	if s.debug {
		ancli.Noticef("now attepmting to handle response")
	}
	outChan, err := s.handleStreamResponse(ctx, res)
	if err != nil {
		return outChan, fmt.Errorf("failed to handle stream response: %w", err)
	}

	return outChan, nil
}

func (s *StreamCompleter) createRequest(ctx context.Context, chat pub_models.Chat) (*http.Request, error) {
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
		StreamOptions: map[string]any{
			"include_usage": true,
		},
		ParallelToolCalls: len(s.tools) > 0,
	}
	if s.debug {
		ancli.PrintOK(fmt.Sprintf("streamcompleter api key: %v...\n", s.apiKey[:5]))
	}
	if len(s.tools) > 0 {
		reqData.Tools = s.tools
		reqData.ToolChoice = s.ToolChoice
	}
	if s.debug {
		noTools := reqData
		noTools.Tools = make([]ToolSuper, 0)
		ancli.PrintOK(fmt.Sprintf("generic streamcompleter request (tools redacted):\nurl: %v\nstruct: %v\n", s.URL, debug.IndentedJsonFmt(noTools)))
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.URL, bytes.NewBuffer(jsonData))
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
			_ = res.Body.Close()
			close(outChan)
		}()
		for {
			if ctx.Err() != nil {
				return
			}
			token, err := br.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					outChan <- fmt.Errorf("failed to read line: %w", err)
				}
				return
			}
			if s.debug {
				ancli.Okf("received data from model, len: '%v', content: '%s'", len(token), token)
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
		if s.debug {
			ancli.Okf("sending StopEvent due to [DONE]")
		}
		return models.StopEvent{}
	}

	if len(token) == 0 {
		return models.NoopEvent{}
	}

	if s.debug {
		ancli.PrintOK(fmt.Sprintf("token: %+v\n", string(token)))
	}
	var chunk chatCompletionChunk
	err := json.Unmarshal(token, &chunk)
	if err != nil {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintWarn(fmt.Sprintf("failed to unmarshal token: %s, err: %v\n", token, err))
			return models.NoopEvent{}
		}
	}

	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}

	if len(chunk.Choices) == 0 {
		return models.NoopEvent{}
	}

	var chosen models.CompletionEvent
	for _, choice := range chunk.Choices {
		compEvent := s.handleChoice(choice)
		switch compEvent.(type) {
		case error, string, models.NoopEvent:
			_, isNoopEvent := chosen.(models.NoopEvent)
			if chosen == nil || isNoopEvent {
				chosen = compEvent
			}
		case pub_models.Call, models.ToolCallsEvent:
			chosen = compEvent
		}
	}

	if s.debug {
		ancli.PrintOK(fmt.Sprintf("chosen: %T -  %+v\n", chosen, chosen))
	}

	return chosen
}

func (s *StreamCompleter) handleChoice(choice Choice) models.CompletionEvent {
	if len(choice.Delta.ToolCalls) > 0 {
		for _, callChunk := range choice.Delta.ToolCalls {
			s.mergeToolCallChunk(callChunk)
		}
	}
	if choice.FinishReason == "tool_calls" {
		return s.flushToolsCallBatch()
	}
	if len(choice.Delta.ToolCalls) == 0 {
		if choice.FinishReason != "" {
			if s.debug {
				ancli.Noticef("stopping due to FinishReason: '%v'", choice.FinishReason)
			}
			return models.StopEvent{}
		}
		return choice.Delta.Content
	}
	if !hasIncompleteToolCallJSON(s.toolCalls) {
		return s.flushToolsCallBatch()
	}
	return models.NoopEvent{}
}

func hasIncompleteToolCallJSON(toolCalls map[int]*toolCallAssembly) bool {
	for _, assembly := range toolCalls {
		var input pub_models.Input
		if err := json.Unmarshal([]byte(assembly.Arguments), &input); err != nil {
			return true
		}
	}
	return false
}

func (s *StreamCompleter) mergeToolCallChunk(callChunk ToolsCall) {
	if s.toolCalls == nil {
		s.toolCalls = make(map[int]*toolCallAssembly)
	}
	idx := max(callChunk.Index, 0)
	assembly, exists := s.toolCalls[idx]
	if !exists {
		assembly = &toolCallAssembly{Index: idx}
		s.toolCalls[idx] = assembly
	}
	if callChunk.ID != "" {
		assembly.ID = callChunk.ID
	}
	if callChunk.Type != "" {
		assembly.Type = callChunk.Type
	}
	if callChunk.Function.Name != "" {
		assembly.Name = callChunk.Function.Name
	}
	if callChunk.Function.Arguments != "" {
		assembly.Arguments += callChunk.Function.Arguments
	}
	if callChunk.ExtraContent != nil {
		assembly.ExtraContent = callChunk.ExtraContent
	}
}

func (s *StreamCompleter) flushToolsCallBatch() models.CompletionEvent {
	if len(s.toolCalls) == 0 {
		return models.StopEvent{}
	}
	indices := make([]int, 0, len(s.toolCalls))
	for idx := range s.toolCalls {
		indices = append(indices, idx)
	}
	slices.Sort(indices)

	calls := make([]pub_models.Call, 0, len(indices))
	for _, idx := range indices {
		call, err := s.assembleToolCall(*s.toolCalls[idx])
		if err != nil {
			return err
		}
		calls = append(calls, call)
	}
	s.toolCalls = nil
	return models.ToolCallsEvent{Calls: calls}
}

func (s *StreamCompleter) assembleToolCall(assembly toolCallAssembly) (pub_models.Call, error) {
	var input pub_models.Input
	err := json.Unmarshal([]byte(assembly.Arguments), &input)
	if err != nil {
		return pub_models.Call{}, fmt.Errorf("failed to unmarshal argument string for index %d: %w", assembly.Index, err)
	}

	userFunc := tools.ToolFromName(assembly.Name)
	userFunc.Arguments = assembly.Arguments
	userFunc.Inputs = &pub_models.InputSchema{}

	return pub_models.Call{
		ID:           assembly.ID,
		Name:         assembly.Name,
		Inputs:       &input,
		Type:         "function",
		Function:     userFunc,
		ExtraContent: assembly.ExtraContent,
	}, nil
}

// heuristicTokenCountFactor is used to approximate token usage when
// the vendor does not expose an endpoint for counting tokens.
const heuristicTokenCountFactor = 1.1

// CountInputTokens estimates the amount of input tokens in the chat.
func (s *StreamCompleter) CountInputTokens(ctx context.Context, chat pub_models.Chat) (int, error) {
	var count int
	for _, m := range chat.Messages {
		count += len(strings.Split(m.Content, " "))
	}
	return int(float64(count) * heuristicTokenCountFactor), nil
}

func (s *StreamCompleter) TokenUsage() *pub_models.Usage {
	return s.usage
}
