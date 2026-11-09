package generic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

var dataPrefix = []byte("data: ")

// maxReasoningContent caps the reasoning text accumulated on the completer
// between stream resets (StreamCompletions clears it). A looping model can
// stream reasoning tokens forever; without the cap the O(n²) string
// concatenation here balloons the heap until the process OOMs (kinoview
// production incident, 2026-08-11). Keeping the tail preserves the reasoning
// context that rides along with tool calls.
const maxReasoningContent = 1 << 20 // 1 MiB

// StreamCompletions taking the messages as prompt conversation. Returns the messages from the chat model.
func (s *StreamCompleter) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	s.ctx = ctx
	s.reasoningContent = s.reasoningContent[:0]
	if s.Clean != nil {
		cpy := make([]pub_models.Message, len(chat.Messages))
		copy(cpy, chat.Messages)
		chat.Messages = s.Clean(cpy)
	}
	req, err := s.createRequest(ctx, chat)
	if err != nil {
		return nil, diag.Errorf("failed to create request: %w", err)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, claierr.NewTransport(err)
	}
	if res.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(res.Body)
		closeErr := res.Body.Close()
		responseErr := ResponseError(res.StatusCode, body, s.DecodeError)
		if readErr != nil {
			responseErr = errors.Join(responseErr, diag.Errorf("read provider error response body: %w", readErr))
		}
		if closeErr != nil {
			responseErr = errors.Join(responseErr, diag.Errorf("close provider error response body: %w", closeErr))
		}
		return nil, responseErr
	}
	s.debugf("now attepmting to handle response")
	outChan, err := s.handleStreamResponse(ctx, res)
	if err != nil {
		return outChan, diag.Errorf("failed to handle stream response: %w", err)
	}

	return outChan, nil
}

func (s *StreamCompleter) createRequest(ctx context.Context, chat pub_models.Chat) (*http.Request, error) {
	respFmt := ResponseFormat{Type: "text"}
	if s.ResponseFormat != nil {
		respFmt = *s.ResponseFormat
	}
	if err := models.CheckImageParts(s.Images, s.Vendor, s.Model, chat.Messages); err != nil {
		return nil, err
	}
	wireMsgs, err := toWireMessages(chat.Messages, s.Images)
	if err != nil {
		return nil, diag.Errorf("project messages to wire shape: %w", err)
	}
	reqData := req{
		Model:            s.Model,
		FrequencyPenalty: s.FrequencyPenalty,
		MaxTokens:        s.MaxTokens,
		PresencePenalty:  s.PresencePenalty,
		Temperature:      s.Temperature,
		TopP:             s.TopP,
		ReasoningEffort:  s.ReasoningEffort,
		ResponseFormat:   respFmt,
		Messages:         wireMsgs,
		Stream:           true,
		StreamOptions: map[string]any{
			"include_usage": true,
		},
		// No support for this yet since it's limited usecase and high complexity
		ParalellToolCalls: false,
		Modalities:        s.OutputModalities,
	}
	// No part of the key is shown: diagnostics reach callers' Reporters.
	s.debugf("streamcompleter api key: set, %d characters", len(s.apiKey))
	if len(s.tools) > 0 {
		reqData.Tools = s.tools
		reqData.ToolChoice = s.ToolChoice
	}
	if s.debugging() {
		noTools := reqData
		noTools.Tools = make([]ToolSuper, 0)
		s.debugf("generic streamcompleter request (tools redacted):\nurl: %v\nstruct: %v", s.URL, debug.IndentedJsonFmt(noTools))
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, diag.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, diag.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", s.apiKey))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")
	for key, value := range s.ExtraHeaders {
		req.Header.Set(key, value)
	}
	return req, nil
}

