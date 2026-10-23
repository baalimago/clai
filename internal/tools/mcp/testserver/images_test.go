package main

import (
	"reflect"
	"testing"
)

func TestServer_EchoImages(t *testing.T) {
	resps := runServer(t, []testReq{{
		JSONRPC: "2.0", ID: 3, Method: "tools/call",
		Params: map[string]any{"name": "echo", "arguments": map[string]any{
			"text":   "hi",
			"images": []any{map[string]any{"data": "AAAA", "mimeType": "image/png"}, "not an object"},
		}},
	}})
	r, ok := byID(resps, 3)
	if !ok {
		t.Fatalf("no response to tools/call: %v", resps)
	}
	content := r["result"].(map[string]any)["content"]
	want := []any{
		map[string]any{"type": "text", "text": "hi"},
		map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"},
	}
	if !reflect.DeepEqual(content, want) {
		t.Fatalf("content = %#v, want %#v", content, want)
	}
}
