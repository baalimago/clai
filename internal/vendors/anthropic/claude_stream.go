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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const heuristicTokenCountFactor = 1.1

func (c *Claude) StreamCompletions(ctx context.Context, chat models.Chat) (chan models.CompletionEvent, error) {
	req, err := c.constructRequest(ctx, chat)
	if err != nil {
		return nil, fmt.Errorf("failed to construct request: %w", err)
	}
	if _, err = c.CountInputTokens(ctx, chat); err != nil {
		return nil, fmt.Errorf("failed to count input tokens: %w", err)
	}
	return c.stream(ctx, req)
}

func (c *Claude) stream(ctx context.Context, req *http.Request) (chan models.CompletionEvent, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAtStr := resp.Header.Get("anthropic-ratelimit-tokens-reset")
			resetAt, timeParseErr := time.Parse(time.RFC3339, retryAtStr)
			if timeParseErr != nil {
				ancli.Warnf("failed to parse rate limit reset, defaulting to 30 seconds from now")
				resetAt = time.Now().Add(time.Minute / 2)
			}

			limitStr := resp.Header.Get("anthropic-ratelimit-input-tokens-limit")
			limit, atoiErr := strconv.Atoi(limitStr)
			if atoiErr != nil {
				ancli.Warnf("failed to parse input tokens limit, defaulting to 0")
				limit = 0
			}

			remainingStr := resp.Header.Get("anthropic-ratelimit-tokens-remaining")
			remaining, atoiErr := strconv.Atoi(remainingStr)
			if atoiErr != nil {
				ancli.Warnf("failed to parse am remaining tokens header: '%s', defaulting to 0", remainingStr)
				remaining = 0
			}

			return nil, models.NewRateLimitError(resetAt, limit, remaining)
		}
		return nil, fmt.Errorf("failed to execute request: %v, body: %v", resp.Status, string(body))
	}

	outChan, err := c.handleStreamResponse(ctx, resp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return outChan, nil
}

func (c *Claude) handleStreamResponse(ctx context.Context, resp *http.Response) (chan models.CompletionEvent, error) {
	outChan := make(chan models.CompletionEvent)
	go func() {
		br := bufio.NewReader(resp.Body)
		defer func() {
			c.debugFullStreamMsg = ""
			resp.Body.Close()
			close(outChan)
		}()
		for {
			token, err := br.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					if token != "" {
						c.handleFullResponse(token, outChan)
					} else {
						outChan <- err
					}
				}
				outChan <- models.CompletionEvent(fmt.Errorf("failed to read line: %w", err))
				return
			}
			token = strings.TrimSpace(token)
			if ctx.Err() != nil {
				outChan <- models.CompletionEvent(errors.New("context cancelled"))
				return
			}
			if token == "" {
				continue
			}
			processed := c.handleToken(br, token)
			if c.debug {
				switch cast := processed.(type) {
				case string:
					c.debugFullStreamMsg += cast
					ancli.Okf("new bit: %v, full message: '%v'\n--\n", cast, c.debugFullStreamMsg)
				}
			}
			outChan <- processed
		}
	}()
	return outChan, nil
}

func (c *Claude) handleFullResponse(token string, outChan chan models.CompletionEvent) {
	var rspBody ClaudeResponse
	err := json.Unmarshal([]byte(token), &rspBody)
	if err != nil {
		outChan <- models.CompletionEvent(fmt.Errorf("failed to unmarshal response: %w, resp body as string: %v", err, token))
		return
	}
	for _, content := range rspBody.Content {
		switch content.Type {
		case "text":
			outChan <- content.Text
		case "tool_use":
			outChan <- tools.Call{
				Name:   content.Name,
				Inputs: content.Input,
			}
		}
	}
}

func (c *Claude) handleToken(br *bufio.Reader, token string) models.CompletionEvent {
	tokSplit := strings.Split(token, " ")
	if len(tokSplit) != 2 {
		return fmt.Errorf("unexpected token length for token: '%v', expected format: 'event: <event>'", token)
	}
	eventTok := tokSplit[0]
	eventType := tokSplit[1]
	if eventTok != "event:" {
		return fmt.Errorf("unexpected token, want: 'event:', got: '%v'", eventTok)
	}
	eventType = strings.TrimSpace(eventType)
	if c.debug {
		fmt.Printf("eventTok: '%v', eventType: '%s'\n", eventTok, eventType)
	}
	switch eventType {
	case "message_stop":
		return io.EOF

	case "content_block_start":
		c.debugFullStreamMsg = ""
		blockStart, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read content_block_delta: %w", err)
		}
		return c.handleContentBlockStart(blockStart)
	// TODO: Print token amount
	case "content_block_delta":
		deltaToken, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read content_block_delta: %w", err)
		}
		return c.handleContentBlockDelta(deltaToken)
	case "content_block_stop":
		blockStop, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read content_block_stop: %w", err)
		}
		return c.handleContentBlockStop(blockStop)
	}

	// Jump down one line to setup next event
	br.ReadString('\n')
	return models.NoopEvent{}
}

