# Phase 2 — Lazy connect

**Status:** Complete — reviews 1 and 2 reopened this phase on seven findings (R1-02, R1-25, R1-14,
R2-02, R2-07, R2-13, R2-15); all seven are fixed and verified in the worklog-work session recorded
in Implementation notes below. R1-14's HTTP-transport half remains open under phase 5.

Back to [README](README.md).

## Goal

Build the machinery that creates a server's connection on the first tool call that targets it, once
per run, and apply it to a server explicitly marked lazy. The default is not changed here and no
behaviour changes for a server that is not so marked; the phase that adds the schema cache flips the
default and delivers the saving.

## Specification

### Behaviour

`setupMcpManager` stops constructing transports for servers whose startup mode is lazy. Each
configured server yields a `Connector` as declared in the README's shared interfaces section, and
`mcpTool` holds that `Connector` in place of the `Conn` the previous phase gave it. The first
`CallWithContext` against any tool of a server resolves the connection, performs the handshake, and
proceeds; later calls reuse it.

This phase ships with the startup-mode default still eager, per decision D16. That is deliberate and
it is the whole reason this phase is safely shippable on its own: a lazy server contributes no tool
schemas until something can supply a `tools/list` result without connecting, and the cache that does
so arrives in the next phase. So this phase builds the machinery and changes no default. Behaviour
at the end of this phase is identical to behaviour before it for every configured server, except
that a server explicitly marked lazy in its own config is now deferred. The next phase flips the
default and owns the behaviour change.

The resolution runs under the **run** context, never under the triggering call's context. A caller
whose own deadline expires therefore abandons its wait without cancelling a resolution that other
callers, or later calls in the same run, are depending on. The memo records the outcome of that
resolution when it completes: a successful connection is reused, and a failure is the run-scoped
failure described below. An abandoned wait leaves the memo untouched, so the next call either finds
the connection or finds the recorded failure, and never starts a second process.

Resolution is memoised per server per run: a batch containing three calls to one server resolves one
connection and starts one process. That outcome is reached through memoisation, because a tool batch
executes sequentially today.

The resolution is additionally single-flight, so concurrent callers share one resolution and the
error propagates to every waiter rather than one succeeding and the others retrying. There is no
production path that produces concurrent calls on one connection today, so that contract is a
defensive unit-level guarantee with no end-to-end trigger: its test drives the connector directly.
It is specified because the seam is public within the package and because any future concurrent
caller would otherwise reintroduce the defects the previous phase removed.

The startup-mode parameter selects the posture per server, and the README parameters table holds its
default, including the conditional case for a server under strict startup. Eager preserves
today's behaviour and exists for a stdio server whose connection requires a human, so that the
prompt appears during setup rather than mid-run. Phase 6 handles the mid-run case for servers that
stay lazy.

Connecting is bounded by the connect-bound parameter, which is distinct from the pre-existing
single-call bound. Charging process birth against a call budget would make a lazy first call look
like a hung tool, so the two bounds never share a value and the connect wait is excluded from the
call bound.

Three bounds now govern one lazy first call and their relationship is fixed here. The connect bound
is the outermost: it wraps spawn plus the handshake, and it is therefore the bound a lazy first call
observes. The handshake-bound parameter remains the connection-level bound and is what an eager or
directly constructed connection observes. The two defaults differ, so each phase's limits test
triggers its own bound deterministically rather than racing the other. The single-call bound governs
only a `tools/call` on an already-resolved connection.

### Failure posture

One outcome is deliberately **not** a failure. A resolution that cannot proceed until something
happens outside the run — a human authorizing a server is the only case this worklog introduces — is
neither a success nor a run-scoped failure, because the blocking condition can change while the run
is still going. Such a resolution returns its typed error to the caller and leaves the memo
untouched, so a later call can resolve successfully. It does not consume the retry-count parameter,
which governs failures. Any resolution that reports this outcome is resolved at most once more per
run; the phase that owns the human wait is the one that triggers that retry.

A connect failure is a fact about this run, never about the server, so nothing is written to disk
and nothing is remembered beyond the run. Within the run the server is marked failed and is not
retried, per the retry-count parameter: a model that keeps calling a broken server must not keep
spawning processes. Every subsequent call to any tool of that server returns the same typed error
as its tool result, so the model learns to stop rather than looping.

The posture distinction already in `setupMcpManager` is preserved and extended to the lazy path. An
ambient server, discovered from the config directory, degrades: its failure warns and the run
continues. An explicit server, supplied through `agent.WithMcpServers` with
`AgentSettings.StrictMcpStartup`, fails.

No new post-setup error channel is introduced, per decision D14. Instead the startup-mode parameter
carries the conditional default recorded in the README: a server under strict startup is eager, so
its failure still lands during setup where the strict contract already delivers it to the caller. An
operator who sets the lazy mode explicitly on such a server has opted out of setup-time failure, and
that server's failure arrives as the typed tool-result error like any other lazy server. That
trade-off is the whole of the strict-plus-lazy behaviour; there is no third reporting path.