func (s *StreamCompleter) handleStreamResponse(ctx context.Context, res *http.Response) (chan models.CompletionEvent, error) {
	outChan := make(chan models.CompletionEvent)
	s.stream = streamState{}
	go func() {
		br := bufio.NewReader(res.Body)
		defer func() {
			res.Body.Close()
			close(outChan)
		}()
		if slices.Contains(s.OutputModalities, "image") {
			select {
			case outChan <- models.ImageProgressEvent{}:
			case <-ctx.Done():
				return
			}
		}
		for {
			if ctx.Err() != nil {
				return
			}
			token, err := br.ReadBytes('\n')
			// The excerpt only describes a body that is no stream; once an
			// event decoded nothing of the body is kept.
			if s.stream.sawData {
				s.stream.excerpt.head = nil
			} else {
				s.stream.excerpt.add(token)
			}
			if err != nil && err != io.EOF {
				select {
				case outChan <- claierr.NewTransport(err):
				case <-ctx.Done():
				}
				return
			}
			s.debugf("received data from model, len: '%v', content: '%s'", len(token), token)
			if len(token) > 0 && s.forwardChunk(ctx, outChan, token) == streamEnded {
				return
			}
			if err == io.EOF {
				s.endStream(ctx, outChan)
				return
			}
		}
	}()

	return outChan, nil
}

type streamProgress int

const (
	streamContinues streamProgress = iota
	streamEnded
)

// forwardChunk sends the events of one stream line: the vendor's DecodeChunk events
// first, then the shared decoder's event.
func (s *StreamCompleter) forwardChunk(ctx context.Context, out chan<- models.CompletionEvent, token []byte) streamProgress {
	events, err := s.vendorEvents(token)
	if err != nil {
		events = []models.CompletionEvent{err}
	} else {
		shared := s.handleStreamChunk(token)
		events = append(append(events, s.stream.takeCalls()...), shared)
	}
	for _, evt := range events {
		switch cast := evt.(type) {
		case models.StopEvent:
			if failure := s.stream.emptyEnd(); failure != nil {
				evt = failure
			}
		case string:
			s.stream.produced = s.stream.produced || cast != ""
		case pub_models.Call, models.ReasoningEvent, models.ImageEvent:
			s.stream.produced = true
		}
		select {
		case out <- evt:
		case <-ctx.Done():
			return streamEnded
		}
		switch evt.(type) {
		case models.StopEvent:
			// Terminal event: the session runner returns on StopEvent and never
			// reads the channel again. Stop here so a trailing blank line or a
			// second [DONE] cannot block forever on the abandoned channel
			// (production goroutine leak, 2026-08-12).
			return streamEnded
		case error:
			// A chunk-handler error is terminal too: the session runner ends
			// the step on every channel error (its io.EOF and context.Canceled
			// branches return as well) and never reads the channel again. Stop
			// here so a provider that streams [DONE] after an error frame
			// cannot block forever on the abandoned channel (worklog
			// 2026-09-05-error-propagation, review 3, R3-01).
			return streamEnded
		}
	}
	return streamContinues
}

func (s *StreamCompleter) vendorEvents(token []byte) ([]models.CompletionEvent, error) {
	if s.DecodeChunk == nil {
		return nil, nil
	}
	return s.DecodeChunk(chunkPayload(token))
}

func chunkPayload(token []byte) []byte {
	return bytes.TrimSpace(bytes.TrimPrefix(token, dataPrefix))
}

// endStream sends what the end of the body means when no finish marker ended
// the stream first: no completion stream at all, or a truncated one.
func (s *StreamCompleter) endStream(ctx context.Context, out chan<- models.CompletionEvent) {
	var end models.CompletionEvent = s.stream.emptyEnd()
	if end == nil {
		if s.stream.finished {
			return
		}
		end = TruncatedError("finish_reason or [DONE]")
	}
	select {
	case out <- end:
	case <-ctx.Done():
	}
}

