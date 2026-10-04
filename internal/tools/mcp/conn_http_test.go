package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/httptestserver"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// newTestHttpConn builds a Conn against srv. The caller must defer
// conn.Close() itself, after its own defer srv.Close(): t.Cleanup always
// runs after a test's own defers, so registering the close there would
// close the fixture before draining the connection's background
// server-stream goroutine, hanging httptest.Server.Close.
func newTestHttpConn(t *testing.T, srv *httptestserver.Server, opts ...HttpConnOption) Conn {
	t.Helper()
	server := pub_models.McpServer{Name: "fake", Url: srv.URL}
	return NewHttpConn(context.Background(), server, nil, opts...)
}

func mustInitialize(t *testing.T, conn Conn) {
	t.Helper()
	if _, err := conn.Call(t.Context(), "initialize", map[string]any{"protocolVersion": ProtocolVersion}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

// TestHttpConnPostReceivesJsonResponse pins the plain-JSON POST response
// path: a JSON body carries the response directly.
func TestHttpConnPostReceivesJsonResponse(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	mustInitialize(t, conn)
	raw, err := conn.Call(t.Context(), "tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if !strings.Contains(string(raw), `"echo"`) {
		t.Errorf("tools/list result = %s, want it to mention the echo tool", raw)
	}
}

// TestHttpConnPostReceivesSseResponse pins the event-stream POST response
// path: an event-stream body is parsed for frames and demuxed exactly as a
// JSON body is.
func TestHttpConnPostReceivesSseResponse(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.ResponseSSE = true })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	mustInitialize(t, conn)
	raw, err := conn.Call(t.Context(), "tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if !strings.Contains(string(raw), `"echo"`) {
		t.Errorf("tools/list result = %s, want it to mention the echo tool", raw)
	}
}

// TestHttpConnAcceptedWithoutBodyIsNotATimeout pins that a notification's
// accepted-with-no-body outcome returns promptly as success, never as a
// pending call or a timeout.
func TestHttpConnAcceptedWithoutBodyIsNotATimeout(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	start := time.Now()
	if err := conn.Notify(t.Context(), "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Notify took %v, want it to return promptly", elapsed)
	}
}

// TestHttpConnCarriesSessionIdAfterInitialize pins the session rule: the
// value of the session header on the initialize response, when present, is
// sent on every later request.
func TestHttpConnCarriesSessionIdAfterInitialize(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.AssignSession = true })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	mustInitialize(t, conn)
	if _, err := conn.Call(t.Context(), "tools/list", nil); err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	got, seen := srv.LastSessionHeaderSeen()
	if !seen {
		t.Fatal("no non-initialize request observed")
	}
	if got == "" {
		t.Error("later request carried no session header, want the one assigned on initialize")
	}
}

// TestHttpConnToleratesMethodNotAllowedOnGetStream pins that a
// method-not-allowed answer to the optional server stream is expected and
// normal: the connection remains fully usable.
func TestHttpConnToleratesMethodNotAllowedOnGetStream(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.RejectGetStream = true })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	mustInitialize(t, conn)
	if _, err := conn.Call(t.Context(), "tools/list", nil); err != nil {
		t.Fatalf("tools/list after a rejected server stream: %v", err)
	}
}

