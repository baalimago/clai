package mistral

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func (m *Mistral) StreamCompletions(ctx context.Context, chat models.Chat) (chan models.CompletionEvent, error) {
	req, err := m.createRequest(ctx, chat)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	return m.doRequest(ctx, req)
}

func (m *Mistral) createRequest(ctx context.Context, chat models.Chat) (*http.Request, error) {
	ret := Request{
		Model:       m.Model,
		MaxTokens:   m.MaxTokens,
		SafePrompt:  m.SafePrompt,
		RandomSeed:  m.RandomSeed,
		Stream:      true,
		Temperature: m.Temperature,
		TopP:        m.TopP,
		Messages:    chat.Messages,
		// Using tools is controlled one level up. If tools are disabled, they simply won't be added
		// to the query
		ToolChoice: "auto",
	}
	if len(m.tools) > 0 {
		ret.Tools = m.tools
	}

	if m.debug {
		ancli.PrintOK(fmt.Sprintf("mistral request: %+v\n", ret))
	}

	jsonData, err := json.Marshal(ret)
	if err != nil {
		return nil, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.Url, bytes.NewBuffer(jsonData))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", m.apiKey))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")

	return req, nil
}

func (m *Mistral) doRequest(ctx context.Context, req *http.Request) (chan models.CompletionEvent, error) {
	res, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do request: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("failed to execute request: %v, body: %v", res.Status, string(body))
	}
	return m.handleStreamResponse(ctx, res), nil
}

func (m *Mistral) handleStreamResponse(ctx context.Context, res *http.Response) chan models.CompletionEvent {
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
			outChan <- m.handleStreamChunk(token)
		}
	}()

	return outChan
}

var dataPrefix = []byte("data: ")

func (m *Mistral) handleStreamChunk(chunkB []byte) models.CompletionEvent {
	chunkB = bytes.TrimPrefix(chunkB, dataPrefix)
	chunkB = bytes.TrimSpace(chunkB)
	if string(chunkB) == "[DONE]" {
		return models.NoopEvent{}
	}
	if len(chunkB) == 0 {
		return models.NoopEvent{}
	}
	var responseChunk Response
	err := json.Unmarshal(chunkB, &responseChunk)
	if err != nil {
		return fmt.Errorf("failed to unmarshal response: %w, bytes as str: %v", err, string(chunkB))
	}
	if m.debug {
		ancli.PrintOK(fmt.Sprintf(string(chunkB) + "\n"))
	}
	if responseChunk.FinishReason != "" {
		if m.debug {
			return fmt.Errorf("finish reason: %v", responseChunk.FinishReason)
		} else {
			return models.NoopEvent{}
		}
	}
	for _, choice := range responseChunk.Choices {
		msg := choice
		return msg.Delta.Content
	}
	return errors.New("Unexpectd logical branch in handleStreamChunk")
}
