# MCP architecture

This document describes how clai connects to **Model Context Protocol** servers: the connection
model, the JSON-RPC demultiplexing contract, the bounds that govern a connection, and the lifecycle
from discovery to teardown.

> Related docs:
>
> - `architecture/tooling.md` owns the tool registry, `-t/-tools` selection, and the tool-call
>   execution loop. MCP tools enter that registry like any other tool; how they get there is here.
> - `architecture/errors.md` owns the typed-error vocabulary. The MCP startup error is a row in its
>   table.
> - `architecture/config.md` owns the config directory layout.

## Terminology

- **Server**: a separate program, written by someone else, that offers tools over MCP. A local one
  is a child process spoken to over its standard input and output; a remote one is an HTTP endpoint.
- **Session**: one MCP protocol conversation, established by an `initialize` handshake. For a local
  server the session *is* the process; there is no session identifier and no way to have two.
- **Connection**: clai's object owning one session — its id sequence, its pending callers, and its
  transport.
- **Run**: one clai invocation. One CLI invocation corresponds to one turn (see
  `architecture/chat.md`), so a run is also one turn.

## The connection model

`internal/tools/mcp` exposes two interfaces. `Conn` owns one session; `Connector` resolves a `Conn`
for a server.

```go
type Conn interface {
	Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error)
	Notify(ctx context.Context, method string, params map[string]any) error
	Close() error
}

type Connector interface {
	Conn(ctx context.Context) (Conn, error)
}
```

`Call` issues a request and returns its response. `Notify` sends a notification, which carries no id
and registers no waiter. `Close` is idempotent and fails every pending caller with a typed error
rather than leaving each to its own context deadline.

The transport is a constructor choice, not a runtime branch. Two implementations satisfy `Conn` —
one spawning a child process, one speaking streamable HTTP — and the interface is what every other
package depends on, so the manager, the tool wrapper, the connector and the registration path are
all transport-agnostic. The HTTP transport was added without changing any of them, which is the
property the seam exists to provide.

### A connection is never shared

**One connection belongs to exactly one run.** There is no pooling, no warm reuse between runs, and
no cross-process broker. This is a correctness constraint, not a performance preference, and it was
established by measurement rather than assumed:

- A second `initialize` on an already-initialized stdio server is **accepted without error** and
  silently re-negotiates the session, replacing the first client's protocol version and
  capabilities. Nothing reports that this happened.
- A server may derive its own authorization boundary from the client. `@modelcontextprotocol/server-filesystem`
  issues a `roots/list` request to the client and stores the answer as its `allowedDirectories`, so
  two callers sharing one process share one security scope.

`roots` is a *client* capability in the specification precisely because a server is expected to
scope itself to exactly one client. Sharing a session is therefore a privilege-boundary problem, not
just a state-leak problem. Any future design that shares a session needs a new decision recorded
against this document.

## The demultiplexing contract

One connection owns one **monotonic id source**, used by the handshake and by every tool of that
server, and one **map from pending id to its waiter**. The frame reader is the only reader of the
transport and the only producer of demuxed responses.

This replaces an earlier design in which each tool kept its own id counter while sharing one
transport, which had two live defects: two tools of one server both issued id `1` for their first
call, colliding with each other and with the handshake's own ids; and a caller's receive loop
discarded any frame whose id did not match its own, so one caller could consume and drop another's
response.

Every inbound frame has exactly one of four dispositions, and they do not share a code path:

| Frame | Disposition |
| --- | --- |
| A response whose id has a pending waiter | Delivered to that waiter, never to another |
| A response whose id has no pending waiter | Counted and dropped. A late response to an abandoned call is normal and is not an error for the connection |
| A request from the server, carrying both a `method` and an `id` | Answered with a JSON-RPC method-not-found error. A definite answer, so a server asking for `roots`, `sampling` or `elicitation` is never left waiting |
| Bytes that are not a JSON-RPC message at all | Fails **every** pending waiter with one typed error, leaving the connection usable |

"Not a JSON-RPC message" means precisely one of two things: the bytes are not valid JSON, or they
are valid JSON that is not an object carrying a `jsonrpc` member. Both carry no usable id, which is
why such a frame cannot be attributed to one caller: picking an arbitrary waiter would silently
corrupt the others.

clai advertises no `roots`, `sampling` or `elicitation` capability in its `initialize`, so a
well-behaved server never issues a server-initiated request. The row above exists because observed
servers issue one regardless.

