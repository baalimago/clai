# Phase 1 — Conn seam and JSON-RPC demux

**Status:** Complete

Back to [README](README.md).

## Goal

Replace the exported channel pair with a `Conn` that owns one MCP session's id sequence and pending
waiters, eliminating the two live demux defects and giving every later phase a single seam.

## Specification

### What is wrong today

`internal/tools/mcp/Client` returns `(chan<- any, <-chan any, error)`. All tools discovered from one
server share that one pair, and each `mcpTool` keeps its own `seq` counter at
`internal/tools/mcp/tool.go:31`. Two consequences, both live:

- Two tools of the same server both issue their first request with the same id, and
  `handleServer` has already used ids for `initialize` and `tools/list` on that same pair.
- The receive loop at `internal/tools/mcp/tool.go:118` discards a frame whose id does not match the
  caller's own, so one caller can consume and drop another's response, leaving the other waiting
  until its context expires.

Both defects are reachable today even without concurrency: `handleServer` issues `initialize` with
id one and `tools/list` with id two on the same channel pair, and each `mcpTool`'s first call also
issues id one, so a server that answers a late handshake frame can satisfy a tool call and vice
versa. Response stealing additionally needs two callers on one connection, which
`toolExecutor.runPlannedCall` in `internal/text/tool_executor.go` does not produce today because it
executes a tool batch in a plain loop with no goroutine. This phase lands first because every later
phase consumes the seam it establishes, not because a later phase introduces concurrency.

### The seam

Introduce the `Conn` and `Connector` interfaces exactly as declared in the README's shared
interfaces section. Add a stdio implementation that owns:

- a connection-wide monotonic id source, shared by the handshake and by every tool
- a map from pending id to its waiter, so a response is delivered to its own caller
- the **frame reader** goroutine, which is the only reader of the process's standard output and the
  only producer of demuxed responses
- the **stderr reader** goroutine, which is a separate reader and feeds `ServerLogSink` line by
  line. It is not part of the demux and never sees a JSON-RPC frame. It must be carried over
  deliberately: the authorization prompts a later phase surfaces arrive here, not in the frame
  stream, and the bounded crash tail depends on it
- the stdin closer, which closes the process's standard input when the context ends
- the reaper, which waits for the process only after the stderr reader has finished, so a crash tail
  is complete before an exit is reported, and which does not gate on the frame reader
- the handshake, bounded by the handshake-bound parameter, relocated from the package-level
  constant onto the connection

The handshake advertises the protocol-version parameter. That is a change: the live code sends an
older version in `handleServer`, and both transports must advertise the same one, so the value is
carried on the connection here rather than restated by the transport phase.

`Notify` sends a notification and carries no id. `Close` is idempotent and fails every pending
waiter with a typed error rather than leaving it to a context deadline.

"Cannot be parsed as JSON-RPC" means precisely one of two things: the line's bytes are not valid
JSON, or they are valid JSON that is not an object carrying a `jsonrpc` member. Both carry no usable
id. This is distinct from a frame that parses correctly and carries an id nobody is waiting for,
which is dropped; the two cases must not share a code path.

A frame that cannot be parsed as JSON-RPC at all carries no id, so it cannot be routed to one
waiter. The rule is therefore explicit: an unparseable frame fails **every** pending waiter on that
connection with the same typed error and leaves the connection usable, because attributing it to one
arbitrary caller would silently corrupt the others. The existing code can speak of "the awaiting
call" only because `tool.go` runs its receive loop inside one caller; a connection-wide demux cannot.

The per-server override `ControlEvent.StartupTimeout` is retired by this phase. It is a test-only
injection point today, consumed in `handleServer`, and its replacement is the handshake bound carried
on the connection. Exactly one site sets it, `internal/tools/mcp/manager_test.go`, and migrating that
one test is this phase's work rather than a later surprise.

A frame whose id matches no pending waiter is dropped after being counted; it is not an error for
the connection, because a late response to an abandoned call is normal. A server-initiated request,
one carrying both a `method` and an `id`, is answered with a JSON-RPC method-not-found error rather
than ignored, so a server that asks for `roots`, `sampling` or `elicitation` receives a definite
answer instead of stalling. clai advertises no such client capability in `initialize`, so a
well-behaved server never asks; the row exists because the probe recorded in the README shows
servers that ask regardless.

