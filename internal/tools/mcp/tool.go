package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/baalimago/clai/internal/tools"
)

// mcpTool wraps a tool provided by an MCP server and implements tools.LLMTool.
type mcpTool struct {
	remoteName string
	spec       tools.Specification
	inputChan  chan<- any
	outputChan <-chan any

	mu  sync.Mutex
	seq int
}

func (m *mcpTool) nextID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return m.seq
}

func (m *mcpTool) Call(input tools.Input) (string, error) {
	id := m.nextID()
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      m.remoteName,
			"arguments": input,
		},
	}

	m.inputChan <- req

	for msg := range m.outputChan {
		raw, ok := msg.(json.RawMessage)
		if !ok {
			if err, ok := msg.(error); ok {
				return "", err
			}
			continue
		}
		var resp Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return "", fmt.Errorf(resp.Error.Message)
		}
		var result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return "", fmt.Errorf("decode result: %w", err)
		}
		var buf bytes.Buffer
		for _, c := range result.Content {
			if c.Type == "text" {
				buf.WriteString(c.Text)
			}
		}
		if result.IsError {
			return "", fmt.Errorf(buf.String())
		}
		return buf.String(), nil
	}
	return "", fmt.Errorf("connection closed")
}

func (m *mcpTool) Specification() tools.Specification {
	return m.spec
}