## Goroutines per stdio connection

Four, each with a distinct job. They are listed because collapsing them loses behaviour that is not
obvious from the type:

| Goroutine | Reads | Why it is separate |
| --- | --- | --- |
| Frame reader | the process's standard output | The only reader of the JSON-RPC stream and the only producer of demuxed responses |
| Stderr reader | the process's standard error | Never sees a JSON-RPC frame. It feeds `ServerLogSink`, which is where a server's authorization prompts and its crash tail arrive |
| Stdin closer | — | Closes the process's standard input when the context ends |
| Reaper | — | Waits for the process only after the stderr reader has finished, so a crash tail is complete before an exit is reported. It deliberately does **not** gate on the frame reader: a server may have written a frame nobody consumed, which would otherwise keep exit detection waiting forever |

## Bounds

Three bounds govern a connection, and they cover different things. Confusing them produces errors
that name the wrong stage.

| Bound | Covers | Default |
| --- | --- | --- |
| Handshake | `initialize` plus `tools/list` on one connection | 30 s |
| Per-message read | one inbound frame's size | 2048 KiB |
| Single call | one `tools/call` on an already-established connection | unbounded; set per server with `timeout_seconds` |

An oversized frame fails its own call with a bounded error and leaves the connection usable.

## Handshake and registration

1. `initialize`, advertising clai's protocol version and an empty capability set.
2. `notifications/initialized`.
3. `tools/list`.
4. Each returned tool is registered as `mcp_<server>_<tool>`, where `<server>` is the config file's
   base name. A tool whose input schema cannot be made acceptable to a model is skipped with a
   warning rather than registered.

Registered MCP tools are scoped to the run that discovered them. They are **never** written into the
process-global tool registry, because that registry's entries would be overwritten by every
concurrent `Setup` — see the comment at `internal/text/querier_setup_tools.go`.

## Transports

Two `Conn` implementations, chosen at parse time from the server's config and never guessed at
runtime. Exactly one of `command` and `url` is set; neither or both is a parse error naming the file
and both fields, and a `url` that is not an absolute `http`/`https` URL is rejected the same way.

### stdio

`command` plus `args` spawns a child process and speaks JSON-RPC over its standard input and
output. This is the only option for a server whose capability is local — a filesystem, a browser, a
local database — because there is nothing remote to talk to.

### Streamable HTTP

`url` reaches a remote server over HTTPS with **no local process at all**. Every JSON-RPC message is
POSTed to the single configured endpoint with an accept header naming both JSON and event-stream,
plus the protocol-version header.

The response is **read, and its content type decides how**: a JSON body carries the response
directly, an event-stream body is parsed for frames until the awaited id arrives, and an accepted
status with no body is a completed notification rather than a pending call. Reading the POST
response is not optional — a transport that discards it and waits for the server stream instead will
hang on every server that answers inline, which is the common case.

The server-initiated `GET` stream is **optional**. It is attempted after initialization to receive
notifications such as tool-list-changed, and a method-not-allowed answer is expected and normal: the
connection stays fully usable. When the stream does exist, its frames enter the same demux as POST
responses, and a server-initiated *request* arriving on it is answered with a POSTed
method-not-found response carrying its id rather than being dropped.

A stream that drops is **reconnected** with `Last-Event-ID`, so a server that supports resumption can
replay what was missed. Only consecutive failed connection attempts are capped; a stream that
established and then dropped resets that counter. Without this a single stream close would silently
end tool-list-changed invalidation for the rest of the run.

A session identifier returned on the `initialize` response is retained and replayed on every later
request. A server that omits it is sessionless and no header is added. Closing a session-bearing
connection sends a best-effort, bounded `DELETE` carrying that identifier, so a server can reclaim
its state rather than waiting for a timeout; failing to deliver it is not an error for the run.

Frame-failure attribution differs by origin, following the stdio rationale: a problem in a POST body
fails only that one call, because the body belongs to it; a problem on the shared GET stream fails
every pending call, because it cannot be attributed.

The read bound is enforced **per frame**, not cumulatively across a stream. A long-lived server
stream legitimately carries more bytes over its lifetime than any single message may, so a
cumulative bound would kill it silently once the total crossed the limit.