### Invariants

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| Handshake `initialize` | Takes its id from the connection id source | `TestConnCallAssignsUniqueIDsAcrossTools` |
| Handshake `tools/list` | Takes its id from the connection id source | `TestConnCallAssignsUniqueIDsAcrossTools` |
| Every `tools/call` from every tool of the server | Takes its id from the connection id source; no per-tool counter exists | `TestConnCallAssignsUniqueIDsAcrossTools` |
| Every response frame | Delivered to the waiter registered for its id, never to another | `TestConnCallDeliversResponseToMatchingWaiter` |
| Two concurrent calls on one connection | Each receives its own response; neither is dropped | `TestConnConcurrentCallsDoNotStealResponses` |
| The stderr reader | Feeds `ServerLogSink` unchanged; carries no JSON-RPC frame | `TestStdioConnStderrStillFeedsSink` |
| A frame with no pending waiter | Dropped without failing the connection | `TestConnUnknownIDFrameIsDropped` |
| An unparseable frame, which has no id | Fails every pending waiter with one typed error; connection stays usable | `TestStdioConnDecodeFailureFailsAllPendingWaiters` |
| A server-initiated request | Answered with a method-not-found error | `TestConnServerToClientRequestIsRejectedWithMethodNotFound` |
| A notification | Carries no id and registers no waiter | `TestConnNotifyCarriesNoID` |
| `Close` | Fails every pending waiter with a typed error, idempotently | `TestConnCloseUnblocksPendingCalls` |

### Limits

| Limit | Injectable field | README parameter | How a test triggers it |
| --- | --- | --- | --- |
| Handshake wait | Connection field | handshake-bound parameter | Fake stdio server that starts and never answers `initialize` |
| Read bound per message | Connection field | stdio-read-bound parameter | Fake stdio server emitting a frame one byte past the bound |
| Single-call wait | Pre-existing per-server config field | single-call bound parameter | Fake stdio server that answers the handshake then stalls a call, asserted by `TestStdioConnSingleCallBoundExpires` |
| Advertised protocol version | Connection field | protocol-version parameter | Fake stdio server asserting the version in the received `initialize`, by `TestConnAdvertisesProtocolVersion` |

### Scope boundaries

The concurrent-calls invariant above is a defensive contract, not a reachable production path: the
tool executor runs a batch in a plain loop with no goroutine, so nothing in this repository issues
two simultaneous calls on one connection today. Its test drives the connection directly. It is
specified because the seam is package-public and because any future concurrent caller would
otherwise reintroduce exactly the defects this phase removes.

Behaviour visible to a user does not change in this phase. Servers are still discovered and
connected during setup, still eagerly, and the ambient-degrades and explicit-fails posture in
`setupMcpManager` is preserved exactly. Only the internal seam moves.

`internal/text/mcp_log_sink.go` keeps its `ServerLogSink` contract unchanged; the stdio
implementation keeps feeding it, including the exit-tail behaviour that distinguishes an unexpected
termination from a context-cancelled teardown.

### Documentation

