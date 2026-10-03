# Phase 4 — Streamable HTTP transport

**Status:** Complete — reviews 1 and 2 reopened this phase and are fixed and verified, 2026-10-03
fix session (see Implementation notes below). A later holistic sign-off review reopened this phase
again on B1; fixed and verified in the 2026-10-03 sign-off fix session (see Implementation notes
below).

Back to [README](README.md).

## Goal

Reach a remote MCP server over spec-compliant streamable HTTP, so a remote service costs clai no
local process at all.

## Specification

### Why the existing branch is not the starting point

An abandoned branch carries an `http_client.go` written before the connection seam existed. Its
transport logic cannot be reused, and the reasons for that are the specification for this phase:

- It opens a `GET` with an event-stream accept header and requires a success status before it will
  proceed. In streamable HTTP the server-initiated stream is optional and a server may answer `GET`
  with method-not-allowed, so a POST-only server could never connect.
- It closes each POST response body without reading it, relying entirely on the `GET` stream for
  every reply. A server that answers on the POST response, which is the common case, would produce
  calls that never resolve.
- It never carries a session identifier, so a session-bearing server rejects every request after
  `initialize`.
- It logs and continues on five failure paths, plus one informational log, where this repository's
  rule is to return the error.
- It repurposes the environment map as a static header map, so authorization is a static header edit with no lifecycle at all.

The frame parser for event-stream bodies is the one reusable idea; the request and response
plumbing is rewritten.

### Transport behaviour

A second `Conn` implementation, beside the stdio one from phase 1, satisfying the same interface so
the manager, the tool wrapper and the connector are unchanged.

Two claims in this phase are derived from the specification rather than from a measurement, and are
labelled as such so a later round does not go looking for evidence that does not exist. First, that a
server answering on the POST response is the common case: the specification permits both, clai must
read both, and no census of which is more common was taken. Second, the legacy-only detection signal
below: all six endpoints probed during the measurement session answered with an authorization
challenge, so none of them exhibited the signal, and it is implemented from the deprecated
transport's own handshake rather than from an observation.

Request path: every JSON-RPC message is POSTed to the single configured endpoint with a JSON content
type and an accept header naming both JSON and event-stream, plus the protocol-version header set
from the protocol-version parameter.

An event-stream body is the standard server-sent-events framing: `event:` and `data:` lines, one
field per line, each event terminated by a blank line, with a `data:` payload accumulated across
consecutive lines and parsed as one JSON-RPC frame. The frame parser from the abandoned branch is
the one part of it worth keeping.

Response path, and this is where the branch failed: the POST response is read, and its content type
decides how. A JSON body carries the response directly. An event-stream body is parsed for frames
until the response for the request's id arrives. An accepted status with no body is a valid outcome
for a notification and must not be treated as a pending call or a timeout. Any other status is a
typed error carrying the status and the server's message.

Session: the value of the session header on the `initialize` response, when present, is retained and
sent on every later request. A server that omits it is sessionless and no header is added.

Server-initiated stream: a `GET` with an event-stream accept header is attempted once after
initialization, to receive server-initiated messages such as tool-list-changed notifications. A
method-not-allowed answer is expected and normal, not a failure, and the connection remains fully
usable. When the stream is available its frames enter the same demux as POST responses, so a
server-initiated request is answered through the same path phase 1 established.

Body reads are bounded by the response-body-limit parameter, matching the existing stdio scanner
bound so the two transports share one notion of an oversized message.

### Configuration

The server model gains an endpoint field. Exactly one of the command field and the endpoint field
must be set: neither, or both, is a parse error naming the file and both field names. This phase
owns that validation, and it also owns the rejection of an endpoint that is not an absolute HTTPS
or HTTP URL.

Authorization is not implemented here. An endpoint that demands it returns the `AuthChallengeError`
declared in the README shared interfaces: the `WWW-Authenticate` header verbatim in `Challenge`, and
the URL parsed out of its `resource_metadata` parameter in `ResourceMetadata`, empty when the
parameter is absent. This phase owns that error type and that parse; the phase that owns
authorization consumes it and parses nothing further. A server reachable without authorization works
fully at the end of this phase.

### Invariants

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| A JSON POST response | Read and demuxed for the request id | `TestHttpConnPostReceivesJsonResponse` |
| An event-stream POST response | Parsed for frames and demuxed | `TestHttpConnPostReceivesSseResponse` |
| An accepted status with no body | Treated as a completed notification | `TestHttpConnAcceptedWithoutBodyIsNotATimeout` |
| Every request after `initialize` | Carries the session header when the server supplied one | `TestHttpConnCarriesSessionIdAfterInitialize` |
| A method-not-allowed answer on the server stream | Connection remains usable | `TestHttpConnToleratesMethodNotAllowedOnGetStream` |
| A frame arriving on the server stream | Enters the same demux as POST responses | `TestHttpConnReadsServerInitiatedMessagesFromGetStream` |
| Any response body | Bounded by the response-body-limit parameter | `TestHttpConnBodyLimitIsEnforced` |
| Any non-success, non-accepted status | Returned as a typed error, never logged and skipped | `TestHttpConnNonOkStatusReturnsTypedError` |
| A configured server | Has exactly one of the command field and the endpoint field | `TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl` |
| An HTTP server's whole lifecycle | Starts no child process | `TestHttpConnCreatesNoChildProcess` |
| A `url`-configured server | Selected by the connector, registered by setup, and callable end to end | `TestSetupRegistersHttpServerToolsAndCallsOne` |
| An endpoint entry past the freshness bound | Entry is a miss | `TestHttpSchemaCacheEntryExpiresOnFreshnessBound` |
| An endpoint entry whose environment digest or envfile delta changed | Entry is a miss | `TestHttpSchemaCacheMissOnEnvOrEnvfileDelta` |
| A tool-list-changed notification | Entry invalidated | `TestHttpSchemaCacheListChangedInvalidatesEntry` |
| An unknown-tool failure | Entry invalidated, no failure recorded | `TestHttpSchemaCacheUnknownToolInvalidatesEntry` |
| A warm cache hit, driven twice through `setupMcpManager` | Registers the same tools; issues no POST until a tool is actually called | `TestHttpSchemaCacheWarmHitIssuesNoRequestUntilCalled` |
| A lazy command-based server's unknown-tool failure and tools-list-changed notification | Entry invalidated, exactly as the endpoint-based half | `TestStdioSchemaCacheUnknownToolInvalidatesEntry`, `TestStdioSchemaCacheListChangedInvalidatesEntry` |
| A server-initiated request arriving on the GET stream | POSTed a `-32601` response carrying its id | `TestHttpConnAnswersServerInitiatedRequestWithMethodNotFound` |
| The GET server stream, bounded by the response-body-limit parameter | Bound applies per frame, never cumulatively across the stream's lifetime | `TestHttpConnServerStreamBodyLimitIsPerFrameNotCumulative` |
| An SSE event whose `data:` field is empty | Treated as a keep-alive, not a malformed frame | `TestSSEFrameReaderSkipsEmptyDataKeepAlive` |
| A connection holding a session id, on `Close` | Sends `DELETE` with that session; a sessionless connection sends none | `TestHttpConnCloseSendsSessionDelete`, `TestHttpConnCloseWithNoSessionSendsNoDelete` |
| `Close`'s production call site | The connection's own context ending closes it and sends the session `DELETE` | `TestHttpConnContextEndingClosesConnectionAndSendsDelete` |
| The GET server stream, dropped without the connection itself closing | Reconnects carrying `Last-Event-ID`, and keeps delivering notifications | `TestHttpConnServerStreamReconnectsWithLastEventID` |
| A JSON-RPC error's code and message, from either transport | One shared `*claierr.McpRPCError`, not a bespoke per-transport type | `Test_Claierr_McpRPCErrorIsOneTypeForBothTransports` |

