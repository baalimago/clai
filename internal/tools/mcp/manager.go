package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// Manager registers MCP servers and their tools.
func Manager(ctx context.Context, controlChannel <-chan ControlEvent, statusChan chan<- error, allToolsWg *sync.WaitGroup) {
	var wg sync.WaitGroup
	readyChan := make(chan struct{})
	defer close(readyChan)
	for {
		select {
		case ev := <-controlChannel:
			wg.Add(1)
			go func(e ControlEvent) {
				defer wg.Done()
				if err := handleServer(ctx, e, readyChan); err != nil {
					allToolsWg.Done()
					statusChan <- err
				}
			}(ev)
		case <-readyChan:
			allToolsWg.Done()
		case <-ctx.Done():
			wg.Wait()
			statusChan <- nil
			return
		}
	}
}

func handleServer(ctx context.Context, ev ControlEvent, readyChan chan struct{}) error {
	// Initialize
	initReq := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"capabilities": map[string]any{},
			"clientInfo": map[string]string{
				"name":    "clai",
				"version": "dev",
			},
			"protocolVersion": "2025-03-26",
		},
	}
	resp, err := sendRequest(ctx, ev.InputChan, ev.OutputChan, initReq)
	if err != nil {
		return fmt.Errorf("initialize err: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize responded with err: %s", resp.Error.Message)
	}

	// Send initialized notification
	ev.InputChan <- map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	}

	// List tools
	listReq := Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}
	resp, err = sendRequest(ctx, ev.InputChan, ev.OutputChan, listReq)
	if err != nil {
		return fmt.Errorf("tools/list err: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("tools/list resp.Error: %s", resp.Error.Message)
	}
	var listRes struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &listRes); err != nil {
		return fmt.Errorf("decode list result: %w", err)
	}

	for _, t := range listRes.Tools {
		t.InputSchema.Patch()
		toolName := fmt.Sprintf("mcp_%s_%s", ev.ServerName, t.Name)

		if !t.InputSchema.IsOk() {
			ancli.Warnf("tool: '%v' has issues that the LLM will complain about, skipping\n", toolName)
			continue
		}
		spec := pub_models.Specification{
			Name:        toolName,
			Description: t.Description,
			Inputs:      &t.InputSchema,
		}
		mt := &mcpTool{
			remoteName: t.Name,
			spec:       spec,
			inputChan:  ev.InputChan,
			outputChan: ev.OutputChan,
		}
		tools.Registry.Set(spec.Name, mt)
	}
	readyChan <- struct{}{}
	return nil
}

func sendRequest(ctx context.Context, in chan<- any, out <-chan any, req Request) (Response, error) {
	select {
	case in <- req:
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	for {
		select {
		case msg := <-out:
			raw, ok := msg.(json.RawMessage)
			if !ok {
				if err, ok := msg.(error); ok {
					return Response{}, fmt.Errorf("failed to parse json.RawMessage: '%v', err: %w", msg, err)
				}
				continue
			}
			var resp Response
			if err := json.Unmarshal(raw, &resp); err != nil {
				continue
			}
			if resp.ID == req.ID {
				return resp, nil
			}
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
}
