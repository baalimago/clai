package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type chatModelQuerier struct {
	model        string
	systemPrompt string
	raw          bool
}

type SystemMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ResponseFormat struct {
	Type string `json:"type"`
}

type Request struct {
	Model          string          `json:"model"`
	ResponseFormat ResponseFormat  `json:"response_format"`
	Messages       []SystemMessage `json:"messages"`
}

type ChatCompletion struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
	SystemFingerprint string   `json:"system_fingerprint"`
}

type Choice struct {
	Index        int         `json:"index"`
	Message      Message     `json:"message"`
	Logprobs     interface{} `json:"logprobs"` // null or complex object, hence interface{}
	FinishReason string      `json:"finish_reason"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (cq *chatModelQuerier) constructMessages(args []string) []SystemMessage {
	var messages []SystemMessage
	messages = append(messages, SystemMessage{Role: "system", Content: cq.systemPrompt})
	messages = append(messages, SystemMessage{Role: "user", Content: strings.Join(args, " ")})
	return messages
}

// queryChatModel using the supplied arguments as instructions
func (cq *chatModelQuerier) queryChatModel(ctx context.Context, API_KEY string, messages []SystemMessage) error {
	url := "https://api.openai.com/v1/chat/completions"
	reqData := Request{
		Model:          cq.model,
		ResponseFormat: ResponseFormat{Type: "text"},
		Messages:       messages,
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", API_KEY))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	strBody := string(body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("response status: %v, response body: %v", resp.Status, strBody)
	}

	var chatCompletion ChatCompletion
	err = json.Unmarshal(body, &chatCompletion)
	if err != nil {
		return fmt.Errorf("failed to decode JSON: %w", err)
	}

	for _, v := range chatCompletion.Choices {
		if cq.raw {
			fmt.Print(v.Message.Content)
			continue
		}
		cmd := exec.Command("glow", "--version")
		if err := cmd.Run(); err != nil {
			fmt.Printf("%v: %v\n", ancli.ColoredMessage(ancli.BLUE, v.Message.Role), v.Message.Content)
			return nil
		}

		cmd = exec.Command("glow")
		cmd.Stdin = bytes.NewBufferString(v.Message.Content)
		cmd.Stdout = os.Stdout
		fmt.Printf("%v:", ancli.ColoredMessage(ancli.BLUE, v.Message.Role))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to run glow: %w", err)
		}
	}

	return nil
}