### Limits

| Limit | Injectable field | README parameter | How a test triggers it |
| --- | --- | --- | --- |
| Response body size | Transport field | response-body-limit parameter | Fake server returning a body one byte past the limit |
| Protocol version advertised | Transport field | protocol-version parameter | Fake server asserting the received header |
| Endpoint entry freshness | Cache field | schema-cache-freshness-bound parameter | Injected clock advanced past the bound |
| Connect wait | Shared with phase 2's connector | connect-bound parameter | Fake server that accepts the connection and never answers, asserted by `TestHttpConnectBoundExpires` |

### Schema cache for endpoint-based servers

The cache mechanism, its digest-keyed storage and its record format are owned by the cache phase,
which deliberately covers command-based servers only. This phase adds the endpoint-based half,
because it is the phase in which endpoint-based servers first exist.

| Identity component | Included |
| --- | --- |
| Endpoint URL | yes |
| Environment map, as a digest of sorted key and value pairs | yes |
| Envfile size and modification time | yes |

The environment components are carried forward from the command-based identity even though this
phase gives `env` and `envfile` no effect on an endpoint server: the authorization phase introduces
`auth.token_env`, resolved from exactly those two sources, and a token change can change which tools
an endpoint exposes. Carrying the components now means that phase adds a scope component rather than
restructuring the key. Until then the rows assert key stability, not behaviour.

An endpoint entry is validated first by the same delta rule the cache phase established for the
components it shares: a change to the environment digest, or to the envfile's size or modification
time, is a miss. There is simply no executable to stat.

Beyond that, an endpoint offers no local evidence that its tool list changed, so an entry carries the
schema-cache-freshness-bound parameter and is a miss beyond it. Inside the bound two signals
invalidate immediately: a `notifications/tools/list_changed` notification received on the
connection, and a tool call that fails because the server does not know the tool. That second
signal is defined concretely so it is not guessed: the `tools/call` response is a JSON-RPC error
whose code is the invalid-params or method-not-found code and whose message names the tool, or a
result whose `isError` is true and whose text names the tool as unknown. Nothing else counts as this
signal; an ordinary tool failure does not invalidate anything.

The invariant that no run-fact is persisted is unchanged: an invalidation removes an entry, it never
records a failure.

### Fixtures introduced

This phase introduces the fake streamable HTTP MCP server that it and every later phase consume, as
a test package beside the existing stdio fake at `internal/tools/mcp/testserver`. It serves on a
loopback listener and is configurable per test to: answer a POST with JSON or with an event stream;
supply or omit a session header; accept or reject the server-initiated stream; answer with an
authorization challenge instead of a result, whose `resource_metadata` parameter points at a
caller-supplied URL so a test can aim it at its own authorization fake's bound address; and answer a
tool call only when a given bearer token is presented. The last two modes are what let the authorization phase use this fixture as its protected
resource. No later phase creates its own MCP-over-HTTP fake, and the authorization phase introduces
only an authorization server.

### Out of scope

The legacy event-stream-first remote transport is not implemented, per decision D7. The server
config carries no transport field, so this is not a parse-time condition. The detection signal is
specific and is specified here so it is not guessed: an endpoint is legacy-only when the
`initialize` POST is refused as method-not-allowed or not-found while a `GET` with an event-stream
accept header succeeds. That is the legacy handshake, in which the client was expected to open the
stream first and learn a separate POST endpoint from an `endpoint` event. On that signal the
connection fails with a typed error naming the unsupported transport and pointing at the supported
one, rather than being silently retried.

### Documentation

`architecture/mcp.md`, created in phase 1, gains the transport section: the request and response
contract, the session rule, the optional server stream, and the configuration validation.

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Handshake against a fake answering JSON on POST | In-repo fake HTTP MCP server on loopback | Tools registered | POST issued per message | No child process |
| Handshake against a fake answering an event stream on POST | Same fake, event-stream mode | Tools registered | Frames parsed from the POST body | POST body not discarded unread |
| Fake returns a session header on initialize | Fake asserting the header on later requests | Tool call succeeds | Session header echoed on every later request | No request sent without the header |
| Fake answers the server stream with method-not-allowed | Fake rejecting the stream request | Tool call succeeds | Connection usable | Connection not failed |
| Fake pushes a tool-list-changed notification on the server stream | Fake with an open stream | Notification observed by the client | Frame demuxed | Notification not dropped |
| Notification sent to a fake that answers with accepted and no body | Fake returning accepted | Notify returns without error | No waiter registered | Call not left pending |
| Fake returns an authorization challenge | Fake HTTP MCP server in challenge mode | `AuthChallengeError` returned | Header preserved verbatim; resource-metadata URL parsed out | No retry loop, no token synthesised |
| A config directory holding one `url` server | Fake HTTP MCP server, real setup path | Its tools registered under the existing prefix and one call succeeds | Connector selects the HTTP implementation | No child process; the stdio path not taken |
| Config with both command and endpoint set | Config fixture | Parse error naming the file and both fields | Error returned | Neither transport attempted |
| Setup for an endpoint server with a warm entry inside the freshness bound | Fake HTTP MCP server, injected clock | Tools registered | Entry read | No request issued to the endpoint |
| Setup for an endpoint server with an entry past the freshness bound | Fake HTTP MCP server, injected clock advanced | Tools registered from a fresh list | Endpoint queried, entry replaced | Stale tool list not served |
| Tool call rejected as unknown by the endpoint | Fake HTTP MCP server rejecting one tool name | Tool result carries the error | Entry invalidated | No failure written to the cache |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| A JSON POST response resolves its call | `TestHttpConnPostReceivesJsonResponse` |
| An event-stream POST response resolves its call | `TestHttpConnPostReceivesSseResponse` |
| The session header is carried after initialize | `TestHttpConnCarriesSessionIdAfterInitialize` |
| A rejected server stream does not break the connection | `TestHttpConnToleratesMethodNotAllowedOnGetStream` |
| Server-initiated messages are received when the stream exists | `TestHttpConnReadsServerInitiatedMessagesFromGetStream` |
| An accepted status with no body is not a timeout | `TestHttpConnAcceptedWithoutBodyIsNotATimeout` |
| Oversized bodies are bounded | `TestHttpConnBodyLimitIsEnforced` |
| A failing status is a typed error | `TestHttpConnNonOkStatusReturnsTypedError` |
| A malformed frame fails its call rather than being dropped | `TestHttpConnMalformedFrameReturnsCallError` |
| Config requires exactly one transport | `TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl` |
| An HTTP server costs no process | `TestHttpConnCreatesNoChildProcess` |
| A `url`-configured server file goes through setup, registers its tools under the existing prefix, and executes a tool call | `TestSetupRegistersHttpServerToolsAndCallsOne` |
| An endpoint entry expires at the freshness bound | `TestHttpSchemaCacheEntryExpiresOnFreshnessBound` |
| An endpoint entry invalidates on an environment or envfile delta | `TestHttpSchemaCacheMissOnEnvOrEnvfileDelta` |
| The connect bound expires for an endpoint connection | `TestHttpConnectBoundExpires` |
| A tool-list-changed notification invalidates an entry | `TestHttpSchemaCacheListChangedInvalidatesEntry` |
| An unknown-tool failure invalidates an entry without recording it | `TestHttpSchemaCacheUnknownToolInvalidatesEntry` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Endpoint unreachable | Typed transport error naming the server and the endpoint | `TestHttpConnUnreachableEndpointIsTypedTransportError` |
| Endpoint returns a server error status | Typed error carrying the status and the server message | `TestHttpConnNonOkStatusReturnsTypedError` |
| Endpoint returns an authorization challenge | `AuthChallengeError`, declared in the README shared interfaces, carrying the header verbatim and the resource-metadata URL parsed out of it | `TestHttpConnChallengeIsTypedAuthErrorWithChallengePreserved` |
| Response content type is neither JSON nor an event stream | Typed error naming the content type | `TestHttpConnUnsupportedContentTypeIsTypedError` |
| Event-stream body ends before the awaited response | Typed error for that call, connection remains usable | `TestHttpConnTruncatedStreamFailsOnlyItsCall` |
| Response body exceeds the limit | Bounded typed error, connection remains usable | `TestHttpConnBodyLimitIsEnforced` |
| Session header rejected by the server after initialize | Typed error naming the session stage | `TestHttpConnRejectedSessionIsTypedSessionError` |
| Endpoint is not an absolute HTTP or HTTPS URL | Parse error naming the file and the field | `TestMcpServerConfigRejectsNonAbsoluteEndpoint` |
| Neither command nor endpoint configured | Parse error naming the file and both fields | `TestMcpServerConfigRejectsMissingTransport` |
| Endpoint speaks only the legacy event-stream-first transport, discovered at connect time | Typed transport error naming the unsupported transport and pointing at the supported one | `TestHttpConnRejectsLegacyOnlyEndpoint` |
| Both command and endpoint set | Parse error naming the file and sets specifically "both", not a shared branch-insensitive string | `TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl` |
| A tool's own validation error, code -32602, naming the tool | Not classified as the unknown-tool signal; the entry is not invalidated | `TestIsUnknownToolFailureRequiresUnknownMarkerFor32602InvalidParams` |

