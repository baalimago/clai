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
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	Raw          bool   `json:"raw"`
	Url          string `json:"url"`
}

type ResponseFormat struct {
	Type string `json:"type"`
}

type Request struct {
	Model          string         `json:"model"`
	ResponseFormat ResponseFormat `json:"response_format"`
	Messages       []Message      `json:"messages"`
	Stream         bool           `json:"stream"`
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

func (cq *chatModelQuerier) constructMessages(args []string) []Message {
	var messages []Message
	messages = append(messages, Message{Role: "system", Content: cq.SystemPrompt})
	messages = append(messages, Message{Role: "user", Content: strings.Join(args, " ")})
	return messages
}

// queryChatModel using the supplied arguments as instructions
func (cq *chatModelQuerier) queryChatModel(ctx context.Context, API_KEY string, messages []Message) (ChatCompletion, error) {
	reqData := Request{
		Model:          cq.Model,
		ResponseFormat: ResponseFormat{Type: "text"},
		Messages:       messages,
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return ChatCompletion{}, fmt.Errorf("failed to encode JSON: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cq.Url, bytes.NewBuffer(jsonData))
	if err != nil {
		return ChatCompletion{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", API_KEY))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return ChatCompletion{}, fmt.Errorf("failed to do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatCompletion{}, fmt.Errorf("failed to read response body: %w", err)
	}

	strBody := string(body)
	if resp.StatusCode != 200 {
		return ChatCompletion{}, fmt.Errorf("response status: %v, response body: %v", resp.Status, strBody)
	}

	var chatCompletion ChatCompletion
	err = json.Unmarshal(body, &chatCompletion)
	if err != nil {
		return ChatCompletion{}, fmt.Errorf("failed to decode JSON: %w", err)
	}

	return chatCompletion, nil
}

func (cq *chatModelQuerier) printChatCompletion(chatCompletion ChatCompletion) error {
	if len(chatCompletion.Choices) != 1 {
		return fmt.Errorf("expected 1 choice, got %d", len(chatCompletion.Choices))
	}
	err := cq.printChatMessage(chatCompletion.Choices[0].Message)
	if err != nil {
		return fmt.Errorf("failed to print chat completion: %w", err)
	}
	return nil
}

func (cq *chatModelQuerier) printChatMessage(chatMessage Message) error {
	color := ancli.BLUE
	switch chatMessage.Role {
	case "user":
		color = ancli.CYAN
	case "system":
		color = ancli.BLUE
	}
	if cq.Raw {
		fmt.Print(chatMessage.Content)
		return nil
	}
	cmd := exec.Command("glow", "--version")
	if err := cmd.Run(); err != nil {
		fmt.Printf("%v: %v\n", ancli.ColoredMessage(color, chatMessage.Role), chatMessage.Content)
	}

	cmd = exec.Command("glow")
	cmd.Stdin = bytes.NewBufferString(chatMessage.Content)
	cmd.Stdout = os.Stdout
	fmt.Printf("%v:", ancli.ColoredMessage(color, chatMessage.Role))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run glow: %w", err)
	}
	return nil
}