func (s *StreamCompleter) handleStreamChunk(token []byte) models.CompletionEvent {
	isData := bytes.HasPrefix(token, dataPrefix)
	token = chunkPayload(token)
	if string(token) == "[DONE]" {
		s.debugf("sending StopEvent due to [DONE]")
		s.stream.sawData = s.stream.sawData || isData
		s.stream.finished = true
		s.flushInvalidToolsCall()
		return models.StopEvent{}
	}

	if len(token) == 0 {
		return models.NoopEvent{}
	}

	s.debugf("token: %+v\n", string(token))
	var chunk chatCompletionChunk
	err := json.Unmarshal(token, &chunk)
	if err != nil {
		// A malformed line is tolerated, as a keep-alive is; the first data
		// event that is no JSON is kept, since a stream that then delivers
		// nothing has failed on it.
		if isData && s.stream.malformed == nil {
			s.stream.malformed = bytes.Clone(token)
		}
		runenv.Debugf(s.diagContext(), runenv.SubsystemChat, "failed to unmarshal token: %s, err: %v", token, err)
		return models.NoopEvent{}
	}
	s.stream.sawData = s.stream.sawData || isData

	// An error frame at HTTP 200 (the OpenAI-compat {"error": …} envelope)
	// is a terminal provider state, never a NoopEvent. The whole raw frame
	// goes through the same decode chain as a non-OK body; the OK status
	// tells a caring decoder the payload arrived as a frame. A JSON null is
	// no envelope. This branch must precede the choices check — an error
	// frame has no choices.
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return ResponseError(http.StatusOK, token, s.DecodeError)
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
		// Set chosen to the first error, string, or reasoning event.
		// NoopEvents and ReasoningEvents can be replaced by more useful events.
		case error, string, models.NoopEvent, models.ReasoningEvent:
			_, isNoopEvent := chosen.(models.NoopEvent)
			_, isReasoningEvent := chosen.(models.ReasoningEvent)
			if chosen == nil || isNoopEvent || isReasoningEvent {
				chosen = compEvent
			}
		case pub_models.Call:
			// Always prefer tools call, if possible
			chosen = compEvent
		}
	}

	s.debugf("chosen: %T -  %+v\n", chosen, chosen)

	return chosen
}

func (s *StreamCompleter) handleChoice(choice Choice) models.CompletionEvent {
	if choice.Delta.ReasoningContent != "" {
		s.appendReasoning(choice.Delta.ReasoningContent)
	}
	// If there is no tools call, just handle it as a strings. This works for most cases
	if len(choice.Delta.ToolCalls) == 0 {
		if choice.FinishReason != "" {
			s.debugf("stopping due to FinishReason: '%v'", choice.FinishReason)
			s.stream.finished = true
			if choice.FinishReason == finishReasonLength {
				runenv.Warnf(s.diagContext(), s.diagSubsystem(), "the reply was cut off at the output token limit (finish_reason: %s)", choice.FinishReason)
			}
			if s.flushInvalidToolsCall() {
				// The calls end the step; a StopEvent would end the run.
				return models.NoopEvent{}
			}
			return models.StopEvent{}
		}
		if choice.Delta.Content == nil && choice.Delta.ReasoningContent != "" {
			return models.ReasoningEvent{Content: choice.Delta.ReasoningContent}
		}
		return choice.Delta.Content
	}

	// Function name is only shown in first chunk of a functions call
	// TODO: Implement support for parallel function calls, now we only handle first tools call in list
	var argChunk string
	if len(choice.Delta.ToolCalls) > 0 && choice.Delta.ToolCalls[0].Function.Name != "" {
		// A new call starts: one still waiting for arguments that parse
		// never got them.
		s.flushInvalidToolsCall()
		s.toolsCallName = choice.Delta.ToolCalls[0].Function.Name
		s.toolsCallID = choice.Delta.ToolCalls[0].ID
	}

	if len(choice.Delta.ToolCalls) > 0 {
		argChunk = choice.Delta.ToolCalls[0].Function.Arguments
		// The arguments is streamed as a stringified json for chatgpt, chunk by chunk, with no apparent structure
		s.toolsCallArgsString += argChunk

		s.debugf("toolsCallArgsString: %v\n", s.toolsCallArgsString)
		var input pub_models.Input
		err := json.Unmarshal([]byte(s.toolsCallArgsString), &input)
		if err == nil {
			s.extraContent = choice.Delta.ToolCalls[0].ExtraContent
			return s.doToolsCall()
		}
	}
	return models.NoopEvent{}
}