## Implementation notes

2026-10-02, phase 4 execution. Deltas from the specification only; see the README session journal for the narrative entry.

`internal/tools/mcp/conn_http.go` carries `HttpConn`, `NewHttpConn`, `NewHttpConnector`/`dialHttp`, the `RequestDecorator` and `NotificationWatcher` seams, and `sseFrameReader`. It duplicates StdioConn's small pending-waiter helpers (`removeWaiter`, `deliver`, `failAllPending`, plus a new `failOne` and `isPending` this transport alone needs) rather than extracting a shared helper type into `conn_stdio.go`: the instruction that "the manager, the tool wrapper and the connector must need no changes" was read as also covering the already-shipped, reviewed `conn_stdio.go`, so refactoring it was judged out of scope for this phase. The duplication is within the dupl gate's own signal-not-verdict policy (`go run github.com/mibk/dupl@latest -t 80 .` reports no new clone group touching either file); flagged here as a legitimate target for phase 8 or a future simplify pass, not fixed now.

Per-call scoping decision, not specified: a malformed or truncated event-stream frame on a POST response fails only that call (`failOne`), since each POST is its own HTTP exchange and other in-flight calls use separate exchanges entirely; the same problem on the shared GET server stream fails every pending call (`failAllPending`), mirroring StdioConn's single-reader rationale. Both error-coverage rows ask for "its call" to fail with the connection remaining usable, which the POST-scoped failure satisfies without touching unrelated callers.

Session-rejection detection (`TestHttpConnRejectedSessionIsTypedSessionError`) is a rule this phase owns rather than a protocol given: a 404 response to a request carrying the session header is treated as the session stage; a 404 with no session header falls through to the ordinary non-ok-status path. The fixture is both sides of this test, so the rule is self-consistent but not externally specified.

Legacy-only detection (`detectLegacyOnlyEndpoint`) triggers on `initialize` refused with 404 or 405 followed by a successful GET probe, exactly as specified; it is wired at the `dialHttp` connect orchestration level (a new function beside, not inside, `connector.go`) rather than inside `HttpConn.Call`, so the probe only runs once at connect time and never on an ordinary runtime call failure.

The unknown-tool invalidation signal and the tools-list-changed watcher are wired in a new `internal/text/mcp_http_schema_cache.go` (not named by a code-layout row, since none covers this wiring specifically): `cacheInvalidatingConn` wraps the live `mcp.Conn` returned by a cache miss or a cache-hit `Connector`, and `watchForToolsListChanged` drains `HttpConn`'s `NotificationWatcher` channel under the run context. This keeps `mcp.Connector`/`mcp.Conn`/the tool wrapper untouched, composing around them instead. `mcp.RPCCallError` (exported from `conn_http.go`) carries the JSON-RPC code and message so this detection does not parse rendered error strings.

The response-body-limit is enforced as a bound on one whole POST response body (both JSON and event-stream framed), not per SSE line: the README states the two transports "share one notion of an oversized message," and one HTTP response is the natural message boundary for this transport, unlike stdio's per-line framing.

Test-infra finding, not a production bug: `httptest.Server.Close()` blocks until every connection is idle, and `t.Context()`/`t.Cleanup` are both guaranteed to run only after a test function's own `defer`s finish — so a `defer srv.Close()` registered as a normal defer always fires before a `t.Cleanup`-registered teardown of a connection whose server-initiated GET stream is still open against that same server, hanging `Close()` for its 5s warning window and then forever. Fixed in the test files (not in production code) by deriving the connection's context from a local `context.WithCancel(context.Background())` and deferring its `cancel()` *after* `defer srv.Close()` is deferred (LIFO executes `cancel()` first). Recorded here because this pattern will recur in every later phase's streamable-HTTP test that keeps a connection open across the test body.

The httptestserver fixture's `HangForever` mode bounds its wait to 2s (not truly infinite) rather than depending on the client's context cancellation to close the underlying TCP connection, which was observed not to happen promptly enough in this sandbox for `httptest.Server.Close()` to return — a second instance of the same test-infra class above. This cost is paid three times under `-count=3` (`TestHttpConnectBoundExpires`); kept at 2s rather than lower to leave comfortable margin over every connect-bound used in this phase's tests (100ms).

All twenty-five declared test names exist and pass, individually verified by name (`grep -rl 'func <name>(' .` each returns exactly one file) and by running `go test ./internal/tools/mcp/... ./internal/text/... -race -cover -count=3 -timeout=30s` repeatedly at low host load.

The full repository gate (`gofumpt -l .` clean, `go vet ./...` clean, `staticcheck ./...` clean, `go fix ./...` no changes, `dupl -t 80 .` no new clone group in a file this phase touches) passes unedited. `go test ./... -race -cover -count=3 -timeout=30s` was run three times as the single monolithic invocation the contract specifies. Every one of the three runs itself drove host load from under 5 up to 17–23 (confirmed by sampling `/proc/loadavg` immediately after each run), and each run's `FAIL`s were exclusively 30s timeouts, landing in a different, shifting subset of packages across the three runs: root `github.com/baalimago/clai`, `internal/audio`, `internal/vendors`, `internal/text`, and once `internal/tools/mcp` itself — never the same set twice, and never an assertion failure in a test this phase added. `internal/vendors` and `internal/audio` are confirmed untouched by this phase's diff (`git status --short` against them is empty). Every package that showed a `FAIL` was re-run alone immediately afterward, at load back down to 2–7, and passed cleanly every time, including a direct re-run of the one pre-existing test that failed once under load (`TestMcpTool_CallWithContext_SuccessWithTimeout`, in `internal/tools/mcp`, untouched by this phase's diff: it spawns a real process under a 1s timeout, which is exactly the shape of test the repository's documented host-load sensitivity describes). This matches the pattern phases 1–3 each recorded, and the worklog's own Strategy section's warning that this gate "times out above roughly load eight" — evidently including load the full invocation induces on itself on this host, not only load present beforehand.