Create `architecture/mcp.md` describing the connection model and the demux contract, and reduce the
MCP sections of `architecture/tooling.md` to a pointer at it, per decision D11. The error-handling
note `architecture/errors.md` gains the new typed error introduced here if it enumerates typed
errors.

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Setup with one configured stdio server | In-repo fake stdio server, existing `internal/tools/mcp/testserver` | Its tools are registered with the `mcp_<server>_<tool>` prefix | One process started, handshake completed | No change to the registered tool names |
| Two tools of one server called in sequence | Fake stdio server echoing request ids | Each call returns its own result | Both calls observed by the server with distinct ids | Neither call observes the other's id |
| Two calls issued concurrently on one connection | Fake stdio server that answers out of order, second request first | Both callers receive their own result | Both responses delivered | No response dropped, no caller left waiting |
| Server sends a frame with an id nobody awaits | Fake stdio server emitting an unsolicited response | Connection stays usable for the next call | Frame counted and dropped | Connection not closed, no error surfaced to a caller |
| Server sends a `roots/list` request | Fake stdio server requesting roots after initialize | Server receives a method-not-found error response | Error response written | No hang, no client capability advertised |
| Run context cancelled mid-call | Fake stdio server that never answers | Pending call returns a typed error | Process reaped, pending waiters failed | No goroutine left blocked on the transport |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| One id source serves the handshake and every tool of a connection | `TestConnCallAssignsUniqueIDsAcrossTools` |
| A response reaches the caller that issued its id | `TestConnCallDeliversResponseToMatchingWaiter` |
| Concurrent calls on one connection never steal each other's responses | `TestConnConcurrentCallsDoNotStealResponses` |
| An unawaited frame does not break the connection | `TestConnUnknownIDFrameIsDropped` |
| A server-initiated request receives a definite error answer | `TestConnServerToClientRequestIsRejectedWithMethodNotFound` |
| A notification carries no id | `TestConnNotifyCarriesNoID` |
| Close fails pending waiters instead of leaking them | `TestConnCloseUnblocksPendingCalls` |
| The handshake is bounded by the handshake-bound parameter and returns a typed error on expiry | `TestConnHandshakeTimeoutReturnsTypedError` |
| Requests reach the process stdin encoded as JSON-RPC | `TestStdioConnEncodesRequestsToProcessStdin` |
| A frame that cannot be parsed is an error for its call, not a dropped frame | `TestStdioConnSurfacesDecodeFailureAsCallError` |
| An unparseable frame fails every pending waiter | `TestStdioConnDecodeFailureFailsAllPendingWaiters` |
| The single-call bound expires with a typed error | `TestStdioConnSingleCallBoundExpires` |
| The handshake advertises the protocol-version parameter, not the older live value | `TestConnAdvertisesProtocolVersion` |
| The retired per-server startup override has no remaining caller | `go build ./...` plus `go vet ./...` from the phase 8 gate table: the field is deleted, so any remaining caller fails to compile |
| Tool registration still happens through the manager and still uses the existing prefix | `TestManagerRegistersToolsThroughConn` |
| Server stderr still reaches the log sink, including the crash tail | `TestStdioConnStderrStillFeedsSink` |
| Close is idempotent | `TestConnCloseIsIdempotent` |
| No goroutine leaks across a connection lifecycle | Existing race gate, invoked from the phase 8 gate table |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Process cannot be started | Typed startup error naming the server and the spawn stage | `TestStdioConnSpawnFailureReturnsTypedStartupError` |
| `initialize` returns a JSON-RPC error | Typed startup error naming the server and the initialize stage | `TestConnInitializeRpcErrorIsTypedStartupError` |
| `initialize` never answers | Typed startup error on expiry of the handshake bound | `TestConnHandshakeTimeoutReturnsTypedError` |
| `tools/list` returns an undecodable result | Typed startup error naming the tools-list stage | `TestConnToolsListUndecodableResultIsTypedStartupError` |
| A response frame is not valid JSON-RPC, one waiter pending | That call returns an error; the frame is not logged and dropped | `TestStdioConnSurfacesDecodeFailureAsCallError` |
| A response frame is not valid JSON-RPC, several waiters pending | All of them fail with the same typed error; connection stays usable | `TestStdioConnDecodeFailureFailsAllPendingWaiters` |
| Transport closes while a call is pending | The call returns a typed connection-closed error | `TestConnCloseUnblocksPendingCalls` |
| `Close` called twice | Second call is a no-op and returns no error | `TestConnCloseIsIdempotent` |
| Server writes a message larger than the read bound | The oversized frame fails its call with a bounded error, the connection survives | `TestStdioConnOversizedFrameIsBoundedError` |

## Implementation notes

Session: executor agent, 2026-10-02T15:[22-50]Z.

**Documentation scope-out.** Partway through this session the coordinating
session took `architecture/mcp.md`, the `architecture/tooling.md` pointer
reduction, and the `architecture/errors.md` typed-error row for itself and
instructed this execution not to touch any file under `architecture/`. No
file under `architecture/` was created, edited, or reverted by this session.
The phase's documentation requirement is therefore **not satisfied by this
implementation note** — it is satisfied (or not) by whatever the
coordinating session produced outside this phase file. Flagging this
explicitly so the requirement is not silently dropped between the two
sessions.

**Design decisions made while implementing** (the spec fixed the seam and
the invariants; these are the choices needed to make it buildable):

