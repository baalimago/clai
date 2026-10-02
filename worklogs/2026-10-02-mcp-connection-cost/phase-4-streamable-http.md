# Phase 4 — Streamable HTTP transport

**Status:** Not Started

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

## Implementation notes

Not started.

## Review findings

None.
