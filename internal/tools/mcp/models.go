package mcp

import (
	"context"
	"encoding/json"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// StartupFailure reports one server whose initialize/tools-list handshake
// failed. The Manager skips the server either way; whether the failure is
// fatal for the caller's setup is the caller's decision (worklog
// 2026-09-05-error-propagation, D13).
type StartupFailure struct {
	ServerName string
	Err        error
}

// ControlEvent instructs the Manager to register a new MCP server. Conn is
// already connected; the handshake bound it carries, if any, replaces the
// retired per-event StartupTimeout override. OnHandshake, when non-nil, is
// called with the initialize result's serverInfo object and the raw
// tools/list array after a successful handshake and before any tool
// registers, so a caller can persist what the handshake observed without this
// package depending on the schema cache. It runs on the Manager's per-server
// goroutine.
type ControlEvent struct {
	ServerName  string
	Server      pub_models.McpServer
	Conn        Conn
	Cancel      context.CancelFunc
	OnHandshake func(serverInfo, toolsArray json.RawMessage)
}

// Request represents a JSON-RPC request.
type Request struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// Response represents a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC error structure.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool describes a tool as returned by tools/list.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema pub_models.InputSchema `json:"inputSchema"`
}
