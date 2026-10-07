package httptestserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func getWithAccept(t *testing.T, url, accept string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return do(t, req)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if line == "\n" {
			return b.String()
		}
		b.WriteString(line)
	}
}

func TestServerInitiatedStream(t *testing.T) {
	srv := New()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", "evt-0")
	resp := do(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	waitFor(t, "an open stream", func() bool { return srv.StreamCount() == 1 })
	if got := srv.LastEventIDHeaderSeen(); got != "evt-0" {
		t.Errorf("LastEventIDHeaderSeen = %q, want evt-0", got)
	}
	if got := srv.GetStreamRequestCount(); got != 1 {
		t.Errorf("GetStreamRequestCount = %d, want 1", got)
	}

	reader := bufio.NewReader(resp.Body)
	if err := srv.PushNotification("notifications/tools/list_changed", nil); err != nil {
		t.Fatalf("PushNotification: %v", err)
	}
	if frame := readFrame(t, reader); !strings.Contains(frame, "list_changed") {
		t.Errorf("notification frame = %q", frame)
	}

	if err := srv.PushNotificationWithID("evt-1", "notifications/message", map[string]any{"level": "info"}); err != nil {
		t.Fatalf("PushNotificationWithID: %v", err)
	}
	frame := readFrame(t, reader)
	if !strings.Contains(frame, "id: evt-1") || !strings.Contains(frame, "message") {
		t.Errorf("identified frame = %q", frame)
	}

	if err := srv.PushServerRequest(42, "roots/list"); err != nil {
		t.Fatalf("PushServerRequest: %v", err)
	}
	if frame := readFrame(t, reader); !strings.Contains(frame, "roots/list") {
		t.Errorf("server request frame = %q", frame)
	}

	srv.DropStreams()
	waitFor(t, "the dropped stream to leave", func() bool { return srv.StreamCount() == 0 })
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Errorf("read after drop = %v, want EOF", err)
	}
}

