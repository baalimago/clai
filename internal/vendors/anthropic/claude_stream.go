package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type Delta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ContentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta Delta  `json:"delta"`
}

func (c *Claude) streamCompletions(ctx context.Context, chat models.Chat) (models.Message, error) {
	req, err := c.constructRequest(ctx, chat)
	if err != nil {
		return models.Message{}, fmt.Errorf("failed to construct request: %w", err)
	}

	nextMsg, err := c.stream(ctx, req)
	if err != nil {
		return models.Message{}, fmt.Errorf("failed to stream completions: %w", err)
	}
	return nextMsg, nil
}

func (c *Claude) stream(ctx context.Context, req *http.Request) (models.Message, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return models.Message{}, fmt.Errorf("failed to do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return models.Message{}, fmt.Errorf("failed to execute request: %v, body: %v", resp.Status, string(body))
	}

	nextMsg, err := c.handleStreamResponse(resp)
	if err != nil {
		return models.Message{}, fmt.Errorf("failed to parse response: %w", err)
	}
	return nextMsg, nil
}

func (c *Claude) handleStreamResponse(resp *http.Response) (models.Message, error) {
	fullMessage := models.Message{
		Role: "system",
	}
	br := bufio.NewReader(resp.Body)
	line := ""
	lineCount := 0
	termWidth, err := tools.TermWidth()
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
	}

	defer func() {
		c.clearAndPrettyPrint(termWidth, lineCount, fullMessage)
	}()

	for {
		token, err := br.ReadString('\n')
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return models.Message{}, fmt.Errorf("failed to read line: %w", err)
		}
		claudeMsg, err := c.handleToken(br, token)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			ancli.PrintWarn(fmt.Sprintf("failed to handle token: %v\n", err))
		}
		fullMessage.Content += claudeMsg
		if termWidth > 0 {
			tools.UpdateMessageTerminalMetadata(claudeMsg, &line, &lineCount, termWidth)
		}
		fmt.Print(claudeMsg)
	}
	return fullMessage, nil
}

func (c *Claude) handleToken(br *bufio.Reader, token string) (string, error) {
	tokSplit := strings.Split(token, " ")
	if len(tokSplit) != 2 {
		return "", fmt.Errorf("unexpected token length for token: '%v', expected format: 'event: <event>'", token)
	}
	eventTok := tokSplit[0]
	eventType := tokSplit[1]
	if eventTok != "event:" {
		return "", fmt.Errorf("unexpected token, want: 'event:', got: '%v'", eventTok)
	}
	eventType = strings.TrimSpace(eventType)
	if c.debug {
		fmt.Printf("eventTok: '%v', eventType: '%s'\n", eventTok, eventType)
	}
	switch eventType {
	case "message_stop":
		return "", io.EOF
	// TODO: Print token amount
	case "content_block_delta":
		deltaToken, err := br.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read content_block_delta: %w", err)
		}
		claudeMsg, err := c.stringFromDeltaToken(deltaToken)
		if err != nil {
			return "", fmt.Errorf("failed to convert string to delta token: %w", err)
		}
		if c.debug {
			fmt.Printf("deltaToken: '%v', claudeMsg: '%v'", deltaToken, claudeMsg)
		}
		return claudeMsg, nil
	}

	// Jump down one line to setup next event
	br.ReadString('\n')
	return "", nil
}

func (c *Claude) stringFromDeltaToken(deltaToken string) (string, error) {
	deltaTokSplit := strings.Split(deltaToken, " ")
	if deltaTokSplit[0] != "data:" {
		return "", fmt.Errorf("unexpected split token. Expected: 'data:', got: '%v'", deltaTokSplit[0])
	}
	deltaJsonString := strings.Join(deltaTokSplit[1:], " ")
	var delta ContentBlockDelta
	err := json.Unmarshal([]byte(deltaJsonString), &delta)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal deltaJsonString: '%v' to struct, err: %w", deltaJsonString, err)
	}
	if c.debug {
		ancli.PrintOK(fmt.Sprintf("delta struct: %+v\nstring: %v", delta, deltaJsonString))
	}
	if delta.Delta.Text == "" {
		return "", errors.New("unexpected empty response")
	}
	return delta.Delta.Text, nil
}

func (c *Claude) clearAndPrettyPrint(termWidth, lineCount int, fullMessage models.Message) {
	// If raw, just leave all the tokens as is, since it's been streamed to terminal already
	if c.Raw {
		return
	}
	if termWidth > 0 {
		tools.ClearTermTo(termWidth, lineCount)
	} else {
		fmt.Println()
	}

	err := tools.AttemptPrettyPrint(fullMessage, c.username)
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to pretty print, normal printing. Error was: %v\n", err))
		fmt.Print(fullMessage.Content)
	}
}

func (c *Claude) constructRequest(ctx context.Context, chat models.Chat) (*http.Request, error) {
	// ignored for now as error is not used
	sysMsg, _ := chat.SystemMessage()
	if c.debug {
		ancli.PrintOK(fmt.Sprintf("pre-claudified messages: %+v\n", chat.Messages))
	}
	claudifiedMsgs := claudifyMessages(chat.Messages)
	if c.debug {
		ancli.PrintOK(fmt.Sprintf("claudified messages: %+v\n", claudifiedMsgs))
	}
	reqData := claudeReq{
		Model:     c.Model,
		Messages:  claudifiedMsgs,
		MaxTokens: c.MaxTokens,
		Stream:    true,
		System:    sysMsg.Content,
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ClaudeReq: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", c.AnthropicVersion)
	if c.debug {
		ancli.PrintOK(fmt.Sprintf("Request: %+v\n", req))
	}
	return req, nil
}
