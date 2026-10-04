package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// mcpServerOutBufferSizeKib bounds one message's line length on both stdout
// and stderr. 2MiB: some mcp servers send very large messages.
const mcpServerOutBufferSizeKib = 2048

// mcpHandshakeBound is the default wait for the initialize/tools-list
// handshake. A later connect-bound (phase 2) wraps spawn plus this.
const mcpHandshakeBound = 30 * time.Second

// ProtocolVersion is the MCP protocol version clai advertises in initialize.
// Both the stdio and the streamable-HTTP transport advertise the same
// value; the schema cache records it verbatim alongside a captured entry.
const ProtocolVersion = "2025-06-18"

// errFrameTooLarge marks a line that exceeded the read bound before a
// newline was found. It never escapes this file: callers see it wrapped as
// *claierr.McpFrameUndecodableError.
var errFrameTooLarge = errors.New("mcp: frame exceeded the read bound")

// errFrameNotJSONRPC marks a line that parsed as JSON but carries no
// jsonrpc member, the second half of "cannot be parsed as JSON-RPC at all".
var errFrameNotJSONRPC = errors.New("mcp: frame has no jsonrpc member")

// pendingResult is what a Call's waiter receives: either the raw result or
// the error the connection decided on its behalf.
type pendingResult struct {
	raw json.RawMessage
	err error
}

// StdioConnOption configures a StdioConn at construction time.
type StdioConnOption func(*StdioConn)

// WithHandshakeBound overrides the duration handleServer waits for the
// initialize/tools-list handshake. It replaces the retired per-server
// ControlEvent.StartupTimeout override.
func WithHandshakeBound(d time.Duration) StdioConnOption {
	return func(c *StdioConn) { c.handshakeBound = d }
}

// WithReadBound overrides the per-message read bound, in bytes.
func WithReadBound(n int) StdioConnOption {
	return func(c *StdioConn) { c.readBound = n }
}

// stdioTransport is what StdioConn's goroutines need, independent of
// whether it came from a spawned process or a test harness.
type stdioTransport struct {
	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
	// wait blocks until the underlying process, if any, has exited. A
	// harness with no process supplies a no-op.
	wait func()
}

// StdioConn is the stdio Conn implementation. It owns the connection-wide id
// source, the pending-waiter map, the frame reader, the stderr reader, the
// stdin closer and the reaper.
type StdioConn struct {
	serverName string
	stdin      io.WriteCloser
	enc        *json.Encoder
	writeMu    sync.Mutex

	handshakeBound time.Duration
	readBound      int
	authSink       AuthPendingSink

	mu      sync.Mutex
	closed  bool
	nextID  int
	pending map[int]chan pendingResult

	notifyCh chan string

	authMu         sync.Mutex
	authChallenged bool
	authDone       func()
}

// Notifications implements NotificationWatcher: a buffered, non-blocking
// channel, mirroring HttpConn's, so a notification is dropped rather than
// blocking the frame reader when nobody is watching.
func (c *StdioConn) Notifications() <-chan string { return c.notifyCh }

