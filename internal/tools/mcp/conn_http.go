package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// mcpSessionHeader is the streamable-HTTP session header name (MCP
// specification, streamable HTTP transport).
const mcpSessionHeader = "Mcp-Session-Id"

// mcpServerStreamRetryBackoff is the production wait between a dropped
// server-initiated stream and its reconnect attempt (R2-11). Not a
// README-declared parameter: an internal resilience constant, not a
// user-facing bound.
const mcpServerStreamRetryBackoff = 2 * time.Second

// mcpServerStreamMaxConsecutiveFailures bounds how many reconnect attempts
// in a row may fail (a GET that never even establishes: refused, wrong
// status, wrong content type) before the server-initiated stream gives up
// for the rest of the run. A stream that does establish and later drops
// resets this counter on every successful (re)connection, so a server that
// works intermittently is retried indefinitely; only a stream that never
// comes back at all stops retrying.
const mcpServerStreamMaxConsecutiveFailures = 5

// mcpProtocolVersionHeader is the header the client advertises its
// negotiated protocol version on.
const mcpProtocolVersionHeader = "Mcp-Protocol-Version"

// errStreamTruncated marks an event-stream POST response that ended before
// the awaited response arrived. It never escapes this file: callers see it
// wrapped as *claierr.McpFrameUndecodableError, the same type an
// undecodable or oversized frame reuses (phase 1's precedent: a bounded
// error differentiated only by its cause).
var errStreamTruncated = errors.New("mcp: event-stream response ended before the awaited frame arrived")

// RequestDecorator mutates an outgoing HTTP request before it is sent, the
// seam the authorization phase supplies (README shared interfaces): this
// phase builds no decorator of its own.
type RequestDecorator func(*http.Request)

// NotificationWatcher is implemented by a Conn that can report unsolicited
// server notifications by method name. HttpConn is the only implementation:
// it is what lets the endpoint-based schema cache invalidate on
// notifications/tools/list_changed without the Conn interface itself
// growing a method. A buffered, non-blocking channel: a notification is
// dropped rather than blocking the demux when nobody is watching, which is
// safe because nothing about correctness depends on a notification being
// observed.
type NotificationWatcher interface {
	Notifications() <-chan string
}

// HttpConnOption configures an HttpConn at construction time.
type HttpConnOption func(*HttpConn)

// WithHttpReadBound overrides the response-body-limit parameter, in bytes.
func WithHttpReadBound(n int) HttpConnOption {
	return func(c *HttpConn) { c.readBound = n }
}

// WithRequestDecorator installs d, called on every outgoing request (POST
// and GET) before it is sent.
func WithRequestDecorator(d RequestDecorator) HttpConnOption {
	return func(c *HttpConn) { c.decorate = d }
}

// WithHttpServerStreamRetryBackoff overrides the wait between a dropped
// server-initiated stream and its reconnect attempt (R2-11). Tests use a
// short value so a reconnect proof does not wait out the production
// default.
func WithHttpServerStreamRetryBackoff(d time.Duration) HttpConnOption {
	return func(c *HttpConn) { c.streamRetryBackoff = d }
}

// HttpConn is the streamable-HTTP Conn implementation. Like StdioConn it
// owns a connection-wide id source and pending-waiter map, but nothing
// about it is shared: each POST is its own HTTP exchange, and the optional
// GET stream is the only long-lived transport it holds.
type HttpConn struct {
	serverName         string
	endpoint           string
	client             *http.Client
	readBound          int
	protocolVersion    string
	decorate           RequestDecorator
	streamRetryBackoff time.Duration

	connCtx    context.Context
	connCancel context.CancelFunc

	mu      sync.Mutex
	closed  bool
	nextID  int
	pending map[int]chan pendingResult
	session string

	streamOnce sync.Once
	notifyCh   chan string
}

