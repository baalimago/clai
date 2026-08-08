package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/baalimago/clai/internal/debugflags"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// 2Mib, some mcp servers sends very large messages
const mcpServerOutBufferSizeKib = 2048

// ServerLogSink receives the stderr output of a spawned MCP server process.
// Implementations decide how to display or buffer the lines. A nil sink keeps
// the legacy direct printing behaviour.
type ServerLogSink interface {
	// AppendServerLog delivers one non-empty stderr line.
	AppendServerLog(server, line string)
	// ServerExited reports that the server process terminated while the run
	// context was still alive. Implementations flush their buffered tail as an
	// error block so the crash reason stays visible.
	ServerExited(server string)
}

// Client starts the MCP server process defined by mcpConfig and returns channels
// for sending requests and receiving responses. sink receives the server's
// stderr lines; a nil sink prints them directly like before.
func Client(ctx context.Context, mcpConfig pub_models.McpServer, sink ServerLogSink) (chan<- any, <-chan any, error) {
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

	// waitDone closes once the process is reaped. The stdout reader selects on
	// it so a send can never stay blocked past process death, and the exit
	// report is never gated on the stdout reader.
	waitDone := make(chan struct{})
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(stdout)

		const maxCapacity = mcpServerOutBufferSizeKib * 1024
		buf := make([]byte, maxCapacity)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			var raw json.RawMessage
			if err := json.Unmarshal(
				scanner.Bytes(), &raw,
			); err != nil {

				if debugflags.Enabled("MCP_TOOL") {
					ancli.Warnf(
						"mcp_server: '%v' got decode error: %v",
						mcpConfig.Name, err,
					)
				}
				// Don't pass faulty messages upstream, instead just log them
				// Assume that the mcp server will eventually return json-formated data
				continue
			}

			select {
			case out <- raw:
			case <-waitDone:
				// The process is reaped; the message died with it. Drop it
				// instead of blocking a send nobody will ever read.
				return
			}
		}
		if ctx.Err() != nil &&
			errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		select {
		case <-waitDone:
			// cmd.Wait closed the pipe under this reader; expected on reap.
			return
		default:
		}
		if err := scanner.Err(); err != nil {
			ancli.Errf("mcp_%v: %s\n", mcpConfig.Name, err)
		}
	}()

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderr)
		const maxCapacity = mcpServerOutBufferSizeKib * 1024
		buf := make([]byte, maxCapacity)
		scanner.Buffer(buf, maxCapacity)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if sink != nil {
				sink.AppendServerLog(mcpConfig.Name, line)
				continue
			}
			ancli.Noticef("mcp_%v: %v\n", mcpConfig.Name, line)
		}
		if ctx.Err() != nil &&
			errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		if err := scanner.Err(); err != nil {
			ancli.Errf("mcp_%v: %s\n", mcpConfig.Name, err)
		}
		// stderr EOF alone does not prove process termination: a server may
		// close stderr intentionally and keep serving. Process-exit detection
		// lives in the cmd.Wait goroutine below.
	}()

	go func() {
		<-ctx.Done()
		stdin.Close()
	}()

	go func() {
		// Reap only after the stderr reader finished, so the crash tail is
		// complete before ServerExited flushes it and the stderr reader never
		// races Wait's pipe cleanup. stdout is deliberately not a gate: a
		// server may have written a message nobody consumed yet, which would
		// keep its reader busy past process death. An unexpected exit while
		// the run context is still alive is reported to the sink; a
		// context-cancelled teardown is not.
		<-stderrDone
		cmd.Wait()
		close(waitDone)
		if ctx.Err() != nil {
			return
		}
		if sink != nil {
			sink.ServerExited(mcpConfig.Name)
		}
	}()

	return in, out, nil
}
