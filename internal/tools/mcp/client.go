package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// 2Mib, some mcp servers sends very large messages
const mcpServerOutBufferSizeKib = 2048

// Client starts the MCP server process defined by mcpConfig and returns channels
// for sending requests and receiving responses.
func Client(ctx context.Context, mcpConfig pub_models.McpServer) (chan<- any, <-chan any, error) {
	cmd := exec.CommandContext(ctx, mcpConfig.Command, mcpConfig.Args...)
	cmd.Env = os.Environ()
	if mcpConfig.EnvFile != "" {
		envFromFile, err := loadEnvFile(mcpConfig.EnvFile)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range envFromFile {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
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
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stderr pipe: %w", err)
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
				err := enc.Encode(msg)
				if err != nil {
					ancli.Errf("client: %v, got error when encoding message: '%v', error: %v", mcpConfig.Name, msg, err)
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stdout)

		const maxCapacity = mcpServerOutBufferSizeKib * 1024
		buf := make([]byte, maxCapacity)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			var raw json.RawMessage
			if err := json.Unmarshal(
				scanner.Bytes(), &raw); err != nil {

				if misc.Truthy(os.Getenv("DEBUG_MCP_TOOL")) {
					ancli.Warnf(
						"mcp_server: '%v' got decode error: %v",
						mcpConfig.Name, err)
				}
				// Don't pass faulty messages upstream, instead just log them
				// Assume that the mcp server will eventually return json-formated data
				continue
			}

			out <- raw
		}
		close(out)
		if ctx.Err() != nil &&
			errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		if err := scanner.Err(); err != nil {
			ancli.Errf("mcp_%v: %s\n", mcpConfig.Name, err)
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		const maxCapacity = mcpServerOutBufferSizeKib * 1024
		buf := make([]byte, maxCapacity)
		scanner.Buffer(buf, maxCapacity)
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				ancli.Noticef("mcp_%v: %v\n", mcpConfig.Name, line)
			}
		}
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		if err := scanner.Err(); err != nil {
			ancli.Errf("mcp_%v: %s\n", mcpConfig.Name, err)
		}
	}()

	go func() {
		<-ctx.Done()
		stdin.Close()
		cmd.Wait()
	}()

	return in, out, nil
}