// NewHttpConn wires an HttpConn to server.Url. sink is accepted for
// signature symmetry with NewStdioConn and NewConnector; an HTTP connection
// has no child process and so nothing to log through it. ctx governs the
// connection's background work (the optional server-initiated stream);
// Close cancels it.
func NewHttpConn(ctx context.Context, server pub_models.McpServer, sink ServerLogSink, opts ...HttpConnOption) Conn {
	connCtx, cancel := context.WithCancel(ctx)
	c := &HttpConn{
		serverName:         server.Name,
		endpoint:           server.Url,
		client:             &http.Client{CheckRedirect: refuseEndpointRedirect(server.Name)},
		readBound:          mcpServerOutBufferSizeKib * 1024,
		protocolVersion:    ProtocolVersion,
		streamRetryBackoff: mcpServerStreamRetryBackoff,
		connCtx:            connCtx,
		connCancel:         cancel,
		pending:            make(map[int]chan pendingResult),
		notifyCh:           make(chan string, 8),
	}
	for _, opt := range opts {
		opt(c)
	}
	// The production call site Close otherwise has none of (R2-10): when
	// the connection's own context ends — run teardown, or the explicit
	// cancel() on a failed resolution — the connection closes itself and
	// sends its session DELETE, mirroring StdioConn's stdin-closer goroutine
	// for the same lifetime event.
	go func() {
		<-connCtx.Done()
		_ = c.Close()
	}()
	return c
}

// Notifications implements NotificationWatcher.
func (c *HttpConn) Notifications() <-chan string { return c.notifyCh }

// HttpRedirectRefusedError means the MCP endpoint answered a request with a
// redirect. Go re-sends a POST body across a 307 or 308 and only strips the
// Authorization header cross-domain, never the body, so following one would
// hand the JSON-RPC request — and, on a same-host redirect, the bearer
// token decorating it — to the target.
type HttpRedirectRefusedError struct {
	ServerName string
	From       string
	To         string
}

func (e *HttpRedirectRefusedError) Error() string {
	return fmt.Sprintf("mcp conn %q: refused to follow a redirect from %q to %q: the request carries the server's credential", e.ServerName, e.From, e.To)
}

// refuseEndpointRedirect is the CheckRedirect policy every HttpConn
// installs. The endpoint is a fixed property of the server's config, so a
// redirect away from it is never something clai should follow silently.
func refuseEndpointRedirect(serverName string) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		from := ""
		if len(via) > 0 {
			from = via[len(via)-1].URL.String()
		}
		return &HttpRedirectRefusedError{ServerName: serverName, From: from, To: req.URL.String()}
	}
}

// Call sends a JSON-RPC request over POST and waits for the response
// carrying its id, wherever it arrives: the POST response itself (JSON or
// event-stream framed) is read and demuxed through the same pending-waiter
// map the optional GET stream feeds.
func (c *HttpConn) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, claierr.NewMcpConnClosed(c.serverName)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan pendingResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	body, err := json.Marshal(Request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		c.removeWaiter(id)
		return nil, fmt.Errorf("mcp conn %q: encode %s: %w", c.serverName, method, err)
	}

	resp, err := c.doPost(ctx, body)
	if err != nil {
		c.removeWaiter(id)
		return nil, err
	}
	if resp.StatusCode == http.StatusAccepted {
		resp.Body.Close()
		c.removeWaiter(id)
		return nil, fmt.Errorf("mcp conn %q: %s: server accepted with no body but a result was requested", c.serverName, method)
	}

	// Run in its own goroutine, which already owns closing resp.Body: Call
	// must resolve as soon as deliver puts a result on ch, not when the
	// body ends. A server may hold an event-stream body open after
	// answering (the specification says SHOULD close it, not MUST), and
	// consumeSSE loops until EOF, so calling this inline would block Call
	// behind the body's own lifetime instead of the answer's arrival.
	go c.consumeResponseBody(resp, id)

	select {
	case res := <-ch:
		if method == "initialize" && res.err == nil {
			c.captureSession(resp)
			c.streamOnce.Do(func() { go c.runServerStream() })
		}
		return res.raw, res.err
	case <-ctx.Done():
		c.removeWaiter(id)
		return nil, fmt.Errorf("mcp conn %q: %s cancelled: %w", c.serverName, method, ctx.Err())
	}
}

// Notify sends a notification over POST. It registers no waiter: an
// accepted status with no body is the expected outcome and is never
// treated as a pending call or a timeout.
func (c *HttpConn) Notify(ctx context.Context, method string, params map[string]any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return claierr.NewMcpConnClosed(c.serverName)
	}
	c.mu.Unlock()

	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("mcp conn %q: encode notify %s: %w", c.serverName, method, err)
	}
	resp, err := c.doPost(ctx, body)
	if err != nil {
		return fmt.Errorf("mcp conn %q: notify %s: %w", c.serverName, method, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, int64(c.readBound)))
	return nil
}

