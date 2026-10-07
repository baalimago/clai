package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// runWithEnv runs main with the given environment and stdin requests, and
// returns the decoded stdout responses and everything written to stderr.
func runWithEnv(t *testing.T, env map[string]string, reqs []testReq) ([]map[string]any, string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}

	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe in: %v", err)
	}
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe out: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe err: %v", err)
	}

	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = rIn, wOut, wErr

	var stderrBuf bytes.Buffer
	var stderrWG sync.WaitGroup
	stderrWG.Go(func() { _, _ = io.Copy(&stderrBuf, rErr) })

	var wg sync.WaitGroup
	wg.Go(func() { main() })

	enc := json.NewEncoder(wIn)
	for _, rq := range reqs {
		if err := enc.Encode(rq); err != nil {
			t.Fatalf("encode req: %v", err)
		}
	}
	_ = wIn.Close()

	wg.Wait()
	_ = wOut.Close()
	os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr

	data, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	_ = wErr.Close()
	stderrWG.Wait()

	var out []map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode resp: %v", err)
		}
		out = append(out, m)
	}
	return out, stderrBuf.String()
}

func TestMainImmediateExitBranches(t *testing.T) {
	testCases := []struct {
		name        string
		env         map[string]string
		wantStderr  []string
		wantOneNote bool
	}{
		{name: "plain exit", env: map[string]string{"TEST_SERVER_EXIT": "1"}},
		{
			name:       "crash tail",
			env:        map[string]string{"TEST_SERVER_CRASH_TAIL": "1"},
			wantStderr: []string{"worker stopped", "signal received"},
		},
		{
			name:        "unconsumed stdout",
			env:         map[string]string{"TEST_SERVER_UNCONSUMED_STDOUT": "1"},
			wantStderr:  []string{"worker stopped"},
			wantOneNote: true,
		},
		{
			name:       "stderr lines",
			env:        map[string]string{"TEST_SERVER_STDERR": "1"},
			wantStderr: []string{"stderr line one", "stderr line two: an error occurred"},
		},
		{
			name:       "stderr bulk",
			env:        map[string]string{"TEST_SERVER_STDERR_BULK": "50"},
			wantStderr: []string{"bulk line 0", "bulk line 49"},
		},
		{
			name:       "closing stderr",
			env:        map[string]string{"TEST_SERVER_CLOSE_STDERR": "1"},
			wantStderr: []string{"stderr line one"},
		},
		{
			name:       "auth hang waits for stdin",
			env:        map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
			wantStderr: []string{"Please authorize this client by visiting:", "https://example.com/authorize?client_id=test"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resps, stderr := runWithEnv(t, tc.env, nil)
			if tc.wantOneNote {
				if len(resps) != 1 || resps[0]["method"] != "notifications/message" {
					t.Errorf("responses = %v, want the one unconsumed notification", resps)
				}
			} else if len(resps) != 0 {
				t.Errorf("responses = %v, want none", resps)
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q, want it to contain %q", stderr, want)
				}
			}
		})
	}
}

func TestMainSpawnLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "spawn.log")
	startedAt := time.Now().UnixNano()
	runWithEnv(t, map[string]string{
		"TEST_SERVER_SPAWN_LOG": logPath,
		"TEST_SERVER_EXIT":      "1",
	}, nil)
	finishedAt := time.Now().UnixNano()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read spawn log: %v", err)
	}
	lines := strings.Fields(strings.TrimSpace(string(data)))
	if len(lines) != 1 {
		t.Fatalf("spawn log = %q, want exactly one timestamp", data)
	}
	timestamp, err := strconv.ParseInt(lines[0], 10, 64)
	if err != nil {
		t.Fatalf("spawn log timestamp = %q, want Unix nanoseconds: %v", lines[0], err)
	}
	if timestamp < startedAt || timestamp > finishedAt {
		t.Fatalf("spawn timestamp = %d, want value between %d and %d", timestamp, startedAt, finishedAt)
	}
}

