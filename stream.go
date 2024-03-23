package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"golang.org/x/term"
)

type ChatCompletionChunk struct {
	Id                string `json:"id"`
	Object            string `json:"object"`
	Created           int    `json:"created"`
	Model             string `json:"model"`
	SystemFingerprint string `json:"system_fingerprint"`
	Choices           []struct {
		Index        int `json:"index"`
		Delta        Message
		Logprobs     interface{} `json:"logprobs"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

var dataPrefix = []byte("data: ")

// streamCompletions taking the messages as prompt conversation. Returns the messages from the chat model.
func (cq *chatModelQuerier) streamCompletions(ctx context.Context, API_KEY string, messages []Message) (Message, error) {
	reqData := Request{
		Model:            cq.Model,
		FrequencyPenalty: cq.FrequencyPenalty,
		MaxTokens:        cq.MaxTokens,
		PresencePenalty:  cq.PresencePenalty,
		Temperature:      cq.Temperature,
		TopP:             cq.TopP,
		ResponseFormat:   ResponseFormat{Type: "text"},
		Messages:         messages,
		Stream:           true,
	}
	if os.Getenv("DEBUG") == "true" {
		fmt.Printf("streamCompletions: %v\n", reqData)
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return Message{}, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cq.Url, bytes.NewBuffer(jsonData))
	if err != nil {
		return Message{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", API_KEY))
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")

	res, err := cq.client.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("failed to execute request: %w", err)
	}
	defer res.Body.Close()
	msg, err := cq.handleStreamResponse(res)
	if err != nil {
		return msg, fmt.Errorf("failed to handle stream response: %w", err)
	}

	return msg, nil
}

func willBeNewLine(line, msg string, termWidth int) bool {
	return utf8.RuneCountInString(line+msg) > termWidth
}

func (cq *chatModelQuerier) handleStreamResponse(res *http.Response) (Message, error) {
	fullMessage := Message{
		Role: "system",
	}
	br := bufio.NewReader(res.Body)
	lineCount := 0
	termInt := int(os.Stderr.Fd())
	line := ""
	failedToGetTerminalSize := false
	termWidth, _, err := term.GetSize(termInt)
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
		failedToGetTerminalSize = true
	}
	defer func() {
		if cq.Raw {
			return
		}
		if !failedToGetTerminalSize {
			clearLine := strings.Repeat(" ", termWidth)
			// Move cursor up line by line and clear the line
			for lineCount > 0 {
				fmt.Printf("\r%v", clearLine)
				fmt.Printf("\033[%dA", 1)
				lineCount--
			}
			fmt.Printf("\r%v", clearLine)
			// Place cursor at start of line
			fmt.Printf("\r")
		} else {
			fmt.Println()
		}
		cq.printChatMessage(fullMessage)
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
		var chunk ChatCompletionChunk
		err = json.Unmarshal(token, &chunk)
		if err != nil {
			if os.Getenv("DEBUG") == "true" {
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
			if !failedToGetTerminalSize {
				amNewlines := strings.Count(msg, "\n")
				if amNewlines == 0 && willBeNewLine(line, msg, termWidth) {
					amNewlines = 1
				}
				if amNewlines > 0 {
					lineCount += amNewlines
					line = ""
				} else {
					line += msg
				}
			}
			fmt.Printf("%v", msg)
		}
	}

	return fullMessage, nil
}