// mcpSessionDeleteBound bounds the best-effort DELETE Close sends to end a
// held session (R2-10): never load-bearing for the connection's own
// teardown, so a slow or unreachable server cannot hang Close.
const mcpSessionDeleteBound = 5 * time.Second

// Close is idempotent: it fails every pending waiter with a typed error,
// stops the optional server-initiated stream, and — when the connection
// holds a session id — sends DELETE to end it per the streamable-HTTP
// spec's session-termination clause (R2-10). A second call is a no-op.
func (c *HttpConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	pending := c.pending
	c.pending = nil
	session := c.session
	// Closed under the same lock publishNotification checks before sending,
	// so a notification arriving concurrently with Close either lands before
	// this point or is dropped by that check, never raced against a closed
	// channel send (R1-27).
	close(c.notifyCh)
	c.mu.Unlock()

	if session != "" {
		c.sendSessionDelete(session)
	}

	c.connCancel()
	for _, ch := range pending {
		ch <- pendingResult{err: claierr.NewMcpConnClosed(c.serverName)}
	}
	return nil
}

// sendSessionDelete is best-effort: Close's own contract (idempotent,
// never blocking indefinitely) does not depend on this request succeeding,
// so every error is swallowed rather than returned. Bounded by its own
// background context rather than connCtx, which Close is about to cancel.
func (c *HttpConn) sendSessionDelete(session string) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpSessionDeleteBound)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set(mcpSessionHeader, session)
	req.Header.Set(mcpProtocolVersionHeader, c.protocolVersion)
	if c.decorate != nil {
		c.decorate(req)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func (c *HttpConn) sessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

func (c *HttpConn) captureSession(resp *http.Response) {
	if sid := resp.Header.Get(mcpSessionHeader); sid != "" {
		c.mu.Lock()
		c.session = sid
		c.mu.Unlock()
	}
}

func (c *HttpConn) removeWaiter(id int) {
	c.mu.Lock()
	if c.pending != nil {
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *HttpConn) isPending(id int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pending[id]
	return ok
}

// failOne fails exactly the waiter for id, if one is still pending, without
// touching any other call: a POST response is its own HTTP exchange, so a
// problem reading one response body never implies another is broken.
func (c *HttpConn) failOne(id int, err error) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- pendingResult{err: err}
	}
}

// failAllPending fails every call currently waiting with err and resets the
// pending set, but leaves the connection open, mirroring StdioConn: an
// id-less or undecodable frame on the shared server-initiated stream gives
// no way to attribute blame to one caller.
func (c *HttpConn) failAllPending(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	pending := c.pending
	c.pending = make(map[int]chan pendingResult)
	c.mu.Unlock()

	for _, ch := range pending {
		ch <- pendingResult{err: err}
	}
}

// deliver routes a response to the waiter registered for its id. A frame
// whose id matches no pending waiter is dropped: a late response to an
// abandoned call is normal and must not fail the connection.
func (c *HttpConn) deliver(id int, resp Response) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	var err error
	if resp.Error != nil {
		err = claierr.NewMcpRPCError(c.serverName, resp.Error.Code, resp.Error.Message)
	}
	ch <- pendingResult{raw: resp.Result, err: err}
}

func (c *HttpConn) publishNotification(method string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.notifyCh <- method:
	default:
	}
}

// doPost sends body to the endpoint and classifies the response by status:
// an authorization challenge, a session rejection, a non-ok status and an
// accepted-with-no-body outcome are all resolved here, before any
// content-type-specific body handling. The caller owns resp.Body on a nil
// error.
func (c *HttpConn) doPost(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp conn %q: build request: %w", c.serverName, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcpProtocolVersionHeader, c.protocolVersion)
	if c.decorate != nil {
		c.decorate(req)
	}
	hadSession := c.sessionID() != ""
	if hadSession {
		req.Header.Set(mcpSessionHeader, c.sessionID())
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, claierr.NewMcpTransport(c.serverName, c.endpoint, err)
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		defer resp.Body.Close()
		challenge := resp.Header.Get("WWW-Authenticate")
		return nil, claierr.NewAuthChallenge(c.serverName, challenge, parseResourceMetadata(challenge))
	case http.StatusAccepted, http.StatusOK:
		return resp, nil
	case http.StatusNotFound:
		if hadSession {
			defer resp.Body.Close()
			msg := readLimited(resp.Body, c.readBound)
			return nil, claierr.NewMcpServerStartup(c.serverName, "session", fmt.Errorf("server rejected the session: %s", msg))
		}
		fallthrough
	default:
		defer resp.Body.Close()
		msg := readLimited(resp.Body, c.readBound)
		return nil, claierr.NewMcpHttpStatus(c.serverName, resp.StatusCode, msg)
	}
}

