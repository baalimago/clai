# MCP connection cost reduction

## Goal

clai is this repository: a command-line program that sends a prompt to a model and runs tools on the
model's behalf. An MCP server is a separate program, written by someone else, that offers clai extra
tools over the Model Context Protocol. A local one is launched as a child process and spoken to over
its standard input and output; a remote one is an HTTPS endpoint. clai reads one JSON file per server
from its config directory and advertises their tools to the model alongside its own built-ins.

Make every configured MCP server pay only the cost it actually incurs. Today every run spawns
every configured server eagerly, so a turn that never calls an MCP tool still pays full process
birth for all of them, and a remote service reached through a stdio adapter pays a local Node
process for what is only HTTPS. This worklog introduces a single connection seam, makes stdio
connections lazy behind a persisted tool-schema cache, and adds a native streamable-HTTP transport
with an OAuth 2.1 client so remote servers need no local process at all. The target is a clai run
whose MCP cost is proportional to the servers it uses, not the servers it is configured with.

## Status board

| # | Phase | Status | Outcome |
| --- | --- | --- | --- |
| 1 | [Conn seam and JSON-RPC demux](phase-1-conn-seam-and-demux.md) | Not Started | One owner per connection holds a connection-wide id counter and an id-to-waiter map; the two live demux defects are gone |
| 2 | [Lazy connect](phase-2-lazy-connect.md) | Not Started | For a server explicitly marked lazy, its process is created on the first tool call that targets it, once per run, with a run-scoped failure that never becomes a spawn storm. The default still flips in phase 3 |
| 3 | [Tool schema cache](phase-3-schema-cache.md) | Not Started | On a warm cache, setup advertises a command-based server's tool schemas from a delta-validated on-disk entry and connects nothing. A miss still connects and captures, per D18 |
| 4 | [Streamable HTTP transport](phase-4-streamable-http.md) | Not Started | A remote server is reached over spec-compliant streamable HTTP with no local process, and the cache gains its endpoint-based half |
| 5 | [OAuth 2.1 and credential sources](phase-5-oauth.md) | Not Started | Discovery, dynamic client registration, PKCE, refresh, a token store, a credential command, and a static bearer fallback |
| 6 | [Mid-run auth surfacing](phase-6-midrun-auth.md) | Not Started | A connection that needs a human is announced, pinned and bounded instead of silently stalling a tool call |
| 7 | [Built-in shadow advisory](phase-7-shadow-advisory.md) | Not Started | `clai tools` gains an MCP tool set from the cache for servers that have succeeded, then names every entry already covered by an in-process built-in |
| 8 | [Quality gate sweep](phase-8-gate-sweep.md) | Not Started | Repository gates pass unedited and the architecture notes match the shipped behaviour |

Complete the phases in order. There is no gating spike: every external claim this design rests on
was measured before the worklog was written, and the measurements are recorded under Strategy. A
phase may be split during execution but never reordered, because each phase consumes the seam the
previous one establishes.

## Strategy

### Measured evidence this design rests on

Taken 2026-10-02 on the maintainer's Linux host. Every number below is observed, not estimated.
An executor must not re-derive these; they are inputs.

Spawn cost of one representative Node MCP server, `@modelcontextprotocol/server-filesystem`,
warm npx cache, three runs each:

| Spawn form | initialize | tools/list | total | RSS of process tree |
| --- | --- | --- | --- | --- |
| `npx -y @modelcontextprotocol/server-filesystem /tmp` | 3.8 to 4.5 s | 31 to 34 ms | 4.0 to 4.5 s | 122 MiB |
| `node .../server-filesystem/dist/index.js /tmp` | 1.05 to 1.22 s | 37 to 51 ms | 1.1 to 1.3 s | 75 MiB |
| `clai version`, whole process, for scale | n/a | n/a | 10 ms | 10 MiB |

The handshake's `tools/list` round trip is between roughly one and five percent of the cost,
depending on the spawn form: 34 ms against a 4.5 s `npx` total is 0.8 percent, and 51 ms against a
1.1 s direct-`node` total is 4.6 percent. Everything else, in both forms, is process birth. Two ratios matter and both must stay in view: the common form, `npx`, costs roughly four
hundred times clai's whole startup, and even the direct form costs seven and a half times clai's
entire resident footprint, rising to twelve times for the `npx` form.

Transport census of the official registry, `registry.modelcontextprotocol.io/v0/servers` with
`version=latest`, 384 pages, 38,306 distinct servers of which 37,851 active:

| Topology, active servers | share |
| --- | --- |
| Remote endpoint only | 57.5 percent |
| Local package only | 36.5 percent |
| Both | 4.8 percent |
| Neither, broken listing | 1.2 percent |

Within those, `remotes[].type` is 95.5 percent `streamable-http` and 4.5 percent legacy `sse`;
`packages[].transport.type` is 97.1 percent `stdio`; local package runtimes are 60.8 percent npm
and 23.9 percent PyPI, so the overwhelming majority of local servers are in the Node or Python
cost class measured above. Dropping stdio support would lose 36.5 percent of the registry and
dropping HTTP support would lose 57.5 percent, so both transports are load-bearing and neither
replaces the other.

Usage weighting inverts toward stdio. Weekly npm downloads from
`api.npmjs.org/downloads/point/last-week/<package>`, same date: `@playwright/mcp`
8,541,359; `chrome-devtools-mcp` 2,487,841; `mcp-remote` 732,960; `server-filesystem` 562,943;
`@upstash/context7-mcp` 447,808.

Two separate facts come out of that list and must not be fused. First, the registry topology above
is about how a server *declares* itself: 57.5 percent of active servers declare only a remote
endpoint and nothing local. Second, and independently, the `mcp-remote` figure is a stdio-to-HTTP
bridge: a local Node process whose only job is to reach a remote endpoint on behalf of a client that
cannot speak HTTP with OAuth itself. So a server declaring a remote endpoint does not imply it is
reached without a local process today. clai is currently in the group that needs such a bridge, and
removing that need is what phase 4 and phase 5 are for.

Vendor endpoint probes. Six endpoints were probed with an unauthenticated `initialize` POST:
`mcp.linear.app/mcp`, `mcp.notion.com/mcp`, `mcp.intercom.com/mcp`, `mcp.sentry.dev/mcp`,
`mcp.atlassian.com/v1/sse`, `api.githubcopilot.com/mcp/`. All six answered `401` with
`WWW-Authenticate: Bearer realm="OAuth"`, and the first four carried RFC 9728
`resource_metadata` pointing at `/.well-known/oauth-protected-resource`. Authorization-server
metadata for Linear, Notion and Intercom each exposes a `registration_endpoint`, advertises
`S256` in `code_challenge_methods_supported`, and lists `refresh_token` in
`grant_types_supported`. Dynamic client registration is therefore available and a design without
it would require a hand-created OAuth application per vendor for no saving.

Multi-client probe of a stdio server, recorded because it closes a design question rather than
opening one: a second `initialize` on an already-initialized `server-filesystem` process is
accepted without error and silently re-negotiates the session, and the server actively issues a
`roots/list` request to the client and stores the answer as its `allowedDirectories`
authorization boundary. A stdio session is therefore inseparable from its process and from that
process's security scope. No part of this worklog shares a stdio connection between runs, agents
or callers, and none may be added without a new decision row.

### Expected end state

This is a projection from the measurements above, not a measurement, and phase 8's human-required
step is what turns it into one. Its arithmetic is shown so it can be checked rather than trusted.

Assumptions, stated because the measurements do not supply them: a representative agent is
configured with two local servers and four remote services; a local server costs 90 MiB, the
midpoint of the measured 75 MiB direct-`node` and 122 MiB `npx` trees, because a real configuration
mixes both forms; clai itself costs the measured 10 MiB; and in the final row one agent in four is
mid-call at any instant, which is the only row where concurrency matters.

| Stage | Per agent | Arithmetic | Fleet of one hundred, binary units |
| --- | --- | --- | --- |
| Today, six servers all spawned eagerly | 550 MiB | 6 × 90 + 10 | 53.7 GiB |
| After the transport phase, the four remote services need no process | 190 MiB | 2 × 90 + 10 | 18.6 GiB |
| After the cache phase, warm, a turn calling one local server | 100 MiB | 1 × 90 + 10 | 9.8 GiB |
| With the shadow advisory applied, so only genuinely-local capability remains | 10 MiB idle, 100 MiB when called | 10, or 1 × 90 + 10 | 1.0 GiB idle; 3.2 GiB at one agent in four, from 25 × 100 + 75 × 10 |

### External specifications

The authorization work is an implementation of published specifications, not an invention. An
executor reads the relevant one rather than inferring it, and each is named here so nobody has to
guess which. Only the protected-resource path was observed directly during the measurement session
recorded above, in four `resource_metadata` parameters; the authorization-server path and the
composition rule below come from the specification, and the three vendors' metadata contents were
observed but not the URL each was fetched from.

| Specification | What clai takes from it |
| --- | --- |
| RFC 9110, HTTP semantics | The grammar of the `WWW-Authenticate` challenge header, whose parameters carry `resource_metadata` |
| RFC 9728, OAuth 2.0 Protected Resource Metadata | The document at `/.well-known/oauth-protected-resource`, fetched with `GET`, whose `authorization_servers` and `scopes_supported` drive the next step |
| RFC 8414, OAuth 2.0 Authorization Server Metadata | The authorization-server document, fetched with `GET`. Its URL is composed from an issuer in the protected-resource document's `authorization_servers` by **inserting** `/.well-known/oauth-authorization-server` between the issuer's authority and its path, which is RFC 8414's rule and differs from OIDC-style appending whenever the issuer has a path component. clai tries the inserted form first and the appended form second, and reports both as missing if neither answers. clai requires five fields: `registration_endpoint`, `authorization_endpoint`, `token_endpoint`, `code_challenge_methods_supported` containing the PKCE parameter's value, and `grant_types_supported` containing the refresh grant |
| RFC 7591, Dynamic Client Registration | The `POST` to `registration_endpoint` that yields a `client_id` and, optionally, a `client_secret` |
| RFC 7636, PKCE | The verifier, and the challenge as the base64url-encoded SHA-256 of it |
| RFC 6749 and OAuth 2.1, authorization code and refresh grants | The authorization request built from `authorization_endpoint`, and the two `POST`s to `token_endpoint` |
| MCP specification, authorization | That an MCP server is an OAuth protected resource and discovery begins at its challenge |
| MCP specification, streamable HTTP transport | The request and response contract the transport phase implements, including the optional server-initiated stream and the session header |

