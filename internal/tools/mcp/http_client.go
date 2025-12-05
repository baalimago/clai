package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func startHttpClient(ctx context.Context, mcpConfig pub_models.McpServer) (chan<- any, <-chan any, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", mcpConfig.Url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")

	// Use Env for headers (e.g. Authorization)
	for k, v := range mcpConfig.Env {
		req.Header.Set(k, v)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to SSE endpoint: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	in := make(chan any)
	out := make(chan any)

	// Streamable HTTP: Default POST endpoint is the same URL
	endpointUrl := mcpConfig.Url
	var endpointMu sync.Mutex

	// Goroutine to read SSE stream
	go func() {
		defer func() {
			resp.Body.Close()
			close(out)
		}()

		scanner := bufio.NewScanner(resp.Body)
		var currentEvent string
		var currentData []string

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				// End of event
				if len(currentData) > 0 {
					dataStr := strings.Join(currentData, "\n")
					if currentEvent == "endpoint" {
						// Backwards compatibility: Update endpoint if specified
						baseUrl, err := url.Parse(mcpConfig.Url)
						if err == nil {
							if refUrl, err := url.Parse(dataStr); err == nil {
								endpointMu.Lock()
								endpointUrl = baseUrl.ResolveReference(refUrl).String()
								endpointMu.Unlock()
							}
						}
						ancli.Noticef("mcp_%s: Connected to endpoint %s\n", mcpConfig.Name, dataStr)
					} else if currentEvent == "message" || currentEvent == "" {
						var msg json.RawMessage
						if err := json.Unmarshal([]byte(dataStr), &msg); err != nil {
							ancli.Warnf("mcp_%s: Failed to unmarshal message: %v\n", mcpConfig.Name, err)
						} else {
							select {
							case out <- msg:
							case <-ctx.Done():
								return
							}
						}
					}
				}
				currentEvent = ""
				currentData = nil
				continue
			}

			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				currentData = append(currentData, strings.TrimPrefix(line, "data: "))
			}
		}
	}()

	// Goroutine to handle sending messages
	go func() {
		for {
			select {
			case msg, ok := <-in:
				if !ok {
					return
				}

				endpointMu.Lock()
				postUrlStr := endpointUrl
				endpointMu.Unlock()

				body, err := json.Marshal(msg)
				if err != nil {
					ancli.Warnf("mcp_%s: Failed to marshal request: %v\n", mcpConfig.Name, err)
					continue
				}

				postReq, err := http.NewRequestWithContext(ctx, "POST", postUrlStr, bytes.NewReader(body))
				if err != nil {
					ancli.Warnf("mcp_%s: Failed to create POST request: %v\n", mcpConfig.Name, err)
					continue
				}
				postReq.Header.Set("Content-Type", "application/json")
				postReq.Header.Set("Accept", "application/json, text/event-stream")
				postReq.Header.Set("MCP-Protocol-Version", "2025-06-18")
				// Use Env for headers
				for k, v := range mcpConfig.Env {
					postReq.Header.Set(k, v)
				}

				postResp, err := client.Do(postReq)
				if err != nil {
					ancli.Warnf("mcp_%s: Failed to send POST request: %v\n", mcpConfig.Name, err)
					continue
				}
				postResp.Body.Close()

				if postResp.StatusCode != http.StatusOK && postResp.StatusCode != http.StatusAccepted {
					ancli.Warnf("mcp_%s: POST request failed with status: %d\n", mcpConfig.Name, postResp.StatusCode)
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return in, out, nil
}
