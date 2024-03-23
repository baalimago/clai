package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type chatModelQuerier struct {
	Model            string  `json:"model"`
	SystemPrompt     string  `json:"system_prompt"`
	Raw              bool    `json:"raw"`
	Url              string  `json:"url"`
	FrequencyPenalty float32 `json:"frequency_penalty"`
	MaxTokens        *int    `json:"max_tokens"`
	PresencePenalty  float32 `json:"presence_penalty"`
	Temperature      float32 `json:"temperature"`
	TopP             float32 `json:"top_p"`
	replyMode        bool
	home             string
	client           *http.Client
}

type ResponseFormat struct {
	Type string `json:"type"`
}

type Request struct {
	Model            string         `json:"model"`
	ResponseFormat   ResponseFormat `json:"response_format"`
	Messages         []Message      `json:"messages"`
	Stream           bool           `json:"stream"`
	FrequencyPenalty float32        `json:"frequency_penalty"`
	MaxTokens        *int           `json:"max_tokens"`
	PresencePenalty  float32        `json:"presence_penalty"`
	Temperature      float32        `json:"temperature"`
	TopP             float32        `json:"top_p"`
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