func TestPostedResponseRecorded(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := do(t, mustPost(t, srv.URL, `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	raw, ok := srv.PostedResponse(7)
	if !ok || !strings.Contains(string(raw), `"ok":true`) {
		t.Errorf("PostedResponse(7) = %s, %v", raw, ok)
	}
	if _, ok := srv.PostedResponse(8); ok {
		t.Errorf("PostedResponse(8) unexpectedly present")
	}
}

func mustPost(t *testing.T, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestPostedResponseID(t *testing.T) {
	testCases := []struct {
		name   string
		body   string
		wantID int
		wantOK bool
	}{
		{name: "result response", body: `{"jsonrpc":"2.0","id":3,"result":{}}`, wantID: 3, wantOK: true},
		{name: "error response", body: `{"jsonrpc":"2.0","id":4,"error":{"code":1}}`, wantID: 4, wantOK: true},
		{name: "request has a method", body: `{"jsonrpc":"2.0","id":5,"method":"x"}`, wantOK: false},
		{name: "no id", body: `{"jsonrpc":"2.0","result":{}}`, wantOK: false},
		{name: "neither result nor error", body: `{"jsonrpc":"2.0","id":6}`, wantOK: false},
		{name: "non-integer id", body: `{"jsonrpc":"2.0","id":"six","result":{}}`, wantOK: false},
		{name: "not json", body: `not json`, wantOK: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := postedResponseID([]byte(tc.body))
			if ok != tc.wantOK || id != tc.wantID {
				t.Errorf("postedResponseID(%s) = %d, %v; want %d, %v", tc.body, id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

func TestGetStreamModes(t *testing.T) {
	t.Run("rejected stream", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.RejectGetStream = true })
		resp := getWithAccept(t, srv.URL, "text/event-stream")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", resp.StatusCode)
		}
	})

	t.Run("legacy handshake answers with an event stream", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.LegacyOnly = true })
		resp := getWithAccept(t, srv.URL, "text/event-stream")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Errorf("Content-Type = %q", ct)
		}
	})
}

func TestDeleteSession(t *testing.T) {
	srv := New()
	defer srv.Close()

	if _, ok := srv.LastDeletedSession(); ok {
		t.Errorf("LastDeletedSession reported a deletion before any DELETE")
	}
	req, err := http.NewRequest(http.MethodDelete, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(SessionHeader, "session-1")
	resp := do(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if srv.DeleteCount() != 1 {
		t.Errorf("DeleteCount = %d, want 1", srv.DeleteCount())
	}
	if got, ok := srv.LastDeletedSession(); !ok || got != "session-1" {
		t.Errorf("LastDeletedSession = %q, %v", got, ok)
	}
}

func TestUnsupportedMethod(t *testing.T) {
	srv := New()
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPut, srv.URL, strings.NewReader("x"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp := do(t, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestSessionLifecycle(t *testing.T) {
	t.Run("assigning a session and recording the header", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.AssignSession = true })

		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
		defer resp.Body.Close()
		if got := resp.Header.Get(SessionHeader); got != sessionToken {
			t.Errorf("session header = %q, want %q", got, sessionToken)
		}

		resp2 := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, map[string]string{
			SessionHeader: "abc",
		})
		defer resp2.Body.Close()
		if got, seen := srv.LastSessionHeaderSeen(); !seen || got != "abc" {
			t.Errorf("LastSessionHeaderSeen = %q, %v; want abc, true", got, seen)
		}
	})

	t.Run("rejecting a session after initialize", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.RejectSessionAfterInit = true })

		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, map[string]string{
			SessionHeader: "stale",
		})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("post request count", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
		resp.Body.Close()
		if srv.PostRequestCount() != 1 {
			t.Errorf("PostRequestCount = %d, want 1", srv.PostRequestCount())
		}
	})
}

func TestPostFailureModes(t *testing.T) {
	t.Run("configured fail status", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) {
			c.FailStatus = http.StatusServiceUnavailable
			c.FailMessage = "boom"
		})
		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "boom") {
			t.Errorf("body = %q, want it to contain boom", body)
		}
	})

	t.Run("unsupported content type", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.UnsupportedContentType = true })
		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("required protocol version", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.RequireProtocolVersion = "2025-06-18" })
		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}

		resp2 := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, map[string]string{
			ProtocolVersionHeader: "2025-06-18",
		})
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			t.Errorf("matching version status = %d, want 200", resp2.StatusCode)
		}
	})

	t.Run("redirected POST", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.RedirectPostTo = "https://elsewhere.example.com/mcp" })
		req := mustPost(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want 307", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "https://elsewhere.example.com/mcp" {
			t.Errorf("Location = %q", loc)
		}
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		resp := do(t, mustPost(t, srv.URL, `{not json`))
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("legacy POST is rejected", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.LegacyOnly = true })
		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", resp.StatusCode)
		}
	})
}

func TestNotificationIsAccepted(t *testing.T) {
	srv := New()
	defer srv.Close()
	resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
}

func TestHangForeverReturnsOnCancellation(t *testing.T) {
	srv := New()
	defer srv.Close()
	srv.Configure(func(c *Config) { c.HangForever = true })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handlePost(rec, req)
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written", rec.Body.String())
	}
}

func TestHoldPostStreamOpen(t *testing.T) {
	srv := New()
	defer srv.Close()
	srv.Configure(func(c *Config) {
		c.ResponseSSE = true
		c.HoldPostStreamOpen = true
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := mustPost(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`).WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if frame := readFrame(t, bufio.NewReader(resp.Body)); !strings.Contains(frame, "protocolVersion") {
		t.Errorf("frame = %q", frame)
	}
	cancel()
	resp.Body.Close()
}

func TestSSEFramingModes(t *testing.T) {
	testCases := []struct {
		name       string
		configure  func(*Config)
		wantSubstr string
	}{
		{
			name:       "malformed SSE frame",
			configure:  func(c *Config) { c.MalformedSSEFrame = true },
			wantSubstr: "not valid json",
		},
		{
			name:       "truncated SSE stream",
			configure:  func(c *Config) { c.TruncateSSEStream = true },
			wantSubstr: "999999",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := New()
			defer srv.Close()
			srv.Configure(tc.configure)
			resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), tc.wantSubstr) {
				t.Errorf("body = %q, want it to contain %q", body, tc.wantSubstr)
			}
		})
	}

	t.Run("oversized response", func(t *testing.T) {
		srv := New()
		defer srv.Close()
		srv.Configure(func(c *Config) { c.OversizedResponseBytes = 512 })
		resp := postJSON(t, srv.URL, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}, nil)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if len(body) < 512 {
			t.Fatalf("body length = %d, want at least 512", len(body))
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("padded body is not valid JSON: %v", err)
		}
		if _, ok := decoded["_pad"]; !ok {
			t.Errorf("padded body has no _pad member")
		}
	})
}

