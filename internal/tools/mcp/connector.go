package mcp

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// mcpConnectBound is the connect-bound parameter: the default wait wrapping
// spawn plus the initialize handshake for a lazily resolved connection. It is
// distinct from mcpHandshakeBound, which governs an already-spawned Conn's
// initialize/tools-list round trip driven by Manager.
const mcpConnectBound = 45 * time.Second

// mcpBlockedRedialCap bounds how many consecutive blocked-outside-run
// outcomes (D20's exemption) a connector will dial for before memoising the
// outcome as a terminal failure: the first attempt plus one re-dial (D36,
// R1-01). Without this cap, a server that never grants its challenge pays a
// fresh process birth and a full auth-timeout wait on every tool call for
// the rest of the run.
const mcpBlockedRedialCap = 2

// dialFunc performs one resolution attempt under the run context. The
// production Connector dials through dialStdio; a test drives the connector
// directly with a fake, with no process involved.
type dialFunc func(ctx context.Context) (Conn, error)

// outsideRunBlocker is implemented by an error reporting that resolution
// cannot proceed until something changes outside the run - a human
// authorizing a credential is the only case this worklog introduces, and it
// is owned by a later phase. The connector neither memoises nor retries such
// an outcome: it is returned to the caller untouched and the next call tries
// again. Nothing in this phase produces one; its own test drives the
// connector directly with a hand-built error satisfying this interface.
type outsideRunBlocker interface {
	BlockedOutsideRun() bool
}

func isBlockedOutsideRun(err error) bool {
	var b outsideRunBlocker
	if errors.As(err, &b) {
		return b.BlockedOutsideRun()
	}
	return false
}

// resolution is one in-flight dial, shared by every caller that arrives
// while it runs.
type resolution struct {
	done chan struct{}
	conn Conn
	err  error
}

// connector is the per-server Connector: single-flight, and memoised once a
// dial produces a terminal outcome (success, or a run-scoped failure). A
// blocked-outside-run outcome is never memoised, so the next call dials
// again.
type connector struct {
	runCtx context.Context
	dial   dialFunc

	mu              sync.Mutex
	resolved        bool
	conn            Conn
	err             error
	inflight        *resolution
	blockedAttempts int
}

func newConnector(runCtx context.Context, dial dialFunc) *connector {
	return &connector{runCtx: runCtx, dial: dial}
}