// consumeResponseBody reads and demuxes a Call's POST response body, bound
// by the response-body-limit parameter. It always closes the body.
func (c *HttpConn) consumeResponseBody(resp *http.Response, forID int) {
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	limited := io.LimitReader(resp.Body, int64(c.readBound)+1)

	switch {
	case strings.HasPrefix(ct, "application/json"):
		data, err := io.ReadAll(limited)
		if err != nil {
			c.failOne(forID, fmt.Errorf("mcp conn %q: read response: %w", c.serverName, err))
			return
		}
		if len(data) > c.readBound {
			c.failOne(forID, claierr.NewMcpFrameUndecodable(c.serverName, fmt.Errorf("%w: bound %d bytes", errFrameTooLarge, c.readBound)))
			return
		}
		c.handleJSONFrame(data, func(e error) {
			c.failOne(forID, claierr.NewMcpFrameUndecodable(c.serverName, e))
		})
	case strings.HasPrefix(ct, "text/event-stream"):
		c.consumeSSE(limited, forID)
	default:
		c.failOne(forID, claierr.NewMcpUnsupportedContentType(c.serverName, ct))
	}
}

// consumeSSE reads every frame of one POST response's event-stream body,
// delivering each to the shared demux, until the body ends. If forID's
// waiter was never resolved by any frame, the stream ended before the
// awaited response arrived: that call alone fails, and the connection
// remains usable for every other.
func (c *HttpConn) consumeSSE(r io.Reader, forID int) {
	fr := newSSEFrameReader(r, c.readBound)
	for {
		data, err := fr.next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.failOne(forID, claierr.NewMcpFrameUndecodable(c.serverName, fmt.Errorf("%w: bound %d bytes", errFrameTooLarge, c.readBound)))
			}
			break
		}
		c.handleJSONFrame(data, func(e error) {
			c.failOne(forID, claierr.NewMcpFrameUndecodable(c.serverName, e))
		})
	}
	if c.isPending(forID) {
		c.failOne(forID, claierr.NewMcpFrameUndecodable(c.serverName, errStreamTruncated))
	}
}

// handleJSONFrame classifies one decoded JSON-RPC frame, from either a POST
// response or the GET server stream: a notification is published to the
// NotificationWatcher channel, a response is delivered to its waiter, and
// an undecodable frame is reported through onMalformed, whose scope (one
// call, or every pending call) depends on which source called this.
func (c *HttpConn) handleJSONFrame(data []byte, onMalformed func(error)) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(data, &generic); err != nil {
		onMalformed(err)
		return
	}
	if _, ok := generic["jsonrpc"]; !ok {
		onMalformed(errFrameNotJSONRPC)
		return
	}

	if _, hasMethod := generic["method"]; hasMethod {
		if idRaw, hasID := generic["id"]; hasID {
			// A server-initiated request: clai advertises no capability
			// that would make one well-formed. Streamable HTTP's duplex
			// answers it the same way phase 1's stdio transport does,
			// POSTed back to the endpoint rather than dropped (R1-13).
			var id int
			if err := json.Unmarshal(idRaw, &id); err == nil {
				go c.respondMethodNotFound(id)
			}
			return
		}
		var m struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(data, &m); err == nil {
			c.publishNotification(m.Method)
		}
		return
	}

	idRaw, hasID := generic["id"]
	if !hasID {
		return
	}
	var id int
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return
	}
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		onMalformed(err)
		return
	}
	c.deliver(id, resp)
}

// respondMethodNotFound POSTs a -32601 response for a server-initiated
// request carrying id, run in its own goroutine so answering it never
// blocks the GET stream's read loop. Best-effort: the connection's
// lifetime does not depend on this POST succeeding, mirroring
// runServerStream's own fire-and-forget background work.
func (c *HttpConn) respondMethodNotFound(id int) {
	body, err := json.Marshal(Response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: -32601, Message: "method not found"}})
	if err != nil {
		return
	}
	resp, err := c.doPost(c.connCtx, body)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// runServerStream keeps the optional server-initiated GET stream alive for