func TestChallenges(t *testing.T) {
	testCases := []struct {
		name string
		cfg  Config
		auth string
		want string
	}{
		{name: "no challenge when unconfigured", cfg: Config{}, want: ""},
		{name: "authorized request is not challenged", cfg: Config{RequireBearerToken: "t"}, auth: "Bearer t", want: ""},
		{name: "token required without metadata", cfg: Config{RequireBearerToken: "t"}, want: `Bearer realm="OAuth"`},
		{name: "always challenge without metadata", cfg: Config{ChallengeAlways: true}, want: `Bearer realm="OAuth"`},
		{name: "challenge names the metadata document", cfg: Config{ChallengeAlways: true, ChallengeResourceMetaURL: "https://auth.example.com/prm"}, want: `Bearer realm="OAuth", resource_metadata="https://auth.example.com/prm"`},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if got := challengeFor(tc.cfg, req); got != tc.want {
				t.Errorf("challengeFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnknownTool(t *testing.T) {
	srv := New()
	defer srv.Close()
	srv.Configure(func(c *Config) { c.UnknownToolName = "ghost" })
	resp := postJSON(t, srv.URL, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ghost", "arguments": map[string]any{}},
	}, nil)
	defer resp.Body.Close()
	var decoded rpcResp
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != -32602 {
		t.Fatalf("Error = %+v, want code -32602", decoded.Error)
	}
}

func TestCallToolInvalidParams(t *testing.T) {
	_, rpcErr := callTool(Config{}, json.RawMessage(`{`))
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("rpcErr = %+v, want -32602", rpcErr)
	}
}

func TestPadJSON(t *testing.T) {
	t.Run("left alone when large enough", func(t *testing.T) {
		body := []byte(`{"a":1}`)
		if got := padJSON(body, 1); !strings.EqualFold(string(got), string(body)) {
			t.Errorf("padJSON = %s", got)
		}
	})

	t.Run("left alone when not JSON", func(t *testing.T) {
		body := []byte(`nope`)
		if got := padJSON(body, 100); string(got) != "nope" {
			t.Errorf("padJSON = %s", got)
		}
	})

	t.Run("grows valid JSON past the minimum", func(t *testing.T) {
		got := padJSON([]byte(`{"a":1}`), 200)
		if len(got) < 200 {
			t.Fatalf("len = %d", len(got))
		}
		var m map[string]any
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatalf("result is not JSON: %v", err)
		}
	})
}

func TestWriteSSEFrame(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSSEFrame(rec, []byte(`{"id":1}`))
	want := "event: message\ndata: {\"id\":1}\n\n"
	if rec.Body.String() != want {
		t.Errorf("frame = %q, want %q", rec.Body.String(), want)
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	if _, rpcErr := dispatch(Config{}, rpcRequest{Method: "nonsense"}); rpcErr == nil || rpcErr.Code != -32601 {
		t.Errorf("rpcErr = %+v, want -32601", rpcErr)
	}
}