func trimDataPrefix(data string) string {
	return strings.TrimPrefix(data, "data: ")
}

func (c *Claude) stringFromDeltaToken(deltaToken string) (Delta, error) {
	deltaTokSplit := strings.Split(deltaToken, " ")
	if deltaTokSplit[0] != "data:" {
		return Delta{}, fmt.Errorf("unexpected split token. Expected: 'data:', got: '%v'", deltaTokSplit[0])
	}
	deltaJSONString := strings.Join(deltaTokSplit[1:], " ")
	var contentBlockDelta ContentBlockDelta
	err := json.Unmarshal([]byte(deltaJSONString), &contentBlockDelta)
	if err != nil {
		return Delta{}, fmt.Errorf("failed to unmarshal deltaJsonString: '%v' to struct, err: %w", deltaJSONString, err)
	}
	if c.debug {
		ancli.PrintOK(fmt.Sprintf("delta struct: %+v\nstring: %v", debug.IndentedJsonFmt(contentBlockDelta), deltaJSONString))
	}
	return contentBlockDelta.Delta, nil
}

func (c *Claude) constructRequest(ctx context.Context, chat models.Chat) (*http.Request, error) {
	// ignored for now as error is not used
	sysMsg, _ := chat.FirstSystemMessage()
	msgCopy := make([]models.Message, len(chat.Messages))
	copy(msgCopy, chat.Messages)
	claudifiedMsgs := claudifyMessages(msgCopy)
	if misc.Truthy(os.Getenv("DEBUG_CLAUDIFIED_MSGS")) {
		ancli.PrintOK(
			fmt.Sprintf(
				"claudified messages: %+v\n",
				debug.IndentedJsonFmt(claudifiedMsgs),
			),
		)
	}

	reqData := claudeReq{
		Model:         c.Model,
		Messages:      claudifiedMsgs,
		MaxTokens:     c.MaxTokens,
		Stream:        true,
		System:        sysMsg.Content,
		Temperature:   c.Temperature,
		TopP:          c.TopP,
		TopK:          c.TopK,
		StopSequences: c.StopSequences,
	}
	if len(c.tools) > 0 {
		reqData.Tools = c.tools
	}
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ClaudeReq: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", c.AnthropicVersion)
	if c.debug && misc.Truthy(os.Getenv("DEBUG_VERBOSE")) {
		ancli.PrintOK(fmt.Sprintf("Request: %+v\n", req))
	}
	return req, nil
}

func (c *Claude) CountInputTokens(ctx context.Context, chat models.Chat) (int, error) {
	msgCopy := make([]models.Message, len(chat.Messages))
	copy(msgCopy, chat.Messages)
	claudifiedMsgs := claudifyMessages(msgCopy)

	reqData := claudeReq{
		Model:    c.Model,
		Messages: claudifiedMsgs,
	}

	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	countURL := strings.TrimSuffix(c.URL, "/messages") + "/messages/count_tokens"
	if !strings.Contains(countURL, "anthropic.com") {
		// In tests or when using a custom URL, fall back to heuristic counting
		var count int
		for _, m := range chat.Messages {
			count += len(strings.Split(m.Content, " "))
		}
		heuristic := int(float64(count) * heuristicTokenCountFactor)
		c.amInputTokens = heuristic
		return heuristic, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, countURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", c.AnthropicVersion)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("token count request failed: %v, body: %v", resp.Status, string(body))
	}

	var tokenResp struct {
		InputTokens int `json:"input_tokens"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return 0, fmt.Errorf("failed to decode token count response: %w", err)
	}

	if c.debug || c.PrintInputCount {
		ancli.Okf("Token count: %d\n", tokenResp.InputTokens)
	}

	c.amInputTokens = tokenResp.InputTokens
	return tokenResp.InputTokens, nil
}