// the connection's lifetime: it reconnects, carrying Last-Event-ID, after
// the stream ends for any reason short of the connection itself closing
// (R2-11). A method-not-allowed answer is expected and normal on the first
// attempt; the connection remains fully usable either way. Consecutive
// failed connection *attempts* (never an established stream that later
// drops) are bounded so a server that never offers the stream at all does
// not retry forever.
func (c *HttpConn) runServerStream() {
	lastEventID := ""
	consecutiveFailures := 0
	for {
		if c.connCtx.Err() != nil {
			return
		}
		established, newLastEventID := c.openServerStreamOnce(lastEventID)
		lastEventID = newLastEventID
		if c.connCtx.Err() != nil {
			return
		}
		if established {
			consecutiveFailures = 0
		} else {
			consecutiveFailures++
			if consecutiveFailures >= mcpServerStreamMaxConsecutiveFailures {
				return
			}
		}
		select {
		case <-c.connCtx.Done():
			return
		case <-time.After(c.streamRetryBackoff):
		}
	}
}

// openServerStreamOnce opens the server-initiated GET stream once, carrying
// Last-Event-ID when lastEventID is non-empty, and reads frames until the
// stream ends. It reports whether the stream was actually established (a
// 200 with an event-stream content type) and the last SSE id: observed, so
// a caller can decide whether to count this as a failure and what id to
// resume from on the next attempt.
func (c *HttpConn) openServerStreamOnce(lastEventID string) (established bool, newLastEventID string) {
	req, err := http.NewRequestWithContext(c.connCtx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return false, lastEventID
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(mcpProtocolVersionHeader, c.protocolVersion)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	if c.decorate != nil {
		c.decorate(req)
	}
	if sid := c.sessionID(); sid != "" {
		req.Header.Set(mcpSessionHeader, sid)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false, lastEventID
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, lastEventID
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return false, lastEventID
	}

	// No outer io.LimitReader here, unlike the POST paths: this stream is
	// long-lived and the response-body-limit parameter is "per message"
	// (R1-12). sseFrameReader's own scanner buffer already bounds each
	// individual frame; wrapping the whole stream in a cumulative limiter
	// would silently kill it, and the tools-list-changed invalidation it
	// carries, after readBound total bytes regardless of how many frames
	// that spans.
	fr := newSSEFrameReader(resp.Body, c.readBound)
	for {
		data, err := fr.next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.failAllPending(claierr.NewMcpFrameUndecodable(c.serverName, fmt.Errorf("%w: bound %d bytes", errFrameTooLarge, c.readBound)))
			}
			return true, fr.LastID()
		}
		c.handleJSONFrame(data, func(e error) {
			c.failAllPending(claierr.NewMcpFrameUndecodable(c.serverName, e))
		})
	}
}

// readLimited reads up to limit bytes of r for an error message, ignoring
// any read failure: a server's own error body is best-effort context, never
// load-bearing.
func readLimited(r io.Reader, limit int) string {
	data, _ := io.ReadAll(io.LimitReader(r, int64(limit)))
	return string(data)
}

// parseResourceMetadata extracts the resource_metadata parameter's value
// out of a WWW-Authenticate header, verbatim quoting removed. Absent is
// reported as "", matching claierr.AuthChallengeError's own contract.
func parseResourceMetadata(challenge string) string {
	const key = "resource_metadata="
	_, after, ok := strings.Cut(challenge, key)
	if !ok {
		return ""
	}
	rest := after
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' {
		end := strings.IndexByte(rest[1:], '"')
		if end == -1 {
			return ""
		}
		return rest[1 : end+1]
	}
	end := strings.IndexAny(rest, ", ")
	if end == -1 {
		return rest
	}
	return rest[:end]
}

// sseFrameReader reads server-sent-events framing: event: and data: lines,
// one field per line, each event terminated by a blank line, with a data:
// payload accumulated across consecutive lines and parsed as one frame.
// Adapted from the abandoned branch's scanner approach, the one reusable
// idea it carries (phase 4 specification); the request and response
// plumbing around it is new.
type sseFrameReader struct {
	scanner *bufio.Scanner
	lastID  string
}

func newSSEFrameReader(r io.Reader, bound int) *sseFrameReader {
	if bound < 16 {
		bound = 16
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), bound)
	return &sseFrameReader{scanner: sc}
}

// LastID reports the most recent SSE id: field value this reader has seen,
// the resumption token Last-Event-ID carries on a reconnect (R2-11).
func (f *sseFrameReader) LastID() string { return f.lastID }

