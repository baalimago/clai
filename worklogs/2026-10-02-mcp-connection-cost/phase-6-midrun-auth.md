# Phase 6 — Mid-run auth surfacing

**Status:** In Progress — human gate outstanding. Every finding that reopened the phase across
review 1, review 2 and the holistic sign-off review (B2's mid-run half) is fixed and verified
(2026-10-03 fix sessions); the automated suite is green. R1-26 (dead `WithAuthPendingSink`) stays
open, owned by phase 1, not this phase: see its own entry below. The phase cannot be marked Complete
because the human-required real mid-run authorization observation is still outstanding (see
Implementation notes and the Human required section) — that gate is not these fix sessions' to
satisfy.

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

**Before, recorded by this execution (2026-10-02):** the full automated suite passes (`go test ./...
-race -cover -count=3 -timeout=30s`, unedited, confirmed on a quiet host — see Implementation notes).
This session stops here and hands off the following concrete setup, rather than a generic request:

- **Server:** any one endpoint-based vendor already confirmed reachable and OAuth-capable by this
  worklog's own measurements (Strategy, "Vendor endpoint probes") — Linear, Notion or Intercom are
  the three with dynamic registration, `S256` PKCE and a refresh grant all confirmed. Configure it
  as a `url`-based server with `"startup": "lazy"` and no `auth.token_command`/`auth.token_env`, so
  the only credential source is the interactive flow plus the token store.
- **Forcing a mid-run prompt from "authorization has expired":** run `clai mcp auth <server>` once
  first (phase 5's own command) so a token store entry exists, then either wait for its natural
  expiry or edit `<clai-config-dir>/mcpAuth/<server>.json` to set `expires_at` into the past (never
  edit `access_token`/`refresh_token` themselves away from what the vendor issued — corrupting the
  *expiry* is enough to force a refresh attempt; if the vendor rejects the now-stale refresh token
  too, that itself drives the interactive fallback this phase adds). Then issue one ordinary query
  that calls a tool from that server.
- **Bound:** leave `auth_timeout_seconds` unset at a real terminal, so the D22 default (120 s)
  applies — long enough to click through a real OAuth consent screen once.
- **What to watch for, matching the artifact above:** the prompt (and its URL) render pinned in a
  reopened per-server window exactly as at setup time; the terminal bell rings once, not per line;
  completing the browser flow lets the triggering tool call finish with its real result, with no
  second prompt and no hang.

**After:** not yet performed. This execution stopped at the point above; the maintainer's
confirmation (or a reported divergence) is still outstanding. No credential was generated, inspected
or recorded by this session beyond what the automated suite's own fakes exercise.

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

**Added by the sign-off review (B2, 2026-10-03).** Every row above bounds a wait; none bounds what
the wait is allowed to *do*, which is how the mid-run path kept the browser hand-off the setup path
had already been stripped of:

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| The mid-run `AuthResolver` on a run whose output is not a terminal | Refuses with a typed error naming the subcommand to run from a terminal; no browser opened, no loopback listener bound, no request issued | `TestMidRunChallengeOnNonInteractiveRunNeverOpensBrowser` |
| The actionable tool result on such a run | Still the endpoint-based one naming `clai mcp auth <server>`, never the command-based wording | Same test's non-nil resolver assertion |

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
| Output is not a terminal and a mid-run challenge arrives anyway (an explicit `auth_timeout_seconds` on a headless run) | `*mcpauth.InteractiveAuthDisabledError`; nothing leaves the process | `TestMidRunChallengeOnNonInteractiveRunNeverOpensBrowser` |

## Implementation notes

2026-10-02, phase 6 execution (this session). Automated suite implemented and green;
the phase's own `Human required` gate is outstanding and is where this session stops
— see that subsection for the handoff. Status is left `In Progress`, not `Complete`.

**`architecture/` untouched, by direction.** This session was told the coordinating
session owns all architecture documentation for this worklog and must not touch it.
No file under `architecture/` was created or edited. The phase's own Documentation
section ("`architecture/mcp.md` gains the authorization-surfacing section...") is
therefore outstanding from this session's point of view, exactly as phase 1's and
phase 5's own sessions recorded for their equivalent sections.

**The signal's two producers are not symmetric in where they call `AuthPendingSink`,
and that is deliberate.** The code-layout table assigns "the call sites that raise
the auth-pending signal" to both `conn_stdio.go` and `conn_http.go`. `conn_http.go`
needed no new call site: `HttpConn.doPost`'s existing 401 handling already returns
`*claierr.AuthChallengeError` unchanged by this phase, and that typed error is itself
the uniform signal both transports now produce. `conn_stdio.go` is the one transport
that needed new code, because stdio has no native "challenge" concept: a lazily
resolved stdio server blocked on a human just looks like an ordinary connect-bound
timeout unless something notices the stderr prompt first. The single surfacing path
— raising `AuthPendingSink.AuthPending`, bounding the wait, retrying once — lives
entirely in `internal/text/tool_executor.go`, driven uniformly off the
`*claierr.AuthChallengeError` type for both transports; neither `conn_stdio.go` nor
`conn_http.go` calls the sink directly. This reading satisfies "one signal, two
producers, one path" literally at the type level rather than duplicating the
sink-calling logic into both transports, which would have risked a double bell (one
from the connection noticing the condition, one from the tool-call site's wait) with
no clean way to deduplicate across two different call sites holding two different
`done` functions for what is really one wait.

**`AuthChallengeError` is the one signal type for both transports**, rather than a
new phase-6 error type. `pkg/claierr/claierr.go` gains one method,
`(*AuthChallengeError) BlockedOutsideRun() bool { return true }`, which satisfies
`internal/tools/mcp/connector.go`'s existing `outsideRunBlocker` interface (shipped
unmodified since phase 2) by structural typing — zero changes to `connector.go`'s
memoisation logic were needed. `internal/tools/mcp/conn_stdio.go` reclassifies a
connect-stage failure into this same type when its stderr reader classified a line
as an auth prompt during that attempt (`utils.IsMcpLogAuthLine`, reused unchanged);
`internal/tools/mcp/connector.go`'s `dialStdio` is where that reclassification is
applied, via a new `reclassifyIfAuthChallenged` helper and an `AuthChallenged() bool`
optional-interface method on `*StdioConn`.

**A false-positive keyword match is bounded to the handshake's own latency, not left
open for the life of a successful connection.** `StdioConn.raiseAuthPending` opens the
sink's window the first time an auth-looking line appears; `deliver()` — the point
where *any* definitive response (success or JSON-RPC error) reaches its waiter —
unconditionally resolves it too, not only `Close()`. Without this, a benign stderr
line that merely contains a matched keyword (e.g. a status message mentioning
"unauthorized") would open an `AuthPendingSink` window that then never closes for a
server that goes on to connect normally and stay open for the whole run, which would
also permanently suspend `mcpLogSink.Drain()` (see below). `raiseAuthPending` is
additionally a no-op with no sink configured (`authSink == nil`): with nobody to
notify and no remedy displayed, reclassifying the failure would silently change its
error type for no observable benefit, which broke the pre-existing
`TestConnectBoundExpiryReturnsTypedStartupError` (sink `nil`) before this guard was
added — fixed, not worked around.

**The wait's success/expiry split, since the phase file states "exactly one further
resolution" only for the resolved case.** `resolveMcpAuthWait` in `tool_executor.go`
retries exactly once only when the wait itself completed with no error (an
`AuthResolver` returning `nil`, or — for a command-based server with no resolver at
all — simply waiting the bound out, since the only lever is time and running out the
clock is the intended outcome, not a failure). A wait that explicitly expires
(`context.DeadlineExceeded` from an `AuthResolver`'s own bounded attempt) goes
straight to the actionable result with **no** retry. This is this session's own
resolution of a point the phase text leaves implicit — "a wait that exceeds the
auth-timeout parameter" (the expiry acceptance row) is never paired with a retry
anywhere in the phase's prose — flagged here in case a future reader expected
expiry-then-retry too; the alternative reading made `TestAuthTimeoutExpiryReturnsActionableToolResult`'s
"no further resolution attempt" assertion and `TestResolvedAuthWaitRetriesResolutionOnce`'s
"exactly one" assertion mutually exclusive, so one of the two readings had to go.

**Where the wait lives relative to `tools.InvokeWith`.** `tools.InvokeWith`
(`internal/tools`) folds every tool error into a plain `"ERROR: ..."` string before
`tool_executor.go` ever sees it, which would destroy the typed
`*claierr.AuthChallengeError` the wait needs to recognise. Rather than changing that
shared, vendor-agnostic function's contract, `internal/tools/mcp/tool.go` exports a
new method on the existing (unexported) `*mcpTool`, `ResolveForCall(ctx) (serverName
string, resolver mcp.AuthResolver, authTimeout time.Duration, err error)`, which drives
the same `Connector.Conn` a real `Call` would. `tool_executor.go`'s new
`invokeToolCall` checks every per-run tool for this method (structural interface
`mcpConnectionResolver`) before calling `tools.InvokeWith`, pre-resolving the
connection outside the generic string-folding path; on success or an unrelated error
it falls through to the normal dispatch unchanged, so `tools.InvokeWith`'s own
resolution of the now-memoised `Connector` is a free replay, never a second real
attempt. `NewTool` and `RegisterTools` (`internal/tools/mcp`) each gained two new
trailing parameters, `authResolver AuthResolver` and `authTimeout time.Duration`,
carried onto every tool a given `RegisterTools` call registers; the one eager call
site (`manager.go`'s `handleServer`) passes `nil, 0`, since an eager server's
connection is already resolved at setup and a later mid-run challenge on that same
connection (e.g. token revocation) is out of this phase's "lazy, mid-run" scope.

**`Configurations.OutputIsTerminal`, not a `setupMcpManager` signature change.**
D22's default needs to know whether the session's output is a terminal, which
`querier_setup.go` already computes before calling `setupTooling`. Rather than
threading a new parameter through `setupTooling` → `setupMcpManager` →
`resolveLazyServerViaCache`/`resolveLazyHttpServerViaCache` (which would have forced
updating roughly twenty-five existing test call sites that construct a bare
`Configurations{}`), `Configurations` gained one field, `OutputIsTerminal bool
json:"-"`, set once in `querier_setup.go` right before `setupTooling` runs. Every
pre-existing test keeps building a zero-valued `Configurations{}` and gets the
correct conservative default (fail-fast) without being touched.

**The actionable tool-result string is exactly the phase's own example**, reused
verbatim as a format string:
`` `ERROR: mcp server %q needs authorization; run: clai mcp auth %s` ``. It is the
whole answer for both the zero-value fail-fast path and an expired wait, and is also
what a second challenge on the one retried resolution degrades to.

**Supplementary tests**, beyond the phase's nineteen declared names, added where a
cheap one closed a real gap in proving the production wiring rather than only the
generic mechanism (the same convention phase 5's own session recorded):
`TestStdioConnectReclassifiesAuthPromptAsChallenge` (`internal/tools/mcp/conn_stdio_test.go`)
drives a real spawned process (the pre-existing `TEST_SERVER_AUTH_HANG` fixture) through
`NewConnector`/`dialStdio` end to end; `TestLazyHttpMidRunChallengeRetriesAfterInteractiveAuth`
(`internal/text/mcp_oauth_setup_test.go`) drives the real `newAuthenticatingHttpConnector`
against the fake HTTP and OAuth servers end to end, including the token-store hit that
makes the retry succeed with no decorator threaded back in by hand;
`TestResolvedAuthWaitRetryAlsoChallenged` and `TestAuthChallengeUnrelatedErrorFallsThrough`
round out the declared family's branches; `TestAuthWindowRespectsPinnedLineCap` executes
the Limits table's third row (the pre-existing auth-pinned-line-cap parameter) for the
reopened-window path specifically; `TestAuthTimeoutSecondsDistinguishesUnsetFromExplicitZero`
(`pkg/text/models/tools_test.go`) and `Test_Claierr_AuthChallengeIsBlockedOutsideRun`
(`pkg/claierr/claierr_test.go`) pin the two small new public surfaces directly.

**Verification.** `gofumpt -l .` clean; `go vet ./...` clean; `staticcheck ./...`
clean; `go fix ./...` ran clean (it rewrote an unrelated pre-existing-style test
helper in a file this phase added, `internal/text/mcp_oauth_test.go`, into Go 1.26's
`new(value)` form; the now-unused helper was deleted rather than left for staticcheck
to flag). `dupl -t 80 .` reports the same thirty-five pre-existing clone groups the
repository already carries; the one touching this phase's files
(`internal/tools/mcp/conn_http.go` vs `conn_stdio.go`, both `Close()`) is the
duplication phase 4's own implementation notes already named and accepted as
out of scope to extract, slightly extended by this phase's one added line
(`c.resolveAuthPending()`) rather than newly introduced. `go test ./... -race -cover
-count=3 -timeout=30s` passed unedited on a quiet host (load ~4, confirmed via
`uptime` before each run), twice in a row after the first run's one incidental
finding: `Test_e2e_skills_descriptor_activation_and_persistence` (root package,
untouched by this phase, a skills-trust/cost-estimate e2e test) failed once under
the full suite's self-induced load and then passed three times in isolation
(`-race -count=3`), matching the repository's documented host-load-sensitivity class
from phases 1–4 rather than a regression. Coverage on this phase's primary packages:
`internal/text` 84.1%, `internal/tools/mcp` 81.0%, `pkg/claierr` 77.5%,
`pkg/text/models` 85.3%.

### 2026-10-03, fix session (review 1 and review 2 findings)

Agent session fixing every finding that reopened this phase: R1-01, R1-06, R1-07, R1-08, R1-21,
R1-30 (review 1) and R2-01, R2-09, R2-26 (review 2). R1-26 is owned by phase 1 and is not this
session's to fix; left open. The Human-required gate (a maintainer observing a real mid-run
authorization) was not attempted by this session and remains outstanding — see that subsection,
unchanged.

**R1-01, the dominant finding.** Fixed all three corrective-action mechanisms together, not just
the keyword-list tightening the finding warned was insufficient alone: (a) `resolveAuthPending`
(`internal/tools/mcp/conn_stdio.go`) now clears `authChallenged` under the same lock it already
clears `authDone` in, called from both `deliver()` and `Close()`; (b) `connector.resolve`
(`internal/tools/mcp/connector.go`) gained a `blockedAttempts` counter and a new
`mcpBlockedRedialCap = 2`, so a blocked-outside-run outcome (D20's exemption) is memoised as a
terminal failure once the cap is spent, bounding the whole run to one dial plus one re-dial per
server (D36) instead of one pair per call; (c) `internal/utils/print.go` now has two keyword lists:
`mcpLogAuthChallengeKeywords` (the imperative/named-failure subset, no bare `401`/`403`) backs a
new `IsMcpLogAuthChallengeLine`, while the existing `mcpLogAuthKeywords`/`IsMcpLogAuthLine` stays
as-is for the sink's pre-emptive window (a UI-only false positive remains harmless and
self-resolving via (a)). `conn_stdio.go`'s `raiseAuthPending` takes a new `confirmed bool`: the
loose classifier still opens the window, the strict one is what sets `authChallenged`, so a bare
`401`/`403` substring can no longer reclassify a connect failure's error type.

**R1-06/D37, the mechanism behind R1-01's second spawn.** The no-resolver (stdio) branch of
`resolveMcpAuthWait` (`internal/text/tool_executor.go`) used to leave `waitErr` nil after
`<-waitCtx.Done()`, so the existing `DeadlineExceeded` check never caught it and the retry ran
unconditionally. It now sets `waitErr = waitCtx.Err()`, which is `DeadlineExceeded` precisely when
the wait's own bound fired (the `ctx.Err() != nil` check above it already handles the cancellation
case first), so the no-resolver branch now ends like any other expiry: no retry, on both
transports, per D37's own ruling as recorded in the README.

**R1-08, transport-aware advice.** `actionableAuthResult` (`internal/text/tool_executor.go`) now
takes `hasResolver bool`. `authResolver == nil` is a reliable, already-documented proxy for
"command-based server" (`internal/tools/mcp/tool.go`'s own field comment: "nil for a command-based
server, where the only lever is time"; confirmed by tracing every `RegisterTools` call site — only
the lazy stdio path ever passes `nil`). The no-resolver message points at the server's own stderr
output instead of `clai mcp auth`, which `cmd.go`'s `McpAuthNotEndpointBasedError` would otherwise
refuse.

**R1-07, scoped suspension.** `mcpLogSink.Drain` (`internal/text/mcp_log_sink.go`) took the
finding's first corrective option: it now drains every entry whose own server has no open window
immediately, deferring only entries for a server that does. In practice a reopened server's own
lines never reach the queue at all (they render straight into the pinned window via the existing
`isReopened` branch), so the deferred bucket is normally empty — the fix is really about not
treating one server's open window as a reason to hold every *other* server's queued lines hostage.

**R1-21/R2-09, folded into one fix.** These two findings describe the same field from two ends
(no test on the assignment side; no reachable input on the SDK side), so the fix addresses both at
once: `Configurations.OutputIsTerminal` (`internal/text/conf.go`) is now `*bool` with an
`OutputIsTerminalOrDefault()` helper; `querier_setup.go` assigns only when nil; `pkg/agent` gained
`WithOutputIsTerminal`. R1-21's "two different terminal checks" half turned out to already be
closed, verified by grep, as a side effect of phase 5's own R2-08 fix session (`newMcpAuthorizer`
now takes the resolved bool directly; no second `os.Stdout` check remains in the D22 path —
`clai mcp auth`'s own unconditional `WithInteractive(true)` is a separate, deliberate posture for
that explicit subcommand, not part of this decision).

**R2-01, scoped to this phase's own test only.** `internal/tools/mcp/conn_stdio_test.go` gained its
own `testServerBinary` helper (the same pattern `internal/text/querier_setup_tools_test.go` already
uses for D41), and the one test this finding names now points at the prebuilt binary. This fixes
R2-01's reproduction exactly (verified cold via a throwaway `GOCACHE`) but leaves every other
`go run ./testserver`/`../tools/mcp/testserver` call site in the repository as it was: those belong
to phases 1–5's own test files and to phase 8's gate-sweep (R2-19), not to this fix session.

**R1-30/R2-26, one fix for both.** Widened every tight margin in
`internal/text/tool_executor_auth_test.go` (roughly 5-15x: 10/20/30/50/80 ms moved to 150 ms) rather
than introducing a clock-injection seam, per the finding's own stated second option. No assertion's
logic changed.

**Supplementary test**, beyond the declared names, closing the corrective action's own explicit
request: `TestAuthChallengeStdioSpawnBoundedAcrossRepeatedToolCallsEndToEnd`
(`internal/text/querier_setup_tools_test.go`) drives `toolExecutor.invokeToolCall` three times
against a real spawned `TEST_SERVER_AUTH_HANG` process and a real `TEST_SERVER_SPAWN_LOG` counter
(not a hand-built dial closure), asserting exactly 2 real process births across 3 tool calls.
`TestNewQuerierThreadsOutputIsTerminalIntoAuthTimeout` (`internal/text/querier_setup_test.go`)
drives the real `NewQuerier` composition root with a pre-warmed schema cache, proving both
R1-21 and R2-09's fix without a live HTTP server. `TestAgent_WithOutputIsTerminal_propagates`
(`pkg/agent/agent_test.go`) pins the new option's own wiring into `asInternalConfig`.

**Test changes to existing declared names.** `TestAuthPendingSignalRaisedByHttpAndStdio`'s
stdio-like subtest now asserts the corrected (D37, R1-08) outcome — the actionable, transport-aware
result and exactly one dial — instead of the old retry-and-succeed behaviour the pre-fix code
happened to produce. `TestAuthTimeoutZeroFailsFast` now supplies a resolver that fails the test if
ever invoked, rather than `nil`, so it keeps asserting the `clai mcp auth` message (an
endpoint-based posture) while adding its own proof that the zero-value bound never drives the
resolver. `TestSessionLoopDoesNotRenderIntoAuthWindow` rewritten for the scoped Drain: an unrelated
server's line drains immediately while another server's window is open, and that other server's
own line renders into its reopened window rather than queuing.

**Verification.** `gofumpt -l .` clean after one run (`internal/tools/mcp/connector.go`'s new
struct field needed realignment, applied with `-w`). `go vet ./...` clean. `staticcheck ./...`
clean. `go fix ./...` produced no changes. `dupl -t 80 .` reports one new clone group beyond the
repository's existing baseline: `internal/text/querier_setup_tools_test.go`'s pre-existing
`testServerBinary` helper and this session's new, near-identical copy in
`internal/tools/mcp/conn_stdio_test.go`. Accepted rather than extracted into a shared package: it
is 20 lines of test-only fixture-build boilerplate, reused exactly once more, in a different test
binary (different package) than the original — the same class of accepted test-helper duplication
phase 8's own gate-sweep session already recorded in writing for this worklog. `go test ./...
-race -cover -count=3 -timeout=30s` passed unedited on a quiet host (load ~2-3, confirmed via
`uptime` before the run). Coverage on this phase's primary packages after the fix:
`internal/text` 85.3%, `internal/tools/mcp` 81.2%, `pkg/claierr` 77.0%, `pkg/text/models` 85.3%,
`pkg/agent` 94.2%.

**Architecture note made stale, read but not edited (the coordinating session owns `architecture/`).**
`architecture/mcp.md`'s "When a human is needed mid-run" section, two sentences:

- "Second, an authorization failure is not a run-scoped failure... otherwise the server would stay
  dead for the rest of the run even after the person had just authorized it." This now holds only
  up to D36's cap: after one re-dial, a server that keeps challenging *is* memoised as a run-scoped
  failure (R1-01's fix), which the current wording's "does not retry it" reads as unconditional.
- "On expiry the tool call returns an actionable result naming the server and the command that
  authorizes it." True only for an endpoint-based server now (R1-08): a command-based server's
  actionable result names its own stderr output instead, since `clai mcp auth` refuses it.

### Sign-off fix session, 2026-10-03 (worklog-work, B2's mid-run half)

The holistic sign-off review's B2 is filed mainly against phase 5, but one of its six items is this
phase's wiring and one of its recorded gaps is this phase meeting phase 5 at only one point. Full
text in the README's Sign-off verdict section and the Sign-off review feedback-index entry.

**`httpChallengeResolver` was not gated on `Interactive`.** The setup path
(`handshakeHttpServerWithAuth`) was fixed for exactly this defect in the review-2 round, as R2-08.
The mid-run path — `httpChallengeResolver` in `internal/text/mcp_oauth.go`, the `mcp.AuthResolver` a
lazily resolved endpoint-based tool carries — was not, and nothing connected the two, because each
review round looked at one phase at a time. A headless `pkg/agent` consumer that merely set
`auth_timeout_seconds` therefore opened a browser and bound a loopback listener inside its own
process on the first challenged tool call.

**Fixed at the root, in phase 5's `AuthorizeInteractive`,** which now refuses with
`*mcpauth.InteractiveAuthDisabledError` before it reads the challenge, so this call site and every
future one inherits the gate rather than each needing its own copy of it — the shape of defect that
produced this finding in the first place.

**Deliberately *not* fixed by returning a nil resolver from `httpChallengeResolver`.**
`resolveMcpAuthWait` reads `authResolver != nil` to choose between the endpoint-based actionable
result (`run: clai mcp auth <server>`) and the command-based wording about a server prompting on its
own stderr. A nil resolver here would have made that message wrong for an endpoint-based server and
would also have burned the whole `auth_timeout` in the no-resolver `<-waitCtx.Done()` branch for a
wait with nothing to wait for. The resolver stays non-nil and refuses instantly instead; the test
asserts both halves.

**Test, red before green:** `TestMidRunChallengeOnNonInteractiveRunNeverOpensBrowser`
(`internal/text/mcp_oauth_setup_test.go`) drives the real `httpChallengeResolver` against the real
composed fixture chain with a counting browser opener and a non-interactive `Authorizer`. Against
the unfixed code the equivalent probe does not fail, it hangs: it ran out the full 60 s test bound
inside `awaitRedirect`, with the goroutine stack showing a loopback listener accepting on a port
nothing would ever connect to. The test asserts the typed refusal, zero browser opens, zero
authorization-server requests, and a non-nil resolver.

**Recorded, not closed: mid-run token expiry never re-authorizes.** The review's own words: phase 5
built refresh, this phase built surfacing, and the two meet only at connect time. Verified against
source this session — `bearerDecorator` captures a token *string* and is installed once on the
`HttpConn`, and `resolveMcpAuthWait` inspects only `resolver.ResolveForCall`, a connector
resolution, never a call result. A 401 on a later `tools/call` therefore becomes an auth-challenge
error that `tools.InvokeWith` folds into a string, and the model is told authorization is required
while a valid refresh token sits unused. Closing it needs a refreshable decorator seam on `HttpConn`
(phase 4's surface), a typed call-result path the executor can classify (this phase's), and a
re-entry rule for a connection already handed out: a phase of its own, not a line in a security fix.
It is recorded in the README's cross-phase gap record and belongs in `architecture/mcp.md`'s "Not
yet implemented" list, which the coordinating session owns.

## Review findings

### Review 1, 2026-10-02 — implementation review

**Status: Fixed, 2026-10-03 fix session.** Every finding below (R1-01, R1-06, R1-07, R1-08, R1-21,
R1-30) is checked off and verified; see each finding's own fixed-note and the phase's
Implementation-notes delta. R1-26 is owned by phase 1 and stays open there. The human gate is
legitimately outstanding and is not a defect.
What is a defect is that this phase's surfacing mechanism, composed with phase 2's deliberate
non-memoisation of a blocked resolution, produces exactly the spawn storm phase 2's outcome
sentence promises cannot happen — reached through a substring keyword match on stderr.

**Verified good:**

- The signal is raised once per wait and deduplicated per server: `mcpLogSink.AuthPending`
  (`internal/text/mcp_log_sink.go:290-310`) returns the same `sync.Once`-guarded close function for
  an already-open server, so no second bell and no second window. The bell goes to the sink's own
  injectable writer, never the process standard output.
- `AuthChallengeError.BlockedOutsideRun` satisfies `connector.go`'s pre-existing
  `outsideRunBlocker` by structural typing with zero changes to the memoisation logic, and
  `TestAuthChallengeIsNotMemoisedAsRunFailure` drives the real typed error rather than a stand-in.
- The wait really does sit outside resolution (D20): `resolveMcpAuthWait` runs with no connector
  resolution in flight, and `TestAuthWaitOutlivesEnclosingBounds` and
  `TestAuthWaitExcludedFromCallTimeout` both demonstrate a wait outliving the enclosing bounds.
- Budget accounting holds. `TestExpiredAuthWaitConsumesNoExtraToolCallSlot` drives the real
  `Execute` pipeline and asserts `ToolCallsUsed == 1`; traced through `runPlannedCall`, there is
  exactly one `recordToolCall` per planned call regardless of the wait's outcome.
- The one-signal-two-producers asymmetry is honestly declared in the implementation notes and is a
  defensible reading of the code-layout row; I am not filing against it.
- `TestStdioConnectReclassifiesAuthPromptAsChallenge` (`internal/tools/mcp/conn_stdio_test.go:621`)
  genuinely drives a spawned process through `NewConnector`/`dialStdio`; it holds at `-count=5
  -race`.

**Findings**

- [x] **R1-01** (blocker) — **every tool call to a stdio server whose stderr once matched the auth
  keyword list costs two process spawns and a full auth-timeout block, repeated per call for the
  rest of the run.** Mechanism, traced end to end:
  1. `StdioConn.readStderr` calls `raiseAuthPending` on any line for which
     `utils.IsMcpLogAuthLine` is true (`internal/tools/mcp/conn_stdio.go:429-431`). That classifier
     is case-insensitive **substring** matching and its list includes `"401"`, `"403"`,
     `"unauthorized"` and `"forbidden"` (`internal/utils/print.go:818-830`), so a benign line such
     as `listening on port 4010` matches.
  2. `authChallenged` is set and **never cleared** (`conn_stdio.go:450-463`, `:484-488`). The
     implementation notes claim the false positive is "bounded to the handshake's own latency"
     because `deliver()` resolves the window — but `deliver` resolves the *sink window* only; it
     does not touch `authChallenged`. So a handshake that gets as far as an `initialize` reply and
     then fails on `tools/list` is still flagged.
  3. `reclassifyIfAuthChallenged` (`internal/tools/mcp/connector.go:215-224`) therefore converts
     an ordinary connect-stage failure into `*claierr.AuthChallengeError`.
  4. `BlockedOutsideRun` makes `connector.resolve` skip memoisation
     (`connector.go:106-109`), so the next call dials again.
  5. `resolveMcpAuthWait` (`internal/text/tool_executor.go`) dials once via `ResolveForCall`,
     waits out the whole bound, then dials a second time.
  **Verified by probe** against the production `invokeToolCall` with a dial that always returns a
  challenge and a nil resolver (the stdio posture): *3 tool calls → 6 dials, elapsed = 3 × the
  bound, 3 bells.* At the production default of 120 s on a terminal that is 6 process spawns and
  6 minutes of a blocked session for a three-call batch, and it scales linearly with the number of
  calls the model makes. This breaks phase 2's invariant "A failed resolution | Marked for the run,
  not retried, not persisted", its integration row "Prohibited side effects: **No repeated spawn
  attempt**", the retry-count parameter ("0 retries, connect attempts per server per run after a
  failure"), and `architecture/mcp.md:252-255`, which states in writing that "a model repeatedly
  calling a broken server cannot cause a spawn storm".
  Corrective action — all three are wanted, in this order: (a) clear `authChallenged` in
  `deliver()` as well as resolving the window, so a connection that got a definitive answer is no
  longer flagged; (b) give the connector a per-run cap on blocked-outcome re-dials (one re-dial per
  run, not per call) so a non-memoised outcome is still bounded per run; (c) tighten the
  reclassification trigger so a bare numeric `401`/`403` substring on a *stdio* server does not by
  itself qualify — the imperative prompts in the keyword list are the signal that matters here.
  Add a test that drives `invokeToolCall` more than once against a real spawn counter.
  **Fixed, 2026-10-03 fix session, all three mechanisms:** (a) `StdioConn.resolveAuthPending`
  (`internal/tools/mcp/conn_stdio.go`), called from both `deliver()` and `Close()`, now clears
  `authChallenged` under the same lock as the `done()` call; (b) `connector.resolve`
  (`internal/tools/mcp/connector.go`) gained `blockedAttempts` and a new `mcpBlockedRedialCap = 2`:
  a blocked-outside-run outcome is memoised as a terminal failure once the cap is reached, so the
  run spends at most one dial plus one re-dial per server, never one pair per call (D36); (c)
  `internal/utils/print.go` splits the auth keyword list into `mcpLogAuthChallengeKeywords` (the
  imperative/named-failure subset, used by the new `IsMcpLogAuthChallengeLine`) and the broader
  `mcpLogAuthKeywords` (unchanged, UI-only, still matches bare `401`/`403`); `conn_stdio.go`'s
  `raiseAuthPending` now takes a `confirmed bool` and only sets `authChallenged` when the stricter
  classifier matched, so a bare numeric substring can still open the pre-emptive sink window (the
  existing, accepted false-positive window) but can no longer reclassify a connect failure's error
  type. Verified by probe against the real production path, not a hand-built dial closure:
  `TestAuthChallengeStdioSpawnBoundedAcrossRepeatedToolCallsEndToEnd`
  (`internal/text/querier_setup_tools_test.go`) drives `toolExecutor.invokeToolCall` three times
  against a real spawned `TEST_SERVER_AUTH_HANG` fixture and a real `TEST_SERVER_SPAWN_LOG` counter:
  exactly 2 real process births across 3 tool calls, not 6, and not growing with further calls.
- [x] **R1-06** (major) — **the no-resolver branch classifies bound expiry as a resolved wait**, so
  the invariant row "A wait that exceeds the auth-timeout parameter | Typed error and actionable
  tool result" and the integration row "Wait exceeding the bound … Prohibited side effects: … **no
  second spawn**" hold only on the resolver (HTTP) path. In `resolveMcpAuthWait`
  (`internal/text/tool_executor.go`) the stdio branch is `<-waitCtx.Done()` with `waitErr` left
  nil, so the subsequent `errors.Is(waitErr, context.DeadlineExceeded)` check is false and the
  retry runs. The implementation notes declare this reading openly and explain why one of the two
  readings had to go — but the consequence (R1-01's second spawn) was not traced, and the declared
  row was left contradicting the code. `TestAuthTimeoutExpiryReturnsActionableToolResult` uses a
  resolver, so the stdio expiry branch has no expiry test;
  `TestAuthPendingSignalRaisedByHttpAndStdio`'s stdio case instead *encodes* the deviation by
  asserting the retry succeeds.
  Ruling: the contract is right and the code is wrong — expiry of the bound must yield the
  actionable result with no retry on both branches, because the retry has no new information to
  act on. Change the code, keep the row, and amend the implementation notes.
  **Fixed, 2026-10-03 fix session (D37):** the no-resolver branch in `resolveMcpAuthWait`
  (`internal/text/tool_executor.go`) now sets `waitErr = waitCtx.Err()` after `<-waitCtx.Done()`,
  so it is `context.DeadlineExceeded` exactly when its own bound fired and the existing
  `errors.Is(waitErr, context.DeadlineExceeded)` check below catches it like any other expiry — no
  retry. `TestAuthPendingSignalRaisedByHttpAndStdio`'s stdio-like subtest now asserts the actionable
  result and exactly one dial instead of encoding the old retry-and-succeed deviation; a new
  end-to-end probe, `TestAuthChallengeStdioSpawnBoundedAcrossRepeatedToolCallsEndToEnd`, confirms no
  second dial happens inside one call for the no-resolver path.
- [x] **R1-07** (major) — **one auth window that nothing closes suspends MCP log output for every
  server for the rest of the run.** `mcpLogSink.Drain` returns `nil` whenever any window is open
  (`internal/text/mcp_log_sink.go:273-276`), and a window opened by
  `StdioConn.raiseAuthPending` is closed only by `deliver()` or `Close()` — both of which require
  traffic on that connection. Scenario: an already-connected stdio server prints `session expired`
  to stderr mid-run while no tool call is outstanding; the window opens, and if the model never
  calls that server again nothing closes it until the run context ends. **Verified by probe**: with
  server `a`'s window open, two `AppendServerLog` calls for an unrelated server `b` (one of them an
  error line) yielded `Drain() == 0 entries`; after closing `a`'s window the same `Drain()` returned
  both. Because the queue is capped at 256 with oldest-non-error eviction (`:28`, `:242`), a chatty
  run also loses lines outright. Only the reopened server's own lines stay visible, via the
  `isReopened` branch at `:151`.
  Corrective action: scope the suspension to the reopened server rather than making it global, or
  bound an un-driven window with the same auth-timeout and close it on expiry.
  **Fixed, 2026-10-03 fix session, first option:** `mcpLogSink.Drain` (`internal/text/mcp_log_sink.go`)
  now partitions the queue by whether each entry's own server has an open window, draining every
  other server's entries immediately and deferring only entries belonging to a server whose window
  is still open — which in practice is none, since a reopened server's own lines render straight
  into its pinned window and never reach the queue at all (`isReopened` branch). `TestSessionLoopDoesNotRenderIntoAuthWindow`
  rewritten to prove the scoped behaviour: an unrelated server's line drains immediately while
  `linear`'s window is open, and `linear`'s own line renders into the reopened window rather than
  being queued.
- [x] **R1-08** (major) — **the actionable tool result names a command that refuses the transport
  that produces the message most often.** `actionableAuthResult`
  (`internal/text/tool_executor.go`) always emits
  `ERROR: mcp server %q needs authorization; run: clai mcp auth %s`, but `runAuthWith`
  (`internal/tools/mcp/cmd.go:89-91`) rejects every server with no `url`:
  `only an endpoint-based server can be authorized interactively`. Since stdio is the transport
  that reaches this path through the keyword reclassification in R1-01, the single piece of advice
  clai gives the operator is, in the common case, guaranteed to fail.
  Corrective action: branch the message on the transport — for a command-based server say that the
  server itself is prompting and point at its own stderr window — or make `clai mcp auth` do
  something useful for a stdio server.
  **Fixed, 2026-10-03 fix session, first option:** `actionableAuthResult` (`internal/text/tool_executor.go`)
  now takes `hasResolver bool` (`authResolver != nil`, which is nil precisely for a command-based
  server — `internal/tools/mcp/tool.go`'s own doc comment on the field) and emits a message naming
  the server's own stderr output instead of `clai mcp auth` when there is no resolver.
  `TestAuthPendingSignalRaisedByHttpAndStdio`'s stdio-like case now asserts the new message;
  `TestAuthTimeoutZeroFailsFast` keeps the `clai mcp auth` message by supplying a (never-invoked)
  resolver, since that test is about the zero-value bound, not the transport branch.
- [x] **R1-21** (minor) — **the D22 default's production wiring has no test; only the helper does.**
  `TestNonTerminalSessionDefaultsToFailFast` (`internal/text/mcp_oauth_test.go:15-36`) is a pure
  unit test of `resolveAuthTimeout`. `grep -rn OutputIsTerminal` over the repository finds four
  non-test sites and **zero** tests, so deleting the single assignment at
  `internal/text/querier_setup.go:245` would make every terminal session fail fast with the whole
  suite still green. Separately, two different terminal checks are in play for one decision:
  `querier.outputIsTerminal = utils.IsTerminalWriter(output)` (`querier_setup.go:208`, the
  querier's possibly-injected writer) versus
  `mcpauth.WithInteractive(utils.IsTerminalWriter(os.Stdout))`
  (`internal/text/mcp_oauth.go:68`, the process stdout), so `Interactive` can be true while
  `OutputIsTerminal` is false. Pick one source and test the wiring.
  **Fixed, 2026-10-03 fix session.** The two-different-checks half was already closed as a side
  effect of phase 5's R2-08 fix: `newMcpAuthorizer` now takes `outputIsTerminal bool` directly and
  there is no second `os.Stdout`-derived check left in `internal/text/mcp_oauth.go` (confirmed by
  `grep -rn IsTerminalWriter`/`WithInteractive`: `clai mcp auth`'s own `WithInteractive(true)` is a
  deliberate, unrelated hardcode for that explicit subcommand). The no-test half is fixed here:
  `TestNewQuerierThreadsOutputIsTerminalIntoAuthTimeout` (`internal/text/querier_setup_test.go`)
  drives the real `NewQuerier` → `querier_setup.go:245` → `setupTooling` → `setupMcpManager` chain
  with a warm schema cache, asserting a registered tool's resolved auth-timeout is 0 for an unset
  field on a non-terminal writer, and nonzero when the field is pre-set — proving the single-line
  assignment runs and that it defers to an explicit value rather than overwriting it (R2-09's own
  fix, same commit).
- [x] **R1-30** (minor) — **the phase's own "The clock is injected and no test sleeps" does not
  hold.** `internal/text/tool_executor_auth_test.go` uses real wall-clock bounds and real sleeps
  throughout: `time.Sleep(3 * shrunkConnectBound)` and `time.Sleep(80*time.Millisecond)` against a
  `tinyCallTimeout` of 10 ms, 20–30 ms auth bounds, a 20 ms sleep before `cancel()`, and an
  `elapsed > time.Second` assertion. The README records this host's race gate as load-sensitive and
  this phase states the no-sleep rule explicitly for that reason. The suite passed for me at load
  15.75, so this is a latent flake rather than an active one — but
  `TestAuthWaitExcludedFromCallTimeout` depends on less than 10 ms of scheduling delay between a
  `context.WithTimeout` and an immediate in-memory call, which is not a safe margin at
  `-count=3` under load.
  Corrective action: inject the clock as the phase says, or widen the margins and delete the
  `elapsed` assertions.
  **Fixed, 2026-10-03 fix session, second option (same fix closes R2-26):** every tight margin in
  `internal/text/tool_executor_auth_test.go` widened roughly 5-15x (10/20/30/50/80 ms bounds and
  sleeps moved to 150 ms, `shrunkConnectBound` 50 ms → 150 ms), with no change to the logic under
  test; the `elapsed > time.Second` assertion in `TestAuthTimeoutZeroFailsFast` was already a
  generous one-sided sanity check and is left as-is. Full suite verified green at `-race -count=3`
  (see Implementation notes).
- [ ] **R1-26** (minor, owned with phase 1) — `WithAuthPendingSink`, added by this phase, is dead.
  Detail in phase 1's R1-26. Out of this fix session's scope: phase 1 owns the symbol and its own
  review findings file tracks it; not re-litigated here.

**On D23 / README invariant 7.** `TestHumanWaitIsNotModelInvocable`
(`internal/text/tool_executor_auth_test.go`) does not test its claim and cannot fail: it asserts
that a plain non-MCP tool returns its own result — which has nothing to do with the wait — and then
makes a compile-time interface assertion. The invariant as the code implements it is narrower than
the prose: the model cannot *request* a wait, and no tool input reaches it, but the model's choice
of `call.Name` does determine whether a wait is entered, so it is reachable indirectly on the
model's initiative. I am **not** filing that as a defect — it is the designed behaviour of this
phase and the server set is fixed config, not model input — but the invariant's wording ("No
model-callable surface reaches it, directly **or indirectly**") overstates what is true, and the
test should either assert something falsifiable (for example that an MCP tool with a resolved
connector never calls `AuthPending`) or be replaced by a statement in the phase file. R1-01 is what
makes this worth saying: the model's tool-call pattern is precisely what multiplies the cost there.

### Review 2, 2026-10-02 — implementation review, round 2

Status: **Fixed, 2026-10-03 fix session.** R2-01, R2-09 and R2-26 are checked off and verified; see
each finding's own fixed-note and the phase's Implementation-notes delta. The human-required
observation gate remains legitimately outstanding and is not a finding.

Round 2 re-verified round 1's phase-6 findings against the code and confirms all of them. Its two
new findings are a reproducible gate failure and the fate of the terminal signal on the SDK path.

**Round 1's phase-6 findings re-verified, all upheld:**

- **R1-01** upheld. `utils.IsMcpLogAuthLine` is a case-insensitive substring match over a keyword
  list containing `"401"` and `"403"`, and `reclassifyIfAuthChallenged` (`connector.go:215-224`)
  turns any connect-stage failure on a connection whose stderr matched into an
  `AuthChallengeError`, which `isBlockedOutsideRun` then exempts from the memo
  (`connector.go:106`). D36's per-run cap is the right ruling.
- **R1-06** upheld, and it is the exact mechanism of R1-01's second spawn. The no-resolver branch
  is `<-waitCtx.Done()` (`internal/text/tool_executor.go`, `resolveMcpAuthWait`), after which
  `waitErr` is nil, so control falls past both the `DeadlineExceeded` and the `waitErr != nil`
  guards and into the second `resolver.ResolveForCall(ctx)`. D37 is the right ruling.
- **R1-07** upheld. `Drain` returns nil whenever `len(s.authOpen) > 0`
  (`internal/text/mcp_log_sink.go`), and the only closer of a window opened by
  `StdioConn.raiseAuthPending` is `resolveAuthPending`, reached from `Close` and `deliver`. A
  process that hangs holding stdout open never reaches either, so the window never closes and
  `Drain` is suspended for every server for the rest of the run.
- **R1-08** upheld: `actionableAuthResult` is unconditional, and `clai mcp auth` refuses a
  command-based server.

**Verified good:**

- `AuthPending` is idempotent per server and returns the same `sync.Once`-guarded closer
  (`mcp_log_sink.go`), so the two independent callers — `StdioConn.raiseAuthPending` and
  `toolExecutor.resolveMcpAuthWait` — cannot open two windows or double-close one.
- `resolveAuthPending` is also called from `deliver` (`conn_stdio.go:411`), so a false-positive
  keyword match that the server then answers normally does close its window. That is the right
  instinct and it is what keeps R1-07 from being universal.
- The wait is unreachable from a model: `invokeToolCall` looks the tool up in the per-run map and
  type-asserts `mcpConnectionResolver`, and MCP tools never enter the process-global registry, so
  no model-supplied name can reach the wait. D23 holds on this path.

**Findings:**

- [x] **R2-01** (blocker, shared with phase 8) — **`TestStdioConnectReclassifiesAuthPromptAsChallenge`
  fails deterministically on a cold Go build cache.** `internal/tools/mcp/conn_stdio_test.go:620-654`
  constructs the connector with `WithConnectBound(200 * time.Millisecond)` and requires that, inside
  that window, `go run ./testserver` compiles, links, starts, and flushes two stderr lines that
  `readStderr` must classify, so that `AuthChallenged()` is true when the bound expires. On a warm
  build cache that is tens of milliseconds and the test passes. On a cold cache it is seconds and
  the test fails every time, because the bound expires before any stderr exists and the error stays
  a plain connect-stage timeout.

  Reproduced, not inferred:

  ```
  go clean -cache
  go test ./internal/tools/mcp/ -run TestStdioConnectReclassifiesAuthPromptAsChallenge -race -count=1
  --- FAIL: TestStdioConnectReclassifiesAuthPromptAsChallenge (0.20s)
      conn_stdio_test.go:634: err = mcp server 'hang' failed to start at stage 'connect':
          mcp conn "hang": initialize cancelled: context deadline exceeded,
          want *claierr.AuthChallengeError
  ```

  Warm, the same command is `ok`. It also failed 3/3 in the full `-race -count=3` suite at host load
  ~20, which is how it was found. This is not the load sensitivity the README records: a clean CI
  checkout has a cold build cache by construction, as does every contributor's first run and every
  run after a `go.sum` change busts `actions/setup-go`'s cache. Phase 8's "gates pass unedited" claim
  is therefore a warm-cache claim.

  Corrective action: build the fixture once into a temporary binary in a `TestMain` and point every
  `go run ./testserver` config at that binary. That removes the compile from inside every bound in
  the suite at once, not only this one, and it also removes the cost R2-19 records. A bigger bound
  is the lesser fix: the test would still be timing a compile, just with more slack.

  **Fixed, 2026-10-03 fix session, scoped to this phase's own test:** `internal/tools/mcp/conn_stdio_test.go`
  gained a `testServerBinary` helper (the same `go build` once, `sync.Once`-guarded pattern
  `internal/text/querier_setup_tools_test.go`'s own D41 fix already established) and
  `TestStdioConnectReclassifiesAuthPromptAsChallenge` now points its `Command` at the prebuilt
  binary instead of `go run ./testserver`, so the 200 ms connect bound only ever times process
  start, never a compile. Reproduced cold to confirm the fix, not just the absence of the old
  failure: `GOCACHE=<fresh empty dir> go test ./internal/tools/mcp/ -run
  TestStdioConnectReclassifiesAuthPromptAsChallenge -race -count=1` — the `go build` step took
  4.48 s (the exact R2-01 scenario) and the test still passed, because that cost now falls outside
  the bound. This fixes R2-01's own test; it leaves every other `go run ./testserver` call site in
  the repository (phases 1–5's own tests, and phase 8's R2-19 cost) untouched, which remains
  phase 8's gate-sweep and those phases' own scope, not this fix session's.
- [x] **R2-09** (major) — **D22's terminal signal is unreachable for an SDK caller, so the
  auth-timeout is always zero there and the `AuthResolver` is never driven.** Two parts:

  1. `querier_setup.go:245` assigns `userConf.OutputIsTerminal = querier.outputIsTerminal`
     **unconditionally**, so the exported field a `pkg/text` caller sets is clobbered before
     `setupTooling` reads it. The field is settable and has no settable semantics.
  2. `querier.outputIsTerminal` derives from `utils.IsTerminalWriter(userConf.Out)`
     (`querier_setup.go:203-208`), and `pkg/agent` hardcodes `Out: io.Discard` with no
     `WithOut`-style option (`pkg/agent/agent.go:255`). So it is false for every SDK consumer,
     always.

  Consequence: `resolveAuthTimeout` (`internal/text/mcp_oauth.go:27-35`) returns 0 for every SDK
  caller that leaves `AuthTimeoutSeconds` nil, and `resolveMcpAuthWait`'s `authTimeout <= 0` guard
  returns `actionableAuthResult` immediately — advice to "run: clai mcp auth <server>", handed to a
  model inside an embedded library, with the `AuthResolver` never consulted even when it could have
  refreshed a token non-interactively. Concrete failure: an SDK consumer with a `token_command`
  whose token has just expired gets a tool result telling it to run a CLI command, rather than the
  refresh the resolver would have performed. Note this is independent of R1-21, which is about the
  decision having no test; this is about the decision having no reachable input. Corrective action:
  make the field `*bool` or assign it only when unset, and expose it on `pkg/agent` as an explicit
  option — a human-presence decision should not be derived from a writer the SDK forces to
  `io.Discard`.

  **Fixed, 2026-10-03 fix session, both parts:** `Configurations.OutputIsTerminal`
  (`internal/text/conf.go`) is now `*bool`, with a new `OutputIsTerminalOrDefault()` helper every
  read site uses; `querier_setup.go:245` assigns only `if userConf.OutputIsTerminal == nil`, so a
  caller-supplied value survives. `pkg/agent` gained `WithOutputIsTerminal(bool)`, wired into
  `asInternalConfig`, so an SDK consumer that wants the bounded D22 default no longer has to fight
  the hardcoded `io.Discard` writer. `TestAgent_WithOutputIsTerminal_propagates`
  (`pkg/agent/agent_test.go`) pins the option's own wiring; the two-subtest
  `TestNewQuerierThreadsOutputIsTerminalIntoAuthTimeout` (R1-21's own fix, `internal/text/querier_setup_test.go`)
  proves both the unset-defaults-false and the explicit-value-survives paths through the real
  `NewQuerier` composition root.

- [x] **R2-26** (note) — **Real wall-clock bounds in this phase's own suite, same class as R1-30
  but in a different file.** `internal/text/tool_executor_auth_test.go:278-292`
  (`TestAuthWaitExcludedFromCallTimeout`) uses a 10 ms `tinyCallTimeout`, and `:88` and `:146` use
  30 ms bounds, on the host whose race-gate load sensitivity the README records. The logic is
  correct and the resolver it races takes 80 ms, so raising `tinyCallTimeout` to a few hundred
  milliseconds costs nothing and removes a flake candidate. Filed as a note only because the two
  30 ms cases bound a wait rather than a success path and are the safer shape.
  **Fixed together with R1-30**, same fix session and same edit: all the margins this finding names
  widened to 150 ms.

### Review 3, 2026-10-03 — sign-off review (holistic)

**Status: Reopened (sign-off), resolved.** The first pass to read the whole effort at once rather
than one phase at a time. Its single blocker, B2, is filed mainly against phase 5; one of its six
items is this phase's wiring, and one of its recorded gaps is this phase meeting phase 5 at only
one point. Full detail in the README's Sign-off verdict section and the Sign-off review
feedback-index entry.

**Findings**

- [x] **B2 (mid-run half)** (blocker) — `httpChallengeResolver` was not gated on
  `authz.Interactive`, although the setup path had been fixed for precisely that defect as R2-08.
  A headless `pkg/agent` consumer that set `auth_timeout_seconds` opened a browser and bound a
  loopback listener inside its own process. The review's own diagnosis of why thirteen passes
  missed it: "same defect, different path, missed because each review looked at one phase at a
  time."

  **Resolved (sign-off fix session, 2026-10-03).** Gated at the root instead of at this call site:
  `mcpauth.AuthorizeInteractive` refuses a non-interactive run with
  `*mcpauth.InteractiveAuthDisabledError` before reading the challenge, so every caller inherits
  it. `httpChallengeResolver` deliberately still returns a non-nil resolver, because
  `resolveMcpAuthWait` reads `authResolver != nil` to pick the actionable result's wording and
  whether to wait at all. `TestMidRunChallengeOnNonInteractiveRunNeverOpensBrowser`, proved red by
  a probe that **hung** for the full 60 s bound inside `awaitRedirect` rather than failing. Full
  detail in the Implementation notes above.

- [ ] **Recorded, not closed: mid-run token expiry never re-authorizes.** Verified against source
  this session; judged a phase of its own, with reasoning in the Implementation notes above and in
  the README's cross-phase gap record.

## Review findings — fix verification, sign-off round

B2's mid-run half is checked off above with its fix and its red-before-green evidence cited in
place. The items left open against this phase are R1-26 (owned by phase 1 and since closed by the
phase-8 sweep), the recorded mid-run re-authorization gap, and the human-required real mid-run
authorization observation.