The legacy event-stream-first transport, deprecated in the specification, is **not** implemented.
Since the config carries no transport field this is a connect-time discovery, not a parse error: an
endpoint is legacy-only when the `initialize` POST is refused as method-not-allowed or not-found
while an event-stream `GET` succeeds, and the connection then fails with a typed error naming the
unsupported transport.

## Authorization

Authorization applies to endpoint-based servers. A challenged endpoint returns
`claierr.AuthChallengeError`, carrying the `WWW-Authenticate` header verbatim and the
`resource_metadata` URL parsed out of it; everything downstream consumes that error and parses
nothing further.

clai implements the published specifications rather than a bespoke scheme, built on the standard
library with no OAuth dependency: RFC 9110 for the challenge grammar, RFC 9728 for protected-resource
metadata, RFC 8414 for authorization-server metadata, RFC 7591 for dynamic client registration,
RFC 7636 for PKCE, RFC 8707 for the `resource` indicator, and RFC 6749 with OAuth 2.1 for the
authorization-code and refresh grants. The `resource` parameter is sent on the authorization request
and on all three token grants, and it is what binds a token to one MCP server so it cannot be
replayed against another behind the same authorization server.

Both discovery documents are fetched with `GET`. The authorization-server document's URL is composed
by **inserting** `/.well-known/oauth-authorization-server` between the issuer's authority and its
path, which is RFC 8414's rule; the OIDC-style appended form is tried second, because the two differ
for any issuer carrying a path component. Five fields are required of that document —
`registration_endpoint`, `authorization_endpoint`, `token_endpoint`, `code_challenge_methods_supported`
containing `S256`, and `grant_types_supported` containing the refresh grant — and a document missing
any of them is a typed error naming which.

Dynamic registration means there is no per-vendor OAuth application to create by hand. It may or may
not issue a client secret; both cases are supported.

### What clai validates, and why fetching is not the whole story

Discovery starts from the server's own `WWW-Authenticate` challenge, so whenever the challenge is
attacker-controlled — a malicious server, or anyone able to forge a 401 — the metadata location is
too. Fetching it is not a neutral act, and the checks below are what make it usable. Each exists
because without it a hostile endpoint could have clai register a client with an attacker and store
the attacker's token as a real credential.

| Check | Rule |
| --- | --- |
| Metadata location | The challenge's `resource_metadata` must be on the **server's own hostname**. Hostname, not port: a resource server may legitimately front its metadata elsewhere on the same host, and this repository's own composed fixtures do. A same-host, different-port variant is therefore not covered, which is a stated limit rather than an oversight |
| Issuer | The authorization-server document's `issuer` must match the issuer whose well-known URL was fetched, per RFC 8414 section 3.3. Only a trailing slash may differ |
| Resource identifier | The protected-resource document's `resource` must cover the server being authorized — scheme, host and port, with the server's path at or under the resource path, per RFC 9728 section 3.3. An **absent** identifier is a refusal, not a pass |
| Transport | `https`, or `http` only on loopback. Enforced on the server endpoint, the metadata location, the issuer, all three advertised endpoints, every discovery redirect hop, and the authorization URL immediately before it reaches a browser |
| Redirects | **Refused outright** on the registration and token endpoints and on the MCP endpoint itself. A 307 or 308 makes Go re-POST the form body, and Go strips an `Authorization` header cross-domain but never strips a body — so a redirecting token endpoint would otherwise be handed the PKCE verifier, the refresh token and the client secret. Discovery `GET`s allow a small bounded number of hops, each re-checked against the transport rule |

A plain-`http` endpoint is still accepted in config, deliberately: a LAN server reached over `http`
with a static `token_env` credential is a legitimate setup whose risk its operator already owns.
What is refused is clai *minting* a credential for one, so an interactive flow will not run against a
non-loopback plain-`http` server endpoint.

### Credential sources

Four, tried in order. The distinction between them is what makes the rule unambiguous:

| Source | Shape | Intended use |
| --- | --- | --- |
| `auth.token_command` | A command whose trimmed standard output is the access token | A fleet, where a driver holds the refresh token and an agent receives only a short-lived access token. Composes with any external secret manager |
| `auth.token_env` | A variable name resolved from the process environment or the configured envfile | A server issuing long-lived personal access tokens |
| Token store | A `0600` file per server under `<clai-config>/mcpAuth/` | A workstation that completed the browser flow once |
| Interactive flow | Discovery, registration, authorization, exchange | First-time authorization |

