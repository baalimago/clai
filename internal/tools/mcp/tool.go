package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/baalimago/clai/internal/utils"
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
		raw, open := msg.(json.RawMessage)
		if !open {
			if err, ok := msg.(error); ok {
				if misc.Truthy(os.Getenv("DEBUG_MCP_TOOL")) {
					ancli.Okf("mcp_server closed outputChan msg: '%s', err: %v", msg, err)
				}
				return "", err
			}
			return "", errors.New("output channel unexpectedly closed")
		}

		if misc.Truthy(os.Getenv("DEBUG_MCP_TOOL")) {
			rawS, _ := raw.MarshalJSON()
			shortened, _ := utils.WidthAppropriateStringTrunc(string(rawS), "", 10)
			ancli.Okf("mcp_server client received: '%s'", shortened)
		}
		var resp Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			ancli.Errf("mcpTool: '%v' failed to unmarshal: '%v'", m.remoteName, err)
			continue
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			if misc.Truthy(os.Getenv("DEBUG_MCP_TOOL")) {
				ancli.Okf("Now returning response.Error: '%v'", resp.Error)
			}
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
			if misc.Truthy(os.Getenv("DEBUG_MCP_TOOL")) {
				ancli.Okf("Now returning result error: '%v'", err)
			}
			return "", fmt.Errorf("decode result: %w", err)
		}
		var buf bytes.Buffer
		for _, c := range result.Content {
			if c.Type == "text" {
				buf.WriteString(c.Text)
			}
		}
		if result.IsError {
			if misc.Truthy(os.Getenv("DEBUG_MCP_TOOL")) {
				ancli.Okf("Now returning result as error: '%v'", buf.String())
			}
			return "", errors.New(buf.String())
		}
		if misc.Truthy(os.Getenv("DEBUG_MCP_TOOL")) {
			ancli.Okf("Now returning: '%v'", buf.String())
		}
		return buf.String(), nil
	}
	return "", fmt.Errorf("connection closed")
}

func (m *mcpTool) Specification() pub_models.Specification {
	return m.spec
}
