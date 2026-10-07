package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type errReadCloser struct{ err error }

func (r errReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (errReadCloser) Close() error               { return nil }

func newBareHttpConn() *HttpConn {
	return &HttpConn{
		serverName:      "srv",
		protocolVersion: ProtocolVersion,
		readBound:       4096,
		connCtx:         context.Background(),
		pending:         map[int]chan pendingResult{},
		notifyCh:        make(chan string, 8),
	}
}

func TestParseResourceMetadata(t *testing.T) {
	tests := []struct {
		challenge string
		want      string
	}{
		{`Bearer realm="x", resource_metadata="https://a/b"`, "https://a/b"},
		{"Bearer realm=\"x\"", ""},
		{"resource_metadata=", ""},
		{`resource_metadata="unterminated`, ""},
		{"resource_metadata=https://a/b,", "https://a/b"},
		{"resource_metadata=https://a/b", "https://a/b"},
	}
	for _, tc := range tests {
		if got := parseResourceMetadata(tc.challenge); got != tc.want {
			t.Fatalf("parseResourceMetadata(%q) = %q, want %q", tc.challenge, got, tc.want)
		}
	}
}

func TestSSEFrameReaderTrailingDataWithoutBlankLine(t *testing.T) {
	fr := newSSEFrameReader(strings.NewReader("id: 7\ndata: {\"a\":1}"), 1)
	data, err := fr.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if string(data) != "{\"a\":1}" {
		t.Fatalf("data = %q", data)
	}
	if fr.LastID() != "7" {
		t.Fatalf("LastID = %q, want 7", fr.LastID())
	}
}

func TestHttpRedirectRefusedErrorFormatting(t *testing.T) {
	err := &HttpRedirectRefusedError{ServerName: "srv", From: "https://a", To: "https://b"}
	if !strings.Contains(err.Error(), "srv") || !strings.Contains(err.Error(), "https://a") || !strings.Contains(err.Error(), "https://b") {
		t.Fatalf("unexpected message: %q", err.Error())
	}
}

func TestHandleJSONFrameMalformedAndRoutes(t *testing.T) {
	t.Run("undecodable", func(t *testing.T) {
		c := newBareHttpConn()
		var got error
		c.handleJSONFrame([]byte("{not json"), func(e error) { got = e })
		if got == nil {
			t.Fatal("expected onMalformed for undecodable frame")
		}
	})

	t.Run("missing jsonrpc", func(t *testing.T) {
		c := newBareHttpConn()
		var got error
		c.handleJSONFrame([]byte(`{"id":1}`), func(e error) { got = e })
		if !errors.Is(got, errFrameNotJSONRPC) {
			t.Fatalf("got %v, want errFrameNotJSONRPC", got)
		}
	})

	t.Run("notification is published", func(t *testing.T) {
		c := newBareHttpConn()
		c.handleJSONFrame([]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`), func(error) {
			t.Fatal("unexpected malformed callback")
		})
		select {
		case method := <-c.Notifications():
			if method != "notifications/tools/list_changed" {
				t.Fatalf("method = %q", method)
			}
		default:
			t.Fatal("expected a published notification")
		}
	})

	t.Run("server request without deliverable id is answered", func(t *testing.T) {
		c := newBareHttpConn()
		c.endpoint = "http://example.test/mcp"
		bodyCh := make(chan []byte, 1)
		c.client = &http.Client{Transport: roundTripFuncErr(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			bodyCh <- body
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Status:     "202 Accepted",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		})}
		c.handleJSONFrame([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), func(error) {
			t.Fatal("unexpected malformed callback")
		})
		select {
		case body := <-bodyCh:
			var got struct {
				JSONRPC string `json:"jsonrpc"`
				ID      int    `json:"id"`
				Error   struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode server response %q: %v", body, err)
			}
			if got.JSONRPC != "2.0" || got.ID != 1 || got.Error.Code != -32601 || got.Error.Message != "method not found" {
				t.Fatalf("server-request response = %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("server request did not receive a method-not-found response")
		}
	})

	t.Run("response without id is ignored", func(t *testing.T) {
		c := newBareHttpConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleJSONFrame([]byte(`{"jsonrpc":"2.0","result":{}}`), func(error) {
			t.Fatal("unexpected malformed callback")
		})
		if len(c.pending) != 1 || len(ch) != 0 {
			t.Fatalf("response without id changed pending calls: pending=%v results=%d", c.pending, len(ch))
		}
	})

	t.Run("unparseable id is ignored", func(t *testing.T) {
		c := newBareHttpConn()
		ch := make(chan pendingResult, 1)
		c.pending[1] = ch
		c.handleJSONFrame([]byte(`{"jsonrpc":"2.0","id":"abc","result":{}}`), func(error) {
			t.Fatal("unexpected malformed callback")
		})
		if len(c.pending) != 1 || len(ch) != 0 {
			t.Fatalf("response with non-numeric id changed pending calls: pending=%v results=%d", c.pending, len(ch))
		}
	})

	t.Run("undecodable response body reports malformed", func(t *testing.T) {
		c := newBareHttpConn()
		var got error
		c.handleJSONFrame([]byte(`{"jsonrpc":"2.0","id":1,"error":"boom"}`), func(e error) { got = e })
		if got == nil {
			t.Fatal("expected onMalformed for a response whose error field is not an object")
		}
	})
}

func TestDoPostRejectsUnparseableEndpoint(t *testing.T) {
	c := &HttpConn{serverName: "srv", endpoint: "://bad", protocolVersion: ProtocolVersion, client: &http.Client{}, readBound: 16}
	if _, err := c.doPost(context.Background(), nil); err == nil {
		t.Fatal("expected an error for an unparseable endpoint")
	}
}

func TestNotifyAndCallOnClosedConnection(t *testing.T) {
	c := newBareHttpConn()
	ctx, cancel := context.WithCancel(context.Background())
	c.connCtx, c.connCancel = ctx, cancel
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Notify(context.Background(), "x", nil); err == nil {
		t.Fatal("expected Notify to fail on a closed connection")
	}
	if _, err := c.Call(context.Background(), "x", nil); err == nil {
		t.Fatal("expected Call to fail on a closed connection")
	}
}

func TestNotifyBuildRequestFailure(t *testing.T) {
	c := &HttpConn{serverName: "srv", endpoint: "://bad", protocolVersion: ProtocolVersion, client: &http.Client{}, readBound: 16}
	if err := c.Notify(context.Background(), "x", nil); err == nil {
		t.Fatal("expected Notify to surface the build-request failure")
	}
}

func TestCallAcceptedWithoutBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	server := pub_models.McpServer{Name: "srv", Url: srv.URL}
	conn := NewHttpConn(context.Background(), server, nil)
	defer conn.Close()

	if _, err := conn.Call(context.Background(), "initialize", nil); err == nil {
		t.Fatal("expected an error for a 202 with no body")
	}
}

func TestCallCanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	server := pub_models.McpServer{Name: "srv", Url: srv.URL}
	conn := NewHttpConn(context.Background(), server, nil)
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := conn.Call(ctx, "initialize", nil); err == nil {
		t.Fatal("expected the cancelled context to fail the call")
	}
}

func TestCloseFailsPendingWaiters(t *testing.T) {
	c := newBareHttpConn()
	ctx, cancel := context.WithCancel(context.Background())
	c.connCtx, c.connCancel = ctx, cancel

	ch := make(chan pendingResult, 1)
	c.pending[3] = ch

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	res := <-ch
	if res.err == nil {
		t.Fatal("expected the pending waiter to be failed by Close")
	}
}

func TestPublishNotificationAfterCloseIsDropped(t *testing.T) {
	c := newBareHttpConn()
	ctx, cancel := context.WithCancel(context.Background())
	c.connCtx, c.connCancel = ctx, cancel
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c.publishNotification("x")
	if method, ok := <-c.Notifications(); ok {
		t.Fatalf("notification after close was delivered: %q", method)
	}
}

func TestFailAllPendingAfterCloseIsNoop(t *testing.T) {
	c := newBareHttpConn()
	ctx, cancel := context.WithCancel(context.Background())
	c.connCtx, c.connCancel = ctx, cancel
	ch := make(chan pendingResult, 2)
	c.pending[1] = ch
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if res := <-ch; res.err == nil {
		t.Fatal("Close did not fail the pending waiter")
	}
	c.failAllPending(errors.New("boom"))
	select {
	case res := <-ch:
		t.Fatalf("closed connection emitted a second result: %+v", res)
	default:
	}
}

func TestConsumeResponseBodyReadError(t *testing.T) {
	c := newBareHttpConn()
	ch := make(chan pendingResult, 1)
	c.pending[9] = ch

	c.consumeResponseBody(&http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   errReadCloser{errors.New("boom")},
	}, 9)

	res := <-ch
	if res.err == nil {
		t.Fatal("expected a read error to fail the waiter")
	}
}

func TestSendSessionDeleteFailureModes(t *testing.T) {
	t.Run("unparseable endpoint", func(t *testing.T) {
		c := &HttpConn{endpoint: "://bad", protocolVersion: ProtocolVersion, client: &http.Client{}}
		c.sendSessionDelete("session")
	})

	t.Run("decorator runs and transport error is swallowed", func(t *testing.T) {
		decorated := false
		c := &HttpConn{
			endpoint:        "http://127.0.0.1:1",
			protocolVersion: ProtocolVersion,
			client:          &http.Client{Transport: roundTripFuncErr(func(*http.Request) (*http.Response, error) { return nil, errors.New("boom") })},
			decorate:        func(*http.Request) { decorated = true },
		}
		c.sendSessionDelete("session")
		if !decorated {
			t.Fatal("expected the request decorator to run")
		}
	})
}

type roundTripFuncErr func(*http.Request) (*http.Response, error)

func (f roundTripFuncErr) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenServerStreamOnceFailureModes(t *testing.T) {
	t.Run("unparseable endpoint", func(t *testing.T) {
		c := &HttpConn{endpoint: "://bad", protocolVersion: ProtocolVersion, client: &http.Client{}, connCtx: context.Background()}
		if established, _ := c.openServerStreamOnce(""); established {
			t.Fatal("expected no stream for an unparseable endpoint")
		}
	})

	t.Run("wrong content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "nope")
		}))
		defer srv.Close()

		c := &HttpConn{endpoint: srv.URL, protocolVersion: ProtocolVersion, client: srv.Client(), connCtx: context.Background(), readBound: 4096}
		if established, _ := c.openServerStreamOnce(""); established {
			t.Fatal("expected no stream for a non-event-stream response")
		}
	})
}

func TestDetectLegacyOnlyEndpointBranches(t *testing.T) {
	t.Run("non-status error passes through", func(t *testing.T) {
		if err := detectLegacyOnlyEndpoint(context.Background(), newBareHttpConn(), pub_models.McpServer{}, errors.New("boom")); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("unrelated status passes through", func(t *testing.T) {
		c := newBareHttpConn()
		initErr := claierr.NewMcpHttpStatus("srv", http.StatusInternalServerError, "boom")
		if err := detectLegacyOnlyEndpoint(context.Background(), c, pub_models.McpServer{}, initErr); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("GET without an event stream passes through", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}))
		defer srv.Close()

		c := &HttpConn{endpoint: srv.URL, client: srv.Client(), protocolVersion: ProtocolVersion, connCtx: context.Background()}
		initErr := claierr.NewMcpHttpStatus("srv", http.StatusMethodNotAllowed, "boom")
		if err := detectLegacyOnlyEndpoint(context.Background(), c, pub_models.McpServer{Name: "srv", Url: srv.URL}, initErr); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("legacy endpoint is detected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: endpoint\ndata: /messages\n\n")
		}))
		defer srv.Close()

		c := &HttpConn{endpoint: srv.URL, client: srv.Client(), protocolVersion: ProtocolVersion, connCtx: context.Background()}
		initErr := claierr.NewMcpHttpStatus("srv", http.StatusNotFound, "boom")
		if err := detectLegacyOnlyEndpoint(context.Background(), c, pub_models.McpServer{Name: "srv", Url: srv.URL}, initErr); err == nil {
			t.Fatal("expected the legacy-only endpoint error")
		}
	})
}
