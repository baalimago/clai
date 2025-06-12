package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/baalimago/clai/internal/tools"
)

// Client spawns the configured MCP server as a subprocess and returns channels
// used to send requests and receive responses. All messages are JSON encoded.
func Client(ctx context.Context, mcpConfig tools.McpServer) (chan<- any, <-chan any, error) {
	cmd := exec.CommandContext(ctx, mcpConfig.Command, mcpConfig.Args...)
	if len(mcpConfig.Env) > 0 {
		env := os.Environ()
		for k, v := range mcpConfig.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start mcp server: %w", err)
	}

	inputChan := make(chan any)
	outputChan := make(chan any)

	go func() {
		enc := json.NewEncoder(stdin)
		for msg := range inputChan {
			_ = enc.Encode(msg)
		}
		stdin.Close()
	}()

	go func() {
		dec := json.NewDecoder(stdout)
		for {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				break
			}
			outputChan <- raw
		}
		close(outputChan)
	}()

	go func() { _ = cmd.Wait() }()

	return inputChan, outputChan, nil
}
