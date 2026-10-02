# Phase 1 — Conn seam and JSON-RPC demux

**Status:** In Progress

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

Not started.

## Review findings

None.
