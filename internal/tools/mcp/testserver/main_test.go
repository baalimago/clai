package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"
)

type testReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func runServer(t *testing.T, reqs []testReq) []map[string]any {
	t.Helper()

	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe in: %v", err)
	}
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe out: %v", err)
	}

	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = rIn, wOut

	var wg sync.WaitGroup
	wg.Go(func() {
		main()
	})

	enc := json.NewEncoder(wIn)
	for _, rq := range reqs {
		if err := enc.Encode(rq); err != nil {
			t.Fatalf("encode req: %v", err)
		}
	}
	_ = wIn.Close()

	wg.Wait()
	_ = wOut.Close()

	data, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}

	os.Stdin, os.Stdout = oldIn, oldOut

	dec := json.NewDecoder(bytes.NewReader(data))
	var out []map[string]any
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
	return out
}

func byID(resps []map[string]any, id int) (map[string]any, bool) {
	for _, r := range resps {
		v, ok := r["id"]
		if !ok {
			continue
		}
		f, ok := v.(float64)
		if !ok {
			continue
		}
		if int(f) == id {
			return r, true
		}
	}
	return nil, false
}

func TestServer_Initialize(t *testing.T) {
	reqs := []testReq{
		{JSONRPC: "2.0", ID: 1, Method: "initialize"},
	}
	resps := runServer(t, reqs)

	if len(resps) != 1 {
		t.Fatalf("want 1 resp, got %d", len(resps))
	}
	r, ok := byID(resps, 1)
	if !ok {
		t.Fatalf("resp id 1 not found")
	}
	if r["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v", r["jsonrpc"])
	}
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not map: %T", r["result"])
	}
	if len(res) != 0 {
		t.Errorf("want empty result, got %v", res)
	}
}

func TestServer_ToolsList(t *testing.T) {
	reqs := []testReq{
		{JSONRPC: "2.0", ID: 2, Method: "tools/list"},
	}
	resps := runServer(t, reqs)

	r, ok := byID(resps, 2)
	if !ok {
		t.Fatalf("resp id 2 not found")
	}
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not map")
	}
	ts, ok := res["tools"].([]any)
	if !ok || len(ts) == 0 {
		t.Fatalf("tools not slice or empty: %T", res["tools"])
	}
	t0, ok := ts[0].(map[string]any)
	if !ok {
		t.Fatalf("tool[0] not map: %T", ts[0])
	}
	if t0["name"] != "echo" {
		t.Errorf("tool name = %v", t0["name"])
	}
}

func TestServer_ToolsCall_Echo(t *testing.T) {
	reqs := []testReq{
		{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params: map[string]any{
				"name": "echo",
				"arguments": map[string]any{
					"text": "hello",
				},
			},
		},
	}
	resps := runServer(t, reqs)

	r, ok := byID(resps, 3)
	if !ok {
		t.Fatalf("resp id 3 not found")
	}
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not map")
	}
	ce, ok := res["isError"].(bool)
	if !ok || ce {
		t.Fatalf("isError want false, got %v", res["isError"])
	}
	cs, ok := res["content"].([]any)
	if !ok || len(cs) == 0 {
		t.Fatalf("content invalid: %T", res["content"])
	}
	c0, ok := cs[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] not map")
	}
	if c0["type"] != "text" {
		t.Errorf("type = %v", c0["type"])
	}
	if c0["text"] != "hello" {
		t.Errorf("text = %v", c0["text"])
	}
}

func TestServer_ToolsCall_Error(t *testing.T) {
	reqs := []testReq{
		{
			JSONRPC: "2.0",
			ID:      4,
			Method:  "tools/call",
			Params: map[string]any{
				"name": "echo",
				"arguments": map[string]any{
					"text": "error",
				},
			},
		},
	}
	resps := runServer(t, reqs)

	r, ok := byID(resps, 4)
	if !ok {
		t.Fatalf("resp id 4 not found")
	}
	res, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not map")
	}
	ce, ok := res["isError"].(bool)
	if !ok || !ce {
		t.Fatalf("isError want true, got %v", res["isError"])
	}
}

func TestServer_UnknownMethod(t *testing.T) {
	reqs := []testReq{
		{JSONRPC: "2.0", ID: 5, Method: "nope"},
	}
	resps := runServer(t, reqs)

	r, ok := byID(resps, 5)
	if !ok {
		t.Fatalf("resp id 5 not found")
	}
	er, ok := r["error"].(map[string]any)
	if !ok {
		t.Fatalf("error not map")
	}
	cd, ok := er["code"].(float64)
	if !ok || int(cd) != -32601 {
		t.Errorf("code = %v", er["code"])
	}
	if er["message"] != "method not found" {
		t.Errorf("message = %v", er["message"])
	}
}
