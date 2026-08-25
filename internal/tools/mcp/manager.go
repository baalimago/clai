package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// ToolRegistrar receives the MCP tools discovered for one server. The
// concrete registry is supplied by the caller so each agent run owns its
// tool set instead of writing into the process-global tools registry.
type ToolRegistrar interface {
	Set(name string, t pub_models.LLMTool)
}

// mcpStartupTimeout bounds one server's initialize+tools/list handshake so a
// slow or hung server cannot block the setup of every other MCP tool. A
// ControlEvent.StartupTimeout overrides it per server.
const mcpStartupTimeout = 30 * time.Second

// Manager registers MCP servers and their tools into registrar. A server that
// fails its handshake is logged and skipped; it never fails the other servers.
func Manager(ctx context.Context, controlChannel <-chan ControlEvent, allToolsWg *sync.WaitGroup, registrar ToolRegistrar) {
	var wg sync.WaitGroup
	for {
		select {
		case ev := <-controlChannel:
			wg.Add(1)
			go func(e ControlEvent) {
				defer wg.Done()
				defer allToolsWg.Done()
				if err := handleServer(ctx, e, registrar); err != nil {
					ancli.Warnf("failed to setup mcp server '%v': %v\n", e.ServerName, err)
				}
			}(ev)
		case <-ctx.Done():
			wg.Wait()
			return
		}
	}
}

func handleServer(ctx context.Context, ev ControlEvent, registrar ToolRegistrar) error {
	// Bound the handshake so a hung server cannot stall the whole setup.
	timeout := ev.StartupTimeout
	if timeout <= 0 {
		timeout = mcpStartupTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Only cancel the client context on failure; on success the client
	// must remain alive to serve tool calls.  Cleanup happens when the
	// parent Manager context is cancelled.
	var initOk bool
	defer func() {
		if !initOk && ev.Cancel != nil {
			ev.Cancel()
		}
	}()
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
			timeout:    time.Duration(ev.Server.TimeoutSeconds) * time.Second,
		}
		registrar.Set(spec.Name, mt)
	}
	initOk = true
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
		case msg, ok := <-out:
			if !ok {
				return Response{}, fmt.Errorf("mcp: connection closed")
			}
			raw, ok := msg.(json.RawMessage)
			if !ok {
				ancli.Errf("failed to parse json.RawMessage, message: '%v'", msg)
				continue
			}
			var resp Response
			if err := json.Unmarshal(raw, &resp); err != nil {
				ancli.Errf("failed to unmarshal to Response, error: '%v'", msg)
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