func (c *StdioConn) publishNotification(method string) {
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

// HandshakeBound reports the duration handleServer should bound the
// initialize/tools-list handshake by. It is not part of the Conn interface;
// handleServer reaches it through an optional-interface check, the same
// feature-detection pattern already used for ServerLogSink.
func (c *StdioConn) HandshakeBound() time.Duration { return c.handshakeBound }

// NewStdioConn spawns the server process described by server and wires a
// StdioConn around its stdio pipes. sink receives the server's stderr lines;
// a nil sink prints them directly.
func NewStdioConn(ctx context.Context, server pub_models.McpServer, sink ServerLogSink, opts ...StdioConnOption) (Conn, error) {
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	// One group per server: a launcher's real process is a descendant.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Assign before Start: watchCtx reads this field unsynchronized.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return killProcessGroup(cmd.Process.Pid)
	}
	cmd.Env = os.Environ()
	if server.EnvFile != "" {
		envFromFile, err := LoadEnvFile(server.EnvFile)
		if err != nil {
			return nil, claierr.NewMcpServerStartup(server.Name, "spawn", err)
		}
		for k, v := range envFromFile {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	for k, v := range server.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	pipes, err := openStdioPipes()
	if err != nil {
		return nil, claierr.NewMcpServerStartup(server.Name, "spawn", err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pipes.stdinR, pipes.stdoutW, pipes.stderrW

	if err := cmd.Start(); err != nil {
		pipes.closeAll()
		return nil, claierr.NewMcpServerStartup(server.Name, "spawn", fmt.Errorf("start mcp server: %w", err))
	}
	pipes.closeChildEnds()
	// Captured once: Process.Pid is not a constant after the process ends.
	pgid := cmd.Process.Pid

	t := stdioTransport{
		stdin:  pipes.stdinW,
		stdout: pipes.stdoutR,
		stderr: pipes.stderrR,
		wait:   func() { reapProcessTree(pgid, server.Name, cmd) },
	}
	return newStdioConn(ctx, server.Name, t, sink, opts...), nil
}

// newStdioConn wires a StdioConn around an already-open transport, starting
// its four goroutines. It performs no handshake of its own; the handshake is
// ordinary traffic driven by the caller through Call and Notify.
func newStdioConn(ctx context.Context, serverName string, t stdioTransport, sink ServerLogSink, opts ...StdioConnOption) *StdioConn {
	c := &StdioConn{
		serverName:     serverName,
		stdin:          t.stdin,
		enc:            json.NewEncoder(t.stdin),
		handshakeBound: mcpHandshakeBound,
		readBound:      mcpServerOutBufferSizeKib * 1024,
		pending:        make(map[int]chan pendingResult),
		notifyCh:       make(chan string, 8),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.authSink == nil {
		// sink (ServerLogSink) may also implement AuthPendingSink: mcpLogSink
		// does, satisfying both roles through one object (the same
		// optional-interface pattern already used for setupSucceeded).
		if as, ok := sink.(AuthPendingSink); ok {
			c.authSink = as
		}
	}

	stderrDone := make(chan struct{})

	// The frame reader is the only reader of the process's standard output
	// and the only producer of demuxed responses.
	go c.readFrames(t.stdout)
	// The stderr reader is a separate reader; it never sees a JSON-RPC frame.
	go c.readStderr(t.stderr, sink, stderrDone)
	// The stdin closer closes the process's standard input when the context
	// ends.
	go func() {
		<-ctx.Done()
		_ = c.stdin.Close()
	}()
	// The reap never waits for the stderr reader: a descendant inherits that
	// pipe. The exit report does, so the tail is complete before it fires.
	go func() {
		t.wait()
		<-stderrDone
		if ctx.Err() != nil {
			return
		}
		if sink != nil {
			sink.ServerExited(serverName)
		}
	}()

	return c
}

// Call sends a JSON-RPC request and waits for the response carrying its id.
func (c *StdioConn) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
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

	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.writeMessage(req); err != nil {
		c.removeWaiter(id)
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			// The connection closed concurrently with this write; report the
			// same typed error a caller blocked in the select below would
			// have seen, rather than a raw pipe-closed error.
			return nil, claierr.NewMcpConnClosed(c.serverName)
		}
		return nil, fmt.Errorf("mcp conn %q: write %s: %w", c.serverName, method, err)
	}

	select {
	case res := <-ch:
		return res.raw, res.err
	case <-ctx.Done():
		c.removeWaiter(id)
		return nil, fmt.Errorf("mcp conn %q: %s cancelled: %w", c.serverName, method, ctx.Err())
	}
}

// Notify sends a notification. It carries no id and registers no waiter.
func (c *StdioConn) Notify(ctx context.Context, method string, params map[string]any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return claierr.NewMcpConnClosed(c.serverName)
	}
	c.mu.Unlock()
	if err := c.writeMessage(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}); err != nil {
		return fmt.Errorf("mcp conn %q: notify %s: %w", c.serverName, method, err)
	}
	return nil
}

// Close is idempotent: it fails every pending waiter with a typed error. A
// second call is a no-op. It does not itself close the process's standard
// input: that is the dedicated stdin closer's job, driven by the run
// context, so that closing it never races a Call that is mid-write.
func (c *StdioConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	pending := c.pending
	c.pending = nil
	// Closed under the same lock publishNotification checks before sending,
	// so a notification arriving concurrently with Close either lands
	// before this point or is dropped by that check, never raced against a
	// closed channel send (R1-27's class, mirroring HttpConn).
	close(c.notifyCh)
	c.mu.Unlock()

	c.resolveAuthPending()
	for _, ch := range pending {
		ch <- pendingResult{err: claierr.NewMcpConnClosed(c.serverName)}
	}
	return nil
}

func (c *StdioConn) removeWaiter(id int) {
	c.mu.Lock()
	if c.pending != nil {
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// failAllPending fails every call currently waiting on this connection with
// err and resets the pending set, but leaves the connection open: attributing
// an id-less frame to one arbitrary caller would silently corrupt the
// others, but the connection itself stays usable for the next call.
func (c *StdioConn) failAllPending(err error) {
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

func (c *StdioConn) writeMessage(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.enc.Encode(v)
}

// readFrames is the only reader of stdout and the only producer of demuxed
// responses. It never terminates the connection on an oversized or
// undecodable frame; it only terminates when the transport itself ends.
func (c *StdioConn) readFrames(stdout io.Reader) {
	defer c.Close()
	defer closeReader(stdout)
	r := newBoundedLineReader(stdout, c.readBound)
	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, errFrameTooLarge) {
				c.failAllPending(claierr.NewMcpFrameUndecodable(c.serverName, fmt.Errorf("%w: bound %d bytes", errFrameTooLarge, c.readBound)))
				continue
			}
			return
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		c.handleLine(line)
	}
}

// handleLine classifies one line from the server: an unparseable frame
// fails every pending waiter; a server-initiated request is answered with a
// method-not-found error; a response is delivered to its matching waiter, or
// dropped if none is waiting.
func (c *StdioConn) handleLine(line []byte) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(line, &generic); err != nil {
		c.failAllPending(claierr.NewMcpFrameUndecodable(c.serverName, err))
		return
	}
	if _, ok := generic["jsonrpc"]; !ok {
		c.failAllPending(claierr.NewMcpFrameUndecodable(c.serverName, errFrameNotJSONRPC))
		return
	}

	if _, hasMethod := generic["method"]; hasMethod {
		idRaw, hasID := generic["id"]
		if !hasID {
			// A notification from the server (e.g.
			// notifications/tools/list_changed): published to the same
			// NotificationWatcher channel HttpConn feeds, so the endpoint
			// schema cache's watcher works identically for either transport
			// (R2-16).
			var m struct {
				Method string `json:"method"`
			}
			if err := json.Unmarshal(line, &m); err == nil {
				c.publishNotification(m.Method)
			}
			return
		}
		var id int
		if err := json.Unmarshal(idRaw, &id); err != nil {
			return
		}
		// clai advertises no sampling/roots/elicitation capability in
		// initialize, so a well-behaved server never asks; a server that
		// does gets a definite answer instead of a stall.
		_ = c.writeMessage(Response{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &RPCError{Code: -32601, Message: "method not found"},
		})
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
	if err := json.Unmarshal(line, &resp); err != nil {
		c.failAllPending(claierr.NewMcpFrameUndecodable(c.serverName, err))
		return
	}
	c.deliver(id, resp)
}

// deliver routes a response to the waiter registered for its id. A frame
// whose id matches no pending waiter is dropped: a late response to an
// abandoned call is normal and must not fail the connection.
func (c *StdioConn) deliver(id int, resp Response) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	// Any definitive answer settles a mid-connect authorization wait this
	// server's stderr may have opened, even one opened on a false-positive
	// keyword match (phase 6): a server is never left waiting on a human
	// past the moment it actually answers.
	c.resolveAuthPending()
	var err error
	if resp.Error != nil {
		err = claierr.NewMcpRPCError(c.serverName, resp.Error.Code, resp.Error.Message)
	}
	ch <- pendingResult{raw: resp.Result, err: err}
}

func (c *StdioConn) readStderr(stderr io.Reader, sink ServerLogSink, done chan<- struct{}) {
	defer close(done)
	defer closeReader(stderr)
	scanner := bufio.NewScanner(stderr)
	buf := make([]byte, c.readBound)
	scanner.Buffer(buf, c.readBound)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if utils.IsMcpLogAuthLine(line) {
			c.raiseAuthPending(utils.IsMcpLogAuthChallengeLine(line))
		}
		if sink != nil {
			sink.AppendServerLog(c.serverName, line)
			continue
		}
		ancli.Noticef("mcp_%v: %v\n", c.serverName, line)
	}
	if err := scanner.Err(); err != nil {
		ancli.Errf("mcp_%v: %s\n", c.serverName, err)
	}
}