- `NewStdioConn` spawns the process and wires the transport only; it
  performs **no handshake**. `handleServer` still drives `initialize` /
  `notifications/initialized` / `tools/list` itself, now through
  `Conn.Call`/`Conn.Notify` instead of raw channels. This keeps spawn and
  handshake as two distinct stages exactly as today — spawn failures still
  surface synchronously in `querier_setup_tools.go`, handshake failures
  still surface asynchronously through `Manager`'s `startupFailures` channel
  — which is what phase 2's parameter row ("connect-bound... wraps spawn
  plus handshake") presupposes still being separate in phase 1.
- The retired `ControlEvent.StartupTimeout` is replaced by
  `StdioConnOption` `WithHandshakeBound`, a construction-time field on the
  connection. `handleServer` reads it back through an unexported
  `handshakeBounder` optional-interface check (`HandshakeBound() time.Duration`),
  the same feature-detection pattern this package already uses for
  `ServerLogSink`'s optional `setupSucceeded` method. `ControlEvent` now
  carries `Conn Conn` instead of `InputChan`/`OutputChan`/`StartupTimeout`.
- `mcpTool` holds `conn Conn` directly, not yet a `Connector` — the README's
  shared-interfaces section and V3-11 are explicit that phase 2 is what
  swaps the field to a `Connector`, "in place of the `Conn` the previous
  phase gave it."
- Two new typed errors, not three: `claierr.ErrMcpConnClosed` /
  `McpConnClosedError`, and `claierr.ErrMcpFrameUndecodable` /
  `McpFrameUndecodableError`. The oversized-frame "bounded error" the limits
  table calls for reuses `McpFrameUndecodableError` with a
  bound-exceeded `Cause` rather than minting a third type: both are the same
  terminal meaning ("a received frame could not be used"), differentiated
  only by the wrapped cause.
- `architecture/mcp.md`, the `tooling.md` pointer, and the `errors.md` row
  are intentionally **not** in this session's diff (see above).

**A real race found and fixed under `-race`, not spec-mandated.** The first
`Close` implementation also closed the process's stdin pipe synchronously.
Under `-race` this raced a `Call` that was still mid-write: `Close` could
fail the pending waiter with `McpConnClosedError` *and* close stdin before
that `Call`'s own write returned, so the write itself then failed with a raw
`io: read/write on closed pipe` error that reached the caller instead of the
typed one (`TestConnCloseUnblocksPendingCalls` caught this directly: `err =
... want ErrMcpConnClosed`). Fixed by making `Close` only fail pending
waiters and mark the connection closed — it no longer touches stdin at all;
that remains the dedicated stdin-closer goroutine's job, driven by the run
context, exactly as the phase's goroutine enumeration separates the two
concerns. `Call` also now normalizes a write failure to the typed
`McpConnClosedError` when it observes `closed` is true, belt-and-suspenders
against the same race by any other path.

**Deviations/simplifications from the old implementation, recorded so a
later phase doesn't rediscover them as bugs:**

- The old stdout reader needed a `waitDone` channel solely so an unbuffered
  channel send to a tool's output channel could never block past process
  death. The new demux delivers to per-call buffered(1) channels (or drops
  an unmatched id), so that failure mode is structurally gone; `waitDone` is
  not reintroduced. The former `TestClient_ServerExitedWithUnconsumedStdout`
  scenario (one unconsumed notification blocking the reader forever) has no
  analogue to port for the same reason — noted rather than silently dropped.
- Dropped the `debugflags.Enabled("CALL")`/`"MCP_TOOL")` tracing that lived
  in the old `tool.go`; grepped the repository first — nothing outside that
  file referenced either flag, and the new seam has no per-tool state left
  to log against.
- `internal/tools/mcp/testserver/main.go` gained one addition beyond what
  phase 1 needed to delete: the `initialize` handler now rejects a
  **present-and-wrong** `protocolVersion` (anything other than
  `2025-06-18`) while still accepting a **missing** one leniently, so every
  pre-existing raw-request test (built without a `protocolVersion` field)
  is unaffected. This is what makes `TestConnAdvertisesProtocolVersion` a
  real assertion instead of a vacuous one. No new fixture package was
  introduced, per the README's fixture-ownership table.
