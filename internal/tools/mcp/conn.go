package mcp

import (
	"context"
	"encoding/json"

	"github.com/baalimago/clai/pkg/claierr"
)

// Conn owns one MCP session: its id sequence, its pending waiters, and its
// underlying transport. One Conn belongs to exactly one run.
type Conn interface {
	Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error)
	Notify(ctx context.Context, method string, params map[string]any) error
	Close() error
}

// Connector resolves a Conn on first use. Implementations are single-flight:
// concurrent callers for one server share one resolution.
type Connector interface {
	Conn(ctx context.Context) (Conn, error)
}

// ServerLogSink receives the stderr output of a spawned MCP server process.
// Implementations decide how to display or buffer the lines. A nil sink keeps
// the legacy direct printing behaviour.
type ServerLogSink interface {
	// AppendServerLog delivers one non-empty stderr line.
	AppendServerLog(server, line string)
	// ServerExited reports that the server process terminated while the run
	// context was still alive. Implementations flush their buffered tail as an
	// error block so the crash reason stays visible.
	ServerExited(server string)
}

// AuthPendingSink is told that a connection is waiting for a human. The
// returned function is called exactly once when the wait resolves, either
// way (worklog 2026-10-02-mcp-connection-cost, phase 6). The tool-call site
// is the sole caller for both transports: an AuthChallengeError is the one
// signal a connect attempt produces, whichever transport produced it, so the
// surfacing code — raising the signal, bounding the wait, retrying once —
// has a single path instead of one per transport.
type AuthPendingSink interface {
	AuthPending(server string) (done func())
}

// AuthResolver drives the one action a tool-call site may take while an
// authorization wait is open. For a command-based (stdio) server there is
// nothing to drive beyond letting time pass — an external tool may finish
// authorizing out of band — so its tools carry a nil resolver and the
// tool-call site merely waits out the bound. For an endpoint-based server it
// is the interactive OAuth flow (phase 5), supplied by internal/text, which
// alone imports mcpauth; this package stays free of that dependency.
type AuthResolver interface {
	ResolveChallenge(ctx context.Context, challenge *claierr.AuthChallengeError) error
}

// AuthResolverFunc adapts a function to AuthResolver.
type AuthResolverFunc func(ctx context.Context, challenge *claierr.AuthChallengeError) error

// ResolveChallenge implements AuthResolver.
func (f AuthResolverFunc) ResolveChallenge(ctx context.Context, challenge *claierr.AuthChallengeError) error {
	return f(ctx, challenge)
}
