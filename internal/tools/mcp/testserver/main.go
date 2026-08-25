package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func main() {
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
	if os.Getenv("TEST_SERVER_CLOSE_STDERR") != "" {
		fmt.Fprintln(os.Stderr, "stderr line one")
		os.Stderr.Close()
	}
	if os.Getenv("TEST_SERVER_EXIT") != "" {
		return
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})
		case "tools/list":
			enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
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
					},
				},
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
