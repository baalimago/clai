package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/baalimago/clai/internal/tools"
)

// ToolWrapper implements tools.LLMTool and forwards calls to an MCP server
// using the provided channels.
type ToolWrapper struct {
	spec       tools.Specification
	inputChan  chan<- any
	outputChan <-chan any
}

func NewToolWrapper(spec tools.Specification, in chan<- any, out <-chan any) ToolWrapper {
	return ToolWrapper{spec: spec, inputChan: in, outputChan: out}
}

func (m ToolWrapper) Call(inp tools.Input) (string, error) {
	req := map[string]any{
		"name":   m.spec.Name,
		"inputs": inp,
	}
	select {
	case m.inputChan <- req:
	default:
		return "", fmt.Errorf("failed to send request to mcp server")
	}

	resp, ok := <-m.outputChan
	if !ok {
		return "", fmt.Errorf("mcp server closed")
	}

	if raw, ok := resp.(json.RawMessage); ok {
		var out string
		if err := json.Unmarshal(raw, &out); err != nil {
			return "", fmt.Errorf("failed to unmarshal mcp response: %w", err)
		}
		return out, nil
	}

	s, ok := resp.(string)
	if !ok {
		return "", fmt.Errorf("unexpected response type %T", resp)
	}
	return s, nil
}

func (m ToolWrapper) Specification() tools.Specification {
	return m.spec
}