### Invariants

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| Constructing a `Connector` for a server marked lazy | Constructs it without dialing: no transport, no process, and no tools until the cache phase gives setup a source for them | `TestConnectorLazyDoesNotConnectDuringSetup` |
| Setup of an eager server | Resolves the connection during setup, as today | `TestConnectorEagerConnectsDuringSetup` |
| First call to any tool of a server | Resolves one connection | `TestConnectorSingleFlightCreatesOneConnPerRun` |
| Concurrent first calls to one server, driven directly | Share one resolution; all receive the same outcome | `TestConnectorSingleFlightCreatesOneConnPerRun` |
| Several calls in one batch to one server | Start exactly one process | `TestThreeCallsOneServerSpawnOnce` |
| A failed resolution | Marked for the run, not retried, not persisted | `TestConnectorConnectFailureIsRunScopedAndNotRetried` |
| A resolution blocked on something outside the run | Returns its typed error, leaves the memo untouched, consumes no retry | `TestBlockedResolutionIsNotMemoisedAsFailure` |
| Every later call to a failed server | Returns the same typed error as a tool result | `TestConnectorConnectFailureSurfacesTypedToolResult` |
| An explicit server under strict startup | Resolves during setup, so its failure is delivered by the existing strict path | `TestLazyConnectHonoursStrictMcpStartupForExplicitServers` |
| An ambient server | Warns and the run continues | `TestLazyConnectDegradesForAmbientServers` |
| Run end | Every resolved connection is closed and its process reaped | `TestConnectorCloseReleasesProcessOnRunEnd` |
| A caller abandoning its wait | Resolution continues under the run context; the memo is untouched; no second process | `TestAbandonedWaitDoesNotCancelResolution` |
| The startup-mode field | Parsed into the named type; an unrecognised value is rejected at parse time | `TestStartupModeRejectsUnknownValue` |

### Limits

| Limit | Injectable field | README parameter | How a test triggers it |
| --- | --- | --- | --- |
| Connect wait, wrapping spawn plus handshake | Per-server config field on the server model | connect-bound parameter | Fake server that completes its spawn but never answers `initialize`, with the handshake bound injected higher so the outer bound expires first |
| Retries after a failed resolution | Connector field | retry-count parameter | Fake server whose spawn always fails; call the tool more than once and count spawns |
| Single-call wait | Pre-existing per-server config field | single-call bound parameter | Fake server that answers the handshake then stalls a `tools/call` |

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Setup with several servers explicitly marked lazy, no tool call | Fake stdio servers | Setup returns; the lazy servers contribute no tools yet, which is why the next phase exists | No process started for any server | No tool schema invented, no transport constructed |
| One tool of one server called | Fake stdio servers | Call succeeds | Exactly one process started, for that server only | No process started for the other servers |
| Three calls to tools of one server in one batch | Fake stdio server counting spawns | All three succeed | One process started | No second process |
| Call to a server whose spawn fails | Fake spawn that always fails | Tool result carries the typed error | Error surfaced once per call | No repeated spawn attempt |
| Explicit server under strict startup whose spawn fails | Fake spawn that always fails | Setup returns the typed error, as it does today | Failure delivered by the existing strict path at setup time | Server not deferred past setup, no new reporting path |
| Run cancelled while a connection is resolving | Fake server that never answers | Resolution returns a typed cancellation error | Process reaped | No orphaned process |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| A server explicitly marked lazy costs no process until used | `TestConnectorLazyDoesNotConnectDuringSetup` |
| An eager server connects at setup | `TestConnectorEagerConnectsDuringSetup` |
| One resolution per server per run, shared by concurrent callers | `TestConnectorSingleFlightCreatesOneConnPerRun` |
| A batch of calls to one server starts one process | `TestThreeCallsOneServerSpawnOnce` |
| A connect failure is run-scoped and not retried | `TestConnectorConnectFailureIsRunScopedAndNotRetried` |
| A resolution blocked outside the run is not memoised as a failure | `TestBlockedResolutionIsNotMemoisedAsFailure` |
| A failed server's later calls return the typed error as a tool result | `TestConnectorConnectFailureSurfacesTypedToolResult` |
| Connect and call bounds are independent | `TestConnectTimeoutIsSeparateFromCallTimeout` |
| Strict startup still fails for explicit servers | `TestLazyConnectHonoursStrictMcpStartupForExplicitServers` |
| Ambient servers still degrade | `TestLazyConnectDegradesForAmbientServers` |
| Connections close and processes are reaped at run end | `TestConnectorCloseReleasesProcessOnRunEnd` |
| An abandoned wait neither cancels the resolution nor causes a second spawn | `TestAbandonedWaitDoesNotCancelResolution` |
| An unrecognised startup mode is a parse error, not a silent default | `TestStartupModeRejectsUnknownValue` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Spawn fails on first call | Typed startup error as tool result, run continues for ambient servers | `TestConnectorConnectFailureSurfacesTypedToolResult` |
| Spawn plus handshake exceeds the connect bound | Typed startup error naming the connect stage, because the connect bound is the outer bound | `TestConnectBoundExpiryReturnsTypedStartupError` |
| Second call after a failed resolution | Same typed error, no new spawn | `TestConnectorConnectFailureIsRunScopedAndNotRetried` |
| Explicit server under strict startup fails to resolve | Typed error returned from setup, by the existing strict path | `TestLazyConnectHonoursStrictMcpStartupForExplicitServers` |
| Run context cancelled during resolution | Typed cancellation error, process reaped | `TestConnectorCancelDuringResolutionReapsProcess` |
| A tool call arrives after run end | Typed connection-closed error, no new spawn | `TestConnectorCallAfterRunEndIsClosedError` |
| The triggering caller's context expires while resolving | Resolution is not cancelled; a later call in the same run reuses its outcome; no second process | `TestAbandonedWaitDoesNotCancelResolution` |
| Startup mode value unrecognised in config | Parse error naming the file and the field, no server started | `TestStartupModeRejectsUnknownValue` |

## Implementation notes

Executed 2026-10-02, worklog-work session.