func TestMainGrandchild(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "grandchild.pid")
	runWithEnv(t, map[string]string{
		"TEST_SERVER_SPAWN_GRANDCHILD":    "0",
		"TEST_SERVER_GRANDCHILD_PID_FILE": pidPath,
		"TEST_SERVER_EXIT":                "1",
	}, nil)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Errorf("pid file is empty")
	}
}

func TestMainPreHandshakeDelay(t *testing.T) {
	resps, _ := runWithEnv(t, map[string]string{"TEST_SERVER_PRE_HANDSHAKE_DELAY_MS": "1"}, []testReq{
		{JSONRPC: "2.0", ID: 1, Method: "initialize"},
	})
	if _, ok := byID(resps, 1); !ok {
		t.Fatalf("no response to delayed initialize: %v", resps)
	}
}

func TestMainProtocolVersion(t *testing.T) {
	t.Run("unsupported version is rejected", func(t *testing.T) {
		resps, _ := runWithEnv(t, nil, []testReq{
			{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{"protocolVersion": "2000-01-01"}},
		})
		r, ok := byID(resps, 1)
		if !ok {
			t.Fatalf("no response: %v", resps)
		}
		er, ok := r["error"].(map[string]any)
		if !ok || int(er["code"].(float64)) != -32602 {
			t.Fatalf("error = %v, want code -32602", r["error"])
		}
	})

	t.Run("current version is accepted", func(t *testing.T) {
		resps, _ := runWithEnv(t, nil, []testReq{
			{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{"protocolVersion": "2025-06-18"}},
		})
		r, ok := byID(resps, 1)
		if !ok || r["result"] == nil {
			t.Fatalf("response = %v, want a result", resps)
		}
	})
}

func TestMainUnknownToolName(t *testing.T) {
	resps, _ := runWithEnv(t, map[string]string{"TEST_SERVER_UNKNOWN_TOOL_NAME": "ghost"}, []testReq{
		{JSONRPC: "2.0", ID: 1, Method: "tools/list"},
		{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: map[string]any{"name": "ghost", "arguments": map[string]any{}}},
	})

	list, ok := byID(resps, 1)
	if !ok {
		t.Fatalf("no tools/list response: %v", resps)
	}
	tools := list["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, raw := range tools {
		names = append(names, raw.(map[string]any)["name"].(string))
	}
	if !strings.Contains(strings.Join(names, ","), "ghost") {
		t.Errorf("tools = %v, want ghost advertised", names)
	}

	call, ok := byID(resps, 2)
	if !ok {
		t.Fatalf("no tools/call response: %v", resps)
	}
	er, ok := call["error"].(map[string]any)
	if !ok || int(er["code"].(float64)) != -32601 {
		t.Fatalf("error = %v, want code -32601", call["error"])
	}
}

func TestMainListChangedTriggerTool(t *testing.T) {
	resps, _ := runWithEnv(t, map[string]string{"TEST_SERVER_LIST_CHANGED_TRIGGER_TOOL": "nudge"}, []testReq{
		{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: map[string]any{"name": "nudge", "arguments": map[string]any{}}},
	})

	var notified bool
	var answered bool
	for _, r := range resps {
		if r["method"] == "notifications/tools/list_changed" {
			notified = true
		}
		if id, ok := r["id"]; ok && id.(float64) == 1 {
			answered = true
		}
	}
	if !notified || !answered {
		t.Fatalf("responses = %v, want both a list_changed notification and the call's answer", resps)
	}
}

func TestMainHangToolNeverAnswers(t *testing.T) {
	resps, _ := runWithEnv(t, nil, []testReq{
		{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: map[string]any{"name": "hang", "arguments": map[string]any{}}},
	})
	if len(resps) != 0 {
		t.Fatalf("responses = %v, want none for a wedged tool", resps)
	}
}