A source is **unset** when its field is absent, and an unset source is skipped silently. A source is
**configured** when its field is set, and a configured source that fails is a typed error with **no
fall-through** — a misconfigured secret manager must not quietly degrade into a browser prompt on a
headless host. The token store is a **cache** rather than a configured source, so its absence or
corruption is a miss and the next source is tried.

A credential command runs under the run's context, so cancellation reaps it, and is subject to the
same command-ban policy that governs the freetext command tools. Its standard output is the token
and is never logged; its standard error is captured and included in a failure, because that is the
only diagnostic an operator gets.

An access token is refreshed before expiry by a margin, and refresh is single-flight per server, so
a run with several calls in flight performs one refresh rather than one per call.

### What is secret

Three fields in the token store are secret and never appear in a log line, an error string, a cache
entry or debug output: the access token, the refresh token, and the client secret. Two more exist
only in flight and are never written at all: the PKCE verifier and the authorization code. The
issuer, the client id, the expiry, the scopes and the resource identifier are not secret and may
appear in an error.

`clai mcp auth <server>` runs the interactive flow once and writes the store entry. It performs no
model call and spends nothing.

### When a human is needed mid-run

A lazily connected server can need a person partway through a run — an expired token is the ordinary
case. Three facts shape how that is handled.

First, **the wait happens outside connection resolution.** A resolution that cannot proceed without
a human fails fast with the authorization error rather than blocking. The wait then happens at the
tool-call site, and exactly one further resolution is attempted afterwards, which gets fresh bounds
in the ordinary way. The reason is concrete: resolution is wrapped in a handshake bound and a connect
bound, both `context.WithTimeout` deadlines, and a deadline cannot be paused. A design that waited
inside resolution would have the enclosing bound fire first, so the user would see a connect-stage
error instead of the prompt they can act on, and the human-wait bound would be unreachable.

Second, **an authorization failure is not immediately a run-scoped failure, but the exemption is
capped.** The connector memoises a terminal outcome and does not retry it, which is what stops a
broken server causing a spawn storm. A challenge reports itself as blocked on something outside the
run, so it leaves the memo untouched — otherwise the server would stay dead for the rest of the run
even after the person had just authorized it.

That exemption is bounded rather than unconditional, and the bound is load-bearing. An unbounded one
turns a *misclassified* line into a spawn storm: the stderr classifier that spots an authorization
prompt is a keyword heuristic, and a server logging something that merely looks like one would be
re-dialed on every tool call, each time waiting out the full human bound. So a blocked-outside-run
outcome buys a small, fixed number of re-dials per server per run; past that the challenge is
memoised like any other terminal failure. The classifier used for *reclassification* is also a
deliberately narrower, higher-confidence subset of the one used for *display*, since a false
positive costs a process where a missed prompt costs only a dimmer window.

The interactive flow is refused outright on a run whose output is not a terminal, and the refusal
lives where the flow begins rather than at each call site. That placement is itself the fix for a
defect: the gate had been applied per caller, so a second caller arrived later without it and would
have opened a browser and bound a loopback listener inside a headless SDK consumer's own process.

Third, **the wait follows the terminal.** When the session's output is a terminal the wait is
bounded and the prompt is surfaced: the pre-session window reopens for that server, the prompt and
its payload lines pin above the bounded tail, and the terminal bell rings once per wait through the
existing theme gate. When the output is not a terminal the wait fails fast instead, so a piped or
headless run is never interactive by default and a fleet configures nothing. An explicit
`auth_timeout_seconds` overrides either way, and its zero value means fail fast.

On expiry the tool call returns an actionable result, the run continues, and the expiry consumes no
tool-call slot beyond the one already reserved for the triggering call. What that result advises
depends on the transport, because the advice has to be followable: an endpoint-based server is
pointed at the command that authorizes it, while a command-based server — which that command does
not serve — is pointed at its own standard error, where its prompt actually appeared.

**The wait is infrastructure-only and never model-invocable.** It may be raised solely by a
component that cannot proceed without a credential. No tool exposes it and no model request reaches
it, directly or indirectly. That line is deliberate: clai is not an interactive agent, and a
model-reachable wait would be an approval-and-clarification channel in all but name. The distinction
worth keeping is between blocking on a secret the machine does not hold and conversing with the
operator.

## Tool schema cache

Setup does not need a connection to tell a model which tools exist — it needs a `tools/list` result,
and that result is the only part of a handshake worth keeping. It is cached under
`<clai-cache>/mcpSchemas/`, in a file named by a digest of the server's identity, following the same
digest-keyed convention as directory-scoped chat bindings.