// raiseAuthPending opens the auth-pending window at most once per attempt,
// for any line the loose classifier (utils.IsMcpLogAuthLine) matched.
// confirmed additionally marks this attempt challenged — read by
// dialStdio through AuthChallenged() to decide whether a connect or
// handshake failure on this same attempt should be reclassified into an
// AuthChallengeError (phase 6, D20) — only when the stricter classifier
// (utils.IsMcpLogAuthChallengeLine) matched: a bare "401"/"403" substring
// opens the window (harmless, self-resolving) but must not by itself
// change a connect failure's error type (R1-01).
func (c *StdioConn) raiseAuthPending(confirmed bool) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.authSink == nil {
		// Nothing can act on the signal and nobody is told about it, so a
		// connect failure stays a plain connect-stage error rather than
		// being reclassified into a challenge with no remedy anyone sees.
		return
	}
	if confirmed {
		c.authChallenged = true
	}
	if c.authDone == nil {
		c.authDone = c.authSink.AuthPending(c.serverName)
	}
}

// resolveAuthPending calls the pending AuthPendingSink's done function
// exactly once, when this connection's wait concludes either way, and
// clears authChallenged: a connection that received a definitive answer
// (deliver) or was torn down (Close) is no longer mid-challenge, even when
// it was raised on a false-positive keyword match (R1-01).
func (c *StdioConn) resolveAuthPending() {
	c.authMu.Lock()
	done := c.authDone
	c.authDone = nil
	c.authChallenged = false
	c.authMu.Unlock()
	if done != nil {
		done()
	}
}

