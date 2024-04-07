package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type responseFormat struct {
	Type string `json:"type"`
}

type chatCompletionChunk struct {
	Id                string `json:"id"`
	Object            string `json:"object"`
	Created           int    `json:"created"`
	Model             string `json:"model"`
	SystemFingerprint string `json:"system_fingerprint"`
	Choices           []struct {
		Index        int `json:"index"`
		Delta        models.Message
		Logprobs     interface{} `json:"logprobs"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

var dataPrefix = []byte("data: ")

// // streamCompletions taking the messages as prompt conversation. Returns the messages from the chat model.
func (g *ChatGPT) StreamCompletions(ctx context.Context, chat models.Chat) (chan models.CompletionEvent, error) {
	reqData := gptReq{
		Model:            g.Model,
		FrequencyPenalty: g.FrequencyPenalty,
		MaxTokens:        g.MaxTokens,
		PresencePenalty:  g.PresencePenalty,
		Temperature:      g.Temperature,
		TopP:             g.TopP,
		ResponseFormat:   responseFormat{Type: "text"},
		Messages:         chat.Messages,
		Stream:           true,
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("streamCompletions: %+v\n", reqData))
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.Url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", g.apiKey))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")

	res, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("failed to execute request: %v, body: %v", res.Status, string(body))
	}
	outChan, err := g.handleStreamResponse(ctx, res)
	if err != nil {
		return outChan, fmt.Errorf("failed to handle stream response: %w", err)
	}

	return outChan, nil
}

func (g *ChatGPT) handleStreamResponse(ctx context.Context, res *http.Response) (chan models.CompletionEvent, error) {
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
				outChan <- models.CompletionEvent(fmt.Errorf("failed to read line: %w", err))
			}
			token = bytes.TrimPrefix(token, dataPrefix)
			token = bytes.TrimSpace(token)
			if string(token) == "[DONE]" {
				return
			}
			var chunk chatCompletionChunk
			err = json.Unmarshal(token, &chunk)
			if err != nil {
				if misc.Truthy(os.Getenv("DEBUG")) {
					// Expect some failing unmarshalls, which seems to be fine
					ancli.PrintWarn(fmt.Sprintf("failed to unmarshal token: %v, err: %v\n", token, err))
					continue
				}
			} else {
				if len(chunk.Choices) == 0 {
					continue
				}
				outChan <- models.CompletionEvent(chunk.Choices[0].Delta.Content)
			}
		}
	}()

	return outChan, nil
}