An entry is valid only while the identity still matches. For a command-based server the evidence is
local and exact, so there is no time bound: the recorded size and modification time of the resolved
executable and of the envfile must still match, as must a digest over the configured `env` map. An
**envfile's contents are never digested** — its freshness rides its size and modification time,
which keeps secret material away from a digest. The inherited process environment is excluded too,
since digesting it would change the key with every shell and defeat the cache.

An endpoint-based server has no executable to stat and no local evidence of change, so its entry
carries a freshness bound and is a miss beyond it. Two signals invalidate immediately inside that
bound: a `notifications/tools/list_changed` notification, and a `tools/call` that fails because the
server does not know the tool. The requested scopes are part of an endpoint identity, because they
change which tools an endpoint exposes.

**Only content-determined data is written.** A connect failure, an authorization failure and a tool
error are facts about one run, not about the server, and none is persisted. An invalidation removes
an entry; it never records a failure. A write failure degrades to connecting rather than failing
setup, and a corrupt entry is a miss rather than an error.

A **miss connects**, exactly as setup did before the cache existed, and captures the result. The
zero-process outcome is therefore a *warm-cache* outcome: a server's first run pays its spawn once
and writes the entry, and every run after that pays nothing until one of its tools is called.

## Startup posture

`startup` selects when a server's connection is made.

| Mode | Behaviour |
| --- | --- |
| `lazy` | The default. No transport is constructed at setup; the connection is made on the first tool call that targets the server, once per run |
| `eager` | Connect during setup. The default for a server supplied through `agent.WithMcpServers` while strict startup is on, so its failure still lands where that contract's callers expect it |

A resolution is memoised per server per run. A connect failure is marked for the run and not
retried, so a model repeatedly calling a broken server cannot cause a spawn storm. A resolution
blocked on something *outside* the run — a human authorizing a server is the only such case — is
neither a success nor a failure: it returns its typed error, leaves the memo untouched, and consumes
no retry, because the blocking condition can change while the run is still going.

## Configuration

One JSON file per server under `<clai-config>/mcpServers/`, unmarshalled into
`pkg/text/models.McpServer`. The file's base name becomes the server name.

```jsonc
// A local server. Exactly one of "command" and "url" is set.
{ "command": "node", "args": ["server.js"],
  "env": { "KEY": "value" },
  "envfile": "~/.secrets/server.env",
  "startup": "lazy",                      // or "eager"
  "timeout_seconds": 0,                   // one tool call; 0 is unbounded
  "connect_timeout_seconds": 45,          // spawn plus handshake
  "auth_timeout_seconds": 120 }           // human wait. Applies here too: a
                                          // local server prompts on stderr

// A remote server.
{ "url": "https://mcp.example.com/mcp",
  "startup": "lazy",
  "auth_timeout_seconds": 120,            // human wait; 0 is fail fast.
                                          // Unset follows the terminal
  "auth": { "token_command": ["doppler", "secrets", "get", "TOKEN", "--plain"],
            "token_env": "EXAMPLE_MCP_TOKEN",
            "scopes": ["read", "write"] } }
```

Discovery is ambient: every configured server is used whenever tooling is on and no tool glob
narrows the set. A querier built with `text.Configurations.SkipAmbientMcpServers` opts out of
directory discovery while servers passed explicitly in `Configurations.McpServers` still apply; the
one-tool conversation summarizer is the in-tree consumer.

## The tool listing

`clai tools` lists built-in tools and, since the shadow-advisory work, MCP tools too. It had claimed
to do the latter for a long time without doing it: `List` reads the process-global tool registry,
that registry is populated only by `registerLocalTools`, and `setupMcpManager` is reached only from
the query path. Writing MCP tools into the global registry is not an option — concurrent setups
would overwrite each other, which is why the per-run registry rule exists — so the listing has its
own **cache-only** source instead.

For each configured server the listing builds the identity, reads its schema-cache entry, and lists
what it finds into a listing-scoped tool set. It **never connects** and never spawns, because a
listing is not a run and must stay as cheap as it is.

A cache entry exists only where a handshake completed, so an entry *is* the evidence of a prior
successful run. A server with no entry — never run, or whose last run failed — contributes nothing
and is **omitted rather than flagged**: a tool listing is what a user reads to find out what they
can call, not a diagnostic surface. An entry appears after the server's first successful query run.