`StartupMode` and `ConnectTimeoutSeconds` land on `pub_models.McpServer`
(`pkg/text/models/tools.go`) exactly as the code-layout table assigns them;
`StartupMode.UnmarshalJSON` rejects any value outside `eager`/`lazy`, which
surfaces through `findConfiguredMcpServers`'s existing per-file unmarshal-error
wrapping, so the parse error already names the file without new code there.

`internal/tools/mcp/connector.go` is new: `connector` (single-flight,
memoising) implements `Connector` over an injected `dialFunc`, white-box
tested directly via `newConnector(runCtx, dialFunc)` so the concurrency,
memoisation and blocked-outcome contracts need no real process. The
production path, `NewConnector`, dials through `dialStdio`, which spawns via
`NewStdioConn` under a per-attempt `context.WithCancel(runCtx)` child: on
success that child context is left running (bound to the connection's whole
run-scoped lifetime, so cancelling it is deliberately not on every path),
and on any failure it is cancelled immediately so the process is reaped
rather than kept alive to the end of the run. `go vet`'s `lostcancel`
analysis does not accept "the cancel escapes via a conditional branch and a
derived parent", so `dialStdio` uses a named return plus a single
unconditional `defer` that cancels only when `resultErr != nil` — this is a
mechanical workaround for the analyser, not a behaviour difference from
what two unconditional `procCancel()` calls on the error paths already did.

Blocked-outside-run classification (the "neither success nor failure"
outcome) is not named by any interface in the README or the phase text —
phase 6 owns the eventual auth-pending signal, but nothing in this worklog
assigns a type to the connector's own memoisation decision. I introduced a
minimal optional interface, `outsideRunBlocker` (`BlockedOutsideRun() bool`),
following the same feature-detection pattern this package already uses for
`handshakeBounder` and the sink's `setupSucceeded()`. No code in this phase
produces such an error; `TestBlockedResolutionIsNotMemoisedAsFailure` drives
the connector directly with a hand-built `blockedOutsideRunErr` to prove the
mechanism, exactly as the phase text anticipates ("its own test drives the
connector directly"). This is a seam for phase 6 to satisfy, not a
commitment on phase 6's eventual error shape.

The connect-bound/handshake-bound stage naming ("Spawn plus handshake
exceeds the connect bound ... naming the connect stage") is implemented by
`reportAsConnectStage`: `dialStdio` reuses the same `initializeHandshake`
helper `handleServer` already calls (extracted from `manager.go` to avoid
duplicating the initialize+notify request), then rewrites the resulting
`*claierr.McpServerStartupError`'s stage from `"initialize"` to `"connect"`
only when the connect-bound's own context is what expired
(`connectCtx.Err() != nil`), leaving a genuine protocol-level initialize
failure (RPC error, decode failure) named `"initialize"` as before.

`mcpTool` now holds `connector Connector` instead of `conn Conn`
(`internal/tools/mcp/tool.go`); `call` resolves the connector first, under
the caller's own ctx (so an abandoned wait returns promptly without
disturbing the resolution), and only wraps the per-call `timeout` around the
subsequent `tools/call`, never around resolution, per the connect-bound
exclusion. An eager server's already-connected `Conn` (built exactly as
before, unchanged, by `handleServer`/`Manager`) is wrapped in a trivial
`resolvedConnector` so every `mcpTool` holds a `Connector` uniformly; this
required updating every existing `mcpTool{...}` literal in `tool_test.go`
and `manager.go` to the new field, with no behavioural change for the eager
path (`TestManagerRegistersToolsThroughConn` and the rest of the pre-existing
package suite pass unmodified).

Judgment call on `setupMcpManager`'s integration: for an explicitly lazy
server, `internal/text/querier_setup_tools.go` now resolves
`effectiveStartupMode` (unconditionally `eager` when unset, D16) and, for
`lazy`, skips the spawn/`ControlEvent` path entirely — no process, no
`mcp.Connector` is constructed there. I read "Setup of a server explicitly
marked lazy | Constructs a Connector, no transport, no process" as a
property of the `mcp` package's own `Connector`/`NewConnector` machinery
(proved by `TestConnectorLazyDoesNotConnectDuringSetup` and
`TestConnectorEagerConnectsDuringSetup`, which construct and resolve a
`Connector` directly), not as a requirement that `setupMcpManager` itself
hold an unused `Connector` per lazy server with no consumer: nothing in this
phase can wire a lazy server's `Connector` to a registered tool, since the
only source of a tool's name/schema is `tools/list`, which requires
connecting — exactly the gap the phase's own Goal and D16 describe, closed
by phase 3's setup-side cache call site. If phase 3 instead expects
`setupMcpManager` to have already stashed a per-server `Connector` somewhere
for it to pick up, that is new information this phase did not have a slot
for; I did not invent a storage mechanism for it since nothing in the
README's code-layout table assigns one to phase 2.