- Most new `Conn`-level tests use an in-process `io.Pipe` double (via the
  package-internal `newStdioConn`/`stdioTransport` seam) rather than a
  spawned subprocess, for determinism and speed; the real `go run
  ./testserver` subprocess is reserved for genuinely process-level behaviour
  (stderr/exit reporting, spawn failure, protocol-version enforcement,
  end-to-end stdin encoding, the single-call bound). This is ordinary test
  infrastructure internal to the package, not a new fixture package.

**Surprise: a pre-existing test's timing budget, exposed by host load, not
a regression.** `TestManager`'s 20×50 ms (1 s) poll for tool registration —
unchanged from the prior implementation — failed once during this session
under an observed host load average of 15–23 on a 22-core box. Isolated
reruns of the same package passed cleanly every time (see verification
below); this matches the repository's documented host-load sensitivity
(`race-gate-is-host-load-sensitive`) and is not attributable to this
phase's changes.

**Verification run** (2026-10-02, this session):

```
go build ./...
go vet ./...
go run mvdan.cc/gofumpt@latest -l .     # no output: clean
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # no output: clean
go fix ./...                             # no changes
go run github.com/mibk/dupl@latest -t 80 .   # 34 pre-existing clone groups, none touching new/changed mcp files
go test ./internal/tools/mcp/... -race -cover -count=3 -timeout=30s
  # ok  internal/tools/mcp            21.956s  coverage: 81.3%  (repeated 4x clean across the session)
  # ok  internal/tools/mcp/testserver  1.303s  coverage: 60.0%
go test ./... -race -cover -count=3 -timeout=30s
  # 45 ok, 0 fail on a quiet host (load avg ~6-13 one-minute); two unrelated
  # packages (root `clai`, `internal/audio` — both untouched by this diff)
  # timed out on a separate run taken during a load-avg 15-23 spike, then
  # passed cleanly in isolation and in a second full-suite run; see above
```

