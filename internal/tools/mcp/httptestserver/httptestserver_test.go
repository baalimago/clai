package httptestserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func postJSON(t *testing.T, url string, body map[string]any, headers map[string]string) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// TestBearerTokenModeChallengesWithoutToken pins the fixture's bearer-token
// mode: a tool call is answered only when the configured token is
// presented, with a 401 challenge otherwise, since this is the capability
// the authorization phase will drive against this same fixture.
func TestBearerTokenModeChallengesWithoutToken(t *testing.T) {
	srv := New()
	defer srv.Close()
	srv.Configure(func(c *Config) {
		c.RequireBearerToken = "secret123"
		c.ChallengeResourceMetaURL = "https://auth.example.com/.well-known/oauth-protected-resource"
	})

	resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, "resource_metadata=") {
		t.Errorf("WWW-Authenticate = %q, want a resource_metadata parameter", challenge)
	}
}

// TestBearerTokenModeAnswersWithCorrectToken pins the bypass half: the
// correct bearer token is answered normally.
func TestBearerTokenModeAnswersWithCorrectToken(t *testing.T) {
	srv := New()
	defer srv.Close()
	srv.Configure(func(c *Config) { c.RequireBearerToken = "secret123" })

	resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, map[string]string{
		"Authorization": "Bearer secret123",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var decoded rpcResp
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("unexpected error: %+v", decoded.Error)
	}
}

// TestToolsListAndCallRoundTrip exercises the default configuration's
// tools/list and tools/call handling directly over HTTP, independent of
// any Conn implementation.
func TestToolsListAndCallRoundTrip(t *testing.T) {
	srv := New()
	defer srv.Close()

	listResp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, nil)
	defer listResp.Body.Close()
	var list rpcResp
	if err := json.NewDecoder(listResp.Body).Decode(&list); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if !strings.Contains(string(list.Result), "echo") {
		t.Fatalf("tools/list result = %s, want it to mention echo", list.Result)
	}

	callResp := postJSON(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "echo", "arguments": map[string]any{"text": "hi"}},
	}, nil)
	defer callResp.Body.Close()
	var call rpcResp
	if err := json.NewDecoder(callResp.Body).Decode(&call); err != nil {
		t.Fatalf("decode tools/call: %v", err)
	}
	if !strings.Contains(string(call.Result), "hi") {
		t.Fatalf("tools/call result = %s, want it to contain hi", call.Result)
	}
}

// TestUnknownMethodReturnsMethodNotFound pins the default dispatch branch
// for a method this fixture does not implement.
func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nonsense"}, nil)
	defer resp.Body.Close()
	var decoded rpcResp
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != -32601 {
		t.Fatalf("Error = %+v, want code -32601", decoded.Error)
	}
}

// TestGetWithoutEventStreamAcceptIsMethodNotAllowed pins that a GET lacking
// the event-stream accept header is rejected the same way a client that
// never attempts the optional stream would see.
func TestGetWithoutEventStreamAcceptIsMethodNotAllowed(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