// next returns the next event's data payload, or io.EOF when the stream
// ends with no further frame. A buffer overflow (a line past the bound) is
// reported as a plain, non-EOF error so the caller can treat it as the
// oversized-frame case. An event whose data: field is empty (or whose only
// data: lines are all empty) is a keep-alive, not a frame: it never
// counts as "carried content", so it is skipped rather than returned as a
// zero-length frame (R2-14), which would otherwise fail every call pending
// on the shared GET stream for no protocol reason.
func (f *sseFrameReader) next() ([]byte, error) {
	var data []string
	for f.scanner.Scan() {
		line := f.scanner.Text()
		if line == "" {
			if joined := strings.Join(data, "\n"); joined != "" {
				return []byte(joined), nil
			}
			data = nil
			continue
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(after, " "))
			continue
		}
		if after, ok := strings.CutPrefix(line, "id:"); ok {
			// Recorded for Last-Event-ID on a reconnect (R2-11); id: never
			// carries a JSON-RPC frame itself.
			f.lastID = strings.TrimPrefix(after, " ")
			continue
		}
		// Other fields (event:, retry:) are read but not interpreted.
	}
	if err := f.scanner.Err(); err != nil {
		return nil, err
	}
	if joined := strings.Join(data, "\n"); joined != "" {
		return []byte(joined), nil
	}
	return nil, io.EOF
}

// NewHttpConnector builds the production Connector for an endpoint-based
// server. Resolving it dials no process; it performs the initialize
// handshake over HTTP, bounded by the connect-bound, exactly as
// NewConnector does for a command-based server.
func NewHttpConnector(runCtx context.Context, server pub_models.McpServer, sink ServerLogSink, opts ...ConnectorOption) Connector {
	o := connectOptions{connectBound: mcpConnectBound}
	for _, opt := range opts {
		opt(&o)
	}
	return newConnector(runCtx, func(ctx context.Context) (Conn, error) {
		return dialHttp(ctx, server, sink, o.connectBound, o.httpDecorate)
	})
}

// dialHttp performs the initialize handshake over an HttpConn, bounded by
// connectBound. decorate, when non-nil, is attached to the connection
// before the initialize call ever goes out (the authorization phase's
// seam for an already resolved credential). On a refused initialize it
// checks for the legacy-only signal before reporting a plain connect
// failure; an authorization challenge (claierr.AuthChallengeError) passes
// through untouched; either way the caller decides what the refusal means.
func dialHttp(runCtx context.Context, server pub_models.McpServer, sink ServerLogSink, connectBound time.Duration, decorate RequestDecorator) (Conn, error) {
	var opts []HttpConnOption
	if decorate != nil {
		opts = append(opts, WithRequestDecorator(decorate))
	}
	conn := NewHttpConn(runCtx, server, sink, opts...)
	httpConn := conn.(*HttpConn)

	connectCtx, cancel := context.WithTimeout(runCtx, connectBound)
	defer cancel()

	if _, err := initializeHandshake(connectCtx, conn, server.Name); err != nil {
		if legacyErr := detectLegacyOnlyEndpoint(connectCtx, httpConn, server, err); legacyErr != nil {
			return nil, legacyErr
		}
		return nil, ReportAsConnectStage(err, connectCtx, server.Name)
	}
	return conn, nil
}

// detectLegacyOnlyEndpoint reports the typed legacy-transport error when
// initErr is a method-not-allowed or not-found refusal of the initialize
// POST and a GET with an event-stream accept header succeeds: the legacy
// handshake, in which the client was expected to open the stream first and
// learn a separate POST endpoint from an "endpoint" event. Any other
// outcome returns nil so the caller reports the plain connect failure.
func detectLegacyOnlyEndpoint(ctx context.Context, conn *HttpConn, server pub_models.McpServer, initErr error) error {
	var statusErr *claierr.McpHttpStatusError
	if !errors.As(initErr, &statusErr) {
		return nil
	}
	if statusErr.StatusCode != http.StatusMethodNotAllowed && statusErr.StatusCode != http.StatusNotFound {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.Url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := conn.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return claierr.NewMcpServerStartup(server.Name, "connect", fmt.Errorf(
			"endpoint speaks only the legacy sse-first mcp transport (GET event-stream succeeded while POST initialize was refused with status %d); streamable HTTP POST is required instead",
			statusErr.StatusCode))
	}
	return nil
}