// Conn implements Connector. ctx bounds only this caller's wait: resolution
// itself always runs under the connector's run context, so a caller that
// abandons its wait neither cancels the resolution nor is retried by it.
func (c *connector) Conn(ctx context.Context) (Conn, error) {
	c.mu.Lock()
	if c.resolved {
		conn, err := c.conn, c.err
		c.mu.Unlock()
		return conn, err
	}
	r := c.inflight
	if r == nil {
		r = &resolution{done: make(chan struct{})}
		c.inflight = r
		c.mu.Unlock()
		go c.resolve(r)
	} else {
		c.mu.Unlock()
	}

	select {
	case <-r.done:
		return r.conn, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *connector) resolve(r *resolution) {
	conn, err := c.dial(c.runCtx)

	c.mu.Lock()
	c.inflight = nil
	switch {
	case err == nil:
		c.resolved = true
		c.conn = conn
	case isBlockedOutsideRun(err):
		c.blockedAttempts++
		if c.blockedAttempts >= mcpBlockedRedialCap {
			// D36: the retry budget for a non-memoised outcome is spent.
			// Memoise it like any other terminal failure so a further call
			// replays it instead of dialing again.
			c.resolved = true
			c.err = err
		}
	default:
		c.resolved = true
		c.err = err
	}
	c.mu.Unlock()

	r.conn, r.err = conn, err
	close(r.done)
}

// ConnectorOption configures a production Connector's dial behaviour at
// construction.
type ConnectorOption func(*connectOptions)

type connectOptions struct {
	connectBound time.Duration
	// httpDecorate is read only by dialHttp; a stdio connector ignores it.
	httpDecorate RequestDecorator
}

// WithConnectBound overrides the connect-bound parameter.
func WithConnectBound(d time.Duration) ConnectorOption {
	return func(o *connectOptions) { o.connectBound = d }
}

// ConnectBoundOf reports the connect-bound duration server resolves to:
// its own connect_timeout_seconds when set, the connect-bound parameter's
// default otherwise. NewConnector applies this internally; it is exported
// so a caller that connects a command-based server without going through a
// Connector (the schema cache's miss path) bounds spawn plus handshake by
// the same value instead of falling through to the unrelated,
// connection-level handshake bound (R1-14).
func ConnectBoundOf(server pub_models.McpServer) time.Duration {
	if server.ConnectTimeoutSeconds > 0 {
		return time.Duration(server.ConnectTimeoutSeconds) * time.Second
	}
	return mcpConnectBound
}

// WithHttpRequestDecorator attaches d to the HttpConn an HTTP connector
// dials. It is the authorization phase's seam for handing an already
// resolved credential to a lazily resolved endpoint-based server, so the
// decorator is in place before the connect-bound's own initialize call ever
// goes out. Ignored by a stdio connector.
func WithHttpRequestDecorator(d RequestDecorator) ConnectorOption {
	return func(o *connectOptions) { o.httpDecorate = d }
}

// DialFunc performs one resolution attempt under the run context.
type DialFunc func(ctx context.Context) (Conn, error)

// NewConnectorFromDial builds a single-flight, memoising Connector around
// dial, the same mechanism NewConnector and NewHttpConnector build
// internally. Exported so the authorization phase can wrap a dial with its
// own credential resolution and interactive fallback (an AuthChallengeError
// from a bare attempt) before handing the result this package's standard
// connect-once-per-server semantics (phase 5).
func NewConnectorFromDial(runCtx context.Context, dial DialFunc) Connector {
	return newConnector(runCtx, dialFunc(dial))
}

// NewResolvedConnector wraps conn, which has already completed its
// handshake, as a Connector with no resolution work left to do. It is the
// schema cache's miss-path seam: setup already holds a working Conn and
// just needs a Connector to hand the tools it registers.
func NewResolvedConnector(conn Conn) Connector { return resolvedConnector{conn} }

// NewConnector builds the production Connector for server. Resolving it,
// whether eagerly right after construction or lazily on first use, spawns
// the process and performs the initialize handshake, bounded by the
// connect-bound. runCtx governs the resolved connection's whole lifetime;
// sink receives the server's stderr.
func NewConnector(runCtx context.Context, server pub_models.McpServer, sink ServerLogSink, opts ...ConnectorOption) Connector {
	o := connectOptions{connectBound: mcpConnectBound}
	for _, opt := range opts {
		opt(&o)
	}
	return newConnector(runCtx, func(ctx context.Context) (Conn, error) {
		return dialStdio(ctx, server, sink, o.connectBound)
	})
}

// dialStdio spawns server and performs the initialize handshake, bounded by
// connectBound. runCtx governs the resulting connection's lifetime: on
// success the spawned process stays alive for the life of the run; on any
// failure the process is cancelled (and so reaped by its own stdin-closer
// and reaper goroutines) before dialStdio returns.
func dialStdio(runCtx context.Context, server pub_models.McpServer, sink ServerLogSink, connectBound time.Duration) (_ Conn, resultErr error) {
	procCtx, procCancel := context.WithCancel(runCtx)
	// On success procCtx must outlive dialStdio, bound to runCtx instead:
	// the connection is reused for the life of the run. This defer only
	// reaps the process when dialStdio itself is about to fail.
	defer func() {
		if resultErr != nil {
			procCancel()
		}
	}()

	conn, err := NewStdioConn(procCtx, server, sink)
	if err != nil {
		// NewStdioConn already returns a typed *claierr.McpServerStartupError
		// naming the spawn stage; propagate it as-is.
		return nil, err
	}

	connectCtx, cancel := context.WithTimeout(runCtx, connectBound)
	defer cancel()

	if _, err := initializeHandshake(connectCtx, conn, server.Name); err != nil {
		return nil, reclassifyIfAuthChallenged(conn, ReportAsConnectStage(err, connectCtx, server.Name), server.Name)
	}
	return conn, nil
}

// reclassifyIfAuthChallenged replaces a connect-stage failure with a typed
// AuthChallengeError when the connection's stderr showed an authorization
// prompt during this attempt: otherwise a human reading a URL would surface
// only as an indistinguishable connect-bound timeout, and the single-flight
// connector would memoise it as an ordinary terminal failure instead of
// leaving the next call free to retry (phase 6, D20). conn is consulted
// through an optional-interface check so a Conn with nothing to report (no
// sink, or an already-typed failure unrelated to stderr) passes err through
// unchanged.
func reclassifyIfAuthChallenged(conn Conn, err error, serverName string) error {
	if err == nil {
		return nil
	}
	challenged, ok := conn.(interface{ AuthChallenged() bool })
	if !ok || !challenged.AuthChallenged() {
		return err
	}
	return claierr.NewAuthChallenge(serverName, err.Error(), "")
}

// ReportAsConnectStage renames a handshake failure's stage to "connect" when
// the connect-bound, rather than the handshake itself, is what expired: the
// connect bound is the outer bound a lazy first call observes. Exported so
// the schema cache's miss path, which connects without a Connector, applies
// the same stage naming when its own connect-bound context is what expired
// (R1-14).
func ReportAsConnectStage(err error, connectCtx context.Context, serverName string) error {
	if connectCtx.Err() == nil {
		return err
	}
	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		return err
	}
	return claierr.NewMcpServerStartup(serverName, "connect", startupErr.Cause)
}