Verification, run on a quiet host (`uptime` load average 4.29/9.08/8.68
after the shared root `clai` and `internal/audio` packages were first seen
to time out once during a load-average 21+ spike and then pass cleanly in
isolation — the documented host-load sensitivity, not a regression; neither
package is touched by this phase's diff):

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go run mvdan.cc/gofumpt@latest -w -l .` — reformatted `connector_test.go` only (method-comment alignment), then clean.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — clean.
- `go fix ./...` — no changes.
- `go run github.com/mibk/dupl@latest -t 80 .` — 34 pre-existing clone groups, none touching a file this phase changed except one group already present before this phase (`internal/text/querier_setup_tools_test.go:373,394` / `pkg/agent/mcp_setup_test.go`, inside the pre-existing `startupErrorNames` helper, untouched by this diff).
- `go test ./... -race -cover -count=3 -timeout=30s` — all packages pass; `internal/tools/mcp` at 86.8% coverage, `internal/text` at 84.5%, `pkg/text/models` at 85.3%.
- All sixteen test names this phase declares exist and pass, run individually by name: `TestAbandonedWaitDoesNotCancelResolution`, `TestBlockedResolutionIsNotMemoisedAsFailure`, `TestConnectBoundExpiryReturnsTypedStartupError`, `TestConnectorCallAfterRunEndIsClosedError`, `TestConnectorCancelDuringResolutionReapsProcess`, `TestConnectorCloseReleasesProcessOnRunEnd`, `TestConnectorConnectFailureIsRunScopedAndNotRetried`, `TestConnectorConnectFailureSurfacesTypedToolResult`, `TestConnectorEagerConnectsDuringSetup`, `TestConnectorLazyDoesNotConnectDuringSetup`, `TestConnectorSingleFlightCreatesOneConnPerRun`, `TestConnectTimeoutIsSeparateFromCallTimeout`, `TestLazyConnectDegradesForAmbientServers`, `TestLazyConnectHonoursStrictMcpStartupForExplicitServers`, `TestStartupModeRejectsUnknownValue`, `TestThreeCallsOneServerSpawnOnce`.
- Readiness-checklist item 2's dedupe grep over `phase-2-lazy-connect.md` alone reports no name repeated within the file.

Not touched, by direction: `architecture/` is owned by the coordinating
session for this worklog; no file under it was created or edited by this
phase's execution.

### Fix session, 2026-10-02 (worklog-work, finding-fix pass after reviews 1 and 2)

Picked up this phase because the README status board named it `Reopened (review 2)` with seven
findings assigned here: R1-02, R1-25, R1-14 (review 1); R2-02, R2-07, R2-13, R2-15 (review 2).
Decisions D35–D44 were read first and not re-litigated. Each finding's write-test-first/fix pair
and its resolution text are recorded inline under its own bullet in Review findings below; this
note is the session-level summary.

**Closed, fully:** R1-02/D35 (strict-explicit exception now checked before any configured value,
unconditionally), R1-25 (strict-startup test split into two subtests that actually exercise
`effectiveStartupMode`'s strict branch; retry-count/batch claim moved to a real-spawn end-to-end
test through `toolExecutor.invokeToolCall`), R2-02/D39 (lazy branch now one goroutine per server,
joined through the pre-existing `toolWg`, with a mutex-guarded `failureCollector` replacing the
sequential-only `classifyServerFailure`), R2-07/D43 (`serverconfig.ValidateTransport` exported and
run over every `userConf.McpServers` entry before it joins the server list), R2-15 (`
effectiveStartupMode` now returns `(StartupMode, error)` and rejects any value outside
`eager`/`lazy`/unset unconditionally, including under the strict-explicit override).

**Closed for this phase's share; one half remains open elsewhere:** R1-14. The parameters-table
connect-bound and its application to the command-based (stdio) cache-miss path — phase 2's own
contract — are fixed (`mcp.ConnectBoundOf`, `mcp.ReportAsConnectStage`, both now exported and
reused rather than duplicated). The HTTP transport's own miss path
(`handshakeHttpServerWithAuth`/`runHandshake` in `internal/text/mcp_oauth.go`) still bounds only
the connection-level handshake bound; that file is phase 5's per the README code-layout table, and
this session made no change to it. Phase 5's own copy of R1-14 is not closed by this delta and
should not be treated as resolved.

**Closed, partly by restating a README-owned item:** R2-13. The three tests that silently ran the
lazy branch while claiming to be eager-only now name their posture explicitly, and the one test
that is deliberately posture-unset (because unset is itself what it asserts) carries a comment
saying so. The finding's second half — the README readiness checklist item 9's wording and its
unfalsifiable grep-based check — is a README-owned artifact, not a phase-2 file; see the README
Strategy/readiness-checklist edit in this same session, described in the README's own session
journal entry rather than restated here.

Repository rules followed: every fix above was preceded by a test that fails against the
pre-fix code and passes against the fix (verified by reverting the specific change in a scratch
copy, running the new test, then restoring — not committed, not left in the tree). No test spends
money or reaches a vendor endpoint. No log-and-return: every new failure path returns a typed
error (`claierr.NewMcpServerStartup`) through the existing `failureCollector`/`classifyServerFailure`-successor
routing, never swallowed.

New test fixture knob: `TEST_SERVER_PRE_HANDSHAKE_DELAY_MS`
(`internal/tools/mcp/testserver/main.go`), and `TEST_SERVER_SPAWN_LOG` now writes a
unix-nanosecond timestamp per line instead of the literal `"spawned"` (every existing consumer
only counts lines, so this is backward compatible); both exist solely to make
`TestLazySetupResolvesServersConcurrently` possible without timing a `go run` compile. That test
also introduces `testServerBinary`, a `sync.Once`-guarded helper that `go build`s the shared
stdio fixture once per test binary run into a file outside any `t.TempDir()` (so it survives past
the one test that first triggers the build), per D41/invariant 15: a tight timing bound must never
enclose a compile. This is the only place in this session's diff with that shape; no other
existing test in this package was touched for D41 (out of scope — R2-01, the finding that raised
D41, is assigned to phases 6 and 8, not phase 2).

Verification, run at host load 9.7–15.1 (`uptime`), which the README already records as making the
race gate unreliable above load 8 — recorded here because every run below passed anyway, not as
an excuse:

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go run mvdan.cc/gofumpt@latest -l .` — no files need formatting.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — clean.
- `go fix ./...` — no changes.
- `go run github.com/mibk/dupl@latest -t 80 .` — 35 clone groups now (34 recorded at this phase's
  first execution). Checked every group against every file this session touched
  (`querier_setup_tools.go`, `querier_setup_tools_test.go`, `schema_cache_setup_test.go`,
  `connector.go`, `serverconfig.go`, `serverconfig_test.go`, `testserver/main.go`, `conn_http.go`):
  none of this session's new code appears in any clone group. The two groups touching those files
  (`querier_setup_tools_test.go`/`pkg/agent/mcp_setup_test.go`'s `startupErrorNames`, and
  `conn_http.go`/`conn_stdio.go`'s `Close()`) both pre-date this session — the first is the one
  this phase's original execution already recorded, the second is untouched code in a file this
  session edited only by a one-token rename elsewhere. The 34→35 shift reflects phases 3–8's
  implementation landing in the shared tree since this phase's first execution, not this session's
  diff.
- `go test ./internal/text/... ./internal/tools/... ./pkg/... -race -cover -count=3 -timeout=30s -p 1` — all pass.
- `go test ./... -race -cover -count=3 -timeout=30s -p 1` — all pass, exit code 0, run in full
  (every package in the repository, not just this phase's).
- Every new or rewritten test run individually by name and confirmed to fail against a
  deliberately reverted copy of the fix and pass against the shipped fix:
  `TestLazyConnectHonoursStrictMcpStartupForExplicitServers` (both subtests),
  `TestExplicitServerBothCommandAndUrlFailsValidation`,
  `TestExplicitServerNeitherCommandNorUrlFailsValidation`,
  `TestLazyCacheMissConnectBoundAppliesToStdioHandshake`,
  `TestLazySetupResolvesServersConcurrently`,
  `TestConnectorConnectFailureSpawnsOnceAcrossRepeatedToolCallsEndToEnd`,
  `TestEffectiveStartupModeRejectsUnrecognisedProgrammaticValue`.

## Review findings

Corrected during phase 3's execution, recorded here rather than silently changed. This phase's
`TestLazyConnectDegradesForAmbientServers` asserted that an explicitly-lazy ambient server never
connects at setup. D18, taken after this phase was written, says a cache miss always connects, cold
or warm, and only a warm cache yields zero processes. The test was rewritten to the corrected
behaviour and the zero-process claim now belongs to the cache phase's
`TestSchemaCacheHitRegistersToolsWithoutTransport`. The invariant row above was also narrowed: it
describes constructing a connector, which still dials nothing, not the behaviour of setup, which
after D18 does connect on a miss.

### Review 1, 2026-10-02 — implementation review

**Status: Reopened (review 1).** Two of this phase's declared guarantees do not hold on the
production path, and the test that was supposed to protect the strict-startup one cannot fail.

**Verified good:**

- `connector`'s single flight is correct on every branch. `Conn` (`internal/tools/mcp/connector.go:73-96`)
  registers one `resolution` under `c.mu`, launches `resolve` once, and a caller that abandons its
  wait neither cancels nor retries the dial, because `resolve` runs under `c.runCtx`, never the
  caller's ctx (`:99`). A terminal failure is memoised exactly once (`:101-110`).
- Eager and lazy posture selection, the ambient-degrade/explicit-fail split
  (`internal/text/querier_setup_tools.go:280-286`), the `StartupMode` parse rejection
  (`pkg/text/models/tools.go:19-29`), and run-end teardown all hold as declared.
- `startupFailures` is sized per server and every send precedes its server's `allToolsWg.Done`
  (`internal/tools/mcp/manager.go:53-63`), so the `close` after the wait
  (`querier_setup_tools.go:249`) cannot race a sender. Traced; no defect.

**Findings**

- [x] **R1-02** (blocker, shared with phase 3) — **`startup: "lazy"` on a server supplied through
  `agent.WithMcpServers` silently defeats `StrictMcpStartup`**, breaking README invariant 9
  ("Laziness must not convert a strict failure into a silent one") and the parameters-table
  startup-mode row, which states the default is "`lazy` after phase 3 flips it, **except `eager`
  for a server supplied through `agent.WithMcpServers` while strict startup is on**" — an
  unconditional exception, not a default. `effectiveStartupMode`
  (`internal/text/querier_setup_tools.go:82-90`) checks `server.Startup != ""` *first*, so the
  config field overrides the strict exception. With a warm cache,
  `resolveLazyServerViaCache`'s hit branch (`:302-307`) registers the tools against an unresolved
  `Connector` and returns `nil`: setup never connects, so no strict failure can exist to report.
  Verified by probe: strict explicit server, `Startup: StartupLazy`, shared cache —
  `setupMcpManager` returned `err=<nil>`, 2 tools, **0 process spawns** on both the cold and the
  warm run. An agent caller is then told its required server is present while the first tool call
  returns `ERROR: mcp server … failed to start` as a model-visible string.
  Corrective action: in `effectiveStartupMode`, return `StartupEager` when `strictExplicit` is
  true regardless of the configured field (and say so in the doc comment), or make a configured
  `lazy` under strict startup a parse error. Then add the missing test (below).
  **Fixed (D35):** `effectiveStartupMode` (`internal/text/querier_setup_tools.go`) now checks
  `strictExplicit` unconditionally, after validating the configured value and before the unset
  default, so no configured value — valid or not — can override it. Regression:
  `TestLazyConnectHonoursStrictMcpStartupForExplicitServers/configured_lazy_under_strict_startup_still_resolves_eagerly_even_with_a_warm_cache`
  (`querier_setup_tools_test.go`) seeds a warm cache via `schemacache.Capture` for the exact
  identity an explicit, strict, `startup:"lazy"` server with an unreachable command would
  produce, then asserts `setupMcpManager` still returns a typed `ErrMcpServerStartup` rather than
  `nil`. Verified failing against the pre-fix `effectiveStartupMode` (checked out from `/tmp`,
  then reverted) and passing against the fix.
- [x] **R1-25** (major) — **the test for the invariant above cannot fail.**
  `TestLazyConnectHonoursStrictMcpStartupForExplicitServers`
  (`internal/text/querier_setup_tools_test.go:613-631`) pins
  `Startup: pub_models.StartupEager` in its own config literal, so it bypasses
  `effectiveStartupMode`'s strict branch entirely and would pass unchanged if that branch returned
  `StartupLazy`. Its own comment claims the opposite — "still resolves during setup **when its
  startup mode is unset**" — which the struct literal contradicts. Same class, two more rows:
  `TestConnectorConnectFailureIsRunScopedAndNotRetried` (`internal/tools/mcp/connector_test.go:111-128`)
  counts *dials against a fake closure* on the unexported `newConnector`, although the limits
  table specifies "Fake server whose spawn always fails; call the tool more than once and **count
  spawns**" — had it counted spawns through the tool-call site, it would have caught R1-01; and
  `TestThreeCallsOneServerSpawnOnce` (`:89-105`), cited in the README Definition of success as the
  evidence that "Three tool calls to one server in one batch create exactly one process", starts
  no process and runs no tool batch.
  Corrective action: drop the `Startup` pin from the strict test and add a second case with
  `StartupLazy` plus a warmed cache asserting `errors.Is(err, claierr.ErrMcpServerStartup)`; count
  real spawns (the `TEST_SERVER_SPAWN_LOG` fixture phase 3 added already does this) through
  `toolExecutor.invokeToolCall` in the retry-count and batch rows.
  **Fixed:** the strict-startup test is now two subtests, neither pinning `Startup` on the config
  the strict branch itself must resolve (one leaves it unset, the other configures `lazy` plus a
  warmed cache — see R1-02 above). The retry-count and batch claims move to a new end-to-end test,
  `TestConnectorConnectFailureSpawnsOnceAcrossRepeatedToolCallsEndToEnd`
  (`querier_setup_tools_test.go`): a real `mcp.NewConnector` over the testserver fixture
  (`TEST_SERVER_EXIT=1`, so the process spawns and exits before answering `initialize`) wired into
  a real `mcp.NewTool`, called three times through `toolExecutor.invokeToolCall` (the production
  dispatch `tools.InvokeWith` sits behind), asserting `TEST_SERVER_SPAWN_LOG` has exactly one line
  across the three calls. Verified failing (6 spawns, i.e. a retry per call) against a
  deliberately reverted `resolve` that drops the terminal-failure memoisation, and passing against
  the shipped code. `TestConnectorConnectFailureIsRunScopedAndNotRetried`
  (`internal/tools/mcp/connector_test.go`) is left in place: it is still the correct unit-level
  proof of the connector's own single-flight memoisation (the property its own row names), and the
  new test is what closes the gap the finding actually identifies — a claim with no real-spawn
  evidence.
- [x] **R1-14** (major, shared with phases 3 and 4) — **`connect_timeout_seconds` has no effect on
  any cache-miss path**, although the parameters table defines the connect-bound as the bound
  "which wraps spawn plus handshake" and this phase owns it. `connectorOptsFor`
  (`internal/text/querier_setup_tools.go:347-352`) is passed only in the cache-**hit** branch
  (`:304`). The miss branch calls `mcp.NewStdioConn` with no options and then bounds only the
  handshake with `mcp.HandshakeBoundOf(conn)` (`:319-325`), which returns the 30 s
  `mcpHandshakeBound` default; the spawn itself is bounded by nothing. The HTTP miss path is the
  same (`internal/text/mcp_oauth.go:171-175`), and `HttpConn` implements no `HandshakeBound()` at
  all, so it also gets 30 s. Scenario: `{"url": "…", "connect_timeout_seconds": 5}` against an
  endpoint that accepts TCP and never answers `initialize` blocks cold setup for 30 s, not 5. No
  test covers it: `TestConnectBoundExpiryReturnsTypedStartupError` and
  `TestHttpConnectBoundExpires` both drive `NewConnector`/`NewHttpConnector` directly, which the
  miss paths never use.
  Corrective action: route both miss paths through the Connector, or thread
  `connectorOptsFor(server)`'s bound into them as an outer `context.WithTimeout` wrapping spawn
  plus handshake.
  **Fixed, stdio half only — the HTTP half is a separate, still-open obligation under phase 5.**
  `mcp.ConnectBoundOf` (exported, `internal/tools/mcp/connector.go`) centralises the
  connect_timeout_seconds-or-default resolution `NewConnector` already used internally;
  `mcp.ReportAsConnectStage` (renamed from the private `reportAsConnectStage`) is reused rather
  than duplicated. `resolveLazyServerViaCache`'s miss branch now wraps `mcp.Handshake` in
  `context.WithTimeout(ctx, mcp.ConnectBoundOf(server))` instead of
  `mcp.HandshakeBoundOf(conn)`, and renames an expiry to the `"connect"` stage. Regression:
  `TestLazyCacheMissConnectBoundAppliesToStdioHandshake`
  (`internal/text/schema_cache_setup_test.go`) configures a 1 s `connect_timeout_seconds` against
  `TEST_SERVER_AUTH_HANG` (spawns, never answers `initialize`) and asserts the whole call returns
  in well under the 30 s handshake-bound default. Verified failing — the unfixed path hangs for
  the full handshake bound and the test's own 8 s harness timeout panics first — and passing
  against the fix (~1 s). The HTTP miss path (`handshakeHttpServerWithAuth` /
  `internal/text/mcp_oauth.go`, phase 5's file per the code-layout table) still bounds only the
  handshake; it is untouched by this session and remains this finding's open half. Phase 5's own
  copy of R1-14 must close it; this phase file checks R1-14 only for the obligation that is
  actually phase 2's — the connect-bound parameter and its command-based (stdio) path.

### Review 2, 2026-10-02 — implementation review, round 2

Status: **Reopened (review 2)**, in addition to the round-1 reopening. One blocker.

Round 1 concentrated on the connector's memoisation and the strict-startup exception. Round 2
looked at what the loop around it does, and at the SDK-facing half of the startup field.

**Verified good:**

- The connector's single-flight and memoisation logic is correct on every branch: a caller that
  abandons its wait neither cancels the resolution nor retries it (`connector.go:73-96`), and
  `resolve` publishes to the shared `resolution` only after updating the memo under `mu`
  (`:98-114`).
- `dialStdio` reaps the process on every failure branch through one conditional defer
  (`connector.go:184-188`) and leaves it alive only on success. No failure path leaks a child.
- `setupTooling` is reached from exactly one call site (`querier_setup.go:246`), so a chat session
  does not re-run MCP setup per turn and cannot accumulate processes.

**Findings:**

- [x] **R2-02** (blocker, shared with phase 3) — **Cold-cache lazy setup resolves servers serially;
  the eager path it replaced resolved them concurrently.** `querier_setup_tools.go:174-236`: the
  lazy branch calls `resolveLazyServerViaCache` / `resolveLazyHttpServerViaCache` inline in the
  per-server `for` loop and only then `toolWg.Done()`. The eager branch instead hands the server to
  `mcp.Manager`, which starts one goroutine per `ControlEvent` (`manager.go:51-63`), so eager
  servers spawn and handshake in parallel. A cache miss is therefore *not* "exactly as today"
  (D18) — it is as today, one server at a time.

  Probe (fake stdio server with a 1 s pre-handshake delay, four servers with distinct identities,
  cold cache, `setupMcpManager` timed directly):

  | configuration | elapsed |
  | --- | --- |
  | `startup: lazy`, 1 server | 1.01 s |
  | `startup: lazy`, 4 servers | **4.05 s** |
  | `startup: eager`, 4 servers | 1.01 s |

  With the worklog's own measured `npx` cost (4.0 to 4.5 s per server) and its own representative
  six-server agent, first-run setup goes from roughly 4.5 s to roughly 27 s. The failure case is
  worse than the success case: each miss is bounded by the handshake bound
  (`querier_setup_tools.go:324`, 30 s), so N unreachable servers stall setup N × 30 s where
  `handleServer` bounded them concurrently at 30 s total. Every user's first run after upgrading
  takes this path, as does every run after a server changes.

  Note the probe also surfaced, and then excluded, a confound worth recording: four servers with
  *identical* command, args and env share one cache entry, because the identity is content-
  determined and carries no server name. The first missed and captured, the other three hit within
  the same setup run, and the run took 1.01 s. That is correct behaviour, not a defect, but it will
  make a naive repro look like the serialisation is absent.

  Corrective action: give the lazy branch the same goroutine-per-server shape Manager already uses,
  joining through the existing `toolWg` before the `select` at `:244`. `explicitFailures` then needs
  a mutex or a channel, since `classifyServerFailure` currently appends from the loop body.
  **Fixed (D39):** the lazy branch in `setupMcpManager` now launches one goroutine per server,
  joined through the pre-existing `toolWg` exactly as `mcp.Manager` already does for an eager
  server. `classifyServerFailure` is replaced by `failureCollector`, a small mutex-guarded
  accumulator (`addIfExplicit`/`addExplicit`/`all`), since the lazy branch's failures and the
  startup-mode validation failures (see R2-15) can now land concurrently. Regression:
  `TestLazySetupResolvesServersConcurrently` (`querier_setup_tools_test.go`) configures four
  ambient lazy servers, each with a distinct identity (a different `TEST_SERVER_SPAWN_LOG` path)
  and a shared 600 ms `TEST_SERVER_PRE_HANDSHAKE_DELAY_MS` (new fixture knob,
  `internal/tools/mcp/testserver/main.go`), and asserts the four recorded spawn timestamps spread
  by under 1.5 s — serial resolution has a 1.8 s floor (3 × 600 ms), concurrent resolution clusters
  well under it. The fixture now points at a binary built once by `go build`
  (`testServerBinary`/`sync.Once`) rather than `go run`, so the bound never encloses a compile
  (D41). Verified failing (1.81 s spread) against the pre-fix inline loop and passing (≈0.86 s)
  against the fix.

- [x] **R2-07** (major, shared with phase 4) — **`agent.WithMcpServers` entries never pass transport
  validation.** `querier_setup_tools.go:145` appends `userConf.McpServers` straight onto the parsed
  list; `serverconfig.FindConfiguredServers` (and so `validateTransport`,
  `serverconfig/serverconfig.go:73-86`) runs only over the config-directory glob. Consequences at
  `:177`, `isHTTP := mcpServer.Url != ""`: a struct with **both** `Command` and `Url` set silently
  resolves as HTTP and ignores the command; a struct with **neither** set takes the stdio branch
  into `exec.CommandContext(ctx, "")` (`conn_stdio.go:118`) and fails at `cmd.Start()` with an
  opaque error naming no field. The XOR that `pub_models.McpServer.Url`'s own doc comment asserts is
  enforced on the file path and unenforced on the public path. Corrective action: export
  `serverconfig.ValidateTransport` and run it over each `userConf.McpServers` entry before the
  append, returning a typed `claierr.McpServerStartupError` so a strict run fails at `Setup`.
  **Fixed (D43):** `serverconfig.ValidateTransport` exports the existing `validateTransport` under
  an identifier the caller supplies (a server name, in place of a config file's path). A new
  `validateExplicitServers` (`internal/text/querier_setup_tools.go`) runs it over every
  `userConf.McpServers` entry before any of them join `mcpServers`; a failure is routed by the
  same explicit/ambient posture as a startup failure — joined for a strict run, warned for an
  ambient-posture one — and the server never reaches the manager. Regressions:
  `TestExplicitServerBothCommandAndUrlFailsValidation` asserts the returned
  `*claierr.McpServerStartupError.Stage == "config"` under strict startup (not just
  `errors.Is(ErrMcpServerStartup)`, since a `url` set alongside `command` still resolves as HTTP
  and a real failed connect attempt also produces that sentinel — the stage is what actually
  discriminates validation from a failed connect, R1-25's class);
  `TestExplicitServerNeitherCommandNorUrlFailsValidation` asserts the warned message under an
  ambient-posture run names the validation failure specifically (`sets neither "command" nor
  "url"`), not `exec: no command`, which an unvalidated empty `Command` would also produce.
  Verified both failing against a reverted `validateExplicitServers` call (one asserting the
  wrong stage, `"initialize"`; one asserting the wrong message) and passing against the fix. The
  quoted message was updated in the phase 4 fix session, 2026-10-03, when R2-18 gave the XOR's two
  branches distinguishable text instead of one shared string.

- [x] **R2-13** (major, shared with phase 3) — **Readiness checklist item 9 is false, and its own
  verification command cannot detect that.** The checklist claims the two spawning test files pin
  the eager posture explicitly. In `internal/text/querier_setup_tools_test.go` three tests reach
  `setupMcpManager` with `startup` unset and so now run the *lazy* branch:
  `Test_setupMcpManager_SpawnFailureSkipsServer` (`:244-262`, config at `:246`),
  `Test_setupMcpManager_StrictModeKeepsAmbientDegrade` (`:433-451`, config at `:435`), and the
  `"strict explicit spawn failure stays typed"` case (`:550-564`, config at `:552`). A fourth,
  `TestLazyConnectDegradesForAmbientServers` (`:589-607`), pins `"startup":"lazy"` at `:591` and
  spawns a real `go run` testserver, which contradicts the sentence outright. The prescribed check,
  `grep -n 'startup' <the two files>`, finds nothing for the three unset cases because there is no
  token to find, and prints the fourth without flagging it. Corrective action: restate the item as
  what it can actually verify — every `setupMcpManager` test that spawns names its posture
  explicitly, eager or lazy — and give it a check that enumerates the tests rather than greps for a
  word.
  **Fixed:** the three tests that left `startup` unset now name a posture explicitly —
  `Test_setupMcpManager_SpawnFailureSkipsServer` and `Test_setupMcpManager_StrictModeKeepsAmbientDegrade`
  pin `"startup":"eager"`, matching what they were already written to exercise (the `mcp.Manager`
  spawn-failure path, not the cache-aware lazy one); the `"strict explicit spawn failure stays
  typed"` case is left unset deliberately, with a comment explaining it is itself the assertion
  that D35's strict-explicit exception resolves eager with nothing configured. The readiness
  checklist item itself (README, item 9) is restated below rather than fixed only in this phase
  file, since its wording and its verification command are both README-owned and the defect is
  cross-phase (see the README Strategy update in this session's delta).

- [x] **R2-15** (minor) — **`StartupMode` is validated only on the JSON path.**
  `pkg/text/models/tools.go` gives it an `UnmarshalJSON` that rejects anything but `eager` and
  `lazy`, but no constructor and no `Valid()`. `effectiveStartupMode`
  (`querier_setup_tools.go:82-90`) returns `server.Startup` verbatim and the single consumer at
  `:178` tests `== pub_models.StartupLazy`, so **any** unrecognised value falls into the eager
  branch. `Startup` is an exported field on a struct the public `agent.WithMcpServers` takes, so
  `models.McpServer{Startup: "LAZY"}` from Go is silently eager with no error and no test.
  Corrective action: per this repository's "encode any failure in expectation as an error" rule,
  switch exhaustively in `effectiveStartupMode` and return a typed error on the default branch.
  **Fixed:** `effectiveStartupMode` now returns `(pub_models.StartupMode, error)` and switches
  exhaustively over `server.Startup` (`"", StartupEager, StartupLazy` as the only accepted cases)
  before applying the strict-explicit override or the unset default, returning a
  `claierr.NewMcpServerStartup(name, "startup-mode", …)` on any other value — checked first, so a
  strict caller handing in garbage still gets the typed error rather than a silent eager. The sole
  call site (`setupMcpManager`'s per-server loop) routes a non-nil error through the same
  `failureCollector` every other startup failure uses. Regression:
  `TestEffectiveStartupModeRejectsUnrecognisedProgrammaticValue`
  (`internal/text/schema_cache_setup_test.go`) constructs `pub_models.McpServer{Startup:
  pub_models.StartupMode("LAZY")}` directly (bypassing `UnmarshalJSON` entirely, as
  `agent.WithMcpServers` would) and asserts `errors.Is(err, claierr.ErrMcpServerStartup)` both
  with and without `strictExplicit`. `TestStartupDefaultIsLazyAfterCache` is updated for the new
  two-return signature and for D35 (see R1-02).
