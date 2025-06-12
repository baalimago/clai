package mcp

import (
	"context"
	"encoding/json"

	"github.com/baalimago/clai/internal/tools"
)

// Manager supervises running MCP clients. Each ControlEvent represents a single
// server with which we exchange tool specifications and requests.
func Manager(ctx context.Context, controlChannel <-chan ControlEvent, statusChan chan<- error) {
	defer close(statusChan)
	for {
		select {
		case <-ctx.Done():
			statusChan <- ctx.Err()
			return
		case ev, ok := <-controlChannel:
			if !ok {
				statusChan <- nil
				return
			}
			go handleServer(ev)
		}
	}
}

func handleServer(ev ControlEvent) {
	// Ask the server for its available tools. We expect it to reply with a
	// JSON encoded list of tools.Specification values.
	ev.InputChan <- map[string]string{"command": "list_tools"}

	msg, ok := <-ev.OutputChan
	if !ok {
		return
	}

	raw, ok := msg.(json.RawMessage)
	if !ok {
		return
	}

	var specs []tools.Specification
	if err := json.Unmarshal(raw, &specs); err != nil {
		return
	}

	for _, spec := range specs {
		tools.Tools.Register(NewToolWrapper(spec, ev.InputChan, ev.OutputChan))
	}
}
