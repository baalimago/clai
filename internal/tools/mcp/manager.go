package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// ToolRegistrar receives the MCP tools discovered for one server. The
// concrete registry is supplied by the caller so each agent run owns its
// tool set instead of writing into the process-global tools registry.
type ToolRegistrar interface {
	Set(name string, t pub_models.LLMTool)
}

// handshakeBounder is implemented by a Conn that carries its own handshake
// bound. It replaces the retired per-event ControlEvent.StartupTimeout: the
// bound now lives on the connection rather than being restated per call,
// found through the same feature-detection pattern already used for
// ServerLogSink's optional setupSucceeded method.
type handshakeBounder interface {
	HandshakeBound() time.Duration
}

// HandshakeBoundOf reports the duration a caller driving conn's handshake
// itself (the schema cache's miss path, beside Manager's own use) should
// bound it by.
func HandshakeBoundOf(conn Conn) time.Duration {
	if hb, ok := conn.(handshakeBounder); ok {
		return hb.HandshakeBound()
	}
	return mcpHandshakeBound
}

// Manager registers MCP servers and their tools into registrar. A server that
// fails its handshake is skipped; it never fails the other servers. When
// startupFailures is non-nil, each failed handshake is reported on it (before
// the server's allToolsWg.Done, so a Wait on that group implies every report
// was sent) and no warning is printed; a nil channel keeps the legacy
// warn-and-skip behaviour (worklog 2026-09-05-error-propagation, D13).
func Manager(ctx context.Context, controlChannel <-chan ControlEvent, allToolsWg *sync.WaitGroup, registrar ToolRegistrar, startupFailures chan<- StartupFailure) {
	var wg sync.WaitGroup
	for {
		select {
		case ev := <-controlChannel:
			wg.Add(1)
			go func(e ControlEvent) {
				defer wg.Done()
				defer allToolsWg.Done()
				if err := handleServer(ctx, e, registrar); err != nil {
					if startupFailures != nil {
						startupFailures <- StartupFailure{ServerName: e.ServerName, Err: err}
						return
					}
					ancli.Warnf("failed to setup mcp server '%v': %v\n", e.ServerName, err)
				}
			}(ev)
		case <-ctx.Done():
			wg.Wait()
			return
		}
	}
}

// initializeHandshake performs the initialize call and the
// notifications/initialized notification, the half of the MCP handshake
// shared by every caller that brings up a connection: handleServer and
// Handshake continue on to tools/list, a lazily resolved Connector stops
// here. The raw initialize result is returned so a caller that must persist
// it (Handshake, for the schema cache's server_info field) does not issue a
// second initialize to get it.
func initializeHandshake(ctx context.Context, conn Conn, serverName string) (json.RawMessage, error) {
	initParams := map[string]any{
		"capabilities": map[string]any{},
		"clientInfo": map[string]string{
			"name":    "clai",
			"version": "dev",
		},
		"protocolVersion": ProtocolVersion,
	}
	raw, err := conn.Call(ctx, "initialize", initParams)
	if err != nil {
		return nil, claierr.NewMcpServerStartup(serverName, "initialize", err)
	}
	if err := conn.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return nil, claierr.NewMcpServerStartup(serverName, "initialize", err)
	}
	return raw, nil
}

// Handshake performs the full connect-time sequence over conn: initialize,
// the initialized notification, and tools/list. It returns the serverInfo
// object from the initialize result (an empty object when absent) and the
// raw tools/list "tools" array, verbatim — the two pieces of the record
// formats a caller that persists a schema cache entry needs, so it never has
// to re-derive them from Manager's registration side effect.
func Handshake(ctx context.Context, conn Conn, serverName string) (serverInfo, toolsArray json.RawMessage, err error) {
	initRaw, err := initializeHandshake(ctx, conn, serverName)
	if err != nil {
		return nil, nil, err
	}
	listRaw, err := conn.Call(ctx, "tools/list", nil)
	if err != nil {
		return nil, nil, claierr.NewMcpServerStartup(serverName, "tools/list", err)
	}
	toolsArray, err = extractToolsArray(listRaw)
	if err != nil {
		return nil, nil, claierr.NewMcpServerStartup(serverName, "tools/list", fmt.Errorf("decode list result: %w", err))
	}
	return extractServerInfo(initRaw), toolsArray, nil
}

// extractServerInfo pulls the serverInfo sub-object out of an initialize
// result, never interpreting its contents. Absent is reported as an empty
// object rather than nil, so a captured record always carries a parseable
// value.
func extractServerInfo(initRaw json.RawMessage) json.RawMessage {
	var parsed struct {
		ServerInfo json.RawMessage `json:"serverInfo"`
	}
	if err := json.Unmarshal(initRaw, &parsed); err != nil || len(parsed.ServerInfo) == 0 {
		return json.RawMessage(`{}`)
	}
	return parsed.ServerInfo
}

// extractToolsArray pulls the "tools" array out of a tools/list result.
func extractToolsArray(listRaw json.RawMessage) (json.RawMessage, error) {
	var parsed struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(listRaw, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Tools) == 0 {
		return json.RawMessage(`[]`), nil
	}
	return parsed.Tools, nil
}

// RegisterTools parses toolsArray (a tools/list result's "tools" array, as
// returned by Handshake or read verbatim from a schema cache entry) and
// registers one LLMTool per entry under mcp_<serverName>_<tool>, each wired
// to connector. A hit and a miss call this with the same shape, which is
// what makes them register an identical tool set. authResolver and
// authTimeout are phase 6's addition, carried onto every tool this call
// registers unchanged; authResolver may be nil.
func RegisterTools(toolsArray json.RawMessage, serverName string, connector Connector, timeout time.Duration, registrar ToolRegistrar, authResolver AuthResolver, authTimeout time.Duration) error {
	var tools []Tool
	if err := json.Unmarshal(toolsArray, &tools); err != nil {
		return claierr.NewMcpServerStartup(serverName, "tools/list", fmt.Errorf("decode tools array: %w", err))
	}
	for _, t := range tools {
		t.InputSchema.Patch()
		toolName := fmt.Sprintf("mcp_%s_%s", serverName, t.Name)

		if !t.InputSchema.IsOk() {
			ancli.Warnf("tool: '%v' has issues that the LLM will complain about, skipping\n", toolName)
			continue
		}
		spec := pub_models.Specification{
			Name:        toolName,
			Description: t.Description,
			Inputs:      &t.InputSchema,
		}
		registrar.Set(spec.Name, NewTool(connector, t.Name, spec, timeout, serverName, authResolver, authTimeout))
	}
	return nil
}

// handleServer performs the initialize/tools-list handshake over ev.Conn and
// registers every tool it advertises. One id source, owned by the Conn,
// serves initialize, tools/list and every tool call of this server.
func handleServer(ctx context.Context, ev ControlEvent, registrar ToolRegistrar) error {
	// Bound the handshake so a hung server cannot stall the whole setup.
	ctx, cancel := context.WithTimeout(ctx, HandshakeBoundOf(ev.Conn))
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

	_, toolsArray, err := Handshake(ctx, ev.Conn, ev.ServerName)
	if err != nil {
		return err
	}
	// An eager server's connection is already resolved at setup time, so a
	// later challenge on this same Conn (e.g. an access token revoked
	// mid-run) is out of this phase's scope: no resolver, no wait. nil, 0
	// keep that explicit rather than implicit.
	if err := RegisterTools(toolsArray, ev.ServerName, resolvedConnector{ev.Conn}, time.Duration(ev.Server.TimeoutSeconds)*time.Second, registrar, nil, 0); err != nil {
		return err
	}
	initOk = true
	return nil
}