Coverage: `internal/tools/mcp` 83.3%, `internal/tools/mcp/schemacache` 84.9%, `internal/tools/mcp/httptestserver` 44.3% (a fixture, exercised far more heavily from `internal/tools/mcp` and `internal/text`'s own test binaries than from its own, which `go test`'s per-package `-cover` does not credit), `internal/text` 84.0%, `pkg/claierr` 74.3%, `pkg/text/models` 85.3% — all at or above the repository's coverage floor.

`architecture/` was not touched, per the coordinating session's standing instruction for this worklog.

### Sign-off fix session, 2026-10-03 (worklog-work, B1)

A later, unbounded holistic review read the whole effort at once rather than one phase at a time
and found a blocker neither prior review round caught: `Call` resolved when the POST response
*body* ended, not when its answer arrived. Full text in the README's Sign-off verdict section and
the Sign-off review entry under the Feedback index.

**B1 — `Call` waited for the stream to end, not for the frame it was waiting for.** `Call`
(`internal/tools/mcp/conn_http.go`) invoked `c.consumeResponseBody(resp, id)` synchronously,
immediately before the `select` on the waiter channel. `consumeSSE` loops reading frames until its
reader returns `io.EOF`. The streamable-HTTP specification says a server *SHOULD* close the stream
after answering, not *MUST* — so a conformant server that flushes its result frame and then holds
the POST response open (nothing in the specification forbids this) made `consumeResponseBody` block
until the body ended, which only happened when the caller's context expired. Every call, including
`initialize`, paid the full connect-bound wait on such a server even though the answer was sitting
in the scanner's buffer from the start.

`architecture/mcp.md`'s transport section already documents the correct behaviour ("an event-stream
body is parsed for frames until the awaited id arrives") — the code did something else, and no test
checked it. Confirmed: the document reads true again after this fix and needed no edit.

**Fixed:** `Call` now starts `consumeResponseBody` in its own goroutine, immediately before the
`select`, instead of calling it inline. `consumeResponseBody` already owns closing `resp.Body` on
every path, so nothing about its contract changes — only when it runs relative to the waiter
channel. The `select` on `ch` now resolves as soon as `deliver` places a result on it (i.e. as soon
as the awaited frame is parsed), not when the background read eventually ends. `consumeSSE`'s
trailing `isPending` check is unchanged and still correctly means "the stream ended before the
answer came" (truncated), since it only fires once the loop itself exits.

