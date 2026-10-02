# Phase 6 — Mid-run auth surfacing

**Status:** Not Started

Back to [README](README.md).

## Goal

Announce, pin and bound a connection that needs a human, so a lazily connected server that asks for
authorization mid-run is actionable rather than a silently stalled tool call.

## Specification

### The problem laziness creates

Before this worklog, every connection happened during setup, so every authorization prompt appeared
in the pre-session window that `internal/text/mcp_startup.go` renders and
`internal/text/mcp_log_sink.go` feeds. After phase 2 a stdio server can connect mid-run, and after
phase 5 an HTTP server can need an interactive flow mid-run. Three concrete obstacles stand in the
way, all of them already present in the code:

- The startup window is one-shot. `mcpStartupWindows` carries a cleared flag and the sink carries an
  attached flag; once setup succeeds the region is retired and later lines queue for the session
  loop instead of rendering as a prompt.
- The handshake bound would kill an authorization wait. A human reading a URL does not finish inside
  the handshake-bound parameter.
- **The connect bound encloses resolution, and it is the outer one.** The lazy-connect phase fixed
  the nesting: the connect bound wraps spawn plus handshake. Any wait performed *inside* resolution
  is therefore enclosed by both the handshake bound and the connect bound, each of whose defaults is
  smaller than the auth-timeout parameter, so such a wait could never reach its own bound. The
  enclosing bound would fire first and the caller would see a connect-stage startup error instead of
  the authorization result it can act on. Decision D20 resolves this by moving the wait out of
  resolution entirely rather than by trying to pause a deadline, which a Go context cannot do.
- The pre-existing single-call bound would expire the triggering tool call and the model would retry.
  That retry does not start a second process, because the lazy-connect phase runs a resolution under
  the run context and records its outcome in a per-run memo, so a retry either joins the resolution
  or finds its recorded failure. What the retry does waste is a budgeted tool call on a wait the
  human has not finished, which is what the bound below exists to prevent.

### The signal, and who acts on it

The auth-pending signal is one small interface, declared beside the connection interfaces and
implemented by the log sink, so neither transport needs to know anything about terminal rendering:

```go
// AuthPendingSink is told that a connection is waiting for a human. The
// returned function is called exactly once when the wait resolves, either way.
type AuthPendingSink interface {
	AuthPending(server string) (done func())
}
```

The connection calls `AuthPending` when it enters a wait and calls the returned `done` when the wait
resolves or expires. The sink owns everything else: opening the window, pinning lines into it,
emitting the bell once, and closing the window in place on `done`. No connection renders anything.

The actionable result the model sees is an ordinary tool-result string, because a tool call returns a
string in this codebase. Its format is fixed here so it is not invented twice, and it reuses the
`ERROR: ` prefix the tool executor already produces for a failed call rather than introducing a new
tool-result type:

```
ERROR: mcp server "linear" needs authorization; run: clai mcp auth linear
```

The exclusion from the single-call bound is mechanical rather than a flag: the wait runs under a
context derived from the run context, and the call's own bound is started only once the wait
resolves. Nothing inspects a context value to decide whether to count.

### Behaviour

The classifiers that already exist are reused, not replaced: `utils.IsMcpLogAuthLine`,
`utils.IsMcpLogAuthPayloadLine` and `utils.IsMcpLogErrorLine` in `internal/utils/print.go` decide
what is a prompt, what is its payload, and what is an error. They are case-insensitive keyword
matches over the line, plus a URL whose path looks like an auth endpoint for a prompt, and a URL or
a short uppercase code for a payload. This phase adds no classifier and changes none of them; read
them before wiring, but do not reimplement them. The sink's existing pinning and
follow-window behaviour is unchanged.

The startup window becomes re-openable. A connection entering an authorization wait opens a window
for its server, renders prompt and payload lines pinned above the bounded tail exactly as during
setup, and closes it when the wait resolves either way. Opening a window while the session loop is
attached suspends the loop's own rendering for the duration, so the two never write the same rows.

An authorization wait raises an auth-pending signal. Both sources raise it: a stdio connection whose
stderr produced a line the classifier calls a prompt, and an HTTP connection that entered the
interactive flow from phase 5. One signal, two producers, so the surfacing code has one path.

On the signal, the terminal bell is emitted through the existing theme gate,
`utils.NotificationBellEnabled`, so a user who disabled the bell is not interrupted. The bell is
emitted once per wait, not once per line.

**The wait happens outside resolution, per D20.** A resolution that cannot proceed without a human
does not block: it fails with the `AuthChallengeError` the transport phase declares, which the
lazy-connect phase does not record as a run-scoped failure. The tool-call site then performs the
wait, bounded by the auth-timeout parameter and by nothing else, because no resolution is in flight
while it waits. When the wait resolves, exactly one further resolution is attempted, and that
attempt gets fresh handshake and connect bounds in the ordinary way.