// TestHttpConnReadsServerInitiatedMessagesFromGetStream pins that a frame
// arriving on the server stream enters the same demux as POST responses:
// it reaches the NotificationWatcher channel.
func TestHttpConnReadsServerInitiatedMessagesFromGetStream(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv)
	defer conn.Close()
	mustInitialize(t, conn)

	watcher, ok := conn.(NotificationWatcher)
	if !ok {
		t.Fatal("HttpConn does not implement NotificationWatcher")
	}

	deadline := time.Now().Add(5 * time.Second)
	for srv.StreamCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server-initiated stream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := srv.PushNotification("notifications/tools/list_changed", nil); err != nil {
		t.Fatalf("PushNotification: %v", err)
	}

	select {
	case method := <-watcher.Notifications():
		if method != "notifications/tools/list_changed" {
			t.Errorf("method = %q, want notifications/tools/list_changed", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification never arrived on the watcher channel")
	}
}

// TestHttpConnCloseClosesNotificationChannel pins R1-27's dead-branch
// finding: Close must close notifyCh so watchForToolsListChanged's
// channel-closed branch is reachable and a watcher started under a context
// that outlives the connection still stops promptly, instead of running
// against a dead connection for the rest of the run.
func TestHttpConnCloseClosesNotificationChannel(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv)
	mustInitialize(t, conn)

	watcher := conn.(NotificationWatcher)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case _, ok := <-watcher.Notifications():
		if ok {
			t.Fatal("expected the notification channel to be closed, got a value instead")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification channel was never closed")
	}
}