// flushInvalidToolsCall queues the call whose arguments never became a JSON
// object, so the model is answered with what was wrong instead of the call
// vanishing. It reports whether there was one.
func (s *StreamCompleter) flushInvalidToolsCall() bool {
	if s.toolsCallName == "" {
		return false
	}
	spec := tools.ToolFromName(s.toolsCallName)
	spec.Arguments = "{}"
	spec.Inputs = &pub_models.InputSchema{}
	s.stream.calls = append(s.stream.calls, pub_models.Call{
		ID:               s.toolsCallID,
		Name:             s.toolsCallName,
		Inputs:           &pub_models.Input{},
		Type:             "function",
		Function:         spec,
		ReasoningContent: s.reasoning(),
		ArgumentsErr:     pub_models.ArgumentsProblem(s.toolsCallName, s.toolsCallArgsString),
	})
	s.toolsCallName = ""
	s.toolsCallArgsString = ""
	return true
}

// doToolsCall by parsing the arguments
func (s *StreamCompleter) doToolsCall() models.CompletionEvent {
	defer func() {
		// Reset tools call construction strings to prepare for consequtive calls
		s.toolsCallName = ""
		s.toolsCallArgsString = ""
	}()
	var input pub_models.Input
	err := json.Unmarshal([]byte(s.toolsCallArgsString), &input)
	if err != nil {
		return diag.Errorf("failed to unmarshal argument string: %w, argsString: %v", err, s.toolsCallArgsString)
	}

	userFunc := tools.ToolFromName(s.toolsCallName)
	userFunc.Arguments = s.toolsCallArgsString
	userFunc.Inputs = &pub_models.InputSchema{}

	return pub_models.Call{
		ID:               s.toolsCallID,
		Name:             s.toolsCallName,
		Inputs:           &input,
		Type:             "function",
		Function:         userFunc,
		ExtraContent:     s.extraContent,
		ReasoningContent: s.reasoning(),
	}
}

// appendReasoning trims back to the tail only past twice the cap, so a stream of
// small deltas costs amortized O(1) per byte instead of a full copy each.
func (s *StreamCompleter) appendReasoning(delta string) {
	s.reasoningContent = append(s.reasoningContent, delta...)
	if len(s.reasoningContent) > 2*maxReasoningContent {
		s.reasoningContent = append(s.reasoningContent[:0], s.reasoningContent[len(s.reasoningContent)-maxReasoningContent:]...)
	}
}

func (s *StreamCompleter) reasoning() string {
	tail := s.reasoningContent
	if len(tail) > maxReasoningContent {
		tail = tail[len(tail)-maxReasoningContent:]
	}
	return string(tail)
}

// heuristicTokenCountFactor is used to approximate token usage when
// the vendor does not expose an endpoint for counting tokens.
const heuristicTokenCountFactor = 1.1

// CountInputTokens estimates the amount of input tokens in the chat.
func (s *StreamCompleter) CountInputTokens(ctx context.Context, chat pub_models.Chat) (int, error) {
	var count int
	for _, m := range chat.Messages {
		count += len(strings.Split(m.Describe(), " "))
	}
	return int(float64(count) * heuristicTokenCountFactor), nil
}

func (s *StreamCompleter) TokenUsage() *pub_models.Usage {
	return s.usage
}

// diagContext is the context of the current stream's diagnostics.
func (s *StreamCompleter) diagContext() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// diagSubsystem is the vendor's subsystem, or the chat one without a vendor.
func (s *StreamCompleter) diagSubsystem() string {
	if s.subsystem == "" {
		return runenv.SubsystemChat
	}
	return s.subsystem
}

// debugging reports whether the vendor's debug detail is shown.
func (s *StreamCompleter) debugging() bool {
	return runenv.DebugEnabled(s.diagContext(), s.subsystem)
}

// debugf reports debug detail of the vendor this completer streams for.
func (s *StreamCompleter) debugf(format string, args ...any) {
	runenv.Debugf(s.diagContext(), s.subsystem, format, args...)
}