All twenty test names the phase declares exist and pass: every invariant,
limit, acceptance, and error-coverage row is covered by name. Coverage for
the touched package: 81.3-83.4% across runs (floor met; `make qa`'s listed
preferred figure of 90%+ not reached on `tool.go`'s untouched error-path
branches already excluded from this phase's scope).

## Review findings

### Review 1, 2026-10-02 — implementation review

Status unchanged: **Complete**. No material finding against this phase's own contract. The two
findings below are minor and non-blocking; the seam itself holds.

**Verified good, traced through every branch rather than read off the notes:**

- One id source serves the handshake and every tool. `StdioConn.Call` (`internal/tools/mcp/conn_stdio.go:214-224`)
  is the only writer of `nextID`, under `c.mu`, and `mcpTool` carries no counter of its own. The
  retired `ControlEvent.StartupTimeout` has no remaining reference anywhere.
- Demux correctness on every failure branch. An unparseable frame and a frame with no `jsonrpc`
  member both reach `failAllPending` and leave the connection open (`conn_stdio.go:345-354`); a
  frame whose id matches no waiter is dropped in `deliver` without touching the connection
  (`:397-406`); `Close` is idempotent and drains `pending` exactly once (`:268-284`); and
  `readFrames`' `defer c.Close()` (`:323`) guarantees pending waiters fail when the transport ends
  rather than hanging on a context deadline. Every waiter channel is buffered(1) and every send
  happens after the id has been removed from the map, so no send can block and no waiter can be
  delivered twice.
- The goroutine enumeration matches the code: four goroutines, the reaper gated on the stderr
  reader and not on the frame reader (`:196-208`), and the stdin closer is the only thing that
  touches stdin — which is what makes the race recorded in the implementation notes genuinely
  fixed rather than papered over.
- A server-initiated request **is** answered with `-32601` on this transport (`:356-376`). The
  HTTP transport does not; that is filed against phase 4 as R1-13, not against this phase.
- `go build ./...` and `go vet ./...` are clean, so the retired-override acceptance row holds.

**Findings**

- [ ] **R1-26** (minor) — `WithAuthPendingSink` (`internal/tools/mcp/conn_stdio.go:70`) is dead:
  definition plus doc comment, zero call sites in the repository including tests. Its sibling
  options in `conn_http.go` are in the same state and are filed with phase 4. `staticcheck` does
  not flag exported symbols, so the gate cannot catch this. The doc comment also describes a
  fallback that is in fact the only path taken. Either wire it or delete it; an exported option
  nothing constructs is API surface the next contributor must reason about for nothing.
- [ ] **R1-31** (note) — the integration-contract row "Server sends a frame with an id nobody
  awaits → Required side effects: **Frame counted** and dropped"
  (phase-1 integration contract) has no implementation: `deliver` (`conn_stdio.go:397-406`) drops
  silently and no counter exists on `StdioConn`. `TestConnUnknownIDFrameIsDropped` asserts only the
  drop. Either add the count as a typed field (the code-style rule forbids a naked int beside a
  value, so it would need a named type) or strike the word from the row. Striking it is the
  recommendation: nothing reads such a count.
- [ ] **R1-34** (note, shared with phase 4) — `pkg/claierr`'s package doc states the error
  vocabulary "is closed — vendors decode their wire formats into these values, they never define
  their own", and this phase's code-layout row puts every typed error the worklog adds in
  `pkg/claierr/claierr.go`. Phase 4 then defined `mcp.RPCCallError`
  (`internal/tools/mcp/conn_http.go:316`) outside that vocabulary, with no sentinel, and
  `StdioConn.deliver` (`conn_stdio.go:414`) still renders the same meaning as a bare `fmt.Errorf`.
  Two transports therefore return two different types for "the server answered with a JSON-RPC
  error", which is why phase 4's unknown-tool classifier works for HTTP only. Ruling: either admit
  `RPCCallError` into `pkg/claierr` with a sentinel and have both transports return it, or add a
  code-layout row recording the exception.
- [ ] **R1-26b** (note) — `NewMcpConnClosed` and `NewMcpFrameUndecodable` are at 0.0% statement
  coverage in `pkg/claierr`'s own suite, so neither `Error()` string is asserted anywhere; the
  types are constructed only from other packages. This is phase 8's reported gap, recorded here
  against the owning phase. One table-driven test over the seven MCP error strings closes it for
  phases 1, 4 and 6 at once.

### Review 2, 2026-10-02 — implementation review, round 2

Status unchanged: **Complete**. Round 2 found no material defect in the seam itself. One note.

**Verified good (traced, not read off the notes):**

- `StdioConn`'s pending-waiter channels cannot deadlock. Each channel is buffered 1
  (`conn_stdio.go:222`) and every waiter can receive at most one send across the three producers:
  `deliver` removes the id under `mu` before sending (`:397-416`), `failAllPending` swaps the whole
  map under `mu` (`:298-311`), and `Close` takes the map and nils it (`:268-284`). An id can
  therefore appear in exactly one snapshot, so no send on a full channel is reachable. The
  write-error path's `removeWaiter` racing a snapshot is harmless for the same reason.
- `readFrames` survives an oversized frame and resynchronises (`:322-339` with
  `discardRestOfLine` at `:528`); it terminates the connection only on real transport end.
- The reaper gates on `stderrDone`, not on the frame reader (`:199-208`), so an unconsumed stdout
  line cannot hide a crash tail — the property `TEST_SERVER_UNCONSUMED_STDOUT` exists for.
- SSE comment keep-alives (`: ping`) are correctly ignored by `sseFrameReader.next`
  (`conn_http.go:586-590`), verified by probe.

**Findings:**

- [ ] **R2-22** (note, shared with phases 4 and 5) — Test-only options whose doc comments describe
  a README parameter production cannot set. `WithHandshakeBound` (`conn_stdio.go:54-59`, sole
  caller `manager_test.go:62`), `WithReadBound` (`conn_stdio.go:61-64`, sole caller
  `conn_stdio_test.go:298`), `WithHttpReadBound` (`conn_http.go:55-58`, sole caller
  `conn_http_test.go:176`), `mcpauth.WithInteractive` (`mcpauth.go:106-109`),
  `mcpauth.WithPrintURL` (`mcpauth.go:94-96`), `schemacache.WithFreshnessBound`
  (`schemacache.go:193-195`). This is R1-26's class with six further symbols, so it is filed once
  here rather than split. **Correction to the reading a fixer might take from
  `WithHandshakeBound`'s comment:** `ControlEvent.StartupTimeout`, which it says it "replaces",
  was already dead at `c3867d3` — `git show c3867d3:internal/text/querier_setup_tools.go` never
  sets it. No per-server handshake bound was lost; there was never one to lose. Corrective action:
  delete the option or wire it, and strike the "replaces" clause either way.