// AuthChallenged reports whether this connection's stderr has shown an
// authorization prompt since it was created. dialStdio reads this through
// an optional-interface check to decide whether a connect-stage failure
// should be reclassified as an AuthChallengeError.
func (c *StdioConn) AuthChallenged() bool {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.authChallenged
}

// boundedLineReader reads newline-delimited frames with a hard per-line
// size bound. Unlike bufio.Scanner, an oversized line does not end the
// stream: the remainder of that line is discarded so the next line can be
// read, and the caller decides whether that is fatal.
type boundedLineReader struct {
	r *bufio.Reader
}

func newBoundedLineReader(r io.Reader, bound int) *boundedLineReader {
	if bound < 16 {
		bound = 16
	}
	return &boundedLineReader{r: bufio.NewReaderSize(r, bound)}
}

func (b *boundedLineReader) readLine() ([]byte, error) {
	line, err := b.r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		if discardErr := b.discardRestOfLine(); discardErr != nil {
			return nil, discardErr
		}
		return nil, errFrameTooLarge
	}
	if err != nil {
		if len(line) > 0 && errors.Is(err, io.EOF) {
			out := make([]byte, len(line))
			copy(out, line)
			return trimNewline(out), nil
		}
		return nil, err
	}
	out := make([]byte, len(line))
	copy(out, line)
	return trimNewline(out), nil
}

// discardRestOfLine consumes whatever remains of an oversized line so the
// stream resynchronises on the next one.
func (b *boundedLineReader) discardRestOfLine() error {
	for {
		_, err := b.r.ReadSlice('\n')
		if err == nil {
			return nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return err
	}
}

func trimNewline(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	return b
}
