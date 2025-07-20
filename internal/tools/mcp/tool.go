package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// mcpTool wraps a tool provided by an MCP server and implements tools.LLMTool.
type mcpTool struct {
	remoteName string
	spec       pub_models.Specification
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

func (m *mcpTool) Call(input pub_models.Input) (string, error) {
	nonNullableInp := make(map[string]any)
	if len(input) != 0 {
		nonNullableInp = input
	}
	id := m.nextID()
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      m.remoteName,
			"arguments": nonNullableInp,
		},
	}
	if misc.Truthy(os.Getenv("DEBUG_CALL")) {
		ancli.Noticef("mcpTool.Call req: %v", debug.IndentedJsonFmt(req))
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
			return "", errors.New(resp.Error.Message)
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
			return "", errors.New(buf.String())
		}
		return buf.String(), nil
	}
	return "", fmt.Errorf("connection closed")
}

func (m *mcpTool) Specification() pub_models.Specification {
	return m.spec
}