Evaluated against every path through `Call`, per the review's own instruction: the plain-JSON
branch is unaffected (one read, one deliver, same outcome, now off the critical path). The
`initialize` branch's `captureSession(resp)` and `c.streamOnce.Do(...)` run in the `select`'s
success case, after `res` arrives on `ch` — `captureSession` only reads `resp.Header`, which the
background goroutine never writes to (it only reads `resp.Header.Get("Content-Type")` and closes
`resp.Body`), so there is no race on `resp` between the two goroutines, confirmed by `-race` across
three runs. The `ctx.Done()` branch's `c.removeWaiter(id)` and a late `deliver`/`failOne` from the
still-running background goroutine are both already serialised by `c.mu` and are mutually exclusive
in effect (whichever reaches the map first "wins"; the loser's send or delete is a no-op), the same
"late response to an abandoned call" case the existing `deliver` doc comment already names — no new
race, no design change needed there.

**Test, proved red before green:** `TestHttpConnCallResolvesWhenFrameArrivesEvenIfStreamStaysOpen`
(`internal/tools/mcp/conn_http_test.go`) drives the reviewer's own reproduction through the real
production path: a 1 s context, an `initialize` call against a fixture that flushes the SSE result
frame and then holds the POST body open. Against the unfixed code it failed with `context deadline
exceeded` at the full 1 s bound; against the fix it returns in well under 500 ms (typically
sub-millisecond on this host).

**Fixture gap closed:** `httptestserver.Config` gained `HoldPostStreamOpen`
(`internal/tools/mcp/httptestserver/httptestserver.go`): when set alongside `ResponseSSE`, the
handler flushes the result frame, then blocks (bounded at 2 s, matching `HangForever`'s own
rationale: still return on its own if the client's cancellation does not close the connection
promptly) instead of returning immediately. No fixture in the tree could express "answer, then hold
open" before this; every existing SSE-response test returns right after writing the frame, which is
exactly why this shipped green through two review rounds and a gate sweep.

**Verification commands, run at the end of this session:**

```
go build ./...                                                               # clean
go vet ./...                                                                 # clean
go run mvdan.cc/gofumpt@latest -l .                                          # no files listed
go run honnef.co/go/tools/cmd/staticcheck@latest ./...                       # clean
go fix ./...                                                                 # no changes
go run github.com/mibk/dupl@latest -t 80 .                                   # 36 clone groups, matching the pre-existing baseline
go test ./internal/tools/mcp/... ./internal/text/... ./pkg/claierr/... -race -cover -count=3 -timeout=60s
                                                                              # ok; mcp 81.5%, internal/text 85.3%, claierr 77.0%
go test ./... -race -cover -count=3 -timeout=30s -p 1                       # ok, every package, host load 2.27 at start
```

`architecture/mcp.md` was not touched, per the coordinating session's standing instruction for this
worklog; its transport section's invariant now holds in practice as well as on paper.

## Review findings

### Review 1, 2026-10-02 — implementation review

**Status: Resolved, phase 4 fix session 2026-10-03.** All findings below are fixed and verified;
see the Implementation-notes delta at the end of this file.

**Verified good:**

- Session handling is correct in both directions: captured only from a successful `initialize` and
  only when non-empty (`internal/tools/mcp/conn_http.go:228-234`), sent only when non-empty, and
  captured before `runServerStream` reads it. A 404 with a session is a typed session error, a 404
  without one an ordinary status error (`:359-369`) — self-consistent and declared.
- `202 Accepted` is success for `Notify` (body drained) and an explicit error for `Call`
  (`:157-161`). Correct both ways.
- Failure scoping is sound: `failOne` for a problem reading one POST body, `failAllPending` for the
  shared GET stream (`:254-283`). Both error-coverage rows hold.
- No local process: `TestHttpConnCreatesNoChildProcess` is weak (it treats a `pgrep` error as zero
  children) but not vacuous — a child outliving the call would fail it — and the stronger structural
  fact holds: there is no `exec` call anywhere in the HTTP path.
- `cacheInvalidatingConn.Call` (`internal/text/mcp_http_schema_cache.go:146-158`) is correct and
  genuinely reachable on the miss path; the invalidation is a pure side effect and the result and
  error pass through untouched. It correctly does not shadow `NotificationWatcher`, which is taken
  from the inner conn before wrapping. No deadlock: the notification send is non-blocking into a
  buffered(8) channel and the watcher selects on `runCtx.Done()`. An invalidation can never delete
  a *fresh* entry, because both triggers mean the stored list is known wrong.
- `TestSetupRegistersHttpServerToolsAndCallsOne` drives real setup and really executes a tool over
  loopback HTTP. Config XOR validation and the absolute-http/https check hold.

**Findings**

- [x] **R1-12** (major) — the response-body-limit is applied cumulatively to the long-lived GET
  server stream, so that stream dies silently after 2 MiB and takes this phase's
  tool-list-changed invalidation with it. `runServerStream` wraps the whole stream in
  `io.LimitReader(resp.Body, int64(c.readBound)+1)` (`internal/tools/mcp/conn_http.go:502`), then
  treats the resulting `io.EOF` as a normal end and returns; `streamOnce` means it is never
  restarted. The README parameters row says "response-body-limit, **per message**", and stdio's
  equivalent genuinely is per line — `boundedLineReader.discardRestOfLine`
  (`conn_stdio.go:528-539`) resynchronises and continues. Scenario: a session whose server stream
  has carried 2 MiB of progress and notification frames stops receiving them with no error
  anywhere, and `watchForToolsListChanged`
  (`internal/text/mcp_http_schema_cache.go:113-130`) then blocks forever on a channel nothing
  feeds, so the invariant row "A tool-list-changed notification → Entry invalidated" stops holding
  for the rest of the run. The same construct at `conn_http.go:378` bounds a whole POST
  event-stream body rather than each frame; that half the implementation notes declared in writing
  ("one HTTP response is the natural message boundary"), which is defensible for a POST and cannot
  be stretched to a GET stream.
  **Fixed, phase 4 fix session 2026-10-03:** the outer `io.LimitReader` is gone from
  `openServerStreamOnce` (the renamed, now-reconnecting `runServerStream`); `sseFrameReader`'s own
  scanner buffer bounds each frame individually, and the whole-body limiter stays on the POST
  paths only, per the implementation notes' own stated boundary. `httptestserver` gained
  `PushNotificationWithID`/`broadcastSSEWithID`, letting a test push several frames past one
  cumulative bound's worth. Test: `TestHttpConnServerStreamBodyLimitIsPerFrameNotCumulative`.
- [x] **R1-13** (major, cross-referenced from phase 1) — a server-initiated request over HTTP is
  silently dropped, although this phase's own prose says "its frames enter the same demux as
  POST responses, so a server-initiated request **is answered through the same path phase 1
  established**", and phase 1's invariant is "A server-initiated request → Answered with a
  method-not-found error". `handleJSONFrame` returns with no answer
  (`internal/tools/mcp/conn_http.go:441-447`), justified by the comment "there is no channel to
  answer it on" — which is also wrong about the protocol: MCP streamable HTTP has the client POST
  its JSON-RPC responses to the same endpoint. Scenario: an endpoint issuing `roots/list` on its
  GET stream — the README's own probe records `server-filesystem` doing exactly this unprompted —
  waits forever, which is the stall phase 1 added the `-32601` answer to prevent. No test is named
  for the clause, so nothing failed.
  **Fixed, phase 4 fix session 2026-10-03:** a server-initiated request (a frame with both `method`
  and `id`) now spawns `respondMethodNotFound`, which POSTs a `-32601` response carrying the
  request's id back to the endpoint, in its own goroutine so it never blocks the GET stream's read
  loop. `httptestserver` gained `PushServerRequest`/`PostedResponse` so a test can push one and
  observe the client's answer. Test: `TestHttpConnAnswersServerInitiatedRequestWithMethodNotFound`.
- [x] **R1-15** (major) — the warm-cache endpoint hit branch has zero test coverage, and its
  invariant is proved against a hand-built seam instead. `newCacheInvalidatingConnector` and
  `cacheInvalidatingConnector.Conn` (`internal/text/mcp_http_schema_cache.go:95-108`) are at
  **0.0%** on the full `internal/text` suite, as are `cacheInvalidatingConn.Notify` and `.Close`.
  They sit on the two lines immediately after `newAuthenticatingHttpConnector`, which shows 100%
  only because `internal/text/mcp_oauth_setup_test.go:151` constructs it directly. No test calls
  `setupMcpManager` twice against an HTTP server with a shared cache, so the integration rows
  "warm entry inside the freshness bound … **No request issued to the endpoint**" and "…past the
  freshness bound → Endpoint queried, entry replaced" are unproven; the two named bound tests
  exercise `Lookup` in isolation. Swapping the arguments to `newCacheInvalidatingConnector`,
  dropping `httpChallengeResolver` from `:47`, or wrapping in the wrong order would leave the
  whole suite green.
  **Fixed, phase 4 fix session 2026-10-03:** added exactly the corrective-action test, driven
  through `setupMcpManager` twice against the same `httptestserver` and the same cache — a cold
  run then a warm run — asserting the same tool set, zero further `httptestserver.PostRequestCount`
  POSTs from the warm run itself, a warm-run tool call that still reaches the endpoint (count rises),
  and a warm-run unknown-tool call that still invalidates the entry. The existing wiring needed no
  code change; it was correct but unproven. Test: `TestHttpSchemaCacheWarmHitIssuesNoRequestUntilCalled`.
- [x] **R1-14** (major, owned with phase 2) — `connect_timeout_seconds` is ignored on the endpoint
  cache-miss path, and `HttpConn` implements no `HandshakeBound()`, so it silently takes the 30 s
  stdio default. Detail in phase 2's R1-14.
  **Not phase 4's to fix:** the README feedback index already records the HTTP half as reassigned
  to phase 5's `internal/text/mcp_oauth.go` (`handshakeHttpServerWithAuth`/`runHandshake`), not
  phase 4 as originally filed. Checked off here only to record that phase 4 owns nothing further
  for this finding; the open HTTP-half work belongs to phase 5.
- [x] **R1-20** (minor) — `isUnknownToolFailure` invalidates on the ordinary invalid-params code,
  contradicting its own documented rule. `internal/text/mcp_http_schema_cache.go:181-187`
  accepts `-32602` and `-32601` when the message merely contains the tool name, while the function
  doc two lines above promises "an ordinary tool failure (e.g. a validation error from the tool
  itself) does not invalidate anything" — and `-32602` *is* the invalid-params code a server
  returns for exactly that. Scenario: the model passes a wrong argument type to
  `mcp_linear_create_issue`; the server answers `-32602 "invalid params: create_issue requires
  title"`; clai deletes a perfectly good cache entry and the next run pays a full reconnect.
  **Fixed, phase 4 fix session 2026-10-03:** `isUnknownToolFailure` now requires `-32601` alone, or
  `-32602` paired with an unknown/not-found marker in the message (mirroring the `isError` branch's
  own rule). Tests: `TestIsUnknownToolFailureRequiresUnknownMarkerFor32602InvalidParams`,
  `TestIsUnknownToolFailureAcceptsMethodNotFoundAlone`,
  `TestIsUnknownToolFailureAcceptsInvalidParamsWithUnknownMarker`,
  `TestIsUnknownToolFailureIgnoresUnrelatedErrorType`. The `isError` half's own test gap is
  unchanged by this fix and remains open (no `httptestserver` `isError: true` mode); not reopened
  as a separate finding since the original review did not file it as one.
- [x] **R1-27** (minor) — watcher ordering and lifetime on the miss path.
  `internal/text/mcp_http_schema_cache.go:66-70` starts `watchForToolsListChanged` *before* `:77`
  writes the entry, so a `list_changed` arriving in that window makes `Invalidate` a no-op
  (ENOENT → nil) and `Capture` then writes an entry already known stale. Separately the goroutine
  is started under `ctx` (the run context) while the deferred cleanup only cancels `connCtx`, so a
  `RegisterTools` failure at `:71-73` leaves it running for the whole run against a dead
  connection whose `notifyCh` is never closed. Corrective action: `Capture` first, then watch, and
  start the watcher under `connCtx` or after `connected = true`. Related dead branch: the
  `if !ok { return }` at `:119-121` is unreachable, since `HttpConn.Close` never closes
  `notifyCh`.
  **Fixed, phase 4 fix session 2026-10-03:** both miss-path resolvers (`resolveLazyHttpServerViaCache`
  and the newly-wired stdio `resolveLazyServerViaCache`, R2-16) now call `cache.Capture` before
  starting `watchForToolsListChanged`, and the watcher runs under `connCtx`, not the run context,
  so a `RegisterTools` failure's `cancel()` stops it. The dead branch is now reachable: both
  `HttpConn.Close` and `StdioConn.Close` close `notifyCh` under the same lock
  `publishNotification` checks before sending (no send-on-closed-channel race). Tests:
  `TestHttpConnCloseClosesNotificationChannel`, `TestStdioConnCloseClosesNotificationChannel`.