Nothing is suspended and no deadline is restarted. That matters because both enclosing bounds are
`context.WithTimeout` deadlines, which cannot be paused, so a design that depended on pausing them
would have had no mechanism in the phases that own them.

The wait is raised only by a connection that cannot proceed without a credential. No tool exposes
it and no model request reaches it; that is decision D23, and it is what keeps this from becoming an
interaction channel.

The wait is bounded by the auth-timeout parameter, whose default follows the terminal per D22: a
session whose output is not a terminal fails fast without waiting, reusing the signal
`mcpLogModeFor` already resolves, so a piped or headless run is never interactive by default. On expiry the connection fails with a typed
authorization error, and the triggering tool call returns an actionable tool result naming the server
and the subcommand that authorizes it, so the model degrades and the run continues rather than
hanging. The zero value of that parameter means fail fast with the same actionable result and no
wait at all, which is the posture a headless fleet selects.

The authorization wait is excluded from the pre-existing single-call bound. A human reading a URL
must not make a tool call look hung.

The budgets in `internal/text/stoploss.go` are counts, not durations, and they are reserved before a
call runs, so a wait cannot consume them by elapsing. The falsifiable contract is therefore narrower
and is stated as such: when a wait expires and the call returns the actionable result, that result
consumes no tool-call slot beyond the one already reserved for the triggering call, so an expired
wait can never cost a budgeted call twice.

### Human required

Every test in this phase drives a fake with an injected clock, and no automated test can prove that
a real human, at a real terminal, sees the prompt and completes the flow while a tool call waits.
The authorization phase's human-required step covers a setup-time flow against a vendor endpoint,
which is a different path.

- **Artifact the person produces:** a confirmation that a lazily connected server prompting mid-run
  renders its prompt, rings the bell once, accepts the authorization, and lets the triggering tool
  call complete.
- **Where the agent stops:** after this phase's automated tests pass, the agent stops and asks the
  maintainer to run one query against a server configured lazy whose authorization has expired.
- **Before:** the agent confirms the fakes pass and states which server and which bound values to use.
- **After:** the agent records the outcome here and in the README session journal. No credential is
  recorded.

### Invariants

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| A lazy stdio connection producing a prompt line | Opens a window, pins the prompt | `TestStartupWindowReopensForLazyConnect` |
| The session loop while a window is open | Does not render into the window's rows | `TestSessionLoopDoesNotRenderIntoAuthWindow` |
| An HTTP connection entering the interactive flow | Raises the same auth-pending signal | `TestAuthPendingSignalRaisedByHttpAndStdio` |
| Every authorization wait | Emits the bell once, subject to the theme gate | `TestAuthPromptRingsBellWhenEnabled` |
| Prompt and payload lines | Pinned above the bounded tail | `TestAuthPromptIsPinnedAboveTail` |
| A wait with the auth-timeout parameter at its zero value | Fails immediately with the actionable result | `TestAuthTimeoutZeroFailsFast` |
| A session whose output is not a terminal, with no explicit value | Fails fast without waiting | `TestNonTerminalSessionDefaultsToFailFast` |
| Every path that can raise the wait | Is a connection lacking a credential; no tool and no model request reaches it | `TestHumanWaitIsNotModelInvocable` |
| A wait that exceeds the auth-timeout parameter | Typed error and actionable tool result | `TestAuthTimeoutExpiryReturnsActionableToolResult` |
| The single-call bound | Excludes the authorization wait | `TestAuthWaitExcludedFromCallTimeout` |
| An expired wait's actionable result | Consumes no tool-call slot beyond the one already reserved for the triggering call | `TestExpiredAuthWaitConsumesNoExtraToolCallSlot` |
| A resolution needing a human | Fails with `AuthChallengeError` and is not memoised as a run-scoped failure, so the retry can succeed | `TestAuthChallengeIsNotMemoisedAsRunFailure` |
| A wait longer than the handshake and connect bounds | Reaches its own bound, because no resolution is in flight during it | `TestAuthWaitOutlivesEnclosingBounds` |
| A resolved wait | Triggers exactly one further resolution attempt, with fresh bounds | `TestResolvedAuthWaitRetriesResolutionOnce` |

### Limits

| Limit | Injectable field | README parameter | How a test triggers it |
| --- | --- | --- | --- |
| Human wait | Per-server config field | auth-timeout parameter | Fake connection that returns `AuthChallengeError` and a wait that never resolves, with an injected clock. No enclosing bound is in flight, so none has to be advanced |
| Fail-fast posture | Same field at its zero value | auth-timeout parameter | Same fake with the parameter zeroed |
| Pinned lines per server | Pre-existing sink constant | auth-pinned-line-cap parameter | Fake stdio server emitting more prompt lines than the cap |

