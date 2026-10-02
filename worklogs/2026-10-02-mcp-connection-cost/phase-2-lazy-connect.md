# Phase 2 — Lazy connect

**Status:** Not Started

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
| Setup of a server explicitly marked lazy | Constructs a `Connector`, no transport, no process, and contributes no tools until the cache phase | `TestConnectorLazyDoesNotConnectDuringSetup` |
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

Not started.

## Review findings

None.