- [x] **R1-26** (minor, same class as phase 1's) — `WithHttpProtocolVersion`
  (`internal/tools/mcp/conn_http.go:62`) and `WithHttpClient` (`:74`) are dead: definition plus doc
  comment, zero call sites anywhere. `WithHttpClient`'s comment is actively false — "Tests use this
  to set a short timeout independent of the caller's context"; no test uses it. Wire or delete.
  **Fixed, phase 4 fix session 2026-10-03:** deleted both (no production or test call site existed
  for either, so nothing needed rewiring). `WithAuthPendingSink` is phase 1's own symbol in
  `conn_stdio.go`, not phase 4's file, and remains open there.
- [x] **R1-34** (note, owned with phase 1) — `mcp.RPCCallError` is a new exported error type
  outside `pkg/claierr`'s declared-closed vocabulary and outside the code-layout table, and the
  stdio transport still returns a bare `fmt.Errorf` for the same meaning. Detail in phase 1's
  R1-34.
  **Fixed as a side effect of R2-16, phase 4 fix session 2026-10-03:** `mcp.RPCCallError` is
  deleted; both `HttpConn.deliver` and `StdioConn.deliver` now build `*claierr.McpRPCError`
  (behind the new `claierr.ErrMcpRPCError` sentinel), closing the vocabulary gap for both
  transports in the same change R2-16 required anyway. Test:
  `Test_Claierr_McpRPCErrorIsOneTypeForBothTransports`.

### Review 2, 2026-10-02 — implementation review, round 2

Status: **Resolved, phase 4 fix session 2026-10-03.** All findings below are fixed and verified;
see the Implementation-notes delta at the end of this file.

Round 1 found the cumulative body limit on the GET stream (R1-12), the dropped server-initiated
request (R1-13), the unbounded spawn/connect relationship (R1-14), the uncovered warm hit (R1-15)
and the `-32602` over-match (R1-20). Round 2 took the transport against the specification clause by
clause, and against malformed input.

**Verified good:**

- The POST contract is right: `Accept: application/json, text/event-stream` on every POST
  (`conn_http.go:337`), the protocol-version header on both POST and GET (`:338`, `:482`), the
  session header attached once captured (`:342-345`, `:486-488`), and `captureSession` ordered
  before the GET stream starts (`:167-169`) so the stream carries the session it was issued.
- `202 Accepted` with no body is classified as a protocol error for a request and as success for a
  notification (`:157-161`, `:181-199`), which is the correct asymmetry.
- `failOne` versus `failAllPending` is the right split: a problem reading one POST body blames only
  that call (`:254-264`), while an unattributable frame on the shared GET stream fails all
  (`:270-283`). Both are reached from the matching `onMalformed` closure.
- A `404` with a session in hand is distinguished from a `404` without one (`:359-365`).
- SSE comment keep-alives are ignored correctly, and a multi-line `data:` payload is joined with
  newlines before parsing (`:576-599`), both verified by probe.

**Findings:**

- [x] **R2-10** (major) — No session is ever terminated, on either side. The MCP
  streamable-HTTP transport specifies that a client holding a session id SHOULD send
  `DELETE` with `Mcp-Session-Id` to end it. `HttpConn.Close` (`conn_http.go:204-220`) cancels
  `connCtx` and fails the waiters and sends nothing. Worse, `Conn.Close()` has **no production
  caller at all** — `grep -n 'Close()' internal/tools/mcp internal/text` outside tests finds only
  `StdioConn.readFrames`'s own `defer`. Every connection's lifetime is purely `runCtx`-driven. And
  the in-repo fake has no `DELETE` branch either: `httptestserver.handle` (`:162-171`) answers
  `POST` and `GET` and returns `405` for everything else. So the Definition-of-success row
  "Integration test against the in-repo spec-compliant fake" overstates the fake: it is compliant
  for the two methods clai uses and silent on the third. Concrete failure: a vendor that bills or
  rate-limits per live session accumulates one abandoned session per clai invocation, since nothing
  tells it the client is gone.
  **Fixed, phase 4 fix session 2026-10-03:** `HttpConn.Close` now sends a best-effort, bounded
  (`mcpSessionDeleteBound`, 5s) `DELETE` carrying the session header whenever one is held, via the
  new `sendSessionDelete`. `NewHttpConn` gives `Close` its missing production call site: a
  goroutine watching `connCtx.Done()` calls `Close()`, mirroring `StdioConn`'s existing stdin-closer
  pattern, so run teardown (not only a test's explicit call) triggers it.
  `httptestserver` gained a `DELETE` branch (`handleDelete`) plus `DeleteCount`/
  `LastDeletedSession` for test observation. Tests: `TestHttpConnCloseSendsSessionDelete`,
  `TestHttpConnCloseWithNoSessionSendsNoDelete`,
  `TestHttpConnContextEndingClosesConnectionAndSendsDelete`.

- [x] **R2-11** (major) — The server-initiated stream is attempted once and can never be
  resumed. `c.streamOnce.Do(func() { go c.runServerStream() })` (`conn_http.go:169`) means at most
  one GET for the connection's lifetime, and `runServerStream` returns on the first `io.EOF` with no
  reconnect (`:504-515`). The specification's resumability mechanism is unavailable by construction:
  `sseFrameReader.next` reads `id:` lines and discards them (`:589-590`, "Other fields (event:, id:,
  retry:) are read but not interpreted"), so no `Last-Event-ID` can be sent and no `retry:` interval
  is honoured. Concrete failure: a load balancer or the server's own idle timeout closes the GET
  stream thirty seconds into a long run. From that moment `notifications/tools/list_changed` can
  never be observed, so the one invalidation signal an endpoint entry has is dead for the rest of
  the run, silently — and phase 3's endpoint entries then rely entirely on the 12 h freshness bound.
  Combined with R1-12 this is two independent ways the same stream dies without a word.
  **Fixed, phase 4 fix session 2026-10-03:** `streamOnce` still starts exactly one background
  goroutine, but that goroutine (`runServerStream`) now loops: `openServerStreamOnce` returns
  whether the stream established and the last SSE `id:` seen (`sseFrameReader.LastID()`, newly
  tracked rather than discarded), and on any non-cancelled end it reconnects after
  `streamRetryBackoff` (2s production default, injectable via the new
  `WithHttpServerStreamRetryBackoff` for tests) carrying `Last-Event-ID`. Consecutive *failed
  connection attempts* are capped at 5 (`mcpServerStreamMaxConsecutiveFailures`) so a server that
  never offers the stream at all does not retry forever; an established stream that later drops
  resets the counter on reconnect, so retrying is otherwise unbounded for the run. `httptestserver`
  gained `DropStreams` (ends the stream without cancelling the request context, simulating a
  dropped connection) and `GetStreamRequestCount`/`LastEventIDHeaderSeen` for observation. Test:
  `TestHttpConnServerStreamReconnectsWithLastEventID`.

- [x] **R2-14** (minor) — An SSE event whose `data:` field is empty is treated as a malformed
  frame, and on the GET stream that fails every in-flight call. `sseFrameReader.next`
  (`conn_http.go:576-599`) appends the payload of every `data:` line, including an empty one, so
  `data:\n\n` yields `len(data) == 1` with an empty string and returns a zero-length frame rather
  than continuing. Probe over `"data:\n\n" + "data: {…}\n\n"`:

  ```
  frame 0: data=""   err=<nil>
  frame 1: data="{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}" err=<nil>
  ```

  `handleJSONFrame` then fails `json.Unmarshal` on the empty frame and calls `onMalformed`
  (`:430-435`), which on a POST body fails that one call and **on the shared GET stream calls
  `failAllPending`** (`:512-514`), failing every concurrent call on the connection. An
  empty-`data:` keep-alive is not the canonical form — a `:`-prefixed comment is, and that is
  handled correctly — but it is in use, and nothing in the suite covers it.
  **Fixed, phase 4 fix session 2026-10-03:** `next` now joins the accumulated `data:` lines and
  checks the *joined string*, not the line count, both at a blank line and at stream end; an event
  whose joined data is empty (no `data:` line, or only empty ones) is skipped and reading
  continues, so it is never returned as a frame. Test: `TestSSEFrameReaderSkipsEmptyDataKeepAlive`.

- [x] **R2-16** (minor) — The unknown-tool invalidation signal can never fire for a stdio
  server, even once the wrapper is wired. `isUnknownToolFailure`'s error branch matches
  `*mcp.RPCCallError` (`internal/text/mcp_http_schema_cache.go:181`), which only
  `HttpConn.deliver` constructs (`conn_http.go:300`). `StdioConn.deliver` builds a bare
  `fmt.Errorf` with a byte-identical message (`conn_stdio.go:414` versus `conn_http.go:323`) — same
  text, no type, no sentinel, no code. So the corrective action for R2-03's fourth mechanism
  (wrapping the stdio path in `cacheInvalidatingConn`) is a no-op until the two transports return
  one type for one meaning. This is R1-34's observation with its consequence attached.
  **Fixed, phase 4 fix session 2026-10-03 — the two mechanisms R2-03's corrective action scoped to
  this phase, both wired for the stdio path:**
  1. **Shared error type (closes this finding and R1-34 together):** `mcp.RPCCallError` is deleted;
     both `HttpConn.deliver` and `StdioConn.deliver` now build `*claierr.McpRPCError` (new type,
     behind a new `claierr.ErrMcpRPCError` sentinel), so `isUnknownToolFailure`'s `errors.As` check
     matches either transport identically.
  2. **`NotificationWatcher` for stdio:** `StdioConn` gained a buffered `notifyCh` and
     `Notifications()`, mirroring `HttpConn`; `handleLine`'s id-less-notification branch now
     publishes the method instead of silently dropping it.
  3. **`cacheInvalidatingConn`/`cacheInvalidatingConnector` wired into the stdio resolver:**
     `resolveLazyServerViaCache` (`querier_setup_tools.go`, the command-based path) now wraps its
     cache-hit `Connector` with `newCacheInvalidatingConnector` and its cache-miss `Conn` with
     `newCacheInvalidatingConn`, and starts `watchForToolsListChanged` under `connCtx` after
     `Capture` (R1-27's ordering fix applied here too) — exactly mirroring the HTTP resolver, since
     both mechanisms already lived in transport-agnostic code
     (`internal/text/mcp_http_schema_cache.go`) despite the file's name.
  `internal/tools/mcp/testserver/main.go` gained `TEST_SERVER_UNKNOWN_TOOL_NAME` and
  `TEST_SERVER_LIST_CHANGED_TRIGGER_TOOL` so a test can drive both signals against a real spawned
  process through `setupMcpManager`, not a hand-built seam (the README's own cross-phase
  invariant). Tests: `TestStdioConnPublishesServerInitiatedNotifications`,
  `TestStdioSchemaCacheUnknownToolInvalidatesEntry`, `TestStdioSchemaCacheListChangedInvalidatesEntry`.

- [x] **R2-18** (minor) — The two transport-XOR tests assert the same branch-insensitive
  string. `internal/text/mcp_http_config_test.go:13` covers both `command` and `url` set; `:30`
  covers neither set; both end in `assertErrNamesFileAndFields` (`:49-57`), which checks only that
  the message contains the path, `"command"` and `"url"`. Production emits one shared string for
  both branches (`serverconfig/serverconfig.go:77`). A regression that collapsed the two branches
  into a single unconditional check, or reported the wrong one, passes both tests.
  **Fixed, phase 4 fix session 2026-10-03:** `validateTransport` now returns two distinguishable
  messages ("sets both ... exactly one is required" / "sets neither ... exactly one is required").
  Both tests assert their discriminating word, with the fixture file renamed away from
  `both.json`/`neither.json` first — those names made the new assertion pass trivially on the
  *old*, undifferentiated message (the file path itself contains "both"/"neither"), which is
  exactly the class of self-inflicted false pass this worklog's cross-phase invariant warns
  against; caught by rerunning the assertion against the unfixed message before applying the fix.
  `TestExplicitServerNeitherCommandNorUrlFailsValidation` (phase 2, D43) asserted the old shared
  string verbatim and is updated to the new one. Tests:
  `TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl`, `TestMcpServerConfigRejectsMissingTransport`.

- [x] **R2-07** — filed against phase 2; it also lands here, because `isHTTP := mcpServer.Url != ""`
  (`querier_setup_tools.go:177`) is this phase's transport selector and it is the line that silently
  prefers `Url` over `Command` for an unvalidated SDK-supplied server.
  **Phase 4's share verified closed by phase 2's fix, 2026-10-03:** phase 2's D43 fix runs
  `serverconfig.ValidateTransport` over every `userConf.McpServers` entry inside
  `validateExplicitServers`, before any of them are appended to `mcpServers`
  (`querier_setup_tools.go:154-158`). The loop containing this phase's `isHTTP` selector
  (`:209`) only ever iterates `mcpServers`, so by the time this line runs, every entry — explicit
  or ambient — has already passed the XOR check; a both-set or neither-set server never reaches
  it. No further code change needed in this phase; `TestExplicitServerBothCommandAndUrlFailsValidation`
  (phase 2) already proves a both-set explicit server fails at the "config" stage, before any
  connect attempt this phase's selector would otherwise drive.

- [x] **R2-22** — filed against phase 1; `WithHttpReadBound` and `WithHttpProtocolVersion` are this
  phase's two entries in it.
  **Phase 4's share, 2026-10-03:** `WithHttpProtocolVersion` is deleted (R1-26, same symbol).
  `WithHttpReadBound` is kept: unlike the three zero-call-site symbols, it has extensive test call
  sites (`TestHttpConnBodyLimitIsEnforced`,
  `TestHttpConnServerStreamBodyLimitIsPerFrameNotCumulative`, others), and the parameter it
  overrides is deliberately *not* meant to be production-configurable per server — the
  specification says the HTTP and stdio transports "share one notion of an oversized message", the
  same hardcoded constant (`mcpServerOutBufferSizeKib`) stdio's own `WithReadBound` mirrors with
  the identical test-only status. Recorded here as an accepted, by-design test-support option, not
  a dead one.

## Implementation notes — fix session delta, 2026-10-03

Session: `worklog-work` fix session, invoked via the coordinating session to resolve phase 4's
review 1 and review 2 reopening. Deltas only; the original execution notes above are unchanged.

**Findings closed, all verified by a red-before-green test run through production code where the
finding named a production seam:**

R1-12, R1-13, R1-14 (not phase 4's — redirected to phase 5, no code change here), R1-15, R1-20,
R1-26 (phase 4's share), R1-27, R1-34 (closed as a side effect of R2-16), R2-07 (phase 4's share —
verified already closed by phase 2's D43, no code change here), R2-10, R2-11, R2-14, R2-16, R2-18,
R2-22 (phase 4's share).

**R2-16 is the one explicitly flagged as the most important item in this session's queue.** Both
of R2-03's phase-4-scoped mechanisms are now wired for the stdio path: a `NotificationWatcher` on
`StdioConn` (previously it silently dropped every id-less server notification), and the
`cacheInvalidatingConn`/`cacheInvalidatingConnector` wrapper around `resolveLazyServerViaCache`'s
both branches, exactly mirroring the HTTP resolver. The blocking root cause — `isUnknownToolFailure`
matching only HTTP's bespoke `*mcp.RPCCallError` — is fixed at the type level: `mcp.RPCCallError`
is deleted and both transports now build `*claierr.McpRPCError` from one shared constructor, which
also closes R1-34 (a new type outside `pkg/claierr`'s closed vocabulary) as a side effect, since it
was the same symbol.

**Self-caught test-construction bug, same class this worklog repeatedly warns about:** the first
draft of R2-18's fix reused the existing tests' temp file names, `both.json` and `neither.json`.
`assertErrNamesFileAndFields` already asserts the message contains the full file path, so the new
discriminating assertion (`strings.Contains(err.Error(), "both")`) passed *before* `validateTransport`
was touched — not because the message said "both", but because the path did. Caught by printing the
actual error text before trusting the green run, renamed the fixture files to `config-a.json`/
`config-b.json`, reconfirmed red, then fixed `validateTransport` for real. Recorded here because it
is exactly the "a test that cannot fail for the reason it states" class the README's cross-phase
invariant names, caught in a test *I* wrote, not an inherited one.

**New production call site for `Conn.Close()` (R2-10):** `NewHttpConn` now starts a goroutine that
waits on `connCtx.Done()` and calls `Close()`, mirroring `StdioConn`'s pre-existing stdin-closer
goroutine for the same event. This means every `HttpConn`, including ones built directly by a test
with a `context.Background()` that is never cancelled, now carries one extra always-present
goroutine that blocks forever until that context ends — harmless (same shape `StdioConn` already
has), but worth naming since it is a behavioural change to every call site, not only new ones.

**`notifyCh` is now closed by both `Close` implementations (R1-27's dead-branch half), which
introduced a send-on-closed-channel risk** (`publishNotification` sends to `notifyCh` from a
reader goroutine that can run concurrently with `Close`). Resolved by giving `publishNotification`
the same mutex `Close` holds while closing the channel and setting `closed = true`, so a
publish-after-close is excluded by the lock rather than racing it: either `publishNotification`'s
`closed` check sees `true` and returns without sending, or it completes its send before `Close`
acquires the lock. No `-race` failures across three runs confirm this.

**R2-11's retry/backoff constants are new, undeclared-parameter implementation choices**, not
derived from a README row: `mcpServerStreamRetryBackoff` (2s production default, injectable via
`WithHttpServerStreamRetryBackoff` for tests) and `mcpServerStreamMaxConsecutiveFailures` (5,
applies only to connection *attempts* that never establish; an established stream that later drops
resets the counter on its next successful reconnect, so it alone never stops a working-but-flaky
stream from being retried for the rest of the run). Named here per the reading contract's
instruction to promote a cross-phase rule rather than leaving it implicit; this one is local to
phase 4's own transport and not promoted to the README, since no other phase's transport has a
comparable long-lived stream.

**`httptestserver` gained substantial new surface** to let every fix above be proven against the
declared fake rather than a hand-built seam: `PostRequestCount` (R1-15), `PushServerRequest`/
`PostedResponse` (R1-13), `DeleteCount`/`LastDeletedSession`/the `DELETE` branch (R2-10),
`DropStreams`/`GetStreamRequestCount`/`LastEventIDHeaderSeen`/`PushNotificationWithID` (R2-11). A
duplication `dupl -t 80 .` flagged between the new `PushNotification` and `PushServerRequest`
(both built-then-broadcast) was fixed by extracting `broadcastSSE`/`broadcastSSEWithID`, confirmed
by rerunning `dupl` before and after (new clone group gone; same total minus one). The pre-existing
clone between `TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl` and
`TestMcpServerConfigRejectsMissingTransport` (`internal/text/mcp_http_config_test.go`) predates
this session (the file was already untracked-but-present before this fix session started) and is
left as is: both tests are named by the phase's own Error-coverage table, and folding them into one
table-driven test would silently rename `TestMcpServerConfigRejectsMissingTransport` out of
existence, breaking the declared-test-name traceability the phase's own invariant tables depend on.
Accepted per the dupl gate's own signal-not-verdict policy, matching this phase's original
precedent for the `conn_http.go`/`conn_stdio.go` helper duplication.

**`internal/tools/mcp/testserver/main.go` gained two env-driven behaviours** so the stdio schema
cache tests (R2-16) run against a real spawned process through `setupMcpManager`, never a hand-built
seam: `TEST_SERVER_UNKNOWN_TOOL_NAME` (tools/list advertises the named tool; tools/call for it
answers `-32601 "method not found: <name>"`) and `TEST_SERVER_LIST_CHANGED_TRIGGER_TOOL` (tools/call
for the named tool first emits an unsolicited `notifications/tools/list_changed` notification, then
answers normally).

**Verification commands, all run at the end of this session:**

```
go build ./...
go vet ./...
go run mvdan.cc/gofumpt@latest -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
go test ./internal/text/... ./internal/tools/mcp/... ./pkg/claierr/... -race -cover -count=3 -timeout=60s
go test ./... -race -cover -count=3 -timeout=30s
```

Results: `gofumpt -l` empty, `go vet` clean, `staticcheck` clean (exit 0), `go fix` made no changes,
`dupl` reports 35 clone groups (one fewer than before this session's httptestserver fix; the one
remaining group touching a file this session edited is the pre-existing, accepted
`mcp_http_config_test.go` pair above). The full monolithic `go test ./... -race -cover -count=3
-timeout=30s` passed clean on the first attempt — every package `ok`, no `FAIL` anywhere — despite
`/proc/loadavg` reading 10.94 immediately beforehand (above the README's documented "times out
above load ~8" threshold for this gate), so no retry was needed and no load-sensitivity note
applies to this run. `internal/tools/mcp` coverage 82.2%, `internal/text` 84.8%, `pkg/claierr`
77.0% — all above the repository's 70% floor.

`architecture/` was not touched, per the coordinating session's standing instruction for this
worklog.

### Review 3, 2026-10-03 — sign-off review (holistic)

**Status: Reopened (sign-off), resolved.** The first pass to read the whole effort at once rather
than one phase at a time, after reviews 1 and 2 had already closed this phase. Found the one
blocker that gated this phase in the final verdict. Full detail in the README's Sign-off verdict
section and the Sign-off review feedback-index entry.

**Findings**

- [x] **B1** (blocker) — `HttpConn.Call` returned when the response *body ends*, not when its
  answer arrives: `consumeResponseBody` was called synchronously at the old `conn_http.go:185`,
  before the `select` on the waiter channel. A server that holds its POST event-stream open after
  flushing the result — permitted by the specification's "SHOULD" close it, not "MUST" — made every
  call, `initialize` included, hang to its context bound and then fail with `context deadline
  exceeded`, killing the whole lazy HTTP path. `architecture/mcp.md` already documented the correct
  behaviour; no test covered it, and no fixture in the tree could even express the failure.

  **Resolved (sign-off fix session, 2026-10-03).** `Call` now runs `consumeResponseBody` in its own
  goroutine, so the `select` resolves as soon as `deliver` places a result on the waiter channel
  instead of when the background read ends. Evaluated against every path through `Call` (plain
  JSON, event-stream, `initialize`'s `captureSession`/`streamOnce`, and the `ctx.Done()` branch) for
  a race on `resp` or the waiter map; none introduced — `-race` stays clean across three runs.
  `httptestserver` gained `HoldPostStreamOpen`, the fixture mode this review named as missing.
  `TestHttpConnCallResolvesWhenFrameArrivesEvenIfStreamStaysOpen`, proved red (unfixed code returns
  only at the full context bound with `context deadline exceeded`) before green (returns in well
  under the bound). Full detail in the Implementation notes above.

## Review findings — fix verification

All review-1 and review-2 findings against this phase are checked off above with their fix and
test cited in place. The sign-off review's B1 is checked off above too, in the Review 3 section,
with its fix and test cited in place. No finding against this phase remains open. Phase status
restored to `Complete`.