// TestHttpConnServerStreamBodyLimitIsPerFrameNotCumulative pins R1-12: the
// response-body-limit parameter is "per message", and the GET
// server-initiated stream is long-lived, so the bound must apply to each
// frame, never to the stream's running total. Three notifications, each
// under the bound but summing well past it, must all arrive.
func TestHttpConnServerStreamBodyLimitIsPerFrameNotCumulative(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	const bound = 200
	conn := newTestHttpConn(t, srv, WithHttpReadBound(bound))
	defer conn.Close()
	mustInitialize(t, conn)

	watcher := conn.(NotificationWatcher)
	deadline := time.Now().Add(5 * time.Second)
	for srv.StreamCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server-initiated stream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	const frames = 4 // 4 * bound well past one cumulative bound's worth
	for i := range frames {
		if err := srv.PushNotification("notifications/tools/list_changed", map[string]any{"i": i}); err != nil {
			t.Fatalf("PushNotification %d: %v", i, err)
		}
		select {
		case method := <-watcher.Notifications():
			if method != "notifications/tools/list_changed" {
				t.Errorf("frame %d: method = %q, want notifications/tools/list_changed", i, method)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("frame %d never arrived; the stream died after exceeding the cumulative bound", i)
		}
	}
}

// TestHttpConnAnswersServerInitiatedRequestWithMethodNotFound pins R1-13:
// a server-initiated request arriving on the GET stream must be answered
// with a POST'd -32601 response carrying its id, exactly as phase 1's
// stdio transport answers one on its own duplex, instead of being
// silently dropped forever.
func TestHttpConnAnswersServerInitiatedRequestWithMethodNotFound(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv)
	defer conn.Close()
	mustInitialize(t, conn)

	deadline := time.Now().Add(5 * time.Second)
	for srv.StreamCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server-initiated stream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := srv.PushServerRequest(777, "roots/list"); err != nil {
		t.Fatalf("PushServerRequest: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for {
		raw, ok := srv.PostedResponse(777)
		if ok {
			var resp struct {
				ID    int `json:"id"`
				Error *struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("decode posted response: %v", err)
			}
			if resp.Error == nil || resp.Error.Code != -32601 {
				t.Fatalf("response = %s, want a -32601 method-not-found error", raw)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("client never POSTed an answer to the server-initiated request")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHttpConnCloseSendsSessionDelete pins R2-10: a connection holding a
// session id sends DELETE with that session on Close, ending it per the
// streamable-HTTP spec, instead of abandoning it silently.
func TestHttpConnCloseSendsSessionDelete(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.AssignSession = true })
	conn := newTestHttpConn(t, srv)
	mustInitialize(t, conn)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if sid, ok := srv.LastDeletedSession(); ok {
			if sid != "test-session-token" {
				t.Fatalf("deleted session = %q, want the session assigned on initialize", sid)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Close never sent a DELETE for the held session")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHttpConnCloseWithNoSessionSendsNoDelete pins the other half: a
// sessionless connection (the server never assigned one) sends no DELETE at
// all on Close, matching "a server that omits it is sessionless".
func TestHttpConnCloseWithNoSessionSendsNoDelete(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv)
	mustInitialize(t, conn)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := srv.DeleteCount(); n != 0 {
		t.Fatalf("DeleteCount = %d, want 0 for a sessionless connection", n)
	}
}

// TestHttpConnContextEndingClosesConnectionAndSendsDelete pins the other
// half of R2-10: Conn.Close must have a real production call site, not
// only a test-only one. NewHttpConn wires Close to its own context ending,
// mirroring run teardown, so cancelling that context (not an explicit Close
// call) must still send the DELETE and tear the connection down.
func TestHttpConnContextEndingClosesConnectionAndSendsDelete(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.AssignSession = true })

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	server := pub_models.McpServer{Name: "fake", Url: srv.URL}
	conn := NewHttpConn(runCtx, server, nil)
	mustInitialize(t, conn)

	runCancel()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := srv.LastDeletedSession(); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelling the connection's own context never triggered a DELETE; Close has no production call site")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSSEFrameReaderSkipsEmptyDataKeepAlive pins R2-14: an SSE event whose
// data: field is empty ("data:\n\n") is a keep-alive, not a frame, and must
// not surface as a zero-length malformed frame that fails every in-flight
// call on the shared GET stream.
func TestSSEFrameReaderSkipsEmptyDataKeepAlive(t *testing.T) {
	input := "data:\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"
	fr := newSSEFrameReader(strings.NewReader(input), 4096)
	data, err := fr.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{}}`
	if string(data) != want {
		t.Fatalf("data = %q, want %q (the empty keep-alive must be skipped, not returned)", data, want)
	}
}

// TestHttpConnServerStreamReconnectsWithLastEventID pins R2-11: the
// server-initiated GET stream, once dropped without the connection itself
// closing, reconnects carrying Last-Event-ID set to the last id: it saw,
// and keeps delivering notifications afterward — instead of dying silently
// at the first stream close, as a single streamOnce attempt would.
func TestHttpConnServerStreamReconnectsWithLastEventID(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv, WithHttpServerStreamRetryBackoff(10*time.Millisecond))
	defer conn.Close()
	mustInitialize(t, conn)

	watcher := conn.(NotificationWatcher)
	deadline := time.Now().Add(5 * time.Second)
	for srv.StreamCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server-initiated stream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := srv.PushNotificationWithID("42", "notifications/tools/list_changed", nil); err != nil {
		t.Fatalf("PushNotificationWithID: %v", err)
	}
	select {
	case <-watcher.Notifications():
	case <-time.After(5 * time.Second):
		t.Fatal("first notification never arrived")
	}

	srv.DropStreams()

	deadline = time.Now().Add(5 * time.Second)
	for srv.GetStreamRequestCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the stream never reconnected after being dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.LastEventIDHeaderSeen(); got != "42" {
		t.Fatalf("Last-Event-ID on reconnect = %q, want %q", got, "42")
	}

	deadline = time.Now().Add(5 * time.Second)
	for srv.StreamCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("reconnected stream never re-registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := srv.PushNotification("notifications/tools/list_changed", nil); err != nil {
		t.Fatalf("PushNotification after reconnect: %v", err)
	}
	select {
	case <-watcher.Notifications():
	case <-time.After(5 * time.Second):
		t.Fatal("notification after reconnect never arrived; the stream died silently")
	}
}

// TestHttpConnBodyLimitIsEnforced pins that any response body is bounded by
// the response-body-limit parameter, and that exceeding it fails only the
// call, leaving the connection usable.
func TestHttpConnBodyLimitIsEnforced(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := newTestHttpConn(t, srv, WithHttpReadBound(4096))
	defer conn.Close()
	mustInitialize(t, conn)

	srv.Configure(func(c *httptestserver.Config) { c.OversizedResponseBytes = 8192 })
	_, err := conn.Call(t.Context(), "tools/list", nil)
	if !errors.Is(err, claierr.ErrMcpFrameUndecodable) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpFrameUndecodable)", err)
	}

	srv.Configure(func(c *httptestserver.Config) { c.OversizedResponseBytes = 0 })
	if _, err := conn.Call(t.Context(), "tools/list", nil); err != nil {
		t.Fatalf("call after an oversized response: %v, want the connection to remain usable", err)
	}
}

// TestHttpConnNonOkStatusReturnsTypedError pins that any non-ok,
// non-accepted status is a typed error carrying the status and the
// server's message.
func TestHttpConnNonOkStatusReturnsTypedError(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.FailStatus = 500; c.FailMessage = "boom" })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	_, err := conn.Call(t.Context(), "initialize", nil)
	var statusErr *claierr.McpHttpStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v, want *claierr.McpHttpStatusError", err)
	}
	if statusErr.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", statusErr.StatusCode)
	}
	if !strings.Contains(statusErr.Message, "boom") {
		t.Errorf("Message = %q, want it to contain %q", statusErr.Message, "boom")
	}
}

// TestHttpConnMalformedFrameReturnsCallError pins that a malformed
// event-stream frame fails its call rather than being silently dropped.
func TestHttpConnMalformedFrameReturnsCallError(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.MalformedSSEFrame = true })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	_, err := conn.Call(t.Context(), "initialize", nil)
	if !errors.Is(err, claierr.ErrMcpFrameUndecodable) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpFrameUndecodable)", err)
	}
}

// TestHttpConnUnreachableEndpointIsTypedTransportError pins that an
// endpoint which never answers at the network level is a typed transport
// error naming the server and the endpoint.
func TestHttpConnUnreachableEndpointIsTypedTransportError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here any more; the connection is refused.

	conn := NewHttpConn(t.Context(), pub_models.McpServer{Name: "unreachable", Url: "http://" + addr}, nil)
	defer conn.Close()

	_, callErr := conn.Call(t.Context(), "initialize", nil)
	var transportErr *claierr.McpTransportError
	if !errors.As(callErr, &transportErr) {
		t.Fatalf("err = %v, want *claierr.McpTransportError", callErr)
	}
	if transportErr.ServerName != "unreachable" {
		t.Errorf("ServerName = %q, want %q", transportErr.ServerName, "unreachable")
	}
	if !strings.Contains(transportErr.Endpoint, addr) {
		t.Errorf("Endpoint = %q, want it to name %q", transportErr.Endpoint, addr)
	}
}

// TestHttpConnChallengeIsTypedAuthErrorWithChallengePreserved pins that an
// authorization challenge returns claierr.AuthChallengeError with the
// WWW-Authenticate header preserved verbatim and the resource_metadata URL
// parsed out of it.
func TestHttpConnChallengeIsTypedAuthErrorWithChallengePreserved(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	const metaURL = "https://auth.example.com/.well-known/oauth-protected-resource"
	srv.Configure(func(c *httptestserver.Config) {
		c.ChallengeAlways = true
		c.ChallengeResourceMetaURL = metaURL
	})
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	_, err := conn.Call(t.Context(), "initialize", nil)
	var challengeErr *claierr.AuthChallengeError
	if !errors.As(err, &challengeErr) {
		t.Fatalf("err = %v, want *claierr.AuthChallengeError", err)
	}
	if !strings.Contains(challengeErr.Challenge, `realm="OAuth"`) {
		t.Errorf("Challenge = %q, want it to preserve the header verbatim", challengeErr.Challenge)
	}
	if challengeErr.ResourceMetadata != metaURL {
		t.Errorf("ResourceMetadata = %q, want %q", challengeErr.ResourceMetadata, metaURL)
	}
}

// TestHttpConnUnsupportedContentTypeIsTypedError pins that a response
// content type that is neither JSON nor an event stream is a typed error
// naming the content type.
func TestHttpConnUnsupportedContentTypeIsTypedError(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.UnsupportedContentType = true })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	_, err := conn.Call(t.Context(), "initialize", nil)
	var ctErr *claierr.McpUnsupportedContentTypeError
	if !errors.As(err, &ctErr) {
		t.Fatalf("err = %v, want *claierr.McpUnsupportedContentTypeError", err)
	}
	if !strings.Contains(ctErr.ContentType, "text/plain") {
		t.Errorf("ContentType = %q, want it to contain %q", ctErr.ContentType, "text/plain")
	}
}

// TestHttpConnTruncatedStreamFailsOnlyItsCall pins that an event-stream
// body which ends before the awaited response arrives fails only that
// call; the connection remains usable for the next one.
func TestHttpConnTruncatedStreamFailsOnlyItsCall(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.TruncateSSEStream = true })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	_, err := conn.Call(t.Context(), "initialize", nil)
	if !errors.Is(err, claierr.ErrMcpFrameUndecodable) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpFrameUndecodable)", err)
	}

	srv.Configure(func(c *httptestserver.Config) { c.TruncateSSEStream = false })
	if _, err := conn.Call(t.Context(), "initialize", nil); err != nil {
		t.Fatalf("call after a truncated stream: %v, want the connection to remain usable", err)
	}
}

// TestHttpConnCallResolvesWhenFrameArrivesEvenIfStreamStaysOpen pins the
// sign-off review's B1: Call must return once the awaited frame arrives, not
// when the POST response body ends. The streamable-HTTP specification
// permits a server to hold the event-stream body open after answering
// ("SHOULD" close it, not "MUST"), so a conformant server that does this
// must not make every call hang to its context bound even though the answer
// was available from the start.
func TestHttpConnCallResolvesWhenFrameArrivesEvenIfStreamStaysOpen(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) {
		c.ResponseSSE = true
		c.HoldPostStreamOpen = true
	})
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := conn.Call(ctx, "initialize", map[string]any{"protocolVersion": ProtocolVersion}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("Call took %s against a 1s context bound; it waited for the held-open stream to end instead of returning once the answer arrived", elapsed)
	}
}

// TestHttpConnRejectedSessionIsTypedSessionError pins that a session
// rejected by the server after initialize is a typed error naming the
// session stage.
func TestHttpConnRejectedSessionIsTypedSessionError(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.AssignSession = true })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()
	mustInitialize(t, conn)

	srv.Configure(func(c *httptestserver.Config) { c.RejectSessionAfterInit = true })
	_, err := conn.Call(t.Context(), "tools/list", nil)
	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", err)
	}
	if startupErr.Stage != "session" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "session")
	}
}

// TestHttpConnAdvertisesProtocolVersionHeader pins the protocol-version
// limit row: the client advertises the protocol-version parameter on every
// request.
func TestHttpConnAdvertisesProtocolVersionHeader(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.RequireProtocolVersion = ProtocolVersion })
	conn := newTestHttpConn(t, srv)
	defer conn.Close()

	if _, err := conn.Call(t.Context(), "initialize", nil); err != nil {
		t.Fatalf("initialize: %v, want the advertised protocol version to satisfy the server", err)
	}
}

// TestHttpConnCreatesNoChildProcess pins that an HTTP server's whole
// lifecycle starts no child process, unlike the stdio transport.
func TestHttpConnCreatesNoChildProcess(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not available on this host")
	}
	countChildren := func() int {
		out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
		if err != nil {
			return 0
		}
		return len(strings.Fields(strings.TrimSpace(string(out))))
	}

	srv := httptestserver.New()
	defer srv.Close()

	before := countChildren()
	conn := newTestHttpConn(t, srv)
	defer conn.Close()
	mustInitialize(t, conn)
	if _, err := conn.Call(t.Context(), "tools/list", nil); err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if _, err := conn.Call(t.Context(), "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "hi"}}); err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	after := countChildren()

	if after != before {
		t.Errorf("child process count went from %d to %d, want unchanged", before, after)
	}
}

// TestHttpConnectBoundExpires pins the connect-bound limit for an endpoint
// connection: a fake server that accepts the connection and never answers
// expires the connect bound rather than hanging.
func TestHttpConnectBoundExpires(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.HangForever = true })

	connector := NewHttpConnector(t.Context(), pub_models.McpServer{Name: "hang", Url: srv.URL}, nil, WithConnectBound(100*time.Millisecond))

	start := time.Now()
	_, err := connector.Conn(t.Context())
	elapsed := time.Since(start)

	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", err)
	}
	if startupErr.Stage != "connect" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "connect")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the connect bound did not fire promptly: elapsed %v", elapsed)
	}
}

// TestHttpConnRejectsLegacyOnlyEndpoint pins the out-of-scope legacy
// signal: an initialize POST refused as method-not-allowed while a GET
// with an event-stream accept header succeeds is reported as a typed
// transport error naming the unsupported transport, not silently retried.
func TestHttpConnRejectsLegacyOnlyEndpoint(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.LegacyOnly = true })

	connector := NewHttpConnector(t.Context(), pub_models.McpServer{Name: "legacy", Url: srv.URL}, nil)
	_, err := connector.Conn(t.Context())

	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", err)
	}
	if startupErr.Stage != "connect" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "connect")
	}
	if !strings.Contains(startupErr.Error(), "legacy") {
		t.Errorf("err = %v, want it to name the legacy transport", err)
	}
}

// TestHttpConnDoesNotRetainClientAcrossClose is a cheap sanity check that
// Close is idempotent and fails a pending call, mirroring StdioConn's own
// contract. Not a declared phase-4 test name; kept minimal.
func TestHttpConnDoesNotRetainClientAcrossClose(t *testing.T) {
	srv := httptestserver.New()
	defer srv.Close()
	conn := NewHttpConn(context.Background(), pub_models.McpServer{Name: "x", Url: srv.URL}, nil)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := conn.Call(context.Background(), "initialize", nil); !errors.Is(err, claierr.ErrMcpConnClosed) {
		t.Fatalf("Call after Close: err = %v, want errors.Is(err, claierr.ErrMcpConnClosed)", err)
	}
}

// TestHttpConnRefusesEndpointRedirect pins the sign-off review's B2 half
// that lives on this transport: no CheckRedirect existed on this client, so
// a 307 or 308 from the MCP endpoint made Go re-POST the JSON-RPC request —
// and the bearer token decorating it, which Go strips only cross-domain —
// to the redirect target. The target counts what reaches it and must count
// nothing.
func TestHttpConnRefusesEndpointRedirect(t *testing.T) {
	var sinkHits atomic.Int64
	var sawBearer atomic.Bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sinkHits.Add(1)
		if r.Header.Get("Authorization") != "" {
			sawBearer.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()

	srv := httptestserver.New()
	defer srv.Close()
	srv.Configure(func(c *httptestserver.Config) { c.RedirectPostTo = sink.URL })

	conn := newTestHttpConn(t, srv, WithRequestDecorator(func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer redirect-leak-canary")
	}))
	defer conn.Close()

	_, err := conn.Call(t.Context(), "initialize", map[string]any{"protocolVersion": ProtocolVersion})
	var refused *HttpRedirectRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v (%T), want it to unwrap to *HttpRedirectRefusedError", err, err)
	}
	if refused.To != sink.URL {
		t.Errorf("refusal names target %q, want %q", refused.To, sink.URL)
	}
	if sinkHits.Load() != 0 {
		t.Errorf("the redirect target was reached %d times, want 0 (bearer seen: %v)", sinkHits.Load(), sawBearer.Load())
	}
	if sawBearer.Load() {
		t.Error("the bearer token was resent to the redirect target")
	}
}