### Non-negotiable invariants

1. **A stdio connection is never shared.** One connection belongs to exactly one run. No pooling,
   no warm reuse, no cross-process broker. The probe above is why.
2. **No run-fact is ever persisted.** A startup failure, an auth failure and a tool error are
   facts about one run, not about the server. Only content-determined data, the negotiated
   `tools/list` result, is cacheable. This continues the rule already established for the foreign
   conversation index.
3. **A failure in expectation is an error.** Absence of a connection, of a schema, of a credential
   or of a tool is returned as a typed error carrying why, never logged and swallowed, and never a
   bare `bool` beside a value.
4. **No new third-party dependency.** `go.mod` carries only `go_away_boilerplate`,
   `golang.org/x/exp`, `golang.org/x/net` and `golang.org/x/text`. The OAuth client is built from
   the standard library. `golang.org/x/oauth2` is not available and must not be added.
5. **No test spends money and no test reaches a vendor endpoint.** Every automated test runs
   against an in-repo fake. Real-endpoint verification is a declared human-required step.
6. **A credential never appears in a log line, an error string or a cached file.** Redaction is a
   table row in phase 5, not a convention.
7. **The human wait is infrastructure-only.** It is raised only by a component that cannot proceed
   without a credential, never by a tool and never on a model's request. No model-callable surface
   reaches it, directly or indirectly.
8. **The generic layer stays vendor-agnostic.** Transport-specific and vendor-specific behaviour
   lives in `internal/tools/mcp`, never in `internal/text/generic`.
9. **Ambient servers degrade, explicit servers fail.** A server discovered from the config
   directory keeps today's warn-and-continue posture; a server passed through
   `agent.WithMcpServers` keeps `StrictMcpStartup`. Laziness must not convert a strict failure
   into a silent one.

### Shared interfaces between phases

Phase 1 introduces the single seam every later phase consumes. It replaces the raw channel pair
returned by `mcp.Client` today:

```go
// Conn owns one MCP session: its id sequence, its pending waiters, and its
// underlying transport. One Conn belongs to exactly one run.
type Conn interface {
	Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error)
	Notify(ctx context.Context, method string, params map[string]any) error
	Close() error
}

// Connector resolves a Conn on first use. Implementations are single-flight:
// concurrent callers for one server share one resolution.
type Connector interface {
	Conn(ctx context.Context) (Conn, error)
}
```

Phase 2 makes `mcpTool` hold a `Connector` instead of channels. Phase 4 adds a second `Conn`
implementation beside the stdio one. Phase 5 supplies a request decorator to the HTTP
implementation. Phase 6 adds the auth-pending signal to both the stdio and the HTTP connection and
owns its interface; neither phase 2 nor phase 5 builds it.

The hand-off from the transport phase to the authorization phase is a typed error, declared here so
both phases agree on its shape. Phase 4 returns it; phase 5 consumes it and nothing else:

```go
// AuthChallengeError reports that an endpoint demands authorization. Challenge
// is the verbatim WWW-Authenticate header value; ResourceMetadata is the URL
// parsed out of its resource_metadata parameter, empty when absent.
type AuthChallengeError struct {
	ServerName       string
	Challenge        string
	ResourceMetadata string
}
```

Transport selection is a parse-time property of the server config, not a runtime guess. Exactly
one of `command` and `url` must be set; neither or both is a parse error naming the file.

Test fixtures are owned, not assumed. The stdio phases reuse the existing fake at
`internal/tools/mcp/testserver`. Exactly two new fixture packages are introduced:

- Phase 4 introduces the fake streamable HTTP MCP server. Among its configurable modes is answering
  with an authorization challenge, which is what makes it serve as phase 5's protected resource as
  well. Phase 5 adds no second MCP-over-HTTP fake; it configures this one.
- Phase 5 introduces the fake OAuth authorization server only.

Each owning phase declares its fixture's package path and its configurable modes under a `Fixtures
introduced` subsection.

### Code layout and owners

Every new symbol, file and test fixture this worklog introduces, and the single phase that
introduces it. An executor creates nothing outside this table without adding a row first.

| Symbol or file | Package path | Owner |
| --- | --- | --- |
| `Conn`, `Connector` interfaces | `internal/tools/mcp/conn.go` | Phase 1 |
| stdio `Conn` implementation, replacing `Client` | `internal/tools/mcp/conn_stdio.go` | Phase 1 |
| `ServerLogSink`, which `client.go` defines today and which phase 1 carries over unchanged | moves to `internal/tools/mcp/conn.go` beside the connection interfaces | Phase 1 |
| typed errors added by this worklog | `pkg/claierr/claierr.go`, beside `ErrMcpServerStartup` | Phase 1 |
| `StartupMode` named type and the `startup` field | `pkg/text/models/tools.go`, on `McpServer` | Phase 2 |
| per-server connector with single-flight resolution | `internal/tools/mcp/connector.go` | Phase 2 |
| schema cache: identity, record, read and write | `internal/tools/mcp/schemacache` | Phase 3 |
| lazy-path end-to-end fixture, so the lazy path runs under the race detector | a root `*_e2e_test.go` beside the existing fixtures | Phase 3 |
| the setup-side cache call site: reads the cached tool list, registers its tools, wires each to its `Connector` without a transport | `internal/text/querier_setup_tools.go`, a new path beside the existing `ControlEvent` flow | Phase 3 |
| endpoint-based cache identity, freshness and invalidation | `internal/tools/mcp/schemacache`, extending Phase 3's record | Phase 4 |
| `AuthChallengeError` | `pkg/claierr/claierr.go` | Phase 4 |
| scopes component of the endpoint cache identity | `internal/tools/mcp/schemacache` | Phase 5 |
| `auth_timeout_seconds` field | `pkg/text/models/tools.go`, on `McpServer` | Phase 6 |
| streamable HTTP `Conn` implementation | `internal/tools/mcp/conn_http.go` | Phase 4 |
| `url` endpoint field and config validation | `pkg/text/models/tools.go`; validation in `internal/text/querier_setup_tools.go` | Phase 4 |
| fake streamable HTTP MCP server | `internal/tools/mcp/httptestserver` | Phase 4 |
| OAuth client: discovery, registration, PKCE, exchange, refresh | `internal/tools/mcp/mcpauth` | Phase 5 |
| token store | `internal/tools/mcp/mcpauth/store.go` | Phase 5 |
| browser opener and its fake: the injectable that opens an authorization URL. No such helper exists in the repository today, so this is new code | `internal/tools/mcp/mcpauth`, with the fake in `internal/tools/mcp/oauthtestserver` | Phase 5 |
| `auth` config block and its fields | `pkg/text/models/tools.go`, on `McpServer` | Phase 5 |
| fake OAuth authorization server, which also serves the protected-resource metadata document. Not an MCP server: phase 4's fake is configured for that role | `internal/tools/mcp/oauthtestserver` | Phase 5 |
| `clai mcp auth` subcommand | `internal/tools/mcp/cmd.go`, injected from `main.go` beside the other commands | Phase 5 |
| `AuthPendingSink` and its wiring into the log sink | interface in `internal/tools/mcp/conn.go`; implementation on `mcpLogSink` in `internal/text/mcp_log_sink.go` | Phase 6 |
| the call sites that raise the auth-pending signal, one per transport | `internal/tools/mcp/conn_stdio.go` and `internal/tools/mcp/conn_http.go` | Phase 6 |
| the tool-call-site wait and the single follow-up resolution | `internal/text/tool_executor.go`, at the existing `InvokeWith` call site | Phase 6 |
| declared MCP-tool-to-built-in mapping | `internal/tools/builtin_shadow.go` | Phase 7 |
| the listing's MCP tool source: a cache-only read that gives `clai tools` an MCP tool set without connecting | `internal/tools/cmd.go`, extending `List` and `Detail`, plus a cache-only entry point in `internal/tools/mcp/schemacache` | Phase 7 |
| `architecture/mcp.md` | repository `architecture/` directory | Phase 1 creates it; later phases add their own sections |

### Record formats

Three records are written or read by this worklog. Each is owned by one phase and shown here once so
no phase has to invent a shape.

**Server config**, pre-existing: one JSON file per server under `<clai-config>/mcpServers/`,
unmarshalled into `pub_models.McpServer`. The file's base name becomes the server name, as
`findConfiguredMcpServers` already does. Phases 2, 4, 5 and 6 add fields to that struct:

```jsonc
{ "command": "node", "args": ["server.js"],        // XOR "url": exactly one is populated
  "env": { "KEY": "value" }, "envfile": "~/.secrets/x.env",
  "timeout_seconds": <single-call bound>,            // values: parameters table below
  "connect_timeout_seconds": <connect bound>,
  "startup": "lazy",                                 // or "eager"
  "auth_timeout_seconds": <auth-timeout>,
  "auth": { "token_command": ["doppler", "secrets", "get", "X", "--plain"],
            "token_env": "LINEAR_MCP_TOKEN",
            "scopes": ["read", "write"] } }
