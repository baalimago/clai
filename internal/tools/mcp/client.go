package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/baalimago/clai/internal/tools"
)

// Client starts the MCP server process defined by mcpConfig and returns channels
// for sending requests and receiving responses.
func Client(ctx context.Context, mcpConfig tools.McpServer) (chan<- any, <-chan any, error) {
	cmd := exec.CommandContext(ctx, mcpConfig.Command, mcpConfig.Args...)
	cmd.Env = os.Environ()
	for k, v := range mcpConfig.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start mcp server: %w", err)
	}

	in := make(chan any)
	out := make(chan any)

	go func() {
		enc := json.NewEncoder(stdin)
		for {
			select {
			case msg, ok := <-in:
				if !ok {
					return
				}
				enc.Encode(msg)
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		dec := json.NewDecoder(stdout)
		for {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				if err == io.EOF {
					close(out)
					return
				}
				out <- fmt.Errorf("decode: %w", err)
				close(out)
				return
			}
			out <- raw
		}
	}()

	go func() {
		<-ctx.Done()
		stdin.Close()
		cmd.Wait()
	}()

	return in, out, nil
}
