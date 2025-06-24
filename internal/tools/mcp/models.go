package mcp

import (
	"encoding/json"

	"github.com/baalimago/clai/internal/tools"
)

// ControlEvent instructs the Manager to register a new MCP server.
type ControlEvent struct {
	ServerName string
	Server     tools.McpServer
	InputChan  chan<- any
	OutputChan <-chan any
}

// Request represents a JSON-RPC request.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
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
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	InputSchema tools.InputSchema `json:"inputSchema"`
}