### Built-in shadow marker

The listing ends with **one footer line** naming the in-process built-ins that may already cover
something a configured local server offers. It is deliberately not a per-tool annotation.

A per-tool marker was built first and removed, and the reason is worth keeping. The mapping it drove
is declared data keyed on an MCP tool's name, and a name is not evidence of locality: the dominant
remote-server shape in the wild is a local launcher proxying an endpoint — `npx -y mcp-remote
https://…` — whose tools are named exactly like a local server's. So the marker confidently told
users that `cat` covers a Notion page read. The phase's own specification had already stated the
governing rule, that a guess marking an unrelated tool as redundant is worse than no marker, and the
per-tool form could not satisfy it. A footer carries the same useful signal with nothing specific to
be wrong about.

Only command-based servers contribute to the footer, since the saving it advertises is a process
removed from every run and a remote endpoint costs no process. A built-in is named only when the
process-global registry actually holds it, so one whose executable is absent is never suggested. The mapping is declared data in `internal/tools/builtin_shadow.go`,
not a heuristic on names, because marking an unrelated tool as redundant is worse than marking
nothing. A built-in counts as available only when the process-global registry actually holds it,
which is how a built-in whose executable is absent is already gated.

The marker **reports and never resolves**. Selection, registration, the schemas sent to a model and
execution are all unchanged by it. Silently dropping a configured tool because clai believes it has
an equivalent would be a surprising behaviour change and would hide real differences in semantics.

The reason this is worth surfacing at all is cost: the most-installed local MCP server duplicates
capabilities clai has in-process for free, so replacing it with built-ins removes a process from
every run.

## Failure posture

The posture differs by how a server was requested, and the distinction is deliberate:

- A server discovered from the config directory is **ambient**. A startup failure warns and the run
  continues, because a broken optional server should not fail a query.
- A server passed through `agent.WithMcpServers` is **explicit** and sets
  `AgentSettings.StrictMcpStartup`. Its startup failure is returned as a typed error, because a
  caller whose task depends on that server must be able to see that it is absent.

Startup failures are reported as `claierr.McpServerStartupError`, carrying the server name and the
stage that failed. See `architecture/errors.md`.

## Server output

A server's standard error is not protocol traffic and is handled separately from the frame stream.
`internal/text/mcp_log_sink.go` classifies each line and decides where it goes: authorization
prompts and their payload lines pin above a bounded rolling tail so machinery chatter cannot evict
the prompt a user must act on, error lines elevate, and a bounded tail is retained per server so an
unexpected termination can be reported with its reason. A context-cancelled teardown is not an
unexpected termination and is not reported as one.

## Package map

| Package | Holds |
| --- | --- |
| `internal/tools/mcp` | `Conn`, `Connector`, both transports, the manager, the tool wrapper |
| `internal/tools/mcp/schemacache` | the tool-schema cache: identity, record, lookup, capture, invalidate, and the listing's cache-only read |
| `internal/tools/mcp/serverconfig` | the server-config parser and transport validation, a leaf package so the tool listing can use it without an import cycle |
| `internal/tools/mcp/mcpauth` | the OAuth client, the token store, and the credential chain |
| `internal/tools/mcp/testserver` | fake stdio MCP server, for tests |
| `internal/tools/mcp/httptestserver` | fake streamable-HTTP MCP server, for tests, including challenge and bearer modes |
| `internal/tools/mcp/oauthtestserver` | fake OAuth authorization server and protected-resource metadata, for tests |

## Not yet implemented

**Mid-run token expiry does not re-authorize.** The bearer credential is captured as a string when
the connection is made and installed once, and the authorization wait inspects only connector
*resolution*, never the result of a call. So a token that expires on an already-established
connection surfaces to the model as "authorization required" while a good refresh token sits unused
in the store. Closing it needs a refreshable decorator seam on the HTTP connection, a typed
call-result path the executor can classify, and a re-entry rule for a connection already handed
out — a piece of work in its own right rather than a patch.

One scope reduction from the authorization work is also live: the full credential chain including
the interactive flow is wired into the lazy endpoint path, which is the default, while an
`eager`-configured endpoint server gets only the static sources, `token_command` and `token_env`.

See `worklogs/2026-10-02-mcp-connection-cost/` for the phased plan, its measurements, and the
decisions behind it.
