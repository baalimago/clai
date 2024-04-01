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
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type responseFormat struct {
	Type string `json:"type"`
}

type request struct {
	Model            string           `json:"model"`
	ResponseFormat   responseFormat   `json:"response_format"`
	Messages         []models.Message `json:"messages"`
	Stream           bool             `json:"stream"`
	FrequencyPenalty float32          `json:"frequency_penalty"`
	MaxTokens        *int             `json:"max_tokens"`
	PresencePenalty  float32          `json:"presence_penalty"`
	Temperature      float32          `json:"temperature"`
	TopP             float32          `json:"top_p"`
}

var defaultGpt = ChatGPT{
	Model:       "gpt-4-turbo-preview",
	Temperature: 1.0,
	TopP:        1.0,
	Url:         ChatURL,
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

// streamCompletions taking the messages as prompt conversation. Returns the messages from the chat model.
func (q *ChatGPT) streamCompletions(ctx context.Context, API_KEY string, messages []models.Message) (models.Message, error) {
	reqData := request{
		Model:            q.Model,
		FrequencyPenalty: q.FrequencyPenalty,
		MaxTokens:        q.MaxTokens,
		PresencePenalty:  q.PresencePenalty,
		Temperature:      q.Temperature,
		TopP:             q.TopP,
		ResponseFormat:   responseFormat{Type: "text"},
		Messages:         messages,
		Stream:           true,
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("streamCompletions: %+v\n", reqData))
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return models.Message{}, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", q.Url, bytes.NewBuffer(jsonData))
	if err != nil {
		return models.Message{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", API_KEY))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")

	res, err := q.client.Do(req)
	if err != nil {
		return models.Message{}, fmt.Errorf("failed to execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return models.Message{}, fmt.Errorf("failed to execute request: %v, body: %v", res.Status, string(body))
	}
	msg, err := q.handleStreamResponse(res, q.raw)
	if err != nil {
		return msg, fmt.Errorf("failed to handle stream response: %w", err)
	}

	return msg, nil
}

func (q *ChatGPT) handleStreamResponse(res *http.Response, printRaw bool) (models.Message, error) {
	fullMessage := models.Message{
		Role: "system",
	}
	br := bufio.NewReader(res.Body)
	line := ""
	lineCount := 0
	termWidth, err := tools.TerminalWidth()
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
	}

	defer func() {
		// Can't cleanly clear the terminal without knowing its width: symbols might be left behind
		if termWidth > 0 {
			tools.ClearTermTo(termWidth, lineCount)
		} else {
			fmt.Println()
		}

		if printRaw {
			fmt.Print(fullMessage.Content)
		} else {
			tools.AttemptPrettyPrint(fullMessage, q.username)
		}
	}()

	for {
		token, err := br.ReadBytes('\n')
		if err != nil {
			lineCount++
			return fullMessage, fmt.Errorf("failed to read token: %w", err)
		}
		token = bytes.TrimPrefix(token, dataPrefix)
		token = bytes.TrimSpace(token)
		if string(token) == "[DONE]" {
			break
		}
		var chunk chatCompletionChunk
		err = json.Unmarshal(token, &chunk)
		if err != nil {
			if misc.Truthy(os.Getenv("DEBUG")) {
				// Expect some failing unmarshalls, which seems to be fine
				// ancli.PrintWarn(fmt.Sprintf("failed to unmarshal token: %v, err: %v\n", token, err))
				continue
			}
		} else {
			if len(chunk.Choices) == 0 {
				continue
			}
			msg := chunk.Choices[0].Delta.Content
			fullMessage.Content += msg
			if termWidth > 0 {
				tools.UpdateMessageTerminalMetadata(msg, &line, &lineCount, termWidth)
			}
			fmt.Print(msg)
		}
	}

	return fullMessage, nil
}