```

**Schema cache entry**, Phase 3 for the command-based identity, Phase 4 for the endpoint-based one,
Phase 5 for the scopes component. Stored under the schema-cache-directory parameter, named by the
schema-cache-file-name parameter. The environment digest is a hex SHA-256 over the environment map
serialised as sorted `key=value` lines, one per line. `server_info` is the `serverInfo` object from
the `initialize` result, stored verbatim as raw JSON and never interpreted:

```jsonc
{ "identity": { "command": "node", "args": ["server.js"], "url": "",
                "env_digest": "<hex sha256>",      // command XOR url is set, never both
                "envfile": { "size": 412, "mtime": "2026-09-30T11:02:14Z" },
                "executable": { "path": "/usr/bin/node", "size": 84213,
                                "mtime": "2026-08-01T09:00:00Z" },
                "scopes": ["read", "write"] },
  "protocol_version": "<protocol-version parameter>",
  "server_info": { "name": "...", "version": "..." },
  "tools": [ "...the tools/list result verbatim..." ],
  "captured_at": "2026-10-02T09:42:11Z" }
```

**Token store entry**, Phase 5. One file per server under the token-store-directory parameter,
written with the token-store-mode parameter. A client secret appears only when dynamic registration
issued one. Three fields are secret and are what the redaction table in phase 5 governs:
`access_token`, `refresh_token` and `client_secret`, together with the PKCE verifier and the
authorization code, which exist only in flight and are never written. `issuer`, `client_id`,
`expires_at` and `scopes` are not secret and may appear in an error or a log line:

```jsonc
{ "issuer": "https://mcp.linear.app", "client_id": "...", "client_secret": "",
  "access_token": "...", "refresh_token": "...",
  "expires_at": "2026-10-02T10:42:11Z", "scopes": ["read", "write"] }
