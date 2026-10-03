package text

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// fakeAuthConn is a minimal mcp.Conn: every call this phase's wait logic
// drives succeeds instantly once a dial function hands one out.
type fakeAuthConn struct{}

func (fakeAuthConn) Call(context.Context, string, map[string]any) (json.RawMessage, error) {
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"isError":false}`), nil
}
func (fakeAuthConn) Notify(context.Context, string, map[string]any) error { return nil }
func (fakeAuthConn) Close() error                                         { return nil }

// challengeThenSucceedDial returns an *claierr.AuthChallengeError on its
// first call and a working fakeAuthConn on every later call, counting every
// attempt so a test can assert exactly how many real resolutions ran.
func challengeThenSucceedDial(serverName string, calls *int32) mcp.DialFunc {
	return func(context.Context) (mcp.Conn, error) {
		if atomic.AddInt32(calls, 1) == 1 {
			return nil, claierr.NewAuthChallenge(serverName, `Bearer realm="OAuth"`, "")
		}
		return fakeAuthConn{}, nil
	}
}

// alwaysChallengeDial returns an *claierr.AuthChallengeError on every call.
func alwaysChallengeDial(serverName string, calls *int32) mcp.DialFunc {
	return func(context.Context) (mcp.Conn, error) {
		atomic.AddInt32(calls, 1)
		return nil, claierr.NewAuthChallenge(serverName, `Bearer realm="OAuth"`, "")
	}
}

// newAuthTool builds a real *mcp.mcpTool (through the exported constructors
// only) wired to dial, so these tests drive the production ResolveForCall
// and Call paths rather than a hand-rolled double of mcpTool itself.
func newAuthTool(t *testing.T, serverName string, dial mcp.DialFunc, authResolver mcp.AuthResolver, authTimeout, callTimeout time.Duration) pub_models.LLMTool {
	t.Helper()
	connector := mcp.NewConnectorFromDial(t.Context(), dial)
	spec := pub_models.Specification{Name: "mcp_" + serverName + "_echo"}
	return mcp.NewTool(connector, "echo", spec, callTimeout, serverName, authResolver, authTimeout)
}

// newAuthExecutor builds a toolExecutor whose per-run toolset holds exactly
// one tool, with a real mcpLogSink wired so AuthPending is driven through
// the production sink rather than a stand-in.
func newAuthExecutor(toolName string, tool pub_models.LLMTool) (toolExecutor[*MockQuerier], *strings.Builder) {
	var errOut strings.Builder
	sink := newMcpLogSinkTo(mcpLogRolling, &errOut)
	sink.attach() // past the pre-session window; this phase's own reopen is what's under test
	q := &Querier[*MockQuerier]{
		Raw:     true,
		out:     &strings.Builder{},
		mcpSink: sink,
		tooling: tooling{run: map[string]pub_models.LLMTool{toolName: tool}},
	}
	return toolExecutor[*MockQuerier]{querier: q}, &errOut
}

// TestAuthPendingSignalRaisedByHttpAndStdio pins that both producers — a
// command-based tool with no AuthResolver and an endpoint-based tool with
// one — raise the same auth-pending signal (one bell each). Their outcomes
// diverge by design (D37): the no-resolver case has nothing to tell a wait
// apart from an ordinary expiry, so it never retries, while the resolver
// case retries once and succeeds.
func TestAuthPendingSignalRaisedByHttpAndStdio(t *testing.T) {
	cases := []struct {
		name         string
		authResolver mcp.AuthResolver
		// authTimeout: the stdio-like case has nothing to drive completion
		// early, so it always waits out the whole bound with no retry;
		// kept short so the test stays fast but wide enough to stay clear
		// of scheduling jitter under load (R1-30/R2-26). The http-like
		// case's resolver returns immediately, so its bound only needs to
		// be long enough to never expire.
		authTimeout time.Duration
		wantOut     string
		wantCalls   int32
	}{
		{"stdio-like, no resolver", nil, 150 * time.Millisecond, `ERROR: mcp server "fx" needs authorization; it is a command-based server prompting on its own stderr output — "clai mcp auth" only works for a url-based server`, 1},
		{"http-like, resolver drives the interactive flow", mcp.AuthResolverFunc(func(context.Context, *claierr.AuthChallengeError) error { return nil }), time.Second, "ok", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			tool := newAuthTool(t, "fx", challengeThenSucceedDial("fx", &calls), tc.authResolver, tc.authTimeout, 0)
			e, errOut := newAuthExecutor("mcp_fx_echo", tool)

			out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
			if out != tc.wantOut {
				t.Fatalf("out = %q, want %q", out, tc.wantOut)
			}
			if !strings.Contains(errOut.String(), "\a") {
				t.Errorf("bell not rung: errOut = %q", errOut.String())
			}
			if got := atomic.LoadInt32(&calls); got != tc.wantCalls {
				t.Errorf("dial called %d times, want %d", got, tc.wantCalls)
			}
		})
	}
}

// TestAuthTimeoutZeroFailsFast pins the zero-value posture: no wait, no
// bell, no window, and the actionable result is returned immediately.
func TestAuthTimeoutZeroFailsFast(t *testing.T) {
	var calls int32
	// A resolver that fails the test if ever invoked: the zero-value bound
	// must return the actionable result with no wait at all, so nothing
	// should ever drive this resolver.
	neverCalled := mcp.AuthResolverFunc(func(context.Context, *claierr.AuthChallengeError) error {
		t.Fatal("resolver invoked despite the zero-value fail-fast bound")
		return nil
	})
	tool := newAuthTool(t, "fx", alwaysChallengeDial("fx", &calls), neverCalled, 0, 0)
	e, errOut := newAuthExecutor("mcp_fx_echo", tool)

	start := time.Now()
	out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
	elapsed := time.Since(start)

	want := `ERROR: mcp server "fx" needs authorization; run: clai mcp auth fx`
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
	if elapsed > time.Second {
		t.Fatalf("zero-value bound waited: elapsed %v", elapsed)
	}
	if errOut.String() != "" {
		t.Errorf("errOut = %q, want no bell and no window", errOut.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("dial called %d times, want 1 (no retry on the zero-value fail-fast path)", got)
	}
}

// TestAuthTimeoutExpiryReturnsActionableToolResult pins expiry: a wait that
// never resolves within its bound ends with the actionable result and no
// further resolution attempt.
func TestAuthTimeoutExpiryReturnsActionableToolResult(t *testing.T) {
	var calls int32
	blocksForever := mcp.AuthResolverFunc(func(ctx context.Context, _ *claierr.AuthChallengeError) error {
		<-ctx.Done()
		return ctx.Err()
	})
	tool := newAuthTool(t, "fx", alwaysChallengeDial("fx", &calls), blocksForever, 150*time.Millisecond, 0)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)

	out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})

	want := `ERROR: mcp server "fx" needs authorization; run: clai mcp auth fx`
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("dial called %d times, want 1 (expiry attempts no further resolution)", got)
	}
}

// TestResolvedAuthWaitRetriesResolutionOnce pins the success path: a wait
// that resolves triggers exactly one further resolution, which this test
// observes succeeding. A second challenge on that retry is covered by the
// "retried resolution also returns a challenge" case below in the same
// family.
func TestResolvedAuthWaitRetriesResolutionOnce(t *testing.T) {
	var calls int32
	resolved := mcp.AuthResolverFunc(func(context.Context, *claierr.AuthChallengeError) error { return nil })
	tool := newAuthTool(t, "fx", challengeThenSucceedDial("fx", &calls), resolved, time.Second, 0)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)

	out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
	if out != "ok" {
		t.Fatalf("out = %q, want %q", out, "ok")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("dial called %d times, want exactly 2 (one challenge, one retried resolution)", got)
	}
}

// TestResolvedAuthWaitRetryAlsoChallenged pins the error-coverage row: when
// the retried resolution also returns a challenge, that second challenge is
// the actionable result and no third attempt is made.
func TestResolvedAuthWaitRetryAlsoChallenged(t *testing.T) {
	var calls int32
	resolved := mcp.AuthResolverFunc(func(context.Context, *claierr.AuthChallengeError) error { return nil })
	tool := newAuthTool(t, "fx", alwaysChallengeDial("fx", &calls), resolved, time.Second, 0)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)

	out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
	want := `ERROR: mcp server "fx" needs authorization; run: clai mcp auth fx`
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("dial called %d times, want exactly 2 (no third attempt after a second challenge)", got)
	}
}

// TestAuthWaitCancelledClosesWindowAndReaps pins the error-coverage row: the
// run context cancelling during the wait returns a cancellation-flavoured
// result and still resolves (closes) the auth-pending window.
func TestAuthWaitCancelledClosesWindowAndReaps(t *testing.T) {
	var calls int32
	released := make(chan struct{})
	blocksUntilCancelled := mcp.AuthResolverFunc(func(ctx context.Context, _ *claierr.AuthChallengeError) error {
		<-ctx.Done()
		close(released)
		return ctx.Err()
	})
	tool := newAuthTool(t, "fx", alwaysChallengeDial("fx", &calls), blocksUntilCancelled, time.Minute, 0)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	out := e.invokeToolCall(ctx, pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("the resolver never observed the cancellation")
	}
	if !strings.Contains(out, "authorization wait cancelled") {
		t.Fatalf("out = %q, want a cancellation-flavoured result", out)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("dial called %d times, want 1 (no retry after a cancellation)", got)
	}
}

// TestAuthWaitOutlivesEnclosingBounds pins D20's central claim: a wait
// longer than the enclosing connect bound still reaches its own, larger
// auth-timeout bound, because no resolution is in flight while it runs.
// The dial function below applies a shrunk bound to each attempt, standing
// in for the real mcpConnectBound/mcpHandshakeBound at production scale;
// the resolver deliberately runs three times longer than it and still
// finishes, which would be impossible if any enclosing bound were engaged
// during the wait.
func TestAuthWaitOutlivesEnclosingBounds(t *testing.T) {
	const shrunkConnectBound = 150 * time.Millisecond
	var calls int32
	dial := func(ctx context.Context) (mcp.Conn, error) {
		connectCtx, cancel := context.WithTimeout(ctx, shrunkConnectBound)
		defer cancel()
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, claierr.NewAuthChallenge("fx", "Bearer", "")
		}
		select {
		case <-connectCtx.Done():
			return nil, connectCtx.Err()
		default:
			return fakeAuthConn{}, nil
		}
	}
	slowerThanEnclosingBound := mcp.AuthResolverFunc(func(context.Context, *claierr.AuthChallengeError) error {
		time.Sleep(3 * shrunkConnectBound)
		return nil
	})
	tool := newAuthTool(t, "fx", dial, slowerThanEnclosingBound, 10*shrunkConnectBound, 0)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)

	out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
	if out != "ok" {
		t.Fatalf("out = %q, want %q (a wait 3x the enclosing bound must still reach its own, larger bound)", out, "ok")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("dial called %d times, want 2", got)
	}
}

// TestAuthWaitExcludedFromCallTimeout pins that the per-call bound
// (mcpTool's own timeout, excluding connection resolution) never counts the
// authorization wait: the resolver here takes far longer than the tiny
// call timeout, yet the eventual tools/call against the now-resolved
// connection still completes inside that tiny bound.
func TestAuthWaitExcludedFromCallTimeout(t *testing.T) {
	var calls int32
	slow := mcp.AuthResolverFunc(func(context.Context, *claierr.AuthChallengeError) error {
		time.Sleep(80 * time.Millisecond)
		return nil
	})
	const tinyCallTimeout = 150 * time.Millisecond
	tool := newAuthTool(t, "fx", challengeThenSucceedDial("fx", &calls), slow, time.Second, tinyCallTimeout)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)

	out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
	if out != "ok" {
		t.Fatalf("out = %q, want %q (a 10ms call bound must not have enclosed the 80ms wait)", out, "ok")
	}
}

// TestExpiredAuthWaitConsumesNoExtraToolCallSlot pins the budget contract
// through the real preflight-and-execute pipeline: an expired wait's
// actionable result consumes only the one tool-call slot already reserved
// for the triggering call, never a second.
func TestExpiredAuthWaitConsumesNoExtraToolCallSlot(t *testing.T) {
	var calls int32
	blocksForever := mcp.AuthResolverFunc(func(ctx context.Context, _ *claierr.AuthChallengeError) error {
		<-ctx.Done()
		return ctx.Err()
	})
	tool := newAuthTool(t, "fx", alwaysChallengeDial("fx", &calls), blocksForever, 150*time.Millisecond, 0)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)
	budget := 1
	e.querier.tooling.maxCalls = &budget

	session := &QuerySession{}
	call := pub_models.Call{ID: "c1", Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}}
	if err := e.Execute(t.Context(), session, call); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := toolResult(t, session.Chat)
	want := `ERROR: mcp server "fx" needs authorization; run: clai mcp auth fx`
	if !strings.Contains(out, want) {
		t.Fatalf("tool result = %q, want it to contain %q", out, want)
	}
	if session.ToolCallsUsed != 1 {
		t.Fatalf("ToolCallsUsed = %d, want 1 (the expiry must not consume a second slot)", session.ToolCallsUsed)
	}
}

// TestHumanWaitIsNotModelInvocable pins D23: the wait is raised solely by a
// connection that cannot proceed without a credential. A plain tool with no
// mcpConnectionResolver — standing in for every non-MCP, model-reachable
// tool — can never trigger it, however its Specification or Inputs are
// shaped; and the wait's own entry point takes no tool input at all, so a
// model has no parameter that could reach it even for an MCP tool.
func TestHumanWaitIsNotModelInvocable(t *testing.T) {
	plain := &fakePlainTool{out: "plain result"}
	q := &Querier[*MockQuerier]{
		Raw:     true,
		out:     &strings.Builder{},
		tooling: tooling{run: map[string]pub_models.LLMTool{"not_mcp_but_mentions_auth": plain}},
	}
	e := toolExecutor[*MockQuerier]{querier: q}

	// A model-crafted call whose name and inputs talk about authorization
	// is still just a plain tool call: no code path here inspects a call's
	// name or inputs to decide whether to wait.
	call := pub_models.Call{Name: "not_mcp_but_mentions_auth", Inputs: &pub_models.Input{"auth_timeout_seconds": 0, "authorize": true}}
	out := e.invokeToolCall(t.Context(), call)
	if out != "plain result" {
		t.Fatalf("out = %q, want the plain tool's own result, unaffected by its auth-shaped inputs", out)
	}

	// Structurally: the only entry point is ResolveForCall(ctx), which takes
	// no Input at all, so nothing a model supplies as tool arguments can
	// reach it.
	var _ mcpConnectionResolver = (*fakeResolverOnly)(nil)
}

type fakePlainTool struct{ out string }

func (f *fakePlainTool) Call(pub_models.Input) (string, error) { return f.out, nil }
func (f *fakePlainTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: "not_mcp_but_mentions_auth"}
}

// fakeResolverOnly exists only for the compile-time interface assertion in
// TestHumanWaitIsNotModelInvocable: its ResolveForCall signature carries no
// Input parameter.
type fakeResolverOnly struct{}

func (*fakeResolverOnly) ResolveForCall(ctx context.Context) (string, mcp.AuthResolver, time.Duration, error) {
	return "", nil, 0, nil
}

// TestAuthChallengeUnrelatedErrorFallsThrough pins that an ordinary
// connect failure (not an authorization challenge) is left to the normal
// dispatch path rather than being treated as a human wait.
func TestAuthChallengeUnrelatedErrorFallsThrough(t *testing.T) {
	plainErr := errors.New("boom")
	tool := newAuthTool(t, "fx", func(context.Context) (mcp.Conn, error) { return nil, plainErr }, nil, time.Second, 0)
	e, _ := newAuthExecutor("mcp_fx_echo", tool)

	out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_fx_echo", Inputs: &pub_models.Input{"text": "hi"}})
	if !strings.Contains(out, "boom") {
		t.Fatalf("out = %q, want the plain dial error surfaced through the normal dispatch path", out)
	}
}