The clock is injected and no test sleeps, for the load-sensitivity reason recorded in phase 3. The
bell assertion writes to an injected writer, never to the process standard output, matching the
sink's existing constructor that accepts its own error writer.

### Documentation

`architecture/mcp.md` gains the authorization-surfacing section: the signal, the window lifecycle,
the bell gate, the bound and its zero value, and the exclusion from call and budget accounting. The
architecture note covering streaming output gains a pointer if it describes the pre-session window.

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Lazy stdio connection whose stderr emits a prompt and a URL, mid-run | Fake stdio server, injected error writer, injected clock | Prompt and URL rendered pinned | Bell emitted once, window opened | Session loop does not overwrite the window; bell not repeated per line |
| Same, with the bell disabled in the theme | Same fakes, theme gate off | Prompt rendered | Window opened | No bell byte written |
| HTTP connection entering the interactive flow mid-run | Fake resource and authorization servers | Prompt rendered through the same path | Auth-pending signal raised once | No second surfacing path |
| Wait resolved by the human in time | Fake that resolves the wait | Tool call completes | Window closed in place; exactly one further resolution attempted | Window not left drawn; no third attempt |
| Wait exceeding the bound | Fake that never resolves, clock advanced past auth-timeout | Tool result names the server and the authorizing subcommand | Typed error returned, window closed; the error is the authorization one, not a connect-stage one | Run not aborted, no second spawn |
| Wait with the bound at its zero value | Same fake | Tool result returned immediately | No wait, no bell | No window opened |
| Wait expiring while one tool-call slot remains | Budget fixture with one slot left | Actionable result returned and the run continues | Only the already-reserved slot consumed | No second slot consumed by the expiry |
| More prompt lines than the pinned cap | Fake emitting many prompt lines | Most recent prompt lines retained | Cap respected | Oldest payload not retained over newest prompt |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| The startup window reopens for a mid-run connection | `TestStartupWindowReopensForLazyConnect` |
| The bell is emitted once per wait, subject to the theme gate | `TestAuthPromptRingsBellWhenEnabled` |
| Prompt and payload lines pin above the tail | `TestAuthPromptIsPinnedAboveTail` |
| The zero value of the bound fails fast | `TestAuthTimeoutZeroFailsFast` |
| A non-terminal session fails fast by default | `TestNonTerminalSessionDefaultsToFailFast` |
| No tool or model request can raise the wait | `TestHumanWaitIsNotModelInvocable` |
| Expiry yields an actionable tool result | `TestAuthTimeoutExpiryReturnsActionableToolResult` |
| The wait is excluded from the call bound | `TestAuthWaitExcludedFromCallTimeout` |
| An expired wait's result consumes no extra budget slot | `TestExpiredAuthWaitConsumesNoExtraToolCallSlot` |
| A wait longer than either enclosing bound still reaches its own bound | `TestAuthWaitOutlivesEnclosingBounds` |
| An authorization challenge does not poison the run's memo | `TestAuthChallengeIsNotMemoisedAsRunFailure` |
| A completed authorization leads to one retried resolution that succeeds | `TestResolvedAuthWaitRetriesResolutionOnce` |
| Both transports raise one signal | `TestAuthPendingSignalRaisedByHttpAndStdio` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Wait exceeds the bound | Typed authorization error plus an actionable tool result | `TestAuthTimeoutExpiryReturnsActionableToolResult` |
| Bound at its zero value on a headless run | Immediate actionable tool result, no wait | `TestAuthTimeoutZeroFailsFast` |
| Run context cancelled during a wait | Typed cancellation error, window closed, process reaped | `TestAuthWaitCancelledClosesWindowAndReaps` |
| The retried resolution also returns a challenge | Second challenge is the actionable result; no third attempt | `TestResolvedAuthWaitRetriesResolutionOnce` |
| Window writer fails mid-render | Render abandoned without panicking; the wait continues | `TestAuthWindowWriteFailureDoesNotPanic` |
| Terminal dimensions unavailable | Deterministic fallback width used, as the sink already does | `TestAuthWindowUsesFallbackWidth` |
| Signal raised twice for one wait | Bell emitted once, one window | `TestDuplicateAuthSignalEmitsOneBell` |
| Output is not a terminal | No window drawn; the prompt is surfaced as an elevated error line, matching the sink's non-rolling modes | `TestAuthPromptElevatesWhenOutputIsNotTerminal` |

## Implementation notes

Not started.

## Review findings

None.