```

### Existing code this worklog builds on

An executor does not have to discover any of this. Each row is verified present at the time of
writing.

| What | Where | Why it matters |
| --- | --- | --- |
| Per-turn MCP setup: ambient discovery, eager spawn, blocking wait | `internal/text/querier_setup_tools.go`, `setupMcpManager` | The function this worklog changes |
| Server config loading and naming | same file, `findConfiguredMcpServers` | Where new fields are parsed |
| The channel pair being replaced | `internal/tools/mcp/client.go`, `Client` | Phase 1 replaces its return shape |
| The per-tool id counter and the id-discarding receive loop | `internal/tools/mcp/tool.go` | The two defects phase 1 fixes |
| Tool registration prefix `mcp_<server>_<tool>` | `internal/tools/mcp/manager.go` | Unchanged by this worklog |
| Sequential tool batch execution | `internal/text/tool_executor.go`, `runPlannedCall` | Why the demux defects are latent today |
| Typed startup error constructor | `pkg/claierr`, `NewMcpServerStartup` and `ErrMcpServerStartup` | The pattern new typed errors follow |
| Strict-startup posture for explicit servers | `pkg/agent/agent.go`, `WithMcpServers` setting `StrictMcpStartup` | The contract phase 2 must not weaken |
| Auth, payload and error line classifiers | `internal/utils/print.go`, `IsMcpLogAuthLine`, `IsMcpLogAuthPayloadLine`, `IsMcpLogErrorLine` | Phase 6 reuses these; it writes no new classifier |
| Pre-session auth window and its one-shot flags | `internal/text/mcp_startup.go`, `mcpStartupWindows.cleared`; `internal/text/mcp_log_sink.go`, `mcpLogSink.attached` | What phase 6 makes re-openable |
| Theme-gated terminal bell | `internal/utils/theme.go`, `NotificationBellEnabled`; emitted in `internal/command.go` | Phase 6 reuses the gate |
| Token and tool-call budgets | `internal/text/stoploss.go` | What phase 6's wait must not consume |
| Command-ban policy carried on the tool-call context | `pkg/tools`, `WithCmdBanContext` | Phase 5's credential command is subject to it |
| Trusted input reader threaded through setup | `internal/text/setup_querier.go`, the `trustInput io.Reader` parameter | Phase 5's printed-URL fallback reads the code from it |
| Digest-keyed on-disk record precedent | `internal/chat/dirscope.go` and `internal/chat/paths.go` | The convention phase 3's cache follows |
| Built-in tool catalogue | `pkg/text/models/tools.go`, the `ToolName` enum | Phase 7's mapping targets |
| The tool listing command and its data source | `internal/tools/cmd.go`: `Command`'s `OnRun` calls `Init()` then `List()`, and `List` reads the process-global `Registry`. It never runs `setupMcpManager`, which `internal/text/querier_setup_tools.go:261` reaches only from the query path | Why phase 7 must add a listing-side MCP source. The command's help text and `architecture/tooling.md` both claim it lists MCP tools, which has never been true |
| The per-run registry rule | `internal/text/querier_setup_tools.go:153`: MCP tools "must never be written into the process-global tools registry" | Why phase 7 may not fix the listing by writing MCP tools into the global registry |
| The abandoned HTTP transport | branch `origin/feat/mcp-client-streamable-http`, `internal/tools/mcp/http_client.go` | Phase 4's starting critique, not its starting code |

### Severity taxonomy

Used by validation and review rounds on this worklog.

| Severity | Meaning |
| --- | --- |
| `blocker` | An invariant above is broken, or a phase cannot be executed as written |
| `major` | A specified behaviour has no test, or two phases disagree |
| `minor` | A contract is ambiguous but has one reasonable reading |
| `note` | Style, wording or a suggestion outside the readiness checklist |

## Parameters and owners

Every default, limit and tunable lives here once. Phase files refer to a row by name and never
restate its value. The Owner column names the single phase that introduces the field, flag or
constant.

| Parameter | Default | Owner |
| --- | --- | --- |
| startup-mode, the `startup` per-server field, `lazy` or `eager` | `eager` as phase 2 ships it; `lazy` after phase 3 flips it, except `eager` for a server supplied through `agent.WithMcpServers` while strict startup is on | Phase 2 introduces the field; Phase 3 flips the default |
| Fixture posture for `startup` in the tests that actually spawn a server, namely `internal/text/querier_setup_tools_test.go` and `internal/text/mcp_log_sink_test.go` | `eager`, pinned explicitly. The root end-to-end fixture creates an empty `mcpServers` directory and spawns nothing, so pinning it there would pin nothing | Phase 3 |
| Cache directory injection wherever `setupMcpManager` is exercised by a test | a per-test temporary directory, never the developer's real cache directory | Phase 3 |
| connect-bound, the `connect_timeout_seconds` field, which wraps spawn plus handshake | 45 | Phase 2 |
| single-call bound, the pre-existing `timeout_seconds` field | 0, meaning unbounded | Phase 1, preserved |
| handshake-bound, for `initialize` plus `tools/list`, pre-existing `mcpStartupTimeout` | 30 s | Phase 1, relocated onto `Conn` |
| retry-count, connect attempts per server per run after a failure | 0 retries | Phase 2 |
| schema-cache-directory, under the clai cache dir | `mcpSchemas` | Phase 3 |
| schema-cache-file-name | `<hex sha256 of identity>.json` | Phase 3 |
| schema-cache-freshness-bound, for endpoint-based servers | 12 h | Phase 4 |
| Freshness rule for command-based servers | size and mtime delta, no time bound | Phase 3 |
| protocol-version, the version clai advertises in `initialize` | `2025-06-18` | Phase 1, which sends the handshake |
| Legacy `sse` remote transport | out of scope, see D7 | Phase 4 |
| response-body-limit, per message | 2048 KiB, matching the existing stdio scanner bound | Phase 4 |
| loopback-host, the OAuth redirect listener host | `127.0.0.1` | Phase 5 |
| loopback-port, the OAuth redirect listener port | ephemeral, kernel-assigned | Phase 5 |
| PKCE, the challenge method | `S256` | Phase 5 |
| token-store-directory, under the clai config dir | `mcpAuth` | Phase 5 |
| token-store-mode | `0600` | Phase 5 |
| refresh-skew, before expiry | 60 s | Phase 5 |
| credential-precedence | `token_command`, then env var, then token store, then interactive | Phase 5 |
| auth-timeout, the `auth_timeout_seconds` field, `0` means fail fast | 120 when the session's output is a terminal, otherwise 0; an explicit value overrides both | Phase 6 |
| `url` server endpoint field, required when `command` is absent | n/a | Phase 4 |
| `auth.token_command` credential command | unset | Phase 5 |
| `auth.token_env` static-bearer variable name | unset | Phase 5 |
| `auth.scopes` requested scopes | whatever the server advertises | Phase 5 |
| `clai mcp auth` subcommand | n/a | Phase 5 |
| Declared MCP-tool-to-built-in mapping, declared data rather than a tunable | the table in `internal/tools/builtin_shadow.go` | Phase 7 |
| injected-clock, used by every time-dependent bound and capture time in this worklog | the real clock | Phase 3 |
| stdio-read-bound, the per-message read bound, pre-existing `mcpServerOutBufferSizeKib` | 2048 KiB | Phase 1, preserved |
| coverage-floor for new code | the repository floor in `CLAUDE.md`, with the higher preferred figure as the target | Phase 8 |
| auth-pinned-line-cap per server, pre-existing `mcpStartupPinnedCap` | 4 | Phase 6, preserved |

## Readiness checklist

Run before requesting validation; record the outcome in the session journal.

1. No numerals in phase files outside integration-contract oracle rows.
   `grep -nE '(^|[^=])\b[0-9]+([.,][0-9]+)? ?(s|ms|h|KiB|MiB|MB|%)\b' phase-*.md`
2. Every test name is declared in exactly one phase. Dedupe per file first: concatenating the
   phase files over-reports every name a phase repeats between its own invariants and acceptance
   tables, which is expected and not a defect.
   `for f in phase-*.md; do grep -ohE '\bTest[A-Za-z0-9_]+' "$f" | sort -u; done | sort | uniq -d`
3. Every config field, flag and new constant has exactly one owner row in the parameters table
   above. A pure injectable with no value to tune, such as a clock or a browser opener, is owned by
   a row in the code-layout table instead, since a parameters row with no default would be noise.
4. Every invariant and every limit is a table with a test named per row; no prose-only invariant.
5. Every phase mentioning listening, manual, paid, credentials or a real endpoint carries a
   `Human required` subsection.
6. No phase references text, symbol or file scheduled for deletion by an earlier phase.
7. New conventions do not contradict existing code conventions. Cite the file checked: command
   placement against `internal/tools/cmd.go`, typed errors against `pkg/claierr/claierr.go:34`,
   cache keying against the dirscope convention, sink behaviour against
   `internal/text/mcp_log_sink.go`.
8. Every claim about an external system traces to a measurement under Strategy, not to an
   assumption, or is labelled in place as specification-derived.
9. The two test files that spawn an MCP server pin the eager posture explicitly.
   `grep -n 'startup' internal/text/querier_setup_tools_test.go internal/text/mcp_log_sink_test.go`

## Decisions log

| ID | Date | Decision | Rationale | Replaces |
| --- | --- | --- | --- | --- |
| D1 | 2026-10-02 | Both stdio and streamable HTTP are first-class; neither is deprecated in clai | Census: dropping stdio loses 36.5 percent of the registry, dropping HTTP loses 57.5 percent | — |
| D2 | 2026-10-02 | No connection, process or session is shared between runs, agents or callers | Probe: a second `initialize` silently clobbers the session, and `server-filesystem` takes its `allowedDirectories` authorization boundary from a per-process `roots/list` answer | An earlier proposal for a warm-pool daemon and an exported MCP pool |
| D3 | 2026-10-02 | A `Conn` interface replaces the exported channel pair, owning a connection-wide id sequence and an id-to-waiter map | The per-tool `seq` collides across tools of one server and the receive loop discards non-matching ids; both are latent only because tool batches run sequentially | The `(chan<- any, <-chan any, error)` return of `mcp.Client` |
| D4 | 2026-10-02 | stdio connections default to `lazy`; `eager` is an explicit per-server opt-in | A turn that never calls an MCP tool should pay nothing; interactive-auth stdio servers still need setup-time connection | Unconditional eager startup in `setupMcpManager` |
| D5 | 2026-10-02 | Only the `tools/list` result is cached; a connect, auth or call failure is never written to disk | A failure is a fact about one run; the tool list is content-determined by the server build | — |
| D6 | 2026-10-02 | Full OAuth 2.1 with discovery, dynamic client registration, PKCE and refresh, built on the standard library | All three probed authorization servers expose a `registration_endpoint` with `S256` and `refresh_token`; a token-store-only design would have no `client_id` without hand-created apps per vendor, for nearly the same build | An earlier recommendation of a token store with refresh but no registration |
| D7 | 2026-10-02 | Legacy `sse` remote transport is out of scope for this worklog | 4.5 percent of remote declarations, officially deprecated and on a removal clock; the streamable-HTTP client already parses SSE framing on POST bodies, so the residual work is a GET-only variant that can be added later without redesign | The SSE-first shape of the abandoned `feat/mcp-client-streamable-http` branch |
| D8 | 2026-10-02 | The token store is a `0600` plaintext file under the clai config directory | No keyring library may be added, and the fleet path avoids secrets at rest entirely by using `token_command` | — |
| D9 | 2026-10-02 | A credential command is a first-class credential source, ahead of the token store | It keeps clai out of the secret-storage business, composes with any external secret manager, and lets a fleet driver hold the refresh token while agents receive only short-lived access tokens | — |
| D10 | 2026-10-02 | Mid-run auth blocks with a bounded `auth_timeout_seconds`, where `0` means fail fast | An unbounded block is a hang, and a headless fleet needs a typed failure rather than a bell nobody is watching | — |
| D11 | 2026-10-02 | MCP behaviour moves to a dedicated `architecture/mcp.md`, with the MCP sections of `architecture/tooling.md` reduced to a pointer | The MCP surface after this worklog is larger than a section of the tooling note | The MCP servers section of `architecture/tooling.md` |
| D12 | 2026-10-02 | Phase order is risk-ascending: seam, laziness, cache, transport, auth, surfacing, advisory, gate | Maintainer declined to choose an order and asked for all of it; risk-ascending keeps every increment independently shippable | — |
| D23 | 2026-10-02 | The human wait is infrastructure-only and never model-invocable. It may be raised solely by a component that cannot proceed without a credential, and no tool exposes it | clai is not an interactive agent and must not drift into one. A model-reachable wait would be an approval-and-clarification channel in all but name, and the distinction worth protecting is between blocking on a secret the machine does not hold and conversing with the operator. Stated as an invariant so a later feature cannot arrive through this seam (maintainer direction, 2026-10-02) | — |
| D22 | 2026-10-02 | The human wait defaults to the terminal: bounded when the session's output is a terminal, fail-fast when it is not. An explicit `auth_timeout_seconds` overrides either way | A fixed waiting default would make every piped or headless run interactive by default, which is the opposite of clai's one-invocation-one-turn character, and would force every fleet to configure its way back out. The repository already resolves this exact signal in `mcpLogModeFor`, so interactivity becomes a property of being at a terminal rather than of clai (maintainer direction, 2026-10-02) | The unconditional 120-second default introduced with the bounded wait |
| D21 | 2026-10-02 | `clai tools` gains its own MCP listing path, owned by phase 7, which reads the schema cache and never connects, and which lists only servers that have succeeded: a cache entry exists only where a handshake completed, so an entry is the evidence of success, and a server without one is omitted rather than flagged, because a tool listing is not a diagnostic surface (maintainer direction, 2026-10-02). D18 loses its claim that the listing warms the cache | Verified in source: `tools.Command`'s `OnRun` is `Init()` then `List()`, `List()` reads the process-global registry, `setupMcpManager` is reached only from `setupTooling` on the query path, and `querier_setup_tools.go:153` forbids MCP tools from entering the process-global registry by design. The listing has therefore never shown an MCP tool, although its own help text and `architecture/tooling.md` both say it does. Phase 7 cannot mark what the listing cannot show, and the warming claim was never true | D18's sentence that `clai tools` warms the cache, and phase 7's assumption that the listing already receives MCP tools |
| D20 | 2026-10-02 | Bound suspension is abandoned. An authorization wait happens **outside** resolution: resolution fails with `AuthChallengeError`, phase 6 performs the bounded wait at the tool-call site, and exactly one further resolution is attempted afterwards with fresh bounds | A `context.WithTimeout` deadline is immutable, which is the shape `manager.go:63` already uses and the shape phases 1 and 2 specify. "Stop counting" and "restart from zero" had no mechanism in the phases that own those bounds, no owner row and no seam, so the only ways to green the test were to reopen two shipped phases or to ship the original bug behind a shrunken bound. Raising the challenge out of resolution is what phase 4 already does for HTTP, needs no new primitive, and makes the enclosing bounds irrelevant rather than suspended | The suspension semantics and the ordering-constraint parameter introduced to close V5-01 |
| D19 | 2026-10-02 | The endpoint cache identity does not include the credential or its principal | Invariant 6 forbids a credential in a cached file, and a digest of issuer plus subject would be a new principal-tracking concept for a benefit nobody has measured. The consequence is accepted and recorded: two principals on one host sharing a server config share one cached tool list. Revisit only with a measured case | — |
| D18 | 2026-10-02 | A cache miss connects during setup, exactly as today. The zero-process outcome is a warm-cache claim, not a cold-cache one | The existing `Test_setupMcpManager_RegistersToolsAndNotifiesSuccess` writes a cold config directory and asserts its tools are registered. A miss that declined to connect would ship a run with an empty MCP tool box and break that test, so the only compatible policy is to connect and capture. The win is undiminished in practice: a fleet's cache is warm after its first run per server | The unqualified zero-process claim in the first draft's Definition of Success |
| D17 | 2026-10-02 | `auth.mode` is removed; the credential source is decided solely by the precedence chain in D15 | A selector and a precedence chain are mutually exclusive, and the worklog specified both: with a selector defaulting to OAuth, a configured credential command would never run. The precedence chain is the one phase 5 actually specifies and tests, so the selector goes | The `auth.mode` field in the first draft's record formats and parameters table |
| D16 | 2026-10-02 | Phase 2 ships the lazy machinery with the `startup` default still `eager`; phase 3 flips the default to `lazy` once the schema cache can supply tool schemas without connecting | Phase 2 cannot both register every tool and construct no transport: the only source of a tool list is `tools/list`, and the cache that serves it without connecting arrives in phase 3. Splitting machinery from activation keeps each phase independently shippable, which is what D12 claims, and matches this repository's preference for flipping a default separately from building the thing it enables | The unconditional `lazy` default attributed to phase 2 in the first draft |
| D14 | 2026-10-02 | A server under strict startup defaults to `eager`, and no new post-setup error channel is introduced | The strict contract's failure already lands at setup time, where `agent` callers observe it. Inventing a run-scoped error channel to report a lazy strict failure would add a seam no code has and no phase owns. An operator who sets `lazy` on a strict server has opted out of setup-time failure and receives the typed error as a tool result instead | The run-error-channel reporting path described in the first draft of phase 2 |
| D15 | 2026-10-02 | A credential source that is unset is skipped silently; a source that is configured and fails is a typed error with no fall-through | Without the distinction the no-fall-through rule and the per-source error rows contradict each other. The token store is a cache rather than a configured source, so its absence or corruption is a miss | The undifferentiated no-fall-through rule in the first draft of phase 5 |
| D13 | 2026-10-02 | Built-in shadowing is reported, never auto-resolved | `clai tools` can tell a user that an MCP tool duplicates an in-process built-in; silently dropping a configured tool would be a surprising behaviour change | — |

## Definition of success

| Outcome | Evidence |
| --- | --- |
| A run with stdio servers configured, **a warm cache**, and no MCP tool call creates no MCP process | End-to-end test asserting zero child processes for the configured fake server after a prior run warmed its entry |
| A first run against a cold cache still registers every tool, by connecting | The repository's existing `Test_setupMcpManager_RegistersToolsAndNotifiesSuccess`, still passing unmodified. It is cited, not introduced, so it is deliberately not a new name under checklist item two |
| A miss is captured, so the next run hits | `TestSchemaCacheMissConnectsCapturesThenHits` |
| A run that calls one tool from one of several configured stdio servers creates exactly one process | End-to-end test counting spawns per server across a multi-server fixture |
| Three tool calls to one server in one batch create exactly one process | `TestThreeCallsOneServerSpawnOnce`. A batch is sequential today, so this is reached through memoisation; the single-flight contract is a separate unit-level test with no production trigger |
| A remote server completes `initialize`, `tools/list` and `tools/call` with no local process | Integration test against the in-repo spec-compliant fake over loopback HTTP |
| A tool schema survives a process restart without reconnecting | Cache hit test asserting no transport construction on the second setup |
| A changed server binary or envfile invalidates the cache | Delta-validation test mutating size and mtime |
| An expired access token is refreshed without human interaction | Refresh test against the fake authorization server |
| A credential never appears in output | Redaction table in phase 5, one test per row |
| Repository gates pass unedited | `make qa`, with `go test ./... -race -cover -count=3 -timeout=30s` unmodified |
| New code meets the project coverage floor | Coverage report in phase 8 against the coverage-floor parameter |

## Validation policy

The repository gates defined in `CLAUDE.md` apply unchanged and must pass unedited. Phase 8 owns the
gate table and is the single place their invocations are written down; no other file in this worklog
restates them. The race, count and timeout flags are not to be altered, no test may be skipped, and
no test may be added that passes for the wrong reason.

Two explicit exceptions, both declared as `Human required` in their phases:

1. Real-endpoint OAuth verification against a live vendor endpoint needs the maintainer's own
   credentials and a browser. It is a one-off manual confirmation, never an automated test.
2. The fleet memory measurement that proves the headline outcome needs the maintainer's host and a
   multi-agent run; the automated suite proves process counts, not fleet RSS.

Note the host sensitivity already recorded for this repository: the race gate is load-sensitive and
times out above roughly load eight. Any new default-on behaviour introduced here must stay off in
the shared end-to-end fixture.

## Feedback index

One entry per round, mapping each finding ID to the edit or decision row that closed it.

### Round 1 — self-validation before implementation, 2026-10-02

| ID | Severity | Finding | Closed by |
| --- | --- | --- | --- |
| V1-01 | blocker | Phase 2 reported a lazy explicit server's failure through a run error channel that does not exist in the code, is absent from the shared interfaces, and is owned by no phase. The phase could not have been executed as written | D14, plus the rewrite of phase 2's failure-posture section, one invariant row, one integration row and one error row to use the existing strict setup path |
| V1-02 | major | Every error-coverage row in phases one through seven cited a test that is also an acceptance criterion, and twenty-eight of them named a happy-path test that does not exercise the named failure. Phase 8's rows named no test at all | Twenty-eight dedicated error tests introduced across phases one through seven; phase 8 gained an explicit statement that a gate phase's rows name commands rather than tests |
| V1-03 | major | The `startup` field had two defaults: the parameters table said lazy, phase 2 said eager for a strict explicit server | Parameters table row now carries the conditional default; phase 2 refers to it without restating |
| V1-04 | major | Phase 5's no-fall-through rule contradicted three error rows that said the next source is tried | D15 defines unset against configured against cache; the rule and all affected rows rewritten |
| V1-05 | major | The fake HTTP MCP server and the fake OAuth servers were named as collaborators in two phases but created by none and located nowhere | `Fixtures introduced` subsections added to phases 4 and 5, plus a fixture-ownership paragraph in the README shared interfaces |
| V1-06 | major | Phase 1 had no limits table although it relocates the handshake bound and depends on the stdio read bound | Limits table added to phase 1 with three rows, each naming its trigger |
| V1-07 | minor | The pre-existing stdio read bound had no owner row | Parameters row added, owned by phase 1 as preserved |
| V1-08 | minor | The coverage floor was stated in the definition of success without a parameters row or owner | Parameters row added, owned by phase 8; the definition of success now refers to the row |
| V1-09 | minor | Readiness checklist item two required every test to appear in a file list, but no phase contains a file list | Checklist item two reduced to the rule the worklog can satisfy: one declaring phase per test name |
| V1-10 | note | Strategy's measured evidence is reproducible only by re-running the described registry query; no artifact is stored in the repository | Accepted. The query, its filter, its page count and its date are recorded so the census can be re-run; storing a snapshot of the registry in the repository is not worth its size |
| V1-11 | note | Phases 7 and 8 have no limits table | Both now state explicitly that they introduce no limit, so a later round does not read the absence as an omission |

### Round 6 — independent validation, plus a targeted phase-6 probe, 2026-10-02

Rounds 1 to 4 and every comprehension finding confirmed resolved with no regressions. V5-02 through
V5-15 confirmed resolved. V5-01 confirmed closed in form but not in substance, and re-raised.

| ID | Severity | Finding | Closed by |
| --- | --- | --- | --- |
| V6-01 | blocker | V5-01's closure had no mechanism. Phase 6 claimed the handshake bound and the connect bound "stop counting" and are "restarted from zero", but both are `context.WithTimeout` deadlines, which cannot be paused; the word appears nowhere in the phases that own those bounds, no code-layout row grants a suspendable bound, and the only ways to green its test were to reopen two shipped phases or to ship V5-01's bug behind a shrunken bound | D20: suspension abandoned. The wait now happens outside resolution — resolution fails with `AuthChallengeError`, the wait runs at the tool-call site under auth-timeout alone, and one further resolution follows with fresh bounds. Phase 6 rewritten accordingly with three new tests; phase 2 states that a resolution blocked outside the run is not memoised as a failure and consumes no retry; the ordering-constraint parameter is deleted, which also closes V6-03 and V6-04 |
| V6-02 | blocker | The sixth instance of the recurring class, in the "Goal unmet with a green table" variant. Phase 7 marks MCP tools in the tool listing, but the listing has no MCP tools and nothing in the worklog gave it any. Verified in source: `List` reads the process-global registry, that registry is populated only by `registerLocalTools`, `setupMcpManager` is reached only from the query path, and `querier_setup_tools.go:153` forbids MCP tools from entering the global registry by design. Every phase-7 criterion could go green while `clai tools` printed no MCP tool at all. The same verification showed D18's claim that `clai tools` warms the cache to be false in source | D21: phase 7 now owns a cache-only listing source with its own code-layout row, three invariant rows, three acceptance rows, three tests and two existing-code rows recording the verified facts. Its Goal leads with giving the listing an MCP tool set. D18 loses the warming claim |
| V6-03 | major | The two halves of the V5-01 fix contradicted each other: the parameters row justified a parse error because auth-timeout sits inside an enclosing bound, while phase 6 said the wait is governed by auth-timeout alone. The constraint also rejected valid configurations, tested only one of its two halves, and compared against a bound that is not a configurable field | Dissolved by D20: the constraint is deleted, since no enclosing bound is in flight during the wait |
| V6-04 | major | `TestAuthWaitSuspendsEnclosingBounds` could not observe its claim. Its limits row advanced an injected clock past two bounds that phases 1 and 2 trigger with a non-answering fake server and never mention a clock for, and a context deadline cannot be advanced by an injected clock | Dissolved by D20: the replacement test waits past both enclosing bounds with no resolution in flight, which needs no clock injection into them |
| V6-05 | major | Adjacent breakage from the V5-02 closure. Moving the protected-resource document onto the OAuth fake put it at a different origin from the MCP resource server, so phase 5's discovery chain needs phase 4's fake to emit a challenge pointing at the authorization fake's bound address — a capability phase 4 did not declare, while phase 5 forbids itself a second MCP-over-HTTP fake | Phase 4's fixture capability list now includes a challenge whose `resource_metadata` points at a caller-supplied URL |
| V6-06 | minor | C4-05 moved protocol-version's owner to phase 1 and changed nothing in phase 1, which never mentioned it. The live handshake advertises an older version than the parameter, so a phase-1 executor would carry the old value forward, phase 3 would cache it, and phase 4's limits row would assert the parameter — the two transports diverging on the wire with no test catching it | Phase 1 now states the version moves onto the connection and that this is a change from the live value, with a limits row and `TestConnAdvertisesProtocolVersion` |
| V6-07 | minor | Checklist item 3 required a parameters row for every injectable, which the browser opener deliberately does not have | Item 3 amended: a pure injectable with no value to tune is owned by a code-layout row |
| V6-08 | minor | Two phase-3 invariant rows named tests that cannot observe their claims — an injected cache directory that defaults to the real one, so forgetting it still passes, and a property of other test files reachable only by static analysis | The cache directory became a required constructor argument, so omitting it fails to construct; the posture row became readiness-checklist item nine, a grep over the two named files |
| V6-09 | minor | V5-01's design change carried no decision row, so the decisions log did not record that the connect bound no longer wraps spawn plus handshake as a whole | D20 records it, with its `Replaces` cell |
| V6-10 | minor | The challenge-to-wait-to-retry step had no stated invocation point, which matters because a failed resolution is memoised and the retry count is zero | Phase 2 states the blocked-resolution outcome explicitly; phase 6 states that the wait happens at the tool-call site and triggers exactly one further resolution |
| V6-11 | note | Phase 6 is the phase most dependent on a human and had no real-world confirmation | A `Human required` subsection added for a mid-run authorization against a real server |
| V6-12 | note | One phase-6 test name cited for two distinct observables | Split into `TestSessionLoopDoesNotRenderIntoAuthWindow` |
| V6-13 | note | The fleet column did not survive the arithmetic column printed beside it | Recomputed in binary units, with the concurrency assumption stated |
| V6-14 | note | Edit-pass residue: unreflowed lines and a stale "omits the executable component" wording | Wording aligned with the canonical-absent-marker definition |

### Comprehension probes, round 4 — phase-6 executor after the bound change, 2026-10-02

| ID | Finding | Closed by |
| --- | --- | --- |
| C5-01 | The probe independently landed on the same clause round 6 raised: "doesn't explain why both enclosing bounds must be advanced if they are suspended", and "'restarted from zero' mechanism undefined". Two independent readers converging on one sentence is what made it worth re-examining rather than defending | D20, which deletes the clause entirely |
| C5-02 | Asked which of phase 6's tests could pass while the feature was broken, it named the ordering-constraint test, which would go green with no suspension mechanism implemented at all | Dissolved by D20 |
| C5-03 | The code-layout table listed phase 6 as touching only the connection interface and the log sink, while the shared-interfaces section says phase 6 adds the signal to both the stdio and the HTTP connection. Two files phase 6 must modify had no row | Rows added for both connection files under phase 6 |

### Round 5 — independent validation, plus two executor probes, 2026-10-02

| ID | Severity | Finding | Closed by |
| --- | --- | --- | --- |
| V5-01 | blocker | An authorization wait is raised inside resolution, so the handshake and connect bounds both enclose it and both defaults are smaller than auth-timeout. The enclosing bound would always fire first, D10's bounded block would be inert, and the expiry test passed only because its limits row injected a shrunken bound | Closed with bound suspension, which round 6 then showed had no mechanism. Superseded by D20 |
| V5-02 | major | Regression of V4-03 in the table that governs file creation: phase 5 was still assigned a protected-resource fake | Row reduced to the authorization server, with a note that phase 4's fake fills the MCP role |
| V5-03 | major | Phase 4's Goal could be unmet with all sixteen acceptance rows green: every row was scoped to the connection in isolation or to config parse errors, so the transport could ship fully tested and unreachable from a config file | Invariant, integration and acceptance rows plus `TestSetupRegistersHttpServerToolsAndCallsOne`, covering config to connector to registration to call |
| V5-04 | minor | V4-01 residue: the status board and phase 3's Goal still said the cache phase connects nothing | Both qualified with the warm-cache condition and a pointer to D18 |
| V5-05 | minor | V4-07 half-resolved: phase 2's Goal was qualified, its status-board Outcome was not | Outcome qualified |
| V5-06 | minor | The external-specifications lead-in claimed every endpoint path was observed; only the protected-resource path was | Lead-in restricted to what was observed |
| V5-07 | minor | C2-01 one level down: the authorization-server path was named but not how its URL is composed from an issuer, and the two candidate rules differ for a path-bearing issuer | Composition rule stated with a fallback order, plus an error row and a test |
| V5-08 | minor | The expected-end-state figures were not derivable from the measurements they cited | Assumptions stated and an arithmetic column added |
| V5-09 | minor | The C2-04 fix introduced a claim its own arithmetic refuted | Restated as a range, each division attributed to its spawn form |
| V5-10 | minor | The browser opener had a fake in two places and no owner row | Code-layout row and limits row added |
| V5-11 | note | Phase 3's lazy end-to-end fixture had no code-layout row | Row added |
| V5-12 | note | Phase 1's acceptance row for the retired startup override cited a test that cannot observe it | Replaced with the build and vet gates |
| V5-13 | note | One test name covered both a close with a pending call and a double close | Idempotency split out |
| V5-14 | note | `ServerLogSink` is defined in the file phase 1 replaces, with no row saying where it lands | Row added |
| V5-15 | note | Phase 4's endpoint identity carried the environment components with no behaviour behind them | Rationale stated: they are carried so the authorization phase adds a component rather than restructuring the key |

### Comprehension probes, round 3 — two executor probes, worklog only, 2026-10-02

| ID | Finding | Closed by |
| --- | --- | --- |
| C3-01 | Phase 1 said "the only reader of the transport" while phase 6 depends on a stdio server's stderr. Verified in source: `mcp.Client` runs four goroutines including a separate stderr reader feeding the log sink. Since phase 1 replaces `Client` wholesale, an implementer would have deleted the input phase 6 is built on, and the crash tail with it | Phase 1 now enumerates all four goroutines, marks the stderr reader separate, says why it must be carried over, and asserts it with `TestStdioConnStderrStillFeedsSink` |
| C3-02 | "Cannot be parsed as JSON-RPC at all" was undefined and its relationship to an unknown-id frame unclear | Defined as invalid JSON or valid JSON without a `jsonrpc` member, and explicitly separated from the unknown-id path |
| C3-03 | The actionable tool-result string had no format and the single-call-bound exclusion no mechanism | Exact string given; the exclusion stated as a run-derived context with the call's bound started after the wait |
| C3-04 | Phase 1 never said its concurrent-calls invariant is defensive with no production trigger | Stated in phase 1, since a phase-1 executor may not read phase 2 |
| C3-05 | The classifiers were named but not characterised, and the single `ControlEvent.StartupTimeout` call site not named | Both stated |
| C4-01 | Phase 3's whole acceptance table could go green while no tool was registered from the cache: the criterion asserted the absence of a transport, never the presence of tools | A `setup-side call site` subsection, the contract that a hit and a miss register identical tool sets, and `TestCacheHitAndMissRegisterIdenticalToolSets` |
| C4-02 | Which environment feeds the digest was unstated, and digesting the inherited process environment would change the key with every shell | Stated: the configured map merged with the envfile, process environment explicitly excluded |
| C4-03 | The identity for an absent envfile and an unresolvable executable was undefined, and each choice changes the digest | Both defined as a canonical absent marker |
| C4-04 | Phase 3 pointed at a record format showing the final shape, so it could not tell which fields were its own; write timing and timestamp format were unstated | All three stated |
| C4-05 | Protocol-version was owned by phase 4 though phase 1 sends it and phase 3 stores it | Owner moved to phase 1 |
| C4-06 | Phase 7 did not say how it detects an unavailable built-in, the Go structure of its mapping, or whether the listing connects | All three stated |

### Round 4 — independent validation, plus three comprehension probes, 2026-10-02

Round 4 verified every prior ID and confirmed all of rounds 1 to 3 closed except two residues
(V4-10 for V1-02, V4-13 for C1-03). It also cross-checked all declared test names against the
repository's existing tests and found no collision.

| ID | Severity | Finding | Closed by |
| --- | --- | --- | --- |
| V4-01 | blocker | The round-4 instance of the class rounds 2 and 3 each surfaced once, one level down: phase 3 never said what setup does on a cache **miss**, and its own artifacts demanded opposite answers. Its integration row said a miss connects; the Goal, the headline Definition of Success row and the fixture pin only made sense if a miss does not. Verified against the repository: `Test_setupMcpManager_RegistersToolsAndNotifiesSuccess` writes a cold config directory and asserts `mcp_echo_echo` is registered, so a miss that declined to connect would break it and ship an empty tool box | D18: a miss connects and captures, exactly as today, and the zero-process outcome is restated as a warm-cache claim. Phase 3 gained a `What a miss does at setup` section stating it first, an invariant row, and `TestSchemaCacheMissConnectsCapturesThenHits`; the Definition of Success gained a cold-cache row citing the existing test |
| V4-02 | major | `auth.mode` was documented as a credential-source selector defaulting to `oauth`, while phase 5 specified a four-source precedence chain and never mentioned the field. With a selector a configured credential command would never run; with precedence the field is inert | D17: `auth.mode` deleted from the record format and the parameters table. The precedence chain from D15 is the only mechanism |
| V4-03 | major | Three overlapping fixture defects: the README said two new fixtures and named three; phase 4 forbade a later HTTP MCP fake while phase 5 introduced a protected-resource fake that answers tool calls, which is one; and phase 4's own challenge integration row needed a fixture mode its capability list lacked | Challenge mode and bearer-token mode added to phase 4's fixture capabilities; phase 5 reduced to exactly one fixture, the authorization server, and now configures phase 4's fake rather than duplicating it into the duplication gate's path; README count corrected |
| V4-04 | major | No invariant or acceptance row covered the capture path, so every phase-3 criterion could go green against a hand-seeded cache file while clai never wrote an entry — the goal unachieved with a fully green table | Invariant and acceptance rows added for the miss, connect, capture, hit round trip |
| V4-05 | major | The fixture pin that closed V3-10 was vacuous: the root end-to-end fixture only `MkdirAll`s an empty `mcpServers` directory and configures no server, verified in `main_test_helpers_test.go`. The tests that actually spawn a server are package tests, and nothing injected a cache directory for them, so a package run would have written into the developer's real cache directory | Parameters row now names the real spawning tests, a second row requires an injected cache directory wherever setup is exercised, and phase 3 adds its own lazy end-to-end fixture so the lazy path is covered under the race detector rather than left uncovered by the pin |
| V4-06 | major | Phase 2 never said which context bounds a resolution or what the memo holds after an abandoned one, and phase 6 asserted a retry "could spawn again", contradicting phase 2's memoisation and its zero-retry parameter | Phase 2 now states the resolution runs under the run context, what the memo holds after an abandoned wait, and adds `TestAbandonedWaitDoesNotCancelResolution`; phase 6's claim corrected to match |
| V4-07 | minor | Residue of V3-01 in the two places the fix did not reach: phase 2's Goal and its status-board Outcome still described the end state after phase 3 | Both qualified. The Goal now describes the machinery and says explicitly that the saving arrives with the cache phase |
| V4-08 | minor | The shared-interfaces section said phases 2 and 5 raise the auth-pending signal, which phase 6 owns and neither mentions — the forward-reach class in documentation form | Reworded: phase 6 adds the signal to both connections and owns its interface |
| V4-09 | minor | "A response frame is not valid JSON-RPC: the awaiting call returns an error" is well defined only with one pending waiter, but the new connection-wide demux establishes several, and an id-less decode failure cannot be routed | Rule stated: an unparseable frame fails every pending waiter with one typed error and leaves the connection usable, with its own test |
| V4-10 | minor | V1-02 residue in phase 5: two error rows named discovery happy-path tests, two distinct refresh invariants shared one test, and the four-carrier redaction table named one | Four dedicated tests added for the redaction carriers, plus dedicated failure tests for both metadata rows and a dedicated single-flight refresh test |
| V4-11 | minor | The server-config record omitted `auth_timeout_seconds`, its lead-in omitted phase 6, and the code-layout table had no row for it although it states nothing is created without a row | All three corrected |
| V4-12 | minor | Two phase-4 claims traced to no measurement: that answering on the POST response is "the common case", and the legacy-only detection signal, which none of the six probed endpoints exhibited | Both labelled in the phase as specification-derived rather than measured, with the reason stated |
| V4-13 | minor | C1-03 residue: the code-layout table placed the cache package but named no setup-side call site that reads the cache, registers its tools and wires each to its connector — a new code path, not an edit | Row added naming the file, plus rows for the endpoint identity, the challenge error and the scopes component |
| V4-14 | note | The endpoint cache identity excludes the credential, so two principals on one host sharing a server config share one cached tool list | D19 records the consequence as accepted, with the condition that would reopen it |
| V4-15 | note | The session journal had no round-3 entry although the index recorded sixteen findings | Entry added below |
| V4-16 | note | Phase 1 relocates the handshake bound but never named the existing per-server `ControlEvent.StartupTimeout` override it retires, which existing tests set | Named, with the migration called out as this phase's work |
| V4-17 | note | Two limits rows had no named test anywhere: phase 1's single-call wait and phase 4's connect wait | `TestStdioConnSingleCallBoundExpires` and `TestHttpConnectBoundExpires` added |

### Comprehension probes, round 2 — three weak models, worklog only, 2026-10-02

One probe was assigned phase 5 as an executor, one checked whether the phases can be executed
strictly in order, one read the worklog with no prior knowledge of the project. All three now
restate the problem, the measured costs, the census, the rejected design and the invariants
correctly, which closes the C1 class. What they could not do is below.

| ID | Finding | Closed by |
| --- | --- | --- |
| C2-01 | The executor probe placed the authorization-server metadata document at the protected-resource path and described both discovery fetches as `POST`s. The worklog named neither path nor method, so it inferred both and got them wrong | A README `External specifications` table naming each RFC, both well-known paths, the `GET` method and the five required authorization-server fields. Four of that probe's seven blockers were this one gap |
| C2-02 | The fresh-eyes probe could not tell what clai or an MCP server is: "the document does not explicitly define clai" | Two sentences opening the Goal |
| C2-03 | The fresh-eyes probe could not answer what changes for a representative configuration, because the memory arithmetic motivating the effort was never written down | An `Expected end state` table in Strategy, labelled a projection from the measurements rather than a measurement |
| C2-04 | The npm download figures had no recorded source, and the "under two percent" claim did not show its arithmetic. The probe verified the division itself and got roughly 0.75 percent | Source URL recorded; the claim restated as roughly one percent with both divisions shown |
| C2-05 | The ordering probe found that phase 2's acceptance criteria could all pass while its stated Goal was unmet, since after D16 the default stays eager | Goal rewritten to describe the machinery, with the saving explicitly attributed to the cache phase. Same finding as V4-07, reached independently |
| C2-06 | The executor probe could not tell which token-store fields the redaction table governs, because the record said every field is a credential carrier when `issuer`, `client_id`, `expires_at` and `scopes` plainly are not | Record format now names the three secret fields plus the two in-flight ones, and states the rest may appear in an error |
| C2-07 | Credential-command stdout versus stderr was unseparated, the `0600` mode had no non-POSIX story, and refresh single-flight did not say whether it reuses the connector's | All three stated in phase 5 |
| C2-08 | The endpoint cache's invalidation rules omitted the environment and envfile deltas its own identity includes, and the unknown-tool signal was never defined in protocol terms | Both added to phase 4, the latter defined as a specific JSON-RPC error code or `isError` result naming the tool |
| C2-09 | The cache record showed `command` and `url` both present without saying exactly one is populated | Stated in the record format |

### Round 3 — independent validation with source cross-checking, 2026-10-02

Run by a separate agent with repository access, which verified the worklog's claims about existing
code line by line. V1-01..V1-08, V1-10, V1-11, V2-02, V2-03 and V2-05 confirmed resolved. V1-09,
V2-01 and V2-04 confirmed only partially resolved and re-raised below.

| ID | Severity | Finding | Closed by |
| --- | --- | --- | --- |
| V3-01 | blocker | The mirror of V2-01. Phase 2's first integration row demanded every tool registered with no transport and no process, but the only source of a tool list is `tools/list` and the cache that serves it without connecting arrives in phase 3. On a cold cache every MCP tool would vanish from the model's tool box, two existing e2e fixtures would regress, and D12's independent-shippability claim was false for phase 2 | D16: phase 2 ships the machinery with the default still `eager` and changes no behaviour; phase 3 flips the default. The `startup` owner row now splits introduction from activation, phase 2's integration and invariant rows were rewritten to an explicitly-marked-lazy server, and phase 3 gained a `This phase owns the behaviour change` section with two new tests |
| V3-02 | major | Residue of V2-01 one category over: `TestSchemaCacheNeverPersistsAuthFailure` sat in phase 3, where no authorization failure can exist until phase 5. The only way to green it was to assert a tautology over a hand-built error, which the repository's own rule against tests passing for the wrong reason forbids | The invariant row, the error row and the test moved to phase 5 beside the scope-change test; phase 3's `What is never cached` reduced to the failures it can actually produce |
| V3-03 | minor | Regression of V1-09. The rule was reduced but its command was not, so the stated command concatenated the phase files and reported sixty-eight names, making the journal's clean record unreproducible | Command replaced with a per-file dedupe, and the over-reporting noted in the checklist item so a later round does not re-file it |
| V3-04 | minor | Phase 8 claimed `make qa` runs every gate. The `Makefile` defines `qa` as `lint` plus the test invocation, and `lint` as staticcheck, gofumpt and `go fix`: vet and the duplication gate are not in the target, and duplication is the gate this worklog singles out as load-bearing | Verified against the `Makefile`. The row relabelled, a paragraph added stating what `qa` does not run, and a separate acceptance row added for vet and duplication |
| V3-05 | minor | Phase 1 restated a gate invocation that the validation policy says phase 8 alone owns | Cell now points at the phase 8 gate table |
| V3-06 | minor | Phase 1 justified landing first with "phase 2 makes concurrency reachable". Phase 2 introduces no concurrency: `tool_executor.go` has zero goroutines. The single-flight invariant therefore had no production trigger, and Definition of Success cited it as evidence for a batch outcome that memoisation actually produces | Verified zero goroutines in the file. Phase 1's justification rewritten to the true reason, which is that the id collision is live even sequentially; phase 2 now states single flight is a defensive unit-level contract with no production trigger; the success row repointed at the batch test |
| V3-07 | minor | The connect bound and the handshake bound both defaulted to the same value and both governed the same wait, so in the integrated path the inner bound's typed error was unreachable and phase 1's limits test could not trigger its own bound | The connect bound's default raised so the two differ, and phase 2 now states the nesting: connect wraps spawn plus handshake and is what a lazy first call observes |
| V3-08 | minor | `TestSchemaCacheBackwardClockIsMiss` contradicted the row it implements, which says the capture time is recorded verbatim and no validity decision depends on it | Renamed to `TestSchemaCacheBackwardClockRecordsCaptureTimeVerbatim` |
| V3-09 | minor | Phase 4 claimed the abandoned branch logs and continues on eight failure paths. It has six `ancli` calls: five warn or error, one informational | Verified against the branch. Corrected to five plus one informational |
| V3-10 | minor | Phase 2 flipped a default on for every ambient server while the validation policy requires new default-on behaviour to stay off in the shared e2e fixture, and nothing owned that | Now moot for phase 2, which changes no default. Phase 3 owns both the flip and a new fixture-posture parameter row with its own test |
| V3-11 | minor | Phase 2 said `mcpTool` holds a `Connector` "instead of a channel pair", but phase 1 has already replaced the channel pair with a `Conn` | Reworded to "in place of the `Conn` the previous phase gave it" |
| V3-12 | note | `TestAuthWaitExcludedFromStoplossBudget` was unfalsifiable: the budgets are counts reserved before a call runs, so a wait cannot consume them by elapsing | Replaced with `TestExpiredAuthWaitConsumesNoExtraToolCallSlot` and a narrower, falsifiable contract |
| V3-13 | note | Residue of the V1-02 class in the one phase it was not swept: one test name cited for four distinct outcomes in phase 7 | Split into three tests |
| V3-14 | note | Partial regression of V2-04: four parameter rows still could not be matched to their phase-side names | Every row in the parameters table renamed to lead with the name the phases use |
| V3-15 | note | A Definition of Success row named an RSS floor as if it were an automated assertion, on the gate already flagged as load-fragile | Clause dropped; the zero-child-process assertion carries the row |
| V3-16 | note | The MCP-tool-to-built-in mapping is new package-level declared data with no owner row | Parameters row added, owned by phase 7, describing it as declared data rather than a tunable |

### Comprehension probe — weak model, worklog only, 2026-10-02

A small model was given the worklog and no source access, and asked to restate the problem, the
solution, every phase, a phase-3 implementation plan and the governing rule. It got all of those
right, which is the evidence that the strategy and the decisions log carry. It failed on two facts
and produced twenty-five points of confusion that collapsed into one class.

| ID | Finding | Closed by |
| --- | --- | --- |
| C1-01 | It restated the per-server cost as the direct-`node` figure only, dropping the `npx` row that most users actually pay | Strategy now states both ratios explicitly rather than leaving them to be read off the table |
| C1-02 | It fused two independent facts into "57.5 percent remote endpoints accessed via local stdio bridges". The topology figure is about how a server declares itself; the `mcp-remote` figure is a separate observation about how remote servers are reached today | Strategy now separates them in prose and says explicitly that a remote declaration does not imply no local process |
| C1-03 | The dominant class, twenty of twenty-five points: the worklog specified behaviour but never said which package or file owns each new symbol, nor what any on-disk record looks like. It could not place `Conn`, the startup-mode type, the new error types, the cache record's server-info field, the token store format, the server config format, the auth-pending signal's shape, the actionable tool result's shape, the shadow marker's format or the mapping table | Three new README sections: `Code layout and owners` with a row per new symbol and its package path, `Record formats` with the three JSON records written out, and `Existing code this worklog builds on` with a verified row per collaborator so no executor has to go looking |
| C1-04 | It could not tell what signal identifies a legacy-only endpoint, a gap introduced by the V2-03 fix | Phase 4 now states the detection rule: `initialize` POST refused as method-not-allowed or not-found while an event-stream `GET` succeeds |
| C1-05 | Event-stream framing, the environment digest algorithm, which tokens the store holds, whether a client secret exists, and the challenge's format were all unstated | Each now stated: SSE framing in phase 4, digest and server-info in the README record formats, token fields and the optional client secret in phase 5 alongside the challenge's definition as the verbatim `WWW-Authenticate` value |

### Round 2 — verification of round 1, then new findings, 2026-10-02

V1-01 through V1-11 were each verified against the edit or decision they cite. Ten are resolved.
V1-03 was partially regressed and is re-raised below as V2-02.

| ID | Severity | Finding | Closed by |
| --- | --- | --- | --- |
| V2-01 | major | Phase 3 specified the endpoint-based half of the cache: an identity including the requested scopes, a time-bounded freshness rule, two invalidation signals, five integration rows naming a fake HTTP server and two acceptance criteria. None of it is buildable when phase 3 is executed, because endpoint-based servers arrive in phase 4 and the scopes field in phase 5. An executor picking up phase 3 in the order fixed by D12 would have been blocked on a transport that does not exist | Phase 3 reduced to the cache mechanism and the command-based identity, with an explicit statement of what it does not own. Phase 4 gained a `Schema cache for endpoint-based servers` section owning the endpoint identity, the freshness bound, both invalidation signals, three invariant rows, three integration rows, three acceptance rows and three new tests. Phase 5 gained a `Schema cache identity extension` section for the scopes component. The freshness-bound parameter's owner moved from phase 3 to phase 4 |
| V2-02 | minor | Regression of V1-03. Phase 2 still restated the startup default in prose, which both duplicates a parameters-table value and contradicts the conditional default the V1-03 fix introduced | Phase 2 now points at the parameters table for the default, including the conditional case, and states no value |
| V2-03 | minor | Phase 4 specified that a server offering only the legacy transport is rejected at parse time, but the server config carries no transport field, so the condition cannot be detected at parse time | Reclassified as a connect-time typed error with its own test, and the out-of-scope paragraph rewritten to match |
| V2-04 | note | Phase 1's limits table and the README rows named the same parameters differently, so a reader could not match them by name | README rows renamed to the names the phases use |
| V2-05 | note | Phase 8 used a combined invariants-and-limits heading where every other phase uses two, so a structural scan reported neither section as present | Headings split |

## Session journal

### 2026-10-02

Investigated the current lifecycle and measured it rather than assuming. Found the per-turn
lifecycle in `internal/text/querier_setup_tools.go:120` onward, where ambient discovery spawns
every configured server and setup blocks on every handshake, and confirmed from
`architecture/chat.md` that one CLI invocation is one turn, so the cost is paid per query. Measured
spawn cost and RSS, censused the registry, pulled npm usage figures, and probed six vendor
endpoints plus three authorization servers.

Found two live demux defects while reading: `mcpTool.seq` at `internal/tools/mcp/tool.go:31` is
per-tool although all tools of a server share one channel pair, so two tools of one server both
issue id one; and `internal/tools/mcp/tool.go:118` discards frames whose id does not match, so one
caller can consume another's response. Both are latent only because `runPlannedCall` executes a
tool batch sequentially. They are the reason phase 1 exists and must land first.

Tested the sharing premise behind a proposed MCP pool and abandoned it: a second `initialize` is
silently accepted and clobbers the session, and `server-filesystem` derives its authorization
boundary from a per-process `roots/list` answer, so two callers sharing a process share a security
scope. Recorded as D2 and dropped from scope at the maintainer's direction.

Maintainer declined to choose a phase order and asked for all of it, so D12 fixes a risk-ascending
order. README written and presented for sign-off; phases not yet written. Readiness checklist not
yet run, since it checks phase files.

Phases one through eight written from the approved strategy. Readiness checklist run with these
outcomes: numerals clean across all phase files under the declared pattern; no test name declared in
more than one phase; every config field, flag, injectable field and subcommand has exactly one owner
row, after seven missing rows were added for the endpoint field, the authorization block, the
credential command, the static-bearer variable, the requested scopes, the new subcommand and the
injected clock; every invariant and every limit is a table with a test named per row; the two phases
needing a person carry a `Human required` subsection, and a false positive in the transport phase was
reworded rather than given a spurious one; no phase references a symbol an earlier phase deletes,
after the transport phase's description of the abandoned branch was reworded to avoid naming the
removed channel pair; conventions checked against `internal/tools/cmd.go` for command placement,
`pkg/claierr/claierr.go` for typed errors, `internal/chat/dirscope.go` and `internal/chat/paths.go`
for digest-keyed cache files, and `internal/text/mcp_log_sink.go` for sink behaviour. The gate
commands were de-duplicated: phase eight owns the gate table and the validation policy now points at
it rather than restating the invocations.

Validation round one run against the written worklog. One blocker, five majors, three minors and two
notes; every finding closed by class rather than at the cited line, and recorded in the feedback
index above. The blocker was a reporting path invented in phase 2 that no code and no phase owned;
removing it rather than specifying it kept the strict-startup contract exactly as the repository
already implements it. The largest class was error coverage: twenty-eight rows named a happy-path
test, so twenty-eight dedicated error tests now exist and the phase files declare one hundred and
eight distinct test names, none repeated across phases. Readiness checklist re-run clean after the
fixes.

Validation round two run. Every round-one finding verified against its cited closure: ten resolved,
one partially regressed and re-raised as V2-02. One new major, two minors and two notes, all closed
by class. The major was an ordering defect the first round missed entirely: the cache phase had
specified the endpoint-based half of its own feature, which the fixed phase order places two phases
later. Splitting that ownership across the cache, transport and authorization phases removed the
only remaining way an executor could pick up a phase it could not finish. Readiness checklist
re-run clean: no numerals outside oracle rows, one hundred and ten distinct test names with none
repeated across phases, every phase carrying both an invariants and a limits section, and the two
human-required steps declared. Worklog handed over at `Conditionally ready`; every phase remains
`Not Started`.

Validation round three run by an independent agent with source access. One blocker, one major, nine
minors and five notes; all closed by class. The blocker was phase 2 promising registered tool
schemas with no transport and no cache, which the cache phase only enables two phases later — the
mirror of round 2's finding. Round three also disproved three of my own claims about existing code:
`make qa` does not run vet or the duplication gate, the abandoned transport branch has six logging
calls rather than eight, and `internal/text/tool_executor.go` contains no goroutine, which
invalidated phase 1's stated reason for landing first. Readiness checklist re-run clean, with its
item-two command corrected to dedupe per file.

Validation round four run, together with three weak-model comprehension probes. One blocker, five
majors, six minors and four notes, plus nine comprehension findings; all closed by class. The
blocker was the same ordering class one level down: phase 3 never stated what setup does on a cache
miss, and its own artifacts required opposite answers. Resolving it with D18 changed the headline
claim honestly — the zero-process outcome is a warm-cache outcome, and a first run still connects
once. Two majors were vacuous closures from earlier rounds: a fixture pin that pinned nothing
because the root end-to-end fixture configures no server, and an acceptance table that could go
green against a hand-seeded cache. The probes closed the comprehension class: all three now restate
the problem, the costs, the census, the rejected design and the invariants correctly, and the
remaining gaps were specifications never named and record fields never separated. Readiness
checklist re-run clean: no numerals outside oracle rows, one hundred and twenty-nine distinct test
names with none repeated across phases and none colliding with the repository's existing tests,
every phase carrying both sections, and every parameter named in a phase resolvable in the table.

Validation round five run with two executor probes. One blocker, two majors, seven minors and five
notes. The blocker was the first that was a design bug rather than a documentation defect, and the
fix I wrote for it did not survive the next round.

Validation round six run with a targeted phase-6 probe. Two blockers. The first was my own round-five
fix: it specified suspending two `context.WithTimeout` deadlines, which cannot be paused, and no
phase owned a mechanism for it — D20 abandons suspension and moves the wait outside resolution
instead. The second was the sixth instance of the recurring class: phase 7 marked MCP tools in a
listing that has never had any, because `clai tools` reads the process-global registry and MCP tools
are kept out of it by an explicit, reasoned design in the code. D21 gives the listing a cache-only
source and removes a claim in D18 that the same verification showed to be false. Six rounds have now
each found exactly one instance of that class, in six different disguises, and the rate is not
decaying.

Data-loss incident, same day. A README and four phase files were truncated to zero bytes when the
host filesystem filled during an edit pass: `write()` returned success and the failure surfaced only
at flush, so the script reported success for files that were already empty. The four phase files were
recovered in full from the validation agents' transcripts, verified by checking that each round-five
fix marker was present and that the section structure was intact. The README was recoverable only to
its post-round-four state, because round six read it in chunks and no single transcript entry held
the whole file; its round-five and round-six edits were replayed from the session record. Two false
recoveries were caught before being written: an extraction that preferred the longest match restored
a pre-round-five phase 7 and a doubled file, and a shorter README candidate was missing the session
journal entirely. Every write in the replay was staged to a temporary file, read back, copied, and
read back again, which is now the pattern any future edit pass to this worklog should use.
