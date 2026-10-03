package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func main() {
	// TEST_SERVER_SPAWN_LOG names a file to append one line to on every
	// invocation, so a caller can count real process births across repeated
	// setup runs (the schema cache's warm-cache proof) without instrumenting
	// anything else about this fixture.
	if logPath := os.Getenv("TEST_SERVER_SPAWN_LOG"); logPath != "" {
		// Fatal, not best-effort (R2-03 review round 2): a spawn whose log
		// write fails is indistinguishable from no spawn at all, and "no
		// spawn" is exactly what every warm-cache test's assertion rests on.
		// A broken counter must fail loudly here rather than silently prove
		// the claim it was supposed to disprove.
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "TEST_SERVER_SPAWN_LOG: open %q: %v\n", logPath, err)
			os.Exit(1)
		}
		// A timestamp, not a constant marker: a caller proving two
		// servers were resolved concurrently rather than one after
		// another (the setup loop's per-server concurrency, D39) reads
		// how tightly the recorded births cluster. A caller counting
		// births, which is every other test using this file, only
		// counts lines and never looks at the content.
		fmt.Fprintln(f, time.Now().UnixNano())
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "TEST_SERVER_SPAWN_LOG: close %q: %v\n", logPath, err)
			os.Exit(1)
		}
	}
	// TEST_SERVER_SPAWN_GRANDCHILD backgrounds a child that outlives this
	// process and inherits stderr, reproducing the launcher shape of a real
	// MCP server (npx -> node -> browser). A teardown that only signals this
	// pid leaves that grandchild running; the test asserts it is gone.
	if life := os.Getenv("TEST_SERVER_SPAWN_GRANDCHILD"); life != "" {
		grandchild := exec.Command("sleep", life)
		grandchild.Stderr = os.Stderr
		if err := grandchild.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "grandchild start: %v\n", err)
			os.Exit(1)
		}
		pid := grandchild.Process.Pid
		grandchild.Process.Release()
		// The caller needs the grandchild's pid to assert it is gone; it
		// cannot learn it any other way, since the process is a
		// grandchild of the process it spawned.
		if path := os.Getenv("TEST_SERVER_GRANDCHILD_PID_FILE"); path != "" {
			if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "grandchild pid file: %v\n", err)
				os.Exit(1)
			}
		}
	}
	if os.Getenv("TEST_SERVER_AUTH_HANG") != "" {
		// Print an OAuth prompt, then wait for an authorization that never
		// comes: stay alive without answering any request.
		fmt.Fprintln(os.Stderr, "Please authorize this client by visiting:")
		fmt.Fprintln(os.Stderr, "https://example.com/authorize?client_id=test")
		io.Copy(io.Discard, os.Stdin)
		return
	}
	if os.Getenv("TEST_SERVER_CRASH_TAIL") != "" {
		fmt.Fprintln(os.Stderr, "worker stopped")
		fmt.Fprintln(os.Stderr, "signal received")
		return
	}
	if os.Getenv("TEST_SERVER_UNCONSUMED_STDOUT") != "" {
		// One valid JSON line nobody will consume, then a keyword-free crash
		// tail, then exit. The client's stdout reader blocks delivering the
		// line; exit detection must not depend on that reader finishing.
		fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"notifications/message","params":{}}`)
		fmt.Fprintln(os.Stderr, "worker stopped")
		return
	}
	if os.Getenv("TEST_SERVER_STDERR") != "" {
		fmt.Fprintln(os.Stderr, "stderr line one")
		fmt.Fprintln(os.Stderr, "stderr line two: an error occurred")
	}
	if n := os.Getenv("TEST_SERVER_STDERR_BULK"); n != "" {
		// More lines than a reader can drain before this exits, so a reap
		// that closes the pipe under the reader shows up as a truncated tail.
		count, err := strconv.Atoi(n)
		if err != nil {
			fmt.Fprintf(os.Stderr, "TEST_SERVER_STDERR_BULK: %v\n", err)
			os.Exit(1)
		}
		for i := range count {
			fmt.Fprintf(os.Stderr, "bulk line %d\n", i)
		}
		return
	}
	if os.Getenv("TEST_SERVER_CLOSE_STDERR") != "" {
		fmt.Fprintln(os.Stderr, "stderr line one")
		os.Stderr.Close()
	}
	if os.Getenv("TEST_SERVER_EXIT") != "" {
		return
	}
	if ms := os.Getenv("TEST_SERVER_PRE_HANDSHAKE_DELAY_MS"); ms != "" {
		if d, err := strconv.Atoi(ms); err == nil {
			time.Sleep(time.Duration(d) * time.Millisecond)
		}
	}
	// TEST_SERVER_UNKNOWN_TOOL_NAME and TEST_SERVER_LIST_CHANGED_TRIGGER_TOOL
	// let a test drive the schema cache's two endpoint-agnostic invalidation
	// signals (R2-16) against a real spawned process, rather than a
	// hand-built seam: a tool that tools/list advertises but tools/call
	// answers as unknown, and a tool whose call first emits an unsolicited
	// notifications/tools/list_changed notification on stdout.
	unknownToolName := os.Getenv("TEST_SERVER_UNKNOWN_TOOL_NAME")
	listChangedTriggerTool := os.Getenv("TEST_SERVER_LIST_CHANGED_TRIGGER_TOOL")

	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			// A missing protocolVersion is accepted leniently, so a raw
			// request built without it (as several low-level tests do)
			// still gets a handshake; a present-but-wrong version is
			// rejected, which is what proves the real client advertises
			// the current one.
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &p)
			if p.ProtocolVersion != "" && p.ProtocolVersion != "2025-06-18" {
				enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32602, "message": "unsupported protocol version: " + p.ProtocolVersion},
				})
				continue
			}
			enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})
		case "tools/list":
			toolList := []map[string]any{
				{
					"name":        "echo",
					"description": "echo text",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"text"},
						"properties": map[string]any{
							"text": map[string]any{
								"type":        "string",
								"description": "text to echo",
							},
						},
					},
				},
				{
					"name":        "hang",
					"description": "never responds, simulating a wedged server",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				},
			}
			for _, extra := range []string{unknownToolName, listChangedTriggerTool} {
				if extra == "" {
					continue
				}
				toolList = append(toolList, map[string]any{
					"name":        extra,
					"description": "test fixture tool",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				})
			}
			enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"tools": toolList},
			})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			if p.Name == "hang" {
				// Simulate a wedged server (e.g. a browser navigation that
				// never completes): swallow the request and answer nothing.
				continue
			}
			if unknownToolName != "" && p.Name == unknownToolName {
				enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32601, "message": "method not found: " + p.Name},
				})
				continue
			}
			if listChangedTriggerTool != "" && p.Name == listChangedTriggerTool {
				// Written before the call's own response, synchronously: this
				// loop is single-threaded, so the two writes never race.
				enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"method":  "notifications/tools/list_changed",
				})
			}
			text, _ := p.Arguments["text"].(string)
			result := map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			}
			if text == "error" {
				result["isError"] = true
			}
			enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  result,
			})
		default:
			enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    -32601,
					"message": "method not found",
				},
			})
		}
	}
}
