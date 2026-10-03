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
| 1 | [Conn seam and JSON-RPC demux](phase-1-conn-seam-and-demux.md) | Complete — annotated by implementation reviews 1 and 2 | One owner per connection holds a connection-wide id counter and an id-to-waiter map; the two live demux defects are gone |
| 2 | [Lazy connect](phase-2-lazy-connect.md) | Complete — findings from reviews 1 and 2 fixed and verified; R1-14's HTTP-transport half remains open under phase 5 | For a server explicitly marked lazy, its process is created on the first tool call that targets it, once per run, with a run-scoped failure that never becomes a spawn storm. The default still flips in phase 3 |
| 3 | [Tool schema cache](phase-3-schema-cache.md) | Complete — findings from reviews 1 and 2 fixed and verified; R1-16 and R2-06 closed at the root for the phases they are shared with too; the sign-off review's B3, B4 and S1 fixed and verified, 2026-10-03 sign-off fix session | On a warm cache, setup advertises a command-based server's tool schemas from a delta-validated on-disk entry and connects nothing. A miss still connects and captures, per D18. The identity now also fingerprints the first on-disk `args` entry and digests `args` rather than storing it, and the freshness bound applies to every entry (D40). `Capture` now refuses an empty tools array (B4); a warm hit against a launcher-only-fingerprinted server prints a setup-time notice (B3); the fixture build moved into a `TestMain`, raising the package's cold-cache timeout headroom from ~4.7s to ~10.1s (S1) |
| 4 | [Streamable HTTP transport](phase-4-streamable-http.md) | Complete — findings from reviews 1 and 2 fixed and verified, 2026-10-03 fix session; R2-16's stdio wiring (handed over from phase 3) and R1-14's HTTP-connect-bound half (handed to phase 5) are the two cross-phase items, both resolved: the former fixed here, the latter confirmed as phase 5's and not reopened against this phase; the sign-off review's B1 fixed and verified, 2026-10-03 sign-off fix session | A remote server is reached over spec-compliant streamable HTTP with no local process; the server-initiated stream is bounded per frame, answers a server-initiated request, reconnects with `Last-Event-ID`, and ends its session on `Close`; the cache gains its endpoint-based half and the unknown-tool/list-changed invalidation mechanisms now also work for the stdio transport. `Call` now resolves as soon as the awaited frame arrives rather than when the POST response body ends, so a server that holds its event-stream open after answering no longer hangs every call to its context bound (B1) |
| 5 | [OAuth 2.1 and credential sources](phase-5-oauth.md) | In Progress — every review 1, review 2 and sign-off review finding fixed and verified (B2, 2026-10-03 sign-off fix session); human gate still outstanding | Discovery, dynamic client registration, PKCE, refresh, a token store, a credential command, and a static bearer fallback. The trust layer B2 found entirely unasserted is now built and pinned: RFC 8707 `resource` on every authorization and token request, `redirect_uri` repeated on the exchange, issuer and resource-identifier validation, a host check on the challenge's metadata location, an https-or-loopback requirement on every chain URL including the one handed to a browser, redirects refused on the token, registration and MCP endpoints, and a root gate on a non-interactive run — each behind a hostile fixture mode that fails without it. Automated suite green, blocked only on a maintainer's real-endpoint confirmation, which B2's missing `redirect_uri` shows has never run |
| 6 | [Mid-run auth surfacing](phase-6-midrun-auth.md) | In Progress — every review 1, review 2 and sign-off review finding fixed and verified (B2's mid-run half, 2026-10-03 sign-off fix session); R1-26 has since been closed by the phase-8 sweep, which deleted the dead option; human gate still outstanding | A connection that needs a human is announced, pinned and bounded instead of silently stalling a tool call. The mid-run `AuthResolver` no longer opens a browser or binds a loopback listener on a run whose output is not a terminal — gated at the root in `AuthorizeInteractive` so no future caller needs its own copy of the gate (B2). Automated suite green, blocked only on the maintainer's real mid-run authorization observation. One recorded gap stays open by decision: mid-run token expiry never re-authorizes (D54) |
| 7 | [Built-in shadow advisory](phase-7-shadow-advisory.md) | Complete — every review 1 and review 2 finding fixed and verified, 2026-10-03 fix session; R1-16's and R2-12's phase-7 shares closed alongside phase 3's own fixes to the same findings; the sign-off review's S2 fixed and verified, 2026-10-03 sign-off fix session | `clai tools` gains an MCP tool set from the cache for servers that have succeeded, then names every entry already covered by an in-process built-in; the advisory is now a single footer line, not a per-tool marker, since "command-based" is not a reliable proxy for "local" (S2, demoting R1-22/R2-17's transport restriction from a per-tool claim to an aggregate one) and covers `create_directory` → `mkdir` (R1-23/R2-23) but no longer `get_file_info` (S2: wrong mapping, corrected by removal) |
| 8 | [Quality gate sweep](phase-8-gate-sweep.md) | In Progress — every review 1 and review 2 finding fixed and verified; R1-17 and R1-35b closed by the coordinating session, which owns `architecture/`, each claim verified against source first; human gate still outstanding | All automated gates green warm (format, staticcheck, vet, fix, dupl, `make qa`, `-race -count=3 -timeout=30s`); a cold-build-cache sweep found the root package's cold-cache timeout is pre-existing (confirmed against the pre-worklog commit) and `internal/text`'s was new — recorded here as a volume effect needing a package split, an acknowledged risk rather than a fix. **Corrected by the sign-off review (S1) and closed by phase 3, 2026-10-03:** the diagnosis was wrong, no repackaging was needed, and the real fix (a `TestMain`) closes it; duplication re-ruled at 36 groups, nothing new beyond an already-accepted group's member count; `WithAuthPendingSink` (R1-26) deleted; the headline multi-server proportionality claim (R2-04) now has a dedicated end-to-end test; coverage re-checked per package |

Implementation reviews 1 and 2 (both 2026-10-02) reopened phases 2 to 8. Phase 1 passed both and
is annotated only. The three human-required gates (live-endpoint OAuth, the mid-run authorization
observation, the fleet memory measurement) remain legitimately outstanding and are not findings.

**Seven blockers were filed: R1-01, R1-02, R1-03, R1-04, R2-01, R2-02 and R2-03. All seven are now
fixed and verified** (R2-01 last, closed in phase 8's 2026-10-03 fix session — see the feedback
index below for each one's fix). Round 2 reviewed
the same unpatched code round 1 saw, re-verified round 1's phase-6 findings and its rulings D35 to
D38 and upholds all of them, and filed 27 further findings in the areas round 1 did not reach:
concurrency and lifetime, HTTP protocol conformance beyond the demux, the public SDK surface,
backward compatibility, and the fixture layer. One correction to round 1 is recorded as R2-23.
Every finding is listed in the feedback index below and filed in full against its owning phase; the
fixer picks up the first non-complete phase off this board and routes off the `## Review findings`
section in that phase file, reading the `### Review 1` and `### Review 2` subsections together.

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
| RFC 9728, OAuth 2.0 Protected Resource Metadata | The document at `/.well-known/oauth-protected-resource`, fetched with `GET`, whose `authorization_servers` and `scopes_supported` drive the next step. Its `resource` identifier is checked against the server being authorized, per section 3.3: same scheme, same host and port after default-port normalisation, and the server's path at or under the resource's. An absent `resource` is a refusal (sign-off review, B2) |
| RFC 8414, OAuth 2.0 Authorization Server Metadata | The authorization-server document, fetched with `GET`. Its URL is composed from an issuer in the protected-resource document's `authorization_servers` by **inserting** `/.well-known/oauth-authorization-server` between the issuer's authority and its path, which is RFC 8414's rule and differs from OIDC-style appending whenever the issuer has a path component. clai tries the inserted form first and the appended form second, and reports both as missing if neither answers. clai requires five fields: `registration_endpoint`, `authorization_endpoint`, `token_endpoint`, `code_challenge_methods_supported` containing the PKCE parameter's value, and `grant_types_supported` containing the refresh grant. The document's own `issuer` is compared to the issuer whose well-known URL it came from, per section 3.3, tolerating only a trailing-slash difference; the three endpoints it names are checked for scheme before any of them is used (sign-off review, B2) |
| RFC 7591, Dynamic Client Registration | The `POST` to `registration_endpoint` that yields a `client_id` and, optionally, a `client_secret` |
| RFC 7636, PKCE | The verifier, and the challenge as the base64url-encoded SHA-256 of it |
| RFC 6749 and OAuth 2.1, authorization code and refresh grants | The authorization request built from `authorization_endpoint`, and the two `POST`s to `token_endpoint`. The authorization-code grant repeats the authorization request's `redirect_uri`, which section 4.1.3 requires whenever one was sent and a conformant server answers `invalid_grant` without (sign-off review, B2). Neither `POST` ever follows a redirect: Go re-sends a request body across a 307 or 308 and strips only the `Authorization` header, never the body |
| RFC 8707, Resource Indicators for OAuth 2.0 | The `resource` parameter, carried on the authorization request and on every token request including the refresh grant, naming the MCP server the token is for. The MCP authorization specification makes it a MUST; it is what stops a token minted for one MCP server being replayed against another behind the same authorization server (sign-off review, B2) |
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
   table row in phase 5, not a convention. **Amended by implementation review 1 (R1-03, R1-10):**
   every config field stored or printed verbatim is a credential carrier, not just the fields
   phase 5 resolves. `env` is digested in the cache identity precisely because it carries secrets;
   `args` must be treated the same, because the documented `mcp-remote --header "Authorization:
   Bearer ..."` shape puts a token there. A redaction row is met only when its test places a known
   secret into that carrier's real input and asserts its absence from that carrier's real output.
7. **The human wait is infrastructure-only.** It is raised only by a component that cannot proceed
   without a credential, never by a tool and never on a model's request. No model-callable surface
   reaches it, directly or indirectly.
8. **The generic layer stays vendor-agnostic.** Transport-specific and vendor-specific behaviour
   lives in `internal/tools/mcp`, never in `internal/text/generic`.
9. **Ambient servers degrade, explicit servers fail.** A server discovered from the config
   directory keeps today's warn-and-continue posture; a server passed through
   `agent.WithMcpServers` keeps `StrictMcpStartup`. Laziness must not convert a strict failure
   into a silent one. **Clarified by implementation review 1 (R1-02):** this is
   unconditional, not a default. A configured `startup: "lazy"` must not override it, because a
   warm cache then means setup never connects and no strict failure can exist to report.
10. **A contract row is met only when its test reaches the row's property through the production
    composition root.** Elevated by implementation review 1, which found the same shape behind
    R1-01, R1-02, R1-05, R1-10, R1-15, R1-16 and R1-25: a test that drives the seam the row names,
    builds its own context, or warms a fixture with the same helper the code under test uses, is
    self-consistent by construction and cannot fail on the one property the row asserts. Each of
    those seven rows was green. When a row's subject is a side effect of the real system — a
    process birth, a context value, a file on disk, a rendered line — the test counts or reads that
    artefact, not a call to the function that would have produced it.
11. **A non-memoised resolution outcome must still be bounded per run, not per call.** Elevated by
    implementation review 1 from R1-01. D20 deliberately exempts a blocked-outside-run outcome from
    the connector's memo so a later call can retry, and the retry-count parameter says "0 retries
    ... per server **per run**". Those two only compose if something caps the re-dials for the run.
    Nothing did, so a model calling a flagged server N times paid 2N process births and N full
    auth-timeout blocks. Any future outcome class that is exempted from memoisation carries its own
    per-run cap in the same change.
12. **A cache entry is trustworthy only when its identity fingerprints the artefact that
    determines its content.** Elevated by implementation review 2 from R2-03. The identity
    currently fingerprints `server.Command`, which for `npx`, `uvx`, `node`, `python` or `docker`
    is the launcher and not the server, so the delta-validation rule the parameters table calls the
    command-based freshness mechanism cannot see a server upgrade. A component added to an identity
    for the purpose of detecting change must be shown, in the same change, to change when the thing
    it stands for changes.
13. **A resolution that was concurrent stays concurrent.** Elevated by implementation review 2 from
    R2-02. Setup's per-server work ran one goroutine per server before this worklog; the cache-aware
    lazy branch does it inline in the loop. Any future path that takes a server out of `mcp.Manager`
    inherits the obligation to keep its own concurrency, and the measurement that justifies the
    change states the wall-clock cost at the representative server count, not just the process
    count.
14. **An error that must cross the strict-versus-degrade fork carries the sentinel that fork tests
    on.** Elevated by implementation review 2 from R2-05. `setupTooling` classifies on
    `claierr.ErrMcpServerStartup` alone, so any new failure producer reachable from setup either
    wraps into that sentinel or is invisible to `StrictMcpStartup`. The wrap belongs at the single
    `classifyServerFailure` funnel, not at each producer (D42).
15. **A test's time bound never encloses work whose cost depends on a cache the gate does not
    control.** Elevated by implementation review 2 from R2-01. A bound wrapping `go run` measures
    the build cache, so the same test passes warm and fails cold; a clean CI checkout is cold by
    construction. This is distinct from the host-load sensitivity already recorded, which is a
    property of the machine rather than of the test.

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
| the setup-side cache call site: reads the cached tool list, **constructs the server's `Connector` via the exported `mcp.NewConnector`**, registers its tools, and wires each to that connector without a transport | `internal/text/querier_setup_tools.go`, a new path beside the existing `ControlEvent` flow | Phase 3 |
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
| Setup-side wiring from `mcpauth` into the eager and lazy HTTP connect paths phases 3–4 own (`staticHttpDecorator`, `dialHttpServerWithAuth`, `handshakeHttpServerWithAuth`, `newAuthenticatingHttpConnector`, `newMcpAuthorizer`) | `internal/text/mcp_oauth.go` | Phase 5 (discovered necessary beyond the phase's original table; added here per the executor-adds-a-row convention phases 2–4 already established) |
| `mcp.NewConnectorFromDial`/`mcp.DialFunc`, letting an externally-supplied dial reuse this package's existing single-flight memoisation; `mcp.WithHttpRequestDecorator` (`ConnectorOption`); `dialHttp` gained a `decorate RequestDecorator` parameter | `internal/tools/mcp/connector.go`, `internal/tools/mcp/conn_http.go` | Phase 5 |
| `mcp.LoadEnvFile`, exported from the already-existing private `loadEnvFile` so the authorization phase's static-bearer envfile fallback reuses it rather than duplicating a parser | `internal/tools/mcp/envfile.go` | Phase 5 |
| `pkgtools.ValidateCmdNotBanned`, an exported wrapper around the existing private ban check, so a credential command run outside `pkg/tools` is still subject to the policy | `pkg/tools/cmd_ban.go` | Phase 5 |
| `Configurations.TrustInput`, the connector's paste-input seam | `internal/text/conf.go`, assigned in `SetupQuerier` | Phase 5 |
| `oauthtestserver.Config.FixedAccessToken`, letting an integration test pre-configure `httptestserver.RequireBearerToken` to the exact token the flow is about to issue | `internal/tools/mcp/oauthtestserver` | Phase 5 |
| `AuthPendingSink` and its wiring into the log sink | interface in `internal/tools/mcp/conn.go`; implementation on `mcpLogSink` in `internal/text/mcp_log_sink.go` | Phase 6 |
| the call sites that raise the auth-pending signal, one per transport | `internal/tools/mcp/conn_stdio.go` and `internal/tools/mcp/conn_http.go` | Phase 6 |
| the tool-call-site wait and the single follow-up resolution | `internal/text/tool_executor.go`, at the existing `InvokeWith` call site | Phase 6 |
| declared MCP-tool-to-built-in mapping | `internal/tools/builtin_shadow.go` | Phase 7 |
| the listing's MCP tool source: a cache-only read that gives `clai tools` an MCP tool set without connecting | `internal/tools/cmd.go`, extending `List` and `Detail`, plus a cache-only entry point in `internal/tools/mcp/schemacache` | Phase 7 |
| `schemacache.DefaultDirName`, `schemacache.ListEntry`, `schemacache.ListCachedServers` — the cache-only entry point's concrete symbols | `internal/tools/mcp/schemacache` | Phase 7 |
| `FindConfiguredServers` (config parse and XOR/URL validation), relocated from the private `findConfiguredMcpServers`/`validateMcpTransport` that `internal/text/querier_setup_tools.go` defined through phase 6 | new package `internal/tools/mcp/serverconfig` | Phase 7 (discovered necessary beyond the phase's original table; added here per the executor-adds-a-row convention phases 2–5 already established: the listing lives in `internal/tools`, which cannot import `internal/text` without a cycle, and a leaf package was needed rather than `internal/tools/mcp` itself, since that package's own tests import `internal/tools`, which now imports this parser) |
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
the `initialize` result, stored verbatim as raw JSON and never interpreted. **Amended by
implementation review 1 (R1-03, ruling D38):** `args` is never stored verbatim — the documented
`mcp-remote --header "Authorization: Bearer ..."` shape puts a credential there — so `args_digest`
(a hex SHA-256 over args joined one per line, order preserved) replaces it, exactly as `env_digest`
already digests `env`. **Amended by implementation review 2 (R2-03, ruling D40):** `script`
fingerprints the first `args` entry that resolves to an existing file on disk (normally an
interpreter-launched server's own script), closing the gap where `executable` alone only ever
fingerprints the launcher (`node`, `npx`, `uvx`, ...), never the server; it is absent when no such
entry exists (an npx package name is not a path). The freshness bound (schema-cache-freshness-bound
parameter) now applies to every entry, command-based included, not only an endpoint-based one — see
the parameters table row below:

```jsonc
{ "identity": { "command": "node", "args_digest": "<hex sha256>", "url": "",
                "env_digest": "<hex sha256>",      // command XOR url is set, never both
                "envfile": { "size": 412, "mtime": "2026-09-30T11:02:14Z" },
                "executable": { "path": "/usr/bin/node", "size": 84213,
                                "mtime": "2026-08-01T09:00:00Z" },
                "script": { "size": 2048, "mtime": "2026-08-01T09:00:00Z" },
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
| Fixture posture for `startup` in the tests that actually spawn a server, namely `internal/text/querier_setup_tools_test.go` and `internal/text/mcp_log_sink_test.go` | `eager`, pinned explicitly. The *shared* root end-to-end fixture, `setupMainTestConfigDir`, creates an empty `mcpServers` directory and spawns nothing, so pinning it there would pin nothing — amended by R2-24, phase 8, 2026-10-03: this is true of the shared fixture only; dedicated, opt-in fixtures layered on top of it (`main_mcp_lazy_e2e_test.go`) do write a live server into their own copy of that directory and do spawn it | Phase 3 |
| Cache directory injection wherever `setupMcpManager` is exercised by a test | a per-test temporary directory, never the developer's real cache directory | Phase 3 |
| connect-bound, the `connect_timeout_seconds` field, which wraps spawn plus handshake | 45 | Phase 2 |
| single-call bound, the pre-existing `timeout_seconds` field | 0, meaning unbounded | Phase 1, preserved |
| handshake-bound, for `initialize` plus `tools/list`, pre-existing `mcpStartupTimeout` | 30 s | Phase 1, relocated onto `Conn` |
| retry-count, connect attempts per server per run after a failure | 0 retries | Phase 2 |
| schema-cache-directory, under the clai cache dir | `mcpSchemas` | Phase 3 |
| schema-cache-file-name | `<hex sha256 of identity>.json` | Phase 3 |
| schema-cache-freshness-bound | 12 h, applied to every entry, command-based or endpoint-based alike (amended by D40/R2-03: a command-based identity's delta validation alone cannot see a server upgrade that touches no fingerprinted local file) | Phase 4 introduces the bound for endpoint-based entries; Phase 3 extends it to command-based ones per D40 |
| Freshness rule for command-based servers | size and mtime delta on the executable, the envfile and, when present, the first `args` entry resolving to a file on disk (D40), plus the schema-cache-freshness-bound above | Phase 3 |
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
9. Every test in the two files that actually drives `setupMcpManager` against a real spawned MCP
   server (a `"go", "run", ".../testserver"` command or equivalent) names its posture explicitly —
   eager or lazy — either in the config it writes or in a comment stating the posture is
   deliberately left unset. **Restated by R2-13**, which found the original wording ("the two test
   files ... pin the eager posture explicitly") false and its own verification command, a bare
   `grep -n 'startup'` over both files, unable to detect that: a test with `startup` unset prints
   nothing and is invisible to the grep, and a test that pins `lazy` while spawning prints a line
   the grep cannot distinguish from compliance. The replacement enumerates each test function that
   both spawns and calls `setupMcpManager` (excluding a test that spawns through the connector or
   the log sink directly, which carries no startup-mode posture to pin), instead of searching for
   a word:
   `awk '/^func Test/{if (fn!="" && spawn && manager && !posture) print fn " spawns via setupMcpManager with no posture token"; fn=$0; spawn=0; posture=0; manager=0} /run.*testserver|TEST_SERVER_SPAWN_LOG/{spawn=1} /setupMcpManager\(/{manager=1} /startup/{posture=1} END{if (fn!="" && spawn && manager && !posture) print fn " spawns via setupMcpManager with no posture token"}' internal/text/querier_setup_tools_test.go internal/text/mcp_log_sink_test.go`
   — a clean run prints nothing; verified clean 2026-10-02 after the fix session's test changes.

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
| D35 | 2026-10-02 | Strict startup is unconditional: a server supplied through `agent.WithMcpServers` while `StrictMcpStartup` is on resolves eagerly even when its config says `startup: "lazy"` | Implementation review 1, R1-02. With a warm cache a lazy strict server never connects during setup, so no strict failure can exist to report and the agent caller is told its required server is present. The parameters table already stated the exception unconditionally; `effectiveStartupMode` implemented it as a default | The reading of the startup-mode parameters row under which a configured value overrides the strict exception |
| D36 | 2026-10-02 | An outcome exempted from the connector's memoisation carries its own per-run re-dial cap, set at one re-dial per server per run | Implementation review 1, R1-01. D20's exemption and the retry-count parameter's "0 retries per server per run" only compose if something bounds the re-dials; without a cap a model calling a flagged server N times paid 2N process births and N full auth-timeout blocks | The unbounded reading of D20's "the next call tries again" |
| D37 | 2026-10-02 | Expiry of the auth-timeout bound yields the actionable tool result with no further resolution, on both the resolver and the no-resolver branch | Implementation review 1, R1-06. Phase 6's own invariant and integration rows say so; the no-resolver branch treated expiry as success and retried, which is where R1-01's second spawn comes from. A retry after expiry has no new information to act on | Phase 6's implementation-note resolution in which waiting the bound out on a command-based server counts as a resolved wait |
| D38 | 2026-10-02 | `args` is digested in the schema cache identity exactly as `env` already is, with at most a non-secret informational copy retained | Implementation review 1, R1-03. A credential written into `args` — the documented `mcp-remote --header "Authorization: Bearer ..."` shape — is otherwise persisted verbatim into a cache file, breaking invariant 6. The identity must still react to an args change, so the field cannot simply be dropped; the dirscope convention of a digest key plus an informational path is the precedent | The verbatim `"args"` field in the README's schema cache entry record format |
| D39 | 2026-10-02 | A per-server resolution that was concurrent stays concurrent: the lazy branch of `setupMcpManager` runs one goroutine per server and joins through the existing `toolWg`, exactly as `mcp.Manager` already does for an eager server | Implementation review 2, R2-02. D18 promises a miss connects "exactly as today"; today is parallel. Measured serially at 4.05 s for four servers against 1.01 s eager, and the failure case is worse, since each miss carries its own 30 s handshake bound | The inline, loop-body resolution introduced when phase 3 added the cache-aware path |
| D40 | 2026-10-02 | A command-based cache identity fingerprints the first `args` entry that resolves to an existing file, in addition to the resolved command, and the freshness bound applies to every entry rather than only endpoint-based ones | Implementation review 2, R2-03. `resolveExecutable(server.Command)` fingerprints `node`, `npx`, `uvx` or `python`, never the server, so delta validation is blind for roughly 85 percent of local servers by this worklog's own census — including the `npx` command the Strategy section measured. Proved by probe: rewriting the launched script leaves the identity key byte-identical | The parameters row "Freshness rule for command-based servers: size and mtime delta, no time bound", which is sound only when `command` is the server binary |
| D41 | 2026-10-02 | A time bound in a test never encloses a compile. The stdio fixture is built once into a temporary binary in a `TestMain` and every config points at that binary instead of `go run ./testserver` | Implementation review 2, R2-01 and R2-19. A 200 ms connect bound wrapping `go run` passes warm and fails cold, deterministically, so the gate record was a property of the reviewer's build cache rather than of the code. The same change removes the 4.32 s the new e2e fixture costs the load-sensitive race gate | The `go run ./testserver` form used by every spawning test in this worklog |
| D42 | 2026-10-02 | Every explicit-server setup failure is wrapped into `claierr.NewMcpServerStartup` at the single `classifyServerFailure` boundary, rather than each producer being trusted to have chosen a type that carries the sentinel | Implementation review 2, R2-05. `setupTooling` forks strict against degrade on one sentinel; a credential-source failure never reaches it, so an explicitly requested server could fail while `Setup` returned nil. One funnel, one wrap, and the class closes | The per-producer typing on which the strict/degrade fork currently depends |
| D43 | 2026-10-02 | `userConf.McpServers` passes the same transport validation the config-directory path does, before it is appended | Implementation review 2, R2-07. The XOR that `pub_models.McpServer.Url`'s own doc comment asserts is enforced on the file path and unenforced on the public path, where both-set silently prefers `url` and neither-set reaches `exec.CommandContext(ctx, "")` | The unvalidated append at `querier_setup_tools.go:145` |
| D44 | 2026-10-02 | A per-file config parse or validation error is reported unconditionally, and returned when the run is strict, rather than read only when no server parsed at all | Implementation review 2, R2-06. The swallow predates this worklog but this worklog adds two new ways to land in it, `StartupMode.UnmarshalJSON` and `validateTransport`, so a one-character typo now deletes a working server in silence | The `if len(mcpServers) == 0` guard around the joined error |
| D45 | 2026-10-03 | An eager, strict-startup endpoint-based server's credential chain is static-only (`auth.token_command`, `auth.token_env`): no token store, no interactive fallback. The gap is accepted as declared, not fixed, because D16/D18 already make `lazy` the ambient default and the eager path runs through `mcp.Manager`'s channel machinery, which has no retry-on-challenge seam without reopening phases 1–2 | Phase 5 fix session, 2026-10-03, promoting its own implementation-note comment to a decision row per this round's instruction that an asymmetry "ruled acceptable as declared" must be a decision row, not only a code comment. The resulting challenge is never silently swallowed: it surfaces as a typed connect failure, and R2-05's fix (the `failureCollector.addExplicit` funnel) now wraps it into `claierr.NewMcpServerStartup` like any other explicit failure, so `StrictMcpStartup` still sees it | The implementation-note-only record of this asymmetry in phase 5's own file |
| D46 | 2026-10-03 | `Cache.Capture` refuses an empty tools array rather than persisting it | Sign-off review, B4. An empty handshake result is a run-fact about one transient zero-tool boot, not content about the server, and this package's own rule (D5) already keeps a run-fact out of the cache; this closes the one path that fact could still take | — |
| D47 | 2026-10-03 | A warm cache hit against a command-based server whose identity's only local evidence is a generic launcher (`node`, `npx`, `npm`, `uv`, `uvx`, `pipx`, `python`, `python3`, `docker`, `bunx`, `deno`) and no on-disk script argument prints a one-line setup-time notice naming the server, restoring (for that shape only) the warning eager setup always gave | Sign-off review, B3. Gated on the identity (`Identity.LauncherOnlyFingerprint`), not printed on every warm hit, so a direct-binary server — whose `Executable` already fingerprints the thing that would have changed — stays quiet; only the shape that genuinely cannot detect its own breakage is noisy | — |
| D48 | 2026-10-03 | The built-in shadow advisory is a single footer line naming every matched, available built-in once, deduplicated, with no attribution to which server or tool triggered which entry — not a per-tool marker. `get_file_info` is removed from `builtinShadowMap` | Sign-off review, S2. "Command-based" is not a reliable proxy for "local" (the documented `npx -y mcp-remote https://...` bridge shape disproves it), so a per-tool claim keyed on it can be wrong about a specific tool; a footer carries the same information with no such claim. Cutting the advisory outright (the review's other offered option) was rejected because it would also discard the information for the still-dominant case where the per-tool marker was correct. `get_file_info → file_type` was simply wrong: the two answer different questions | The per-tool `[shadowed by built-in: <name>]` marker (phase 7's original Behaviour section) and the `get_file_info` entry in `builtinShadowMap` |
| D49 | 2026-10-03 | `HttpConn.Call` runs `consumeResponseBody` in its own goroutine instead of calling it inline before the `select` | Sign-off review, B1. A server may hold its POST event-stream open after answering (the specification says SHOULD close it, not MUST); calling `consumeResponseBody` synchronously made `Call` wait for the body to end rather than for the waiter channel to receive the frame it was already holding, so a conformant server that keeps the stream open hangs every call to its context bound. Running it in a goroutine lets the `select` resolve the moment `deliver` places a result, since `consumeResponseBody` already owns closing the body on every path | The synchronous `c.consumeResponseBody(resp, id)` call immediately before `Call`'s `select` |
| D50 | 2026-10-03 | RFC 8707 `resource` is sent on the authorization request and on all three token grants, and is persisted on the token store entry so a refresh names the same identifier without re-walking discovery. An entry written before this falls back to the server's own endpoint | Sign-off review, B2. It is a MUST in the MCP authorization specification and the only mechanism that stops a token minted for one MCP server being replayed against another behind the same authorization server. Persisting it rather than recomputing it keeps the refresh grant from depending on a document fetch it does not otherwise need | — |
| D51 | 2026-10-03 | Every URL on the authorization chain must be https, or http on a loopback host: the server endpoint, the challenge's `resource_metadata` location, the issuer, all three advertised endpoints, every discovery redirect hop, and the authorization URL immediately before the browser hand-off. `serverconfig.go` deliberately still admits a plain-http endpoint | Sign-off review, B2. The refusal is scoped to the credential clai mints itself, where a cleartext hop would leak clai's own token; a LAN server reached over plain http with a static `token_env` credential is a legitimate configuration whose risk its operator already owns, and refusing it at parse time would break working setups for no gain clai controls. The loopback exception is what keeps a local MCP server, and this repository's fixtures, usable | — |
| D52 | 2026-10-03 | Redirects are refused outright on the registration endpoint, both token grants and the MCP endpoint itself, and bounded at three hops with each hop re-checked on the discovery GETs. `NewAuthorizer` derives two clients by copying whatever `WithHTTPClient` installed rather than mutating it | Sign-off review, B2. Go re-sends a POST body across a 307 or 308 and strips only the cross-domain `Authorization` header, never the body, so a redirect on a credential-bearing POST is a credential handed to the target — observed directly: the unfixed client delivered its PKCE verifier to a redirecting token endpoint. Discovery is treated differently because it carries no credential and a real issuer may legitimately redirect a well-known URL for trailing-slash normalisation, which refusing outright would break before the human gate could reveal it | — |
| D53 | 2026-10-03 | The non-interactive gate lives in `AuthorizeInteractive` itself, not in each caller. `httpChallengeResolver` keeps returning a non-nil `AuthResolver` on a headless run | Sign-off review, B2. The finding exists *because* the gate was per-caller: the setup path got one as R2-08 and the mid-run path did not. Gating at the root means no future caller needs its own copy. The resolver stays non-nil because `resolveMcpAuthWait` reads `authResolver != nil` to choose the actionable result's wording and whether to wait at all, so a nil would both misdescribe an endpoint-based server as command-based and burn the whole `auth_timeout` waiting for nothing | R2-08's setup-path branch, now redundant defence rather than the only gate |
| D54 | 2026-10-03 | Mid-run token expiry re-authorization is recorded as not implemented rather than built in this session | Sign-off review's own recorded cross-phase gap, with the review's explicit option to record rather than close. Verified against source: `bearerDecorator` captures a token string and is installed once at connect time, and `resolveMcpAuthWait` inspects only connector resolution, never a call result. Closing it needs a refreshable decorator seam on `HttpConn` (phase 4), a typed call-result path the executor can classify (phase 6), and a re-entry rule for a connection already handed out — a phase of its own, not a line in a security fix | — |
| D55 | 2026-10-03 | `oauthtestserver`'s zero configuration is hostile: it requires `resource` on the authorization request and on every token request and compares the authorization-code grant's `redirect_uri` against the issued one. The single relaxation is phrased negatively, `AllowMissingResourceParam` | Sign-off review, B2's sixth item. A permissive fixture is why this blocker shipped green through two review rounds and a gate sweep: it recorded `issuedCode.redirectURI` and never compared it. A negative relaxation keeps the zero value strict, so a future test cannot opt out of conformance by forgetting to opt in | The fixture's previous posture, which asserted neither parameter |


## Definition of success

| Outcome | Evidence |
| --- | --- |
| A run with stdio servers configured, **a warm cache**, and no MCP tool call creates no MCP process | End-to-end test asserting zero child processes for the configured fake server after a prior run warmed its entry |
| A first run against a cold cache still registers every tool, by connecting | The repository's existing `Test_setupMcpManager_RegistersToolsAndNotifiesSuccess`, still passing unmodified. It is cited, not introduced, so it is deliberately not a new name under checklist item two |
| A miss is captured, so the next run hits | `TestSchemaCacheMissConnectsCapturesThenHits` |
| A run that calls one tool from one of several configured stdio servers creates exactly one process | `TestLazyMultiServerSpawnsOnlyTheCalledServer` (added R2-04, phase 8, 2026-10-03 fix session: the previously cited description named no test, because none existed) |
| Three tool calls to one server in one batch create exactly one process | `TestConnectorConnectFailureSpawnsOnceAcrossRepeatedToolCallsEndToEnd` (updated 2026-10-02 fix session, R1-25: the previously cited `TestThreeCallsOneServerSpawnOnce` starts no real process; the new test counts real spawns through `toolExecutor.invokeToolCall`). `TestThreeCallsOneServerSpawnOnce` remains as the connector's own unit-level memoisation proof, which phase 2's invariants table cites separately |
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

Validation rounds use `V{n}-*` and comprehension probes `C{n}-*`. Implementation review rounds use
`R{n}-*` and are listed first, because they are the only rounds whose findings are still open.

### Sign-off review — holistic review, 2026-10-03

The first pass to read the whole effort at once rather than one phase at a time (see the Sign-off
verdict section below the session journal). Found four blockers thirteen prior passes missed. B3
and B4 gated phases 1–3 and 7; both, plus two smaller items the same review named, were fixed in
the first sign-off fix session. B1 gated phase 4 alone and was fixed in a second. B2 gated phases 5
and 6 and is fixed in a third, below. **All four blockers and both recommended items are now
closed.** One item the same review recorded is deliberately left open rather than fixed: mid-run
token expiry never re-authorizes (D54), a phase of its own rather than a line in a security fix.

| ID | Severity | Summary | Phase |
| --- | --- | --- | --- |
| B1 | blocker | `HttpConn.Call` returned when the response body ended, not when its answer arrived: `consumeResponseBody` was called synchronously before the `select` on the waiter channel, so a server permitted by the specification to hold its POST event-stream open after answering ("SHOULD" close it, not "MUST") made every call, `initialize` included, hang to its context bound and fail. `architecture/mcp.md` already documented the correct behaviour; no test covered it, and no fixture could express the failure | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 sign-off fix session: `Call` now runs `consumeResponseBody` in its own goroutine, so the `select` resolves on the waiter channel as soon as `deliver` places a result there rather than when the background read ends; evaluated for a race on `resp` and the waiter map across every call path, none introduced, confirmed by `-race` across three runs. `httptestserver` gained `HoldPostStreamOpen`, the missing fixture mode. `TestHttpConnCallResolvesWhenFrameArrivesEvenIfStreamStaysOpen`, proved red (unfixed code returns only at the full context bound) before green |
| B2 | blocker | The OAuth client was neither conformant nor safe — *"this is an OAuth-shaped object, not an OAuth client."* RFC 8707 `resource` was never sent and `ProtectedResourceMetadata.Resource` had zero readers; `redirect_uri` was absent from the authorization-code token request although the authorization request always set one, which a conformant server answers `invalid_grant` (direct evidence the interactive flow had never completed against a real authorization server); the discovery chain had no trust validation at all — `doc.Issuer` never compared to the issuer fetched, `prm.Resource` never to the server, no scheme requirement, no `CheckRedirect` on either `http.Client`; and `httpChallengeResolver` was not gated on `Interactive` although the setup path had been fixed for exactly that (R2-08). Composed: a malicious server, or an on-path attacker forging the 401, controlled resource metadata, issuer, registration, `/authorize` and `/token`; clai registered a client with the attacker, handed the attacker's `authorization_endpoint` to `xdg-open`, and stored whatever the attacker's token endpoint returned. Root cause of it shipping green: `oauthtestserver` was permissive at every one of those points | [phase 5](phase-5-oauth.md), [phase 6](phase-6-midrun-auth.md) — fixed, 2026-10-03 sign-off fix session (D50–D53, D55). `resource` on the authorization request and all three token grants, persisted on the entry; `redirect_uri` repeated on the exchange; a new `internal/tools/mcp/mcpauth/trust.go` validating the issuer (RFC 8414 §3.3), the resource identifier (RFC 9728 §3.3), the metadata location's hostname and the scheme of every chain URL including the browser hand-off and the server endpoint; redirects refused on the registration endpoint, both token grants and the MCP endpoint, bounded-and-rechecked on discovery; `AuthorizeInteractive` refusing a non-interactive run at the root. The fixture is hostile by default and every fix has a mode that fails without it. Red observed first in each case, the sharpest being `verifierLeaked=true` (the unfixed client POSTing its PKCE verifier to a redirecting token endpoint) and the non-interactive case **hanging** a full 60 s inside `awaitRedirect` rather than failing. `TestOauthSendsResourceOnAuthorizationAndTokenRequests`, `TestOauthRefreshGrantSendsResource`, `TestOauthTokenRequestRepeatsRedirectUri`, `TestOauthIssuerMismatchIsRefused`, `TestOauthResourceMismatchIsRefused`, `TestOauthAbsentResourceIdentifierIsRefused`, `TestOauthCrossHostResourceMetadataIsRefused`, `TestOauthPlainHttpDiscoveryUrlIsRefused`, `TestOauthPlainHttpServerEndpointIsRefused`, `TestOauthInsecureAuthorizationEndpointIsNeverOpened`, `TestOauthInsecureTokenEndpointIsRefused`, `TestOauthRedirectingTokenEndpointIsRefused`, `TestOauthRedirectingRegistrationEndpointIsRefused`, `TestOauthRedirectingRefreshEndpointIsRefused`, `TestOauthNonInteractiveRunIsRefused`, `TestRequireSecureURLAcceptsLoopbackAndHttps`, `TestRequireResourceCoversOriginAndPath`, `TestRequireIssuerMatchToleratesOnlyATrailingSlash`, `TestSecureHopRedirectBoundsAndChecksEveryHop`, `TestHttpConnRefusesEndpointRedirect`, `TestMidRunChallengeOnNonInteractiveRunNeverOpensBrowser`. The doc comment that presented the unvalidated fetch as a feature is corrected. Known limit, stated not hidden: the metadata-location check compares hostname, not port |
| B3 | blocker | A warm cache hides a dead server: for the dominant `npx`-launched shape, `resolveExecutable` fingerprints only the launcher, so a server broken by anything else is invisible at setup for up to twelve hours, and never reported at all if the model never calls it. Probed end to end: `err=<nil>`, zero spawns, tool still advertised | [phase 3](phase-3-schema-cache.md) — fixed, 2026-10-03 sign-off fix session: `Identity.LauncherOnlyFingerprint` (`schemacache.go`) reports true exactly when the command is a known generic launcher (`node`, `npx`, `npm`, `uv`, `uvx`, `pipx`, `python`, `python3`, `docker`, `bunx`, `deno`) and no args entry resolved to an on-disk script; `resolveLazyServerViaCache`'s cache-hit branch calls the new `warnIfLauncherOnlyFingerprint` helper, which prints one `ancli.Noticef` line naming the server before registering its cached tools. A direct-binary server (Executable fingerprints the server itself) stays silent, by design. `TestWarmCacheLauncherOnlyServerPrintsSetupNotice`, `TestWarmCacheDirectBinaryServerPrintsNoNotice` (control) |
| B4 | blocker | An empty tools array is captured and served as truth for twelve hours: `Capture` has no guard, `extractToolsArray` normalises an absent `tools` key to `[]`, and `RegisterTools([])` returns nil, so the capture is reached. Neither invalidation signal can fire on the resulting zero-tool entry. Probed: `Capture(empty) accepted=true, Lookup hit=true, tools=[]` | [phase 3](phase-3-schema-cache.md) — fixed, 2026-10-03 sign-off fix session: `Cache.Capture` (`schemacache.go`) now refuses (returns an error, never panics) when `tools` decodes to a JSON array with zero elements, via the new `toolsArrayIsEmpty` helper; both production call sites already degrade a `Capture` failure to a warning-and-connect, so no caller change was needed. `TestSchemaCacheRefusesEmptyToolsArray`. Three pre-existing tests that seeded a warm cache with `[]byte("[]")` purely to exercise unrelated mechanics were updated to seed a non-empty placeholder tool instead, since that was never their own point |
| S1 | minor | `internal/text`'s testserver fixture was built behind a `sync.Once` *inside* a test, so its ~4s `go build` ran against the `-timeout=30s` race-gate clock; measured cold at 25.3s, 84% of budget. [phase 8](phase-8-gate-sweep.md)'s own gate sweep recorded this as needing a package split to fix properly — that conclusion is wrong: Go arms the `-timeout` alarm inside `m.Run()`, so building before it, in a `TestMain`, removes the cost from the timed window entirely rather than needing less of it | [phase 3](phase-3-schema-cache.md) — fixed, 2026-10-03 sign-off fix session: new `internal/text/main_test.go` adds a `TestMain` that builds the fixture once before calling `m.Run()`; `testServerBinary` (`querier_setup_tools_test.go`) now only reads the result. Measured cold (`GOCACHE` pointed at a fresh throwaway directory): total `go test` time essentially unchanged (25.353s before, 25.352s after, since the same work runs either way) but the portion exposed to the `-timeout` alarm dropped from the whole figure to ~19.9s (instrumented directly: build 4.31s, then `m.Run()` 19.93s), raising headroom from ~4.7s to ~10.1s against the 30s bound. [phase 8](phase-8-gate-sweep.md)'s own stale "needs a package split" conclusion (lines 395–401 of that phase file) is corrected in place, with a pointer to this fix |
| S2 | minor | The shadow marker gated on transport (`server.Command != ""`), not locality: `npx -y mcp-remote https://...`, a shape this package's own schema-cache comments document, is command-based but genuinely remote, so its tools could be marked `[shadowed by built-in: cat]` on a bare name match — cat cannot read a Notion page. Also, one existing mapping entry (`get_file_info` → `file_type`) is wrong on its merits: the MCP tool returns size/mtime/permissions, `file_type` wraps `file(1)`, a different question | [phase 7](phase-7-shadow-advisory.md) — fixed, 2026-10-03 sign-off fix session. **Decision:** demoted to a single footer line (the smaller of the two offered corrective actions) rather than cut outright, since a footer can carry the same "a local built-in may already cover this" information with no claim about any specific listed tool to be wrong about. `mcpListingEntries` now returns the listing entries (no per-entry marker; the `marker` field is gone from `mcpListingEntry`) alongside a deduplicated, sorted slice of built-in names matched across command-based hits only; `List` prints them as one trailing `Note:` line. `get_file_info` removed from `builtinShadowMap` outright (no built-in answers what it actually returns). `TestToolsListNeverEmitsPerToolShadowMarker`, `TestShadowFooterNamesAvailableBuiltin`, `TestShadowFooterOmitsUnregisteredBuiltin`, `TestShadowFooterDeduplicatesAcrossServers`, `TestShadowFooterCoversLazyCachedServer`, `TestShadowFooterSkipsEndpointBasedServers`, `TestShadowFooterCoversCreateDirectory`, `TestShadowFooterNeverAttributesToASpecificRemoteTool` (the direct Notion/`mcp-remote` regression), `TestBuiltinShadowMapExcludesGetFileInfo` |

### Implementation review 2 — second code review of the same unpatched diff, 2026-10-02

Round 2 reviewed the identical working tree round 1 reviewed; nothing had been patched. It
re-verified a sample of round 1's findings against the code — all four blockers and rulings D35 to
D38 hold, and every phase-6 finding is upheld — and then went where round 1 did not: concurrency
and lifetime, HTTP protocol conformance, the `pkg/*` public surface, backward compatibility of
existing configurations, the fixture layer, and whether the code path can produce the measured goal
at all. One round-1 finding needed a correction, filed as R2-23.

| ID | Severity | Summary | Phase |
| --- | --- | --- | --- |
| R2-01 | blocker | `TestStdioConnectReclassifiesAuthPromptAsChallenge` fails deterministically on a cold Go build cache: a 200 ms connect bound must contain a `go run` compile. Reproduced with `go clean -cache`; the suite's green record is warm-cache-only | [phase 6](phase-6-midrun-auth.md) — fixed, 2026-10-03 fix session: `internal/tools/mcp/conn_stdio_test.go` gained its own `testServerBinary` helper (D41's pattern) and the one test this finding names now points at the prebuilt binary; reproduced cold via a throwaway `GOCACHE` to confirm. [phase 8](phase-8-gate-sweep.md)'s share fixed, 2026-10-03 fix session: every remaining direct `go run ./testserver` test call site repository-wide converted to a prebuilt binary (closing R2-19's cost as a side effect); the gate table now states the race row runs warm and a dedicated cold-cache sweep (three throwaway-`GOCACHE` runs) found no further instance of this bug class — only two distinct, different findings: the root package's cold-cache timeout is pre-existing (confirmed against the pre-worklog commit `384e8d2`, out of this worklog's scope) and `internal/text`'s is this worklog's own added test volume, not a tight bound, left as an acknowledged risk rather than fixed |
| R2-02 | blocker | Cold-cache lazy setup resolves servers serially where the eager path it replaced resolved them concurrently, contradicting D18. Probe: 4 servers × 1 s → lazy 4.05 s, eager 1.01 s. Six `npx` servers go from ~4.5 s to ~27 s on every first run | [phase 2](phase-2-lazy-connect.md), [phase 3](phase-3-schema-cache.md) — phase 2's share fixed (D39), 2026-10-02 fix session: one goroutine per server, joined through `toolWg`; `TestLazySetupResolvesServersConcurrently` |
| R2-03 | blocker | A command-based cache entry can never be invalidated for an interpreter-launched server: the identity fingerprints `node`/`npx`, not the server; no time bound; no watcher; no unknown-tool wrapper on the stdio path. Verified by probe | [phase 3](phase-3-schema-cache.md) — fixed (D40), 2026-10-03 fix session: `Identity.Script` fingerprints the first `args` entry resolving to an existing file, and the freshness bound now applies to every entry, not only an endpoint-based one; `TestSchemaCacheMissOnScriptFileDelta`, `TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo`. The watcher and unknown-tool-wrapper mechanisms remain phase 4's to wire (R2-16) |
| R2-04 | major | Two Definition-of-success rows have no evidence: the multi-server spawn-counting fixture does not exist, and R1-25 already voided the other. "Cost proportional to servers used" is unproven by any test | [phase 8](phase-8-gate-sweep.md) — fixed, 2026-10-03 fix session: `TestLazyMultiServerSpawnsOnlyTheCalledServer` (`main_mcp_lazy_e2e_test.go`), two lazy servers pre-warmed via `schemacache.Capture` (not through a real round trip, which would spawn both and make a "zero" assertion trivial), one tool called via the mock vendor's `tool_<name>` prompt convention: called server 1 spawn, uncalled server 0 |
| R2-05 | major | An explicit server's credential failure under strict startup degrades to a warning: `*mcpauth.CredentialSourceError` never unwraps to `ErrMcpServerStartup`, the only sentinel `setupTooling` classifies on | [phase 5](phase-5-oauth.md) — fixed (D45's wrap is a side effect), 2026-10-03 fix session: the funnel (`failureCollector.addExplicit`) wraps any explicit failure not already carrying the sentinel into `claierr.NewMcpServerStartup`; `Test_AgentSetup_ExplicitMcpCredentialFailure_FailsSetup` |
| R2-06 | major | A per-file config error is dropped whenever any other server parses, so this effort's two new validations silently delete a server with no message. A `startup` typo is enough | [phase 3](phase-3-schema-cache.md), [phase 4](phase-4-streamable-http.md) — fixed (D44), 2026-10-03 fix session: `setupMcpManager` warns on the joined `findConfiguredMcpServers` error unconditionally and joins it into the explicit failures under strict startup, closing both phases' share at the one shared read site; `Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers`, `Test_setupMcpManager_PerFileConfigErrorFailsStrictRun` |
| R2-07 | major | `agent.WithMcpServers` entries never pass `validateTransport`: both `command` and `url` set means `url` silently wins, neither set means `exec.CommandContext(ctx, "")` | [phase 2](phase-2-lazy-connect.md), [phase 4](phase-4-streamable-http.md) — phase 2's share fixed (D43), 2026-10-02 fix session: exported `serverconfig.ValidateTransport`, run over every `userConf.McpServers` entry in `setupMcpManager`; `TestExplicitServerBothCommandAndUrlFailsValidation`, `TestExplicitServerNeitherCommandNorUrlFailsValidation`. Phase 4's share verified closed by the same fix, 2026-10-03 fix session: its `isHTTP` selector only ever sees `mcpServers` entries, which have all passed `validateTransport` by construction; no code change needed in phase 4 |
| R2-08 | major | `Authorizer.Interactive` is set by both production constructors and read nowhere, `AuthorizeInteractive` on the setup path is unbounded, and `defaultPrintURL` writes the OAuth URL to process stdout. `agent.Setup` can hang and can print into a library consumer's stdout | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: `newMcpAuthorizer` sets `WithInteractive` from `userConf.OutputIsTerminal`; the setup-path flow branches on `authz.Interactive` before attempting it, and bounds the attempt by the resolved auth-timeout when it proceeds; `WithPrintURL` now routes through the sink's own writer via a new `AuthPrintWriter` capability; `TestSetupNonInteractiveSkipsAuthorizeInteractive`, `TestAuthPrintURLRoutesThroughSinkNotStdout` |
| R2-09 | major | `OutputIsTerminal` is assigned unconditionally and is always false for `pkg/agent` (`Out: io.Discard`, no option), so D22's auth-timeout is 0 for every SDK consumer and the `AuthResolver` is never driven | [phase 6](phase-6-midrun-auth.md) — fixed, 2026-10-03 fix session, folded together with R1-21: `Configurations.OutputIsTerminal` is now `*bool`, assigned only when unset; `pkg/agent.WithOutputIsTerminal` added and wired into `asInternalConfig`; `TestAgent_WithOutputIsTerminal_propagates`, `TestNewQuerierThreadsOutputIsTerminalIntoAuthTimeout` |
| R2-10 | major | No session is ever terminated: `Close` sends no `DELETE`, nothing in production calls `Conn.Close` at all, and the "spec-compliant" fake has no `DELETE` branch | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: `HttpConn.Close` sends a bounded `DELETE` when a session is held; `NewHttpConn` gives `Close` a production call site (a goroutine on `connCtx.Done()`); `httptestserver` gained a `DELETE` branch; `TestHttpConnCloseSendsSessionDelete`, `TestHttpConnCloseWithNoSessionSendsNoDelete`, `TestHttpConnContextEndingClosesConnectionAndSendsDelete` |
| R2-11 | major | The server-initiated GET stream is attempted once and never resumed; `id:` is discarded so `Last-Event-ID` is impossible. After the first stream close, list-changed invalidation is silently dead for the run | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: `runServerStream` now loops, reconnecting with `Last-Event-ID` (`sseFrameReader.LastID()`, newly tracked) after a bounded backoff, capping only consecutive failed connection *attempts*; `TestHttpConnServerStreamReconnectsWithLastEventID` |
| R2-12 | major | The lazy e2e fixture's "registers the same tools" claim is unasserted; an unresolved `-t` is a warning and exit 0, so a warm run registering zero tools passes both assertions | [phase 3](phase-3-schema-cache.md), [phase 7](phase-7-shadow-advisory.md) — phase 3's fixture fixed, 2026-10-03 fix session: `TestLazyStartupE2EUnderRace` now keeps both runs' stdout, asserts a positive `mcp_echo_echo` match and the absence of "which doesn't exist" on both runs, pins the cold spawn count to exactly 1, and drops the `"startup"` field entirely so it also proves the flipped default (closing R2-27 in the same edit); phase 7's listing-path share fixed in the same fix session: `Test_goldenFile_TOOLS_lists_mcp_tools_from_cache` warms a real cache entry and runs the real `clai tools` command, asserting both the cache-sourced tool and its shadow marker appear in stdout |
| R2-13 | major | Readiness checklist item 9 is false — three `setupMcpManager` tests leave `startup` unset and so now run the lazy branch, and a fourth pins `lazy` while spawning — and the item's own grep cannot detect either case | [phase 2](phase-2-lazy-connect.md), [phase 3](phase-3-schema-cache.md) — phase 2's tests fixed, 2026-10-02 fix session: the three unset-posture tests now pin a posture explicitly (two eager, one deliberately left unset with a comment explaining why); item 9 itself restated below |
| R2-14 | minor | An SSE event whose `data:` field is empty is treated as a malformed frame; on the shared GET stream that fails every in-flight call. Verified by probe | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: `sseFrameReader.next` checks the joined `data:` content, not the line count, so an empty-only event is skipped rather than returned as a zero-length frame; `TestSSEFrameReaderSkipsEmptyDataKeepAlive` |
| R2-15 | minor | `StartupMode` is validated only in `UnmarshalJSON`; `effectiveStartupMode` returns it verbatim, so any unrecognised value set programmatically through the public struct is silently eager | [phase 2](phase-2-lazy-connect.md) — fixed, 2026-10-02 fix session: `effectiveStartupMode` now returns `(StartupMode, error)` and rejects any value outside eager/lazy/unset unconditionally; `TestEffectiveStartupModeRejectsUnrecognisedProgrammaticValue` |
| R2-16 | minor | `isUnknownToolFailure` matches `*mcp.RPCCallError`, which only `HttpConn` produces, so the unknown-tool signal can never fire for stdio even once R2-03's wrapper is wired. R1-34's consequence | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: `mcp.RPCCallError` replaced by one shared `claierr.McpRPCError` built from both transports' `deliver`; `StdioConn` gained `NotificationWatcher`; `resolveLazyServerViaCache` (stdio) now wraps both branches with `cacheInvalidatingConn`/`cacheInvalidatingConnector` exactly as the HTTP resolver does; `TestStdioConnPublishesServerInitiatedNotifications`, `TestStdioSchemaCacheUnknownToolInvalidatesEntry`, `TestStdioSchemaCacheListChangedInvalidatesEntry` |
| R2-17 | minor | `TestShadowAdvisoryDoesNotAlterSelection`'s "no marker" assertion is unreachable by construction: the marker lives only in `mcpListingEntries`, which the test never calls | [phase 7](phase-7-shadow-advisory.md) — fixed, 2026-10-03 fix session: the test now drives `List()` then `Detail()` over one cached, shadowed entry and asserts the cached description survives unmarked; the old `mcp.RegisterTools`-based body was removed |
| R2-18 | minor | The two transport-XOR tests assert the same branch-insensitive error string, so collapsing the two branches into one check passes both | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: `validateTransport` returns two distinguishable messages; both tests assert the discriminating word (fixture files first renamed off `both.json`/`neither.json`, which made the naive assertion pass trivially on the old message); `TestMcpServerConfigRequiresExactlyOneOfCommandOrUrl`, `TestMcpServerConfigRejectsMissingTransport` |
| R2-19 | minor | The new lazy e2e test is 4.32 s, ~26 percent of the root race package and ~13 s at `-count=3`, and is the only root test invoking the Go toolchain, against a gate recorded as load-sensitive | [phase 8](phase-8-gate-sweep.md) — fixed, 2026-10-03 fix session: a root-package `testServerBinary(t)` helper (`main_test_helpers_test.go`), same pattern as the other two packages'; `TestLazyStartupE2EUnderRace` measured dropping from 4.32s to 0.25s |
| R2-20 | minor | The cache-injection parameters row is unmet outside `internal/text`: the shared root fixture does not pin `CLAI_CACHE_DIR`, so root e2e resolves the developer's real cache directory | [phase 3](phase-3-schema-cache.md), [phase 8](phase-8-gate-sweep.md) — fixed, 2026-10-03 fix session: `setupMainTestConfigDir` now sets `CLAI_CACHE_DIR` under its own temp config dir, and `TestSetupTooling_injectedToolsRegisterWithoutWarning` got its own line |
| R2-21 | note | The spawn counter every warm-cache proof rests on is fail-open: a spawn whose log write fails reads as no spawn | [phase 3](phase-3-schema-cache.md) — fixed, 2026-10-03 fix session: `testserver/main.go` now exits non-zero on either the open or the close failing instead of swallowing the error |
| R2-22 | note | Six further test-only exported options with doc comments describing README parameters production cannot set. R1-26's class; also corrects the reading that `ControlEvent.StartupTimeout` was a lost capability — it was already dead at `c3867d3` | [phase 1](phase-1-conn-seam-and-demux.md), [phase 4](phase-4-streamable-http.md), [phase 5](phase-5-oauth.md) — phase 4's share, 2026-10-03 fix session: `WithHttpProtocolVersion` deleted (same symbol as R1-26); `WithHttpReadBound` kept and recorded as an accepted, by-design test-support option (heavily test-used, and the parameter it overrides is deliberately shared with stdio's identical constant, never meant to be per-server configurable). Phase 5's share resolved as a side effect of its own R2-08 fix, same date: `WithInteractive` and `WithPrintURL` both now have a real production setter whose value is read, so neither is dead or test-only any longer. Phase 1's share, closed by [phase 8](phase-8-gate-sweep.md)'s 2026-10-03 fix session: `WithAuthPendingSink` deleted (R1-26) |
| R2-23 | note | **Correction to R1-23.** Its premise is true — there is no `MkdirTool` constant — but the inference is wrong: `ToolName` is a string type and `mkdir` is registered at `internal/tools/handler.go:43`. The recommendation stands, the reason does not | [phase 7](phase-7-shadow-advisory.md) — closed together with R1-23, 2026-10-03 fix session: `MkdirTool` added as a named constant rather than an inline string literal |
| R2-24 | note | The Validation-policy sentence "the root end-to-end fixture … spawns nothing" is stale for the root e2e package, though the shared helper is still clean | [phase 8](phase-8-gate-sweep.md) — fixed, 2026-10-03 fix session: the Parameters table's fixture-posture row now names `setupMainTestConfigDir` specifically (still clean) and notes the dedicated, opt-in fixtures layered on top of it that do spawn |
| R2-25 | note | `Lookup` treats a corrupt or unreadable entry as a silent miss while `Capture` warns every run, so a permanently unusable cache gives no diagnostic | [phase 3](phase-3-schema-cache.md) — deferred, 2026-10-03 fix session: would need `Lookup` to report a miss reason, a wider signature change than this note's severity warrants; no blocker or major depends on it |
| R2-26 | note | Real wall-clock bounds of 10 ms and 30 ms in `tool_executor_auth_test.go` on a host recorded as load-sensitive. R1-30's class, different file | [phase 6](phase-6-midrun-auth.md) — fixed together with R1-30, 2026-10-03 fix session: margins widened to 150 ms |
| R2-27 | note | The lazy e2e fixture pins `startup:"lazy"` and so cannot prove the flipped default; dropping the field proves it at no cost. Folded into R2-12's phase-3 entry | [phase 3](phase-3-schema-cache.md) — fixed together with R2-12, 2026-10-03 fix session |

### Implementation review 1 — code review of the shipped diff against contract, 2026-10-02

Reviewed `git diff c3867d3` plus the untracked additions: the README, all eight phase files and
every file the diff touches. Severities are the README taxonomy. A `blocker` or `major` reopens its
phase; a `minor` is annotated in place; a `note` is recorded only. "Where tracked" names the phase
file whose `## Review findings` section carries the full detail and the corrective action.

| ID | Severity | Finding | Where tracked |
| --- | --- | --- | --- |
| R1-01 | blocker | A stdio server whose stderr once matched the auth keyword list (substring `401` suffices) has its ordinary connect failure reclassified as a never-memoised `AuthChallengeError`, so every tool call costs two process spawns and a full auth-timeout block. Probe: 3 calls → 6 dials, 3 × bound elapsed | [phase 6](phase-6-midrun-auth.md) — fixed, 2026-10-03 fix session, all three corrective mechanisms: `resolveAuthPending` clears `authChallenged`; `connector.resolve` gained a per-run `mcpBlockedRedialCap` (D36); `IsMcpLogAuthChallengeLine`/`mcpLogAuthChallengeKeywords` (no bare `401`/`403`) now gate reclassification while the broader list stays UI-only; `TestAuthChallengeStdioSpawnBoundedAcrossRepeatedToolCallsEndToEnd` drives `invokeToolCall` three times against a real spawn counter: 2 spawns, not 6, not growing |
| R1-02 | blocker | `startup: "lazy"` on a server supplied through `agent.WithMcpServers` overrides the strict-startup exception; with a warm cache setup never connects, so `StrictMcpStartup` reports nothing. Probe: `err=<nil>`, 2 tools, 0 spawns | [phase 2](phase-2-lazy-connect.md), [phase 3](phase-3-schema-cache.md) — phase 2's share fixed (D35), 2026-10-02 fix session: strict-explicit checked unconditionally before any configured value; `TestLazyConnectHonoursStrictMcpStartupForExplicitServers`. Verified fixed from phase 3's own call site too, 2026-10-03 fix session |
| R1-03 | blocker | A credential written into `args` is persisted verbatim into a schema cache file, breaking invariant 6. `env` is digested; `args` is not. Verified by probe | [phase 3](phase-3-schema-cache.md) — fixed (D38), 2026-10-03 fix session: `Identity.ArgsDigest` replaces the verbatim `Args` field; `TestSchemaCacheArgsAreDigestedNotStoredVerbatim` |
| R1-04 | blocker | `clai mcp` panics with a nil pointer dereference: the parent command has `Subs` but no `OnRun`. Reproduced against a build of the working tree | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: `Command()` assigns `c.OnRun` to print help; `Test_e2e_mcp_bare_invocation_does_not_panic`, `{"mcp -h", ...}` added to `Test_e2e_command_help` |
| R1-05 | major | The credential command's ban policy reaches no production call site: `WithCmdBanContext` is applied only at the tool-call site, while every credential resolution runs under the setup or run context. The named test builds the ban context itself | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: `setupTooling` attaches `pkgtools.WithCmdBanContext(ctx, userConf.CmdBan)` to the one ctx threaded into `setupMcpManager`, reaching both the eager and lazy call sites; `Test_AgentSetup_CmdBanAppliesToMcpCredentialCommand`, `Test_AgentSetup_CmdBanAppliesToAmbientLazyHttpCredentialCommand` |
| R1-06 | major | The no-resolver (stdio) branch classifies auth-timeout expiry as a resolved wait and retries, contradicting the invariant row and the "no second spawn" integration row; it is R1-01's second spawn | [phase 6](phase-6-midrun-auth.md) — fixed (D37), 2026-10-03 fix session: `resolveMcpAuthWait`'s no-resolver branch now sets `waitErr = waitCtx.Err()`, so expiry is caught like any other case, no retry |
| R1-07 | major | One auth window that nothing closes suspends `mcpLogSink.Drain` for every server for the rest of the run, and the 256-entry queue then evicts non-error lines. Verified by probe | [phase 6](phase-6-midrun-auth.md) — fixed, 2026-10-03 fix session: `Drain` now scopes its suspension per server, draining every other server's entries immediately; `TestSessionLoopDoesNotRenderIntoAuthWindow` rewritten to prove it |
| R1-08 | major | The actionable tool result always says `run: clai mcp auth <server>`, which the subcommand refuses for every command-based server — the transport that reaches this path most often | [phase 6](phase-6-midrun-auth.md) — fixed, 2026-10-03 fix session: `actionableAuthResult` now branches on `hasResolver` (nil ⟺ command-based, per `tool.go`'s own doc comment), naming the server's own stderr output instead of `clai mcp auth` for a stdio server |
| R1-09 | major | A token-store write failure discards a valid, freshly issued token, contradicting "the run continues with the token it already holds"; the named test exercises `store.Save` alone | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: `AuthorizeInteractive`/`doRefresh`/`ResolveCached` return the valid entry alongside the write error instead of discarding it; `dialHttpServerWithAuth`/`httpConnOptsFor`/the interactive-retry branch all tolerate it; `TestSetupToolCallSucceedsDespiteUnwritableTokenStore` |
| R1-10 | major | The redaction invariant's four tests do not establish it: one is tautological, one re-asserts the other's string, and no carrier that embeds a response body or a config dump is covered. R1-03 is the proof it matters | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: the DEBUG dump was the real, proven leak — `redactMcpServersForDebug` now redacts `args`/`env` values before `querier_setup_tools.go`'s DEBUG dump; `TestDebugDumpNeverLeaksArgsOrEnvSecret`, `TestSecretsNeverLeakIntoUncoveredErrorTypes` (6 previously-uncovered error types), `TestMcpHttpStatusErrorNeverLeaksAccessToken`, `TestCredentialCommandOutputNeverEchoed` |
| R1-11 | major | The OAuth `state` is generated, sent, captured into `redirectResult.state` and never read; the loopback handler also ignores the request path. PKCE mitigates injection, so this is a claimed-but-absent defence rather than an exploit | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: `prepareAuthorization` returns its minted state, compared against `res.state` with a `*RedirectError` on mismatch; the loopback handler rejects any path but `/callback`; `TestOauthLoopbackStateMismatchIsTypedError`, `TestLoopbackHandlerIgnoresOtherPaths` |
| R1-12 | major | The response-body-limit is applied cumulatively to the long-lived GET server stream, so it dies silently after 2 MiB and takes tool-list-changed invalidation with it. The parameters row says "per message" | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: the outer `io.LimitReader` removed from the GET stream path; `sseFrameReader`'s own scanner buffer bounds each frame; `TestHttpConnServerStreamBodyLimitIsPerFrameNotCumulative` |
| R1-13 | major | A server-initiated request over HTTP is silently dropped, although phase 4 says it "is answered through the same path phase 1 established" and phase 1's invariant requires a `-32601` answer. No test is named for the clause | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: a server-initiated request now gets a POSTed `-32601` response carrying its id, from a new `respondMethodNotFound`; `TestHttpConnAnswersServerInitiatedRequestWithMethodNotFound` |
| R1-14 | major | `connect_timeout_seconds` has no effect on either cache-miss path: both bound only the handshake, at the 30 s default, and the spawn is unbounded. The parameters row defines it as wrapping spawn plus handshake | [phase 2](phase-2-lazy-connect.md) — stdio half fixed, 2026-10-02 fix session: `mcp.ConnectBoundOf`/`mcp.ReportAsConnectStage` exported and reused for the command-based miss path; `TestLazyCacheMissConnectBoundAppliesToStdioHandshake`. **The HTTP half fixed, 2026-10-03 fix session**, owned by [phase 5](phase-5-oauth.md)'s `internal/text/mcp_oauth.go`, not phase 4 as originally filed: `handshakeHttpServerWithAuth`'s own `runBoundedHandshake` now applies `mcp.ConnectBoundOf`/`mcp.ReportAsConnectStage`, mirroring the stdio fix exactly; `TestLazyHttpCacheMissConnectBoundAppliesToHandshake`. Both halves of R1-14 are now closed |
| R1-15 | major | The warm-cache endpoint hit branch has 0.0% coverage; its two integration rows are unproven and `newAuthenticatingHttpConnector` shows 100% only because a test constructs it by hand | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: the corrective-action test added exactly as specified, driven twice through `setupMcpManager` against the same cache and fixture; `TestHttpSchemaCacheWarmHitIssuesNoRequestUntilCalled` |
| R1-16 | major | Setup and the `clai tools` listing build different cache keys for a command-based server declaring `auth.scopes`, so it is omitted from the listing permanently and silently. The listing's test warms the cache with the listing's own builder | [phase 3](phase-3-schema-cache.md), [phase 7](phase-7-shadow-advisory.md) — fixed (D40), 2026-10-03 fix session: `BuildIdentityWithScopes` now ignores `Auth.Scopes` whenever `server.Command != ""`, so setup's `BuildIdentity` and the listing's `BuildIdentityWithScopes` agree for every command-based server; `TestSchemaCacheCommandServerIgnoresAuthScopes`; phase 7's test-design share fixed in the same fix session: `TestToolsListAgreesWithProductionSetupForAuthScopedCommandServer` warms the cache through the real `setupMcpManager` path and reads it back through the real `tools.List()`, instead of warming it with the listing's own builder |
| R1-17 | major | `architecture/tools-command.md` and `architecture/tooling.md` still state MCP tools are registered into the global registry and name a file that does not exist; `architecture/errors.md` documents the wrong `NewMcpTransport` signature and omits `Endpoint` | [phase 8](phase-8-gate-sweep.md) — re-confirmed still open, 2026-10-03 fix session: all three rows checked against current source and still false (`tools-command.md:24,93`, `tooling.md:86-87`, `errors.md:142`). Not fixed: fixing it means editing `architecture/`, which this session was instructed not to do. Left open for a session that can |
| R1-18 | major | A declared error-coverage row ("named environment variable unset → skipped, next tried") contradicts D15 and the code, and its named test covers two different cases | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: the code and its tests (`TestUnsetCredentialSourceIsSkipped`, `TestConfiguredEnvVarAbsentIsTypedError`) already matched D15 from an earlier session; only the phase file's Error coverage row was wrong, now split into the two distinct cases |
| R1-19 | minor | `loadNamedServer` is a third MCP config parser: it accepts `command` and `url` both set, and resolves a relative envfile against the process CWD | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: `loadNamedServer` now calls `serverconfig.FindConfiguredServers`; `TestLoadNamedServerRejectsBothCommandAndUrl`, `TestLoadNamedServerExpandsEnvfileRelativeToConfigDir` |
| R1-20 | minor | `isUnknownToolFailure` invalidates on `-32602`, the ordinary invalid-params code, contradicting its own documented rule; the `isError` half of the signal has no test and no fixture mode | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: requires `-32601` alone or `-32602` with an unknown/not-found marker; `TestIsUnknownToolFailureRequiresUnknownMarkerFor32602InvalidParams` and three sibling tests. The `isError` half's test gap is unchanged (not separately filed) |
| R1-21 | minor | The D22 default's production wiring has no test — zero test references to `OutputIsTerminal` — and two different terminal checks feed one decision | [phase 6](phase-6-midrun-auth.md) — fixed, 2026-10-03 fix session: the two-checks half was already closed as a side effect of phase 5's R2-08 fix (verified by grep); the no-test half closed together with R2-09, folding `OutputIsTerminal` into `*bool`; `TestNewQuerierThreadsOutputIsTerminalIntoAuthTimeout` drives the real `NewQuerier` composition root |
| R1-22 | minor | The shadow marker is keyed on the bare remote tool name, so a remote `read_file` is reported as shadowed by local `cat` | [phase 7](phase-7-shadow-advisory.md) — fixed, 2026-10-03 fix session: the marker now applies only when the owning server is command-based (`server.Command != ""`); `TestShadowMarkerSkipsEndpointBasedServers` |
| R1-23 | minor | `create_directory` → `mkdir` is omitted on the false premise that `mkdir` has no `ToolName` constant; `mkdir` is a registered native built-in | [phase 7](phase-7-shadow-advisory.md) — fixed, 2026-10-03 fix session (per R2-23's corrected reasoning): added `MkdirTool ToolName = "mkdir"` and a `builtinShadowMap` entry; `TestShadowMarkerCoversCreateDirectory` |
| R1-24 | minor | `clai mcp auth`'s eight failure paths are all bare `fmt.Errorf`, and the test named "...IsTypedError" asserts only `err != nil` | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: seven new typed errors in `internal/tools/mcp/cmd_errors.go`; `TestMcpAuthCommandMissingServerIsTypedError` now asserts the type; `TestMcpAuthCommandNonEndpointServerIsTypedError`, `TestMcpAuthCommandNoChallengeIsTypedError` cover the two previously-untested branches |
| R1-25 | major | Three tests cannot fail for the reason they state: the strict-startup test pins `StartupEager` in its own config, the retry-count test counts fake dials instead of spawns, and `TestThreeCallsOneServerSpawnOnce` — the Definition-of-success evidence for "one process" — starts no process | [phase 2](phase-2-lazy-connect.md) — fixed, 2026-10-02 fix session: strict-startup test split into two subtests exercising the actual strict branch; retry-count/batch claim moved to `TestConnectorConnectFailureSpawnsOnceAcrossRepeatedToolCallsEndToEnd`, a real spawn counted through `toolExecutor.invokeToolCall` |
| R1-26 | minor | Three exported options are dead with false doc comments: `WithAuthPendingSink`, `WithHttpProtocolVersion`, `WithHttpClient`. `staticcheck` does not flag exported symbols | [phase 1](phase-1-conn-seam-and-demux.md), [phase 4](phase-4-streamable-http.md) — phase 4's share fixed, 2026-10-03 fix session: `WithHttpProtocolVersion` and `WithHttpClient` deleted (zero call sites, nothing to wire). Phase 1's share (`WithAuthPendingSink`), deliberately left for the final sweep by phase 6's fixer, closed by [phase 8](phase-8-gate-sweep.md)'s 2026-10-03 fix session: confirmed zero call sites anywhere including tests, deleted from `internal/tools/mcp/conn_stdio.go` |
| R1-26b | note | `NewMcpConnClosed` and `NewMcpFrameUndecodable` are at 0.0% in `pkg/claierr`'s own suite, so no new MCP error's `Error()` string is asserted anywhere | [phase 1](phase-1-conn-seam-and-demux.md) |
| R1-27 | minor | The tools-list-changed watcher starts before `Capture`, so an invalidation in that window is lost, and it is started under the run context on a path that can fail; its `!ok` branch is unreachable | [phase 4](phase-4-streamable-http.md) — fixed, 2026-10-03 fix session: both miss-path resolvers `Capture` before watching, start the watcher under `connCtx`; `notifyCh` is now closed by `Close` under the same lock `publishNotification` checks (no send-on-closed-channel race); `TestHttpConnCloseClosesNotificationChannel`, `TestStdioConnCloseClosesNotificationChannel` |
| R1-28 | minor | `resolveExecutable`'s `exec.LookPath` reintroduces the `PATH` dependence the identity design excluded, so a different shell is a spurious miss | [phase 3](phase-3-schema-cache.md) — accepted as a known consequence, 2026-10-03 fix session: not fixed; D40's freshness bound now backstops a spurious miss's impact (one extra connect, never a stale serve), so the open-ended risk this finding warned about is now bounded |
| R1-29 | minor | `handshakeHttpServerWithAuth` leaks the first `HttpConn` on the interactive-retry path and `conn2` on a failed retry; `runAuthWith` leaks its probe connection | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: all three connections are now `Close`d at their respective points; `TestMcpAuthCommandNoChallengeIsTypedError` runs the fixture twice to prove the first connection was not held open |
| R1-30 | minor | "The clock is injected and no test sleeps" does not hold: the authorization family uses real sleeps against 10 to 30 ms bounds on a host the README records as load-sensitive | [phase 6](phase-6-midrun-auth.md), [phase 5](phase-5-oauth.md) — phase 5's share verified, 2026-10-03 fix session: audited; the one sleep in phase 5's own files is a liveness poll with no timing assertion, not a bound; phase 6's share (confirmed by R2-26) fixed, same date: every margin in `tool_executor_auth_test.go` widened roughly 5-15x (to 150 ms), no logic change |
| R1-31 | note | Phase 1's integration row requires an unawaited frame to be "counted and dropped"; no counter exists. Recommendation is to strike the word | [phase 1](phase-1-conn-seam-and-demux.md) |
| R1-32 | note | `mcp.LoadEnvFile` is a one-line wrapper giving one function two names; rename instead | [phase 5](phase-5-oauth.md) — fixed, 2026-10-03 fix session: `loadEnvFile` renamed to `LoadEnvFile`, wrapper deleted, one internal call site updated |
| R1-33 | note | Phase 8's documentation audit is a stale record: all four rows it reports as failing now hold, so it will send the next contributor to re-fix completed work | [phase 8](phase-8-gate-sweep.md) — fixed, 2026-10-03 fix session: the stale section's four rows are struck through to "resolved" in place, and a new "Documentation audit, re-run" subsection re-runs the audit against today's source, folding in R1-17's three rows (never covered by the original audit) and the sentinel count's further drift from seven to eight |
| R1-34 | note | `mcp.RPCCallError` is a new exported error type outside `pkg/claierr`'s declared-closed vocabulary and outside the code-layout table; the two transports return different types for one meaning | [phase 1](phase-1-conn-seam-and-demux.md), [phase 4](phase-4-streamable-http.md) — fixed as a side effect of R2-16, 2026-10-03 fix session: `mcp.RPCCallError` deleted, both transports build `*claierr.McpRPCError`; `Test_Claierr_McpRPCErrorIsOneTypeForBothTransports` |
| R1-35 | note | Phase 7 suite gaps: two tests exercise one branch, "no process started" is asserted by nothing, and a parse error is discarded with a bare `_` | [phase 7](phase-7-shadow-advisory.md) — fixed, 2026-10-03 fix session, all three: `TestShadowAdvisoryHandlesNoMcpServers` now creates an empty `mcpServers/`; `TestToolsListShowsMcpToolsFromCache` uses a real sentinel script in place of `unreachableCommand`; the discard site gained a one-line comment naming D21 |
| R1-35b | note | `architecture/README.md`'s index entry covers only phase 1, and `architecture/config.md` has no MCP field reference or pointer to `mcp.md` | [phase 8](phase-8-gate-sweep.md) — re-confirmed still open, 2026-10-03 fix session: both rows, plus the related `auth_timeout_seconds`-only-in-the-remote-example sub-point, checked against current source and still true (`config.md` has zero MCP headings across all 20 of its sections). Not fixed, same `architecture/`-editing restriction as R1-17 |
| R1-36 | note | A backwards clock makes an endpoint cache entry immortal; phase 3's "no validity decision depends on the clock" became false when phase 4 added the bound | [phase 3](phase-3-schema-cache.md) — resolved as a side effect of D40, 2026-10-03 fix session: the fail-open behaviour is now stated directly in phase 3's own Freshness section and `Lookup`'s doc comment, since the bound now applies there too |

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
| C4-02 | Which environment feeds the digest was unstated, and digesting the inherited process environment would change the key with every shell | Stated: the configured `env` map alone, with the process environment explicitly excluded. The closure first said "merged with the envfile", which contradicted the record-format algorithm and was corrected during phase 3's execution — an envfile's freshness rides its size and modification time, and its contents never reach a digest |
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

### 2026-10-02, implementation review 2

Second code review of the same working tree review 1 saw. Nothing had been patched, so this round
was spent on two things: verifying a sample of round 1's findings against the code, and covering the
areas round 1 did not reach.

**Verification of round 1.** Every phase-6 finding re-checked — R1-01, R1-06, R1-07, R1-08 — holds,
and the mechanisms are as described: `utils.IsMcpLogAuthLine` is a case-insensitive substring match
over a list containing `"401"` and `"403"`; `reclassifyIfAuthChallenged` (`connector.go:215-224`)
produces the challenge that `isBlockedOutsideRun` then exempts from the memo; the no-resolver branch
is `<-waitCtx.Done()` followed by a fall-through into a second `ResolveForCall`; `Drain` returns nil
whenever `authOpen` is non-empty. R1-12, R1-13, R1-16 and R1-22 were also re-checked against the
code and hold. D35 to D38 are the right rulings for the findings that produced them. One finding
needed correcting and is filed as R2-23: R1-23's premise about the missing `ToolName` constant is
true, but its inference is not, because `ToolName` is a string type.

**Gates re-run.** `go build ./...`, `go vet ./...`, `gofumpt -l .`, `staticcheck ./...` and
`go fix ./...` are all clean. `dupl -t 80 .` reports 35 clone groups, and phase 8's accounting of
them is accurate down to the one new production pair. The full
`go test ./... -race -cover -count=3 -timeout=30s` failed at host load 19 to 25, but every timeout
reproduced as `ok` per package at the same load (`internal/text` 7.9 s, root `clai` 23.0 s,
`internal/audio` 13.7 s, `internal/tools/mcp` 15.9 s), so the timeouts are load. The exception is
real and is R2-01: `TestStdioConnectReclassifiesAuthPromptAsChallenge` failed 3/3 in the suite and
then reproduced deterministically with `go clean -cache`, because its 200 ms connect bound has to
contain a `go run` compile. The suite was never green on a cold build cache, which is what CI has.

**Probes run, all deleted afterwards.**

| Probe | Result | Finding |
| --- | --- | --- |
| `go clean -cache` then the single auth-reclassification test | FAIL every time, `want *claierr.AuthChallengeError`; `ok` warm | R2-01 |
| Four lazy servers with distinct identities, cold cache, a fake stdio server with a 1 s pre-handshake delay, `setupMcpManager` timed | lazy 1 server 1.01 s, lazy 4 servers 4.05 s, eager 4 servers 1.01 s | R2-02 |
| `BuildIdentity` for a `node`-launched script, then rewrite the script with a different size and mtime | identity key byte-identical; fingerprint was `{Path:…/node Size:122889056}` | R2-03 |
| `sseFrameReader` over `"data:\n\n" + "data: {…}\n\n"` | frame 0 is a zero-length payload, which `handleJSONFrame` treats as malformed | R2-14 |

The second probe also surfaced a confound worth recording for whoever reproduces it: four servers
with identical command, args and env share one cache entry, because the identity is
content-determined and carries no server name. The first misses and captures and the other three
hit within the same setup run, so a naive repro shows 1.01 s and looks as though the serialisation
is absent. Distinct `env` per server is what exposes it. That sharing is correct behaviour, not a
defect.

**Cross-cutting observations that are not themselves findings.**

- `Conn.Close()` has no production caller. Outside tests, the only call is `StdioConn.readFrames`'s
  own `defer`. Every connection's and every goroutine's lifetime is `runCtx`-driven: the stdin
  closer, the stderr reader, the reaper and the frame reader all end when the run context does, and
  `dialStdio` reaps the process on every failure branch through one conditional defer. That is
  coherent, and it is why R1-29's "leaks" are a class rather than three sites — but it means the
  HTTP session is never terminated (R2-10) and that a connection abandoned mid-run keeps its child
  process for the rest of the run.
- `session_runner.go:246` cancels the root context on a `StopEvent` with no pending tool calls. A
  lazy connector captures that same root context as its `runCtx`, so a tool call arriving after a
  cancellation would dial under a dead context and memoise the failure permanently. At `c3867d3`
  the eager path was equally broken in that situation — its connection was killed by the same
  cancellation — so this is not a regression and is not filed as a finding. It is flagged here
  because the default flip makes `chat`/`-re` plus MCP the combination most likely to meet it, and
  the maintainer is better placed than this review to say whether that combination is reachable.
- `setupMcpManager` sends `ControlEvent` on an unbuffered channel with no `ctx.Done()` guard
  (`querier_setup_tools.go:230`), while `mcp.Manager` returns on `ctx.Done()`
  (`manager.go:64-66`). A cancellation between those two therefore deadlocks setup. The shape is
  identical at `c3867d3`, so it is pre-existing and not filed; R2-02's serialisation widens the
  window it needs.

**Verdict: not ready.** The gates do not pass on a clean machine (R2-01), the cold-cache path the
default flip made universal is N times slower than the path it replaced (R2-02), and the cache the
whole design rests on cannot be invalidated for roughly 85 percent of local servers by this
worklog's own census (R2-03). Beyond those, the one sentence this worklog exists to prove — cost
proportional to servers used — has no automated evidence of any kind (R2-04), and the public SDK
contract is breakable from the public API on three separate paths (R2-05, R2-07, R2-08). Green gates
are not a verdict here for the second round running: four of round 1's blockers and all three of
round 2's pass every gate, and R2-01 passes every gate on the machine that ran them.

**Routing.** Phases 2 to 8 carry round-2 findings and are set to `Reopened (review 2)`; phase 1
takes one note and stays complete. The findings stay on their owning phases rather than being
collected into an addendum phase, because R2-01, R2-02 and R2-03 are each local to one phase's
contract and each has a decision row (D41, D39, D40) that says what the fix is. R2-05, R2-06 and
R2-07 share one shape — an error or a validation that does not reach the boundary that would act on
it — and D42 to D44 record the rulings so the fixer does not have to re-derive them.

### 2026-10-02, implementation review 1

Reviewed the shipped diff against contract rather than against the implementation notes: the
README, all eight phase files, and every file in `git diff c3867d3` plus the untracked additions.
Nothing was fixed; four throwaway probe tests were written, run and deleted, and the working tree is
unchanged apart from this worklog.

**Gates re-run independently, all from the repository root.** `go build ./...`, `go vet ./...`,
`gofumpt -l .`, `staticcheck ./...` and `go fix ./...` are all clean, with the working tree
byte-identical before and after `go fix`. `dupl -t 80 .` reports 35 clone groups, matching phase 8's
claim exactly; filtered to this effort's files there are precisely two, both pre-declared, and zero
new. `go test ./... -race -cover -count=3 -timeout=30s -p 1` passed all 53 packages, exit 0, at a
one-minute load average of 15.75 — above the threshold the README records as the failure point, so
this run is stronger evidence than the executors' own. **Green gates are not the verdict:** all four
blockers below pass every gate, and two of them pass a named test that cannot fail.

**Verdict: not ready.** Four blockers, fourteen majors. Phases 2 to 8 reopened; phase 1 passed and
is annotated only. The three human-required gates are legitimately outstanding and were treated as
such.

**The four blockers**, each proven rather than argued:

1. **R1-01.** `utils.IsMcpLogAuthLine` is case-insensitive substring matching over a list that
   includes `"401"`, so `listening on port 4010` on a stdio server's stderr sets a sticky
   `authChallenged` flag; `dialStdio` then reclassifies an ordinary connect failure as
   `AuthChallengeError`, which `BlockedOutsideRun` exempts from the connector's memo, and
   `resolveMcpAuthWait` dials once, waits out the whole bound, and dials again. Probe through the
   production `invokeToolCall`: **3 tool calls → 6 dials, elapsed = 3 × the bound, 3 bells.** At the
   120 s terminal default that is six process births and six minutes of blocked session for one
   three-call batch, scaling linearly with the model's call count. The implementation notes argue
   the false positive is "bounded to the handshake's own latency" because `deliver()` resolves the
   window — true of the window, but `deliver` never clears `authChallenged`.
2. **R1-02.** `effectiveStartupMode` checks the configured field before the strict-startup
   exception, so `startup: "lazy"` on an `agent.WithMcpServers` server wins. Probe: strict explicit
   server, `StartupLazy`, shared cache — `setupMcpManager` returned `err=<nil>`, 2 tools and **0
   spawns** on both the cold and the warm run. The named test pins `StartupEager` in its own config
   literal, so it would pass unchanged if the strict branch were deleted.
3. **R1-03.** The cache identity digests `Env` and stores `Args` verbatim. Probe: the same secret in
   `args` appears in plain text in the 0600 cache file while the one in `env` does not. The
   precondition is a literal token in the server JSON, which is the documented `mcp-remote --header`
   shape the README's own census puts at 732,960 weekly downloads.
4. **R1-04.** `clai mcp` panics — the parent command has `Subs` and no `OnRun`, so
   `internal/command.go:139` dereferences a nil querier. It is the only parent command in `main.go`'s
   map that omits `OnRun`, and the repository's own `Test_e2e_command_help` gained no row for the new
   command.

**Cross-cutting observation, elevated into Strategy as invariants 10 and 11.** The recurring shape
behind R1-01, R1-02, R1-05, R1-10, R1-15, R1-16 and R1-25 is not seven unrelated mistakes: it is one
testing habit. Each of those rows is green because its test drives the seam the row names rather than
the production composition root — it builds its own `WithCmdBanContext`, pins the posture the
production resolver was supposed to choose, warms a cache with the same identity builder the code
under test uses, constructs the connector the setup path would have built, counts dials where the row
says spawns, or asserts a secret's absence from a struct that could never hold it. Driving any one of
them through the real entry point would have caught its blocker; three of my four blocker probes are
four-line tests through the existing production functions. Invariant 11 is the composition failure
underneath R1-01: D20's exemption from memoisation and the retry-count parameter's "per run" wording
only agree if something caps the re-dials, and nothing did.

**Honest loose ends ruled on**, since each was reported by its executor rather than hidden:

- *Phase 5's eager-versus-lazy credential asymmetry* — **accepted as declared.** The posture split
  is documented at the call site, the eager path is an explicit opt-in, and the stated reason
  (reopening phases 1–2 to give `mcp.Manager` a retry-on-challenge seam) is proportionate. It should
  be a decision row rather than only a comment.
- *Three dead exported options* — **minor, R1-26.** Not harmless: `WithHttpClient`'s doc comment
  asserts a use ("Tests use this to set a short timeout") that does not exist, and
  `staticcheck` structurally cannot catch an unused exported symbol.
- *The untested `cacheInvalidatingConnector`* — **upgraded to major, R1-15.** The honest report
  understated it: the whole warm-cache endpoint hit branch is 0.0%, so two integration rows are
  unproven and the wrapper order could be wrong with the suite still green.
- *Five typed errors' `Error()` never exercised* — **note, R1-26b.** One table-driven test over the
  seven MCP strings closes it for three phases at once.
- *The accepted duplication* — **accepted again.** Verified as exactly two groups, both declared,
  zero new. Extracting the transport `Close()` pair would couple two deliberately independent
  transports for sixteen lines.
- *The widened surfaces* — **mostly right, one wrong, one pointless.** `pkgtools.ValidateCmdNotBanned`
  is the right shape but reaches no production path (R1-05), which is the real defect, not the export.
  `Configurations.TrustInput` and `Configurations.OutputIsTerminal` are the right trade against
  twenty-five test call sites, but `OutputIsTerminal`'s wiring is referenced by zero tests (R1-21),
  so the saving bought an untested seam. `mcp.LoadEnvFile` is a one-line wrapper that should have
  been a rename (R1-32).

**On phase 8's documentation audit.** All four rows its notes report as failing now hold — they were
fixed after the notes were written. Three different false claims remain in three architecture notes
(R1-17), one of them introduced by the fix that closed the sentinel-count row. The notes are
therefore a stale record and will send the next contributor to re-fix completed work (R1-33).


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

### 2026-10-02, phase 1 execution

Phase 1 implemented and marked `Complete`. `Conn`/`Connector` land in
`internal/tools/mcp/conn.go` exactly as specified; `internal/tools/mcp/conn_stdio.go`
replaces `client.go`/the per-tool `seq` in `tool.go` with a stdio `Conn` owning one
id source, one pending-waiter map, and the four enumerated goroutines. The retired
`ControlEvent.StartupTimeout` is migrated to a construction-time `StdioConnOption`
on the connection, read back by `handleServer` through an optional-interface check
rather than restated per call. Two new typed errors (`McpConnClosedError`,
`McpFrameUndecodableError`) were added to `pkg/claierr`, not three: the
oversized-frame "bounded error" reuses the undecodable-frame type, differentiated
only by its cause. All twenty declared test names exist and pass; the full gate
(`gofumpt`, `staticcheck`, `go vet`, `go fix`, `dupl`, and
`go test ./... -race -cover -count=3 -timeout=30s`) passes unedited, confirmed
twice on a quiet host after two unrelated packages (root `clai`, `internal/audio` —
neither touched by this phase) timed out once during a load-average-20+ spike and
then passed cleanly in isolation; this matches the repository's documented
host-load sensitivity, not a regression.

A real `-race` finding during implementation, not anticipated by the spec: the
first `Close` closed the process's stdin synchronously, which could race a
`Call` still mid-write and surface a raw pipe-closed error instead of the typed
`McpConnClosedError`. Fixed by having `Close` only fail pending waiters and mark
the connection closed, leaving stdin teardown to the dedicated stdin-closer
goroutine alone, as the phase's own goroutine enumeration already separates the
two concerns.

Documentation gap, by direction mid-session rather than by discovery: the
coordinating session reassigned `architecture/mcp.md`, the `architecture/tooling.md`
pointer reduction, and the `architecture/errors.md` typed-error row to itself and
told this execution not to touch `architecture/`. No file under `architecture/`
was created or edited by this phase's execution. The phase's documentation
requirement is therefore outstanding from this session's point of view; whether
it is satisfied depends on work done outside this phase file, and a future
validation pass should check `architecture/mcp.md` exists and
`architecture/tooling.md`/`architecture/errors.md` were actually updated before
treating phase 1's documentation obligation as closed.

Phase 1's documentation requirement, which its executor was told to leave alone, was completed by
the coordinating session and is now closed. `architecture/mcp.md` was created as the single owner of
the connection model, the demultiplexing contract and its four frame dispositions, the goroutines
per stdio connection, the three bounds, the configuration layout, and the ambient-versus-explicit
failure posture, with the never-shared property stated together with the two measurements that
establish it. The MCP sections of `architecture/tooling.md` were reduced to pointers holding no
detail of their own, which removed the untrue claim that `clai tools` lists MCP tools; phase 7 owns
adding an accurate statement to `architecture/mcp.md` once it makes one true. `architecture/errors.md`
gained rows for the two typed errors phase 1 introduced, `ErrMcpConnClosed` and
`ErrMcpFrameUndecodable`, with the paragraph about which errors wrap a cause corrected, since the
first wraps none. Two incidental repository fixes: `tooling.md` pointed at a nonexistent
`architecture/tools.md`, and the new note is registered in the architecture index. Verified
independently of the executor: `go build ./...` and `go vet ./...` clean, and
`internal/tools/mcp` plus `internal/text` passing under `-race` at 81.3 percent coverage for the
mcp package.

### 2026-10-02, phase 2 execution

Phase 2 implemented and marked `Complete`. `mcp.Connector` lands in
`internal/tools/mcp/connector.go` as specified: single-flight, memoised on a
terminal outcome, never memoised or retried on a resolution blocked outside
the run (a new `outsideRunBlocker` optional interface this phase defines as
the seam, with no producer of its own — phase 6's eventual auth-pending
signal is the first). `mcpTool` now holds a `Connector` rather than a `Conn`;
an eager server's already-connected `Conn` is wrapped in a trivial
`resolvedConnector` so both postures call the same way, with no behaviour
change for the unconditionally-`eager` default this phase ships (D16).
`internal/text/querier_setup_tools.go` skips the spawn/`ControlEvent` path
for a server whose resolved `startup` field is `lazy`; nothing wires that
server's `Connector` to a tool yet, since no tool name exists without
`tools/list`, which phase 3's cache is what supplies without connecting. All
sixteen declared test names exist and pass; the full gate passes unedited,
confirmed on a quiet host after the shared root `clai` and `internal/audio`
packages (neither touched by this phase) were first seen to time out once
during a load-average 21+ spike, matching the repository's documented
host-load sensitivity rather than a regression.

A real `go vet` finding during implementation, not anticipated by the spec:
the per-attempt spawn context's cancel function is intentionally left
uncalled on the success path (the connection must outlive the dial
attempt, bound to the run instead), which `go vet`'s `lostcancel` analysis
flags regardless. Resolved with a named return and a single `defer` that
cancels only on failure, a mechanical fix with no behavioural change.

A scope judgment made while implementing, flagged for phase 3 rather than
decided silently: this phase does not make `setupMcpManager` construct or
retain an `mcp.Connector` for a lazy server, since nothing in phase 2 can
wire one to a registered tool (no schema exists without connecting) and no
code-layout row assigns that storage to phase 2. The "constructs a
Connector" invariant is instead proven at the `mcp` package level, driving
`NewConnector` directly. If phase 3's setup-side cache call site expects to
find an already-constructed `Connector` per lazy server rather than
constructing its own, that expectation was not visible from this phase's
own file and should be checked against what phase 3 actually needs.

Phase 2's executor flagged a handover question rather than resolving it silently, and the resolution
is recorded here rather than left to phase 3 to rediscover. Phase 2 deliberately does not have
`setupMcpManager` construct or retain a `Connector` for a lazy server: it skips such a server
entirely at setup, because nothing in phase 2 can wire a connector to a registered tool when there
is no tool list to register. Verified against the shipped code: `effectiveStartupMode` gates the
skip in `querier_setup_tools.go`, and `mcp.NewConnector(runCtx, server, sink, opts...)` is exported
and usable from `internal/text`. Constructing the connector is therefore phase 3's work, performed
at the setup-side cache call site the code-layout table already assigns to it, and that row now says
so explicitly. No amendment to phase 2 is needed and its status stands.

### 2026-10-02, phase 3 execution

Phase 3 implemented and marked `Complete`. `internal/tools/mcp/schemacache` lands as specified:
identity, record, read and write, with `Identity.Key()` the hex SHA-256 of the identity's own JSON
encoding, so the entry filename is the content key and an identity delta is a miss by construction.
`BuildIdentity` is infallible — an unresolvable executable and a configured-but-missing envfile both
collapse to the same canonical absent (`nil`) marker — which turned out to be the one mechanism that
satisfies both the unresolvable-executable and the missing-envfile error-coverage rows without a
second code path: neither state could ever have a prior successful capture, so both are always a
miss, and the pre-existing typed envfile error surfaces unchanged once setup connects. The
setup-side call site replaces phase 2's dead-end lazy branch (`toolWg.Done(); continue`) with
`resolveLazyServerViaCache`, reached only for the lazy-resolved case; the eager
`ControlEvent`/`Manager` path, and its test coverage, is untouched. All eighteen declared test names
exist and pass, including the new root fixture `TestLazyStartupE2EUnderRace`; the full gate passed
unedited, confirmed on a quiet host, with one later `internal/vendors` timeout (untouched by this
phase's diff) at a load spike to 18, which reran clean in isolation — the repository's documented
host-load sensitivity, not a regression.

One ambiguity resolved rather than escalated, recorded so it is not re-litigated: the identity
section's "which environment is digested" prose named the env map merged with the envfile's
contents, while the record-formats section's own algorithm sentence says the digest is over the
environment map alone, no envfile. Implemented per the latter, more precise statement — `env_digest`
covers only `server.Env`; the envfile's freshness is carried entirely by its separate size/mtime
component, with no file read for cache purposes — which also keeps a secret-bearing envfile's
contents out of a digest computation that invariant 6 already forbids from a cached file in any
other form. This is flagged here as the one place a later reader might expect the merged behaviour
the prose describes.

One pre-existing test was corrected rather than left to break or silently deleted:
`TestLazyConnectDegradesForAmbientServers` (phase 2) asserted that an explicitly-lazy ambient server
never connects at setup, unconditionally. D18 supersedes that by design — a miss always connects,
cold or warm — so the test's body was rewritten to assert the corrected cold-cache behaviour, with
the zero-process claim it used to make now proven by the new `TestSchemaCacheHitRegistersToolsWithoutTransport`
instead. This is a direct, textually-unambiguous consequence of D18 already in the README, not a new
decision made by this phase.

`mcp.ProtocolVersion`, `mcp.HandshakeBoundOf`, `mcp.Handshake`, `mcp.RegisterTools`, `mcp.NewTool` and
`mcp.NewResolvedConnector` were added to the `mcp` package beyond the code-layout table's explicit
`mcp.NewConnector` mention, extracted from `handleServer`'s previously-inlined sequence so the
cache's miss path reuses the same initialize/tools-list/register logic instead of duplicating it.
None of this changes `handleServer`'s or the connector's observable behaviour, confirmed by
`internal/tools/mcp`'s own suite passing unmodified; flagged here since the table named only the one
symbol.

Two plan defects surfaced by phase 3's executor and corrected rather than worked around. The
identity specification said the environment digest covered the `env` map merged with the envfile's
contents, while the record-format section's own algorithm said the map alone; the executor
implemented the map alone and flagged the conflict. That is the correct reading — an envfile's
freshness is already carried by its size and modification time, and keeping its contents away from
a digest is what invariant 6 requires — so all three copies of the claim now say so, including the
feedback-index row that repeated the wrong version. Separately, phase 2's invariant row described
setup as constructing a connector with no process, which D18 has since made misleading: a cache miss
does connect. The row was narrowed to describe constructing a connector, which still dials nothing,
and phase 2 carries a review finding recording that its ambient-degrades test was rewritten to the
post-D18 behaviour with the zero-process claim moving to the cache phase.

### 2026-10-02, phase 4 execution

Phase 4 implemented and marked `Complete`. `internal/tools/mcp/conn_http.go` adds `HttpConn`, a
second `Conn` implementation satisfying phase 1's interface unchanged, plus `NewHttpConnector`
and `dialHttp` beside it; `mcp.Manager`, `mcpTool` and `connector.go` were not modified. Request
and response plumbing was written from the specification's own contract (read-and-classify the
POST response by content type, an optional GET stream, a session header, a connect-time legacy
signal); the abandoned branch's SSE line/event accumulation shape was the only part reused, as
specified. The endpoint-based half of the schema cache (freshness bound, `Invalidate`) landed in
`internal/tools/mcp/schemacache`; the two invalidation signals are wired from a new
`internal/text/mcp_http_schema_cache.go` that wraps the live `Conn`/`Connector` rather than
changing either. The fake streamable-HTTP server lives at `internal/tools/mcp/httptestserver`,
with every mode the phase's Fixtures section names, including the challenge-with-caller-supplied-URL
and bearer-token modes phase 5 will configure. All twenty-five declared test names exist and pass;
see the phase file's Implementation notes for the duplication trade-off (StdioConn's pending-waiter
helpers are duplicated, not extracted, since refactoring the already-shipped `conn_stdio.go` was
read as out of scope) and for the test-infrastructure finding about `httptest.Server.Close()`
racing a connection's background stream against `t.Context()`/`t.Cleanup` ordering, which will
recur in later phases' own streamable-HTTP tests.

The full-suite gate (`go test ./... -race -cover -count=3 -timeout=30s`) was run three times. Each
run itself drove this host's load from under 5 to 17–23, and each run's resulting `FAIL`s were
exclusively 30s timeouts in a different, shifting subset of packages this phase never touches
(root, `internal/audio`, `internal/vendors`) plus, once each, `internal/text` and
`internal/tools/mcp` themselves on an assertion/timing basis unrelated to any test this phase
added. Every failing package was re-run alone at load 2–7 immediately after and passed cleanly
every time. This is the same host-load-sensitivity class phases 1–3 each recorded, now additionally
confirmed to include load the monolithic `./...` invocation induces on itself, not only load present
beforehand. `gofumpt`, `go vet`, `staticcheck` and `go fix` are clean; `dupl` reports no new clone
group in a file this phase touches.

### 2026-10-02, phase 5 execution

Phase 5 implemented. `internal/tools/mcp/mcpauth` lands the OAuth client, the token store and the
credential-precedence chain; `internal/tools/mcp/oauthtestserver` is the one new fixture, serving
the protected-resource document alongside the authorization server exactly as specified, with
`httptestserver`'s existing challenge and bearer-token modes configured rather than duplicated. All
thirty-four declared test names exist and pass, plus several supplementary ones (error-method
coverage, a real binary-vs-unit integration test, a missing-server-config case) added where the
declared set left an easy, cheap gap. Full details — the necessary additions beyond the original
code-layout table (now reflected there with new rows), the scope reduction limiting the full
credential chain to the lazy HTTP path (eager HTTP servers get only the static half), the scopes
component resolving from static config rather than the live negotiated value, and two load-exposed
issues caught by `-count=3` (one host-load timeout unrelated to this phase, one genuine test-design
flaw in the single-flight test, fixed with a deterministic test-only join barrier) — are in the
phase file's own Implementation notes rather than repeated here.

`architecture/` was left untouched on this session's explicit instruction that the coordinating
session owns it for this worklog; the phase's own Documentation section is therefore outstanding.

The phase carries a human-required gate this execution cannot satisfy itself: a maintainer must run
`clai mcp auth <server>` against one real, OAuth-protected vendor endpoint, confirm the interactive
flow completes and that a second run reuses the stored token with no prompt, and report which
endpoint was used and whether the printed-URL fallback was exercised. The automated suite is green
and the subcommand prints the token store's path and mode on success, but no fake was substituted
for that confirmation and no real vendor endpoint was contacted. The phase is left `In Progress`
rather than `Complete`; the status board reflects this.

### 2026-10-02, phase 6 execution

Phase 6 implemented. All nineteen declared test names exist and pass, plus seven
supplementary ones added where a cheap test closed a real gap in proving the production
wiring (`TestStdioConnectReclassifiesAuthPromptAsChallenge` and
`TestLazyHttpMidRunChallengeRetriesAfterInteractiveAuth` drive the real stdio and HTTP
paths end to end, not only a fake). `pkg/claierr.AuthChallengeError` gains one method,
`BlockedOutsideRun() bool`, which is the entire change connector.go needed: phase 2's
existing single-flight memoisation (shipped unmodified) already reads exactly that
interface. `AuthPendingSink` and `AuthResolver` land in `internal/tools/mcp/conn.go`
exactly as the shared-interfaces section specifies; `conn_stdio.go` reclassifies a
connect-stage timeout into the same `AuthChallengeError` type an HTTP 401 already
produces, so the tool-call-site wait in `internal/text/tool_executor.go` has one path for
both transports rather than one per transport. Per D20, the embedded interactive-flow
retry `internal/text/mcp_oauth.go`'s `dialHttpServerWithAuth` carried since phase 5 was
removed from that function (a lazy HTTP server's *mid-run* dial, reached on its first
tool call) and left untouched on `handshakeHttpServerWithAuth` (the lazy *cache-miss*
path, which still runs at setup time, a different path phase 5 already owns and tests).

One point this execution had to resolve rather than find stated: the phase's prose pairs
"exactly one further resolution" only with a *resolved* wait, never with an *expired* one,
and the two readings are mutually exclusive once both outcomes have their own declared
test. Implemented as: a resolved wait (success, or — for a command-based server with
nothing to drive — simply waiting the bound out) retries once; an expired wait
(`context.DeadlineExceeded` from an `AuthResolver`'s own bounded attempt) never retries.
Recorded in the phase's own Implementation notes in case a later reader expected the
other pairing.

`Configurations` gained one field, `OutputIsTerminal bool`, rather than threading a new
parameter through `setupTooling`/`setupMcpManager`/the two lazy-resolve functions, which
would have forced touching roughly twenty-five existing test call sites that construct a
bare `Configurations{}`; every one of them keeps compiling and gets the conservative
fail-fast default untouched. `architecture/` was left untouched on this session's
explicit instruction that the coordinating session owns it for this worklog; the phase's
own Documentation section is therefore outstanding, as phase 1's and phase 5's equivalent
sections were from their own executors' point of view.

The full gate (`gofumpt`, `go vet`, `staticcheck`, `go fix`, `dupl`, and
`go test ./... -race -cover -count=3 -timeout=30s`) passed unedited, confirmed twice in a
row on a quiet host (load ~4) after one incidental finding on the first run:
`Test_e2e_skills_descriptor_activation_and_persistence` (root package, a skills-trust and
cost-estimate test this phase never touches) failed once under the full suite's own
induced load and then passed three times in isolation under `-race -count=3`, matching
the host-load-sensitivity class phases 1 through 4 already recorded rather than a
regression.

The phase carries a human-required gate this execution cannot satisfy itself: a real
human, at a real terminal, completing a mid-run authorization flow while a tool call
waits. The automated suite is green and the phase's own Human Required subsection now
names a concrete server, a concrete way to force a mid-run prompt from an expired
authorization, and the bound to use, so the maintainer's confirmation is the only
outstanding step. No real vendor endpoint was contacted and no credential was generated
by this session. The phase is left `In Progress` rather than `Complete`; the status board
reflects this.

### 2026-10-02, phase 7 execution

Phase 7 implemented and complete: all ten declared test names exist and pass, plus three
supplementary ones (two pinning `schemacache.ListCachedServers` directly, one pinning `Detail`'s
cache-only fallback). `clai tools` now has a real MCP tool set for the first time, sourced entirely
from the schema cache, correcting the standing untruth D21 identified. Executing this phase surfaced
two import-cycle constraints the original code-layout row didn't anticipate: the shared config
parser and the schema-cache directory name both had to move to new homes (`internal/tools/mcp/
serverconfig`, and a constant onto `schemacache` itself) because `internal/tools` cannot import
`internal/text`, and — less obviously — cannot import `internal/tools/mcp` either, since that
package's own tests import `internal/tools`. Both relocations are behaviour-preserving (the
existing callers in `internal/text` are now one-line delegates, all their original tests still
pass unmodified) and got their own code-layout rows per the executor-adds-a-row convention. Full
details, including the dupl-driven removal of three now-redundant tests and the help-text fix this
phase's own change to `main_dispatch_e2e_test.go` required, are in the phase's Implementation
notes. `architecture/mcp.md` and the MCP sentence in `architecture/tooling.md` were deliberately
left untouched on this session's explicit instruction; the coordinating session owns them. Full
gate green: `go build`, `go vet`, `gofumpt`, `staticcheck`, `go fix`, `dupl`, and
`go test ./... -race -count=3 -timeout=30s` (run with `-p 1` to avoid this session's own test run
compounding a busy shared host's ambient load of 8–20; the race/count/timeout flags themselves were
never altered). Two timeouts seen in an earlier, concurrent-package run
(`internal/audio`, `internal/tools/mcp`, plus the root package) were confirmed as the repository's
documented host-load sensitivity, not regressions, by re-running each in isolation.

### 2026-10-02, phase 8 execution

Ran every gate in the phase's table unmodified: gofumpt, staticcheck, `go vet`, `go fix` and the
duplication scan all clean or resolved; the full `-race -cover -count=3 -timeout=30s` suite passed
with no `FAIL` across all 53 packages (run with `-p 1`, permitted). A bare `make qa` hit the
repository's documented host-load sensitivity (two packages timed out under a load spike from ~11 to
~37) and both were confirmed non-regressions by isolated re-run immediately after, consistent with
every earlier phase's experience of this same host. Verified directly against the `Makefile` that
`go vet` and `dupl` are not part of `qa`/`lint` and ran them separately, as the phase itself says to.

Duplication: one pre-declared clone (`conn_http.go`/`conn_stdio.go`'s `Close()` and pending-waiter
shape) accepted in writing per phase 4's deliberate trade-off. One further clone not previously
declared by the worklog — a verbatim test-helper duplicate between
`internal/text/querier_setup_tools_test.go` and the pre-existing `pkg/agent/mcp_setup_test.go` —
found, judged, and accepted in writing rather than fixed, since lifting a 20-line unexported test
helper across those two packages has no existing shared home and is a bigger move than this phase
should make unreviewed.

Coverage: every package this worklog substantially changed clears the 70% floor once correctly
scoped (one global `-coverpkg=./...` run was caught producing a wrong 0% for a package later proven
at 87.2% once scoped to its real consumer — recorded as a tooling caution for future sessions).
Five concrete, named gaps reported for the owning phases to judge: two unused functional options in
`conn_http.go`/`conn_stdio.go` with zero call sites anywhere in the repository, one untested
cache-invalidation decorator in the warm-cache lazy-HTTP path, and five of six new typed errors'
`Error()` string methods never exercised by any test.

Documentation audit: of the phase's seven consistency rows, three hold (the new `mcp.md`'s shape,
`tooling.md`'s pointer reduction, the corrected `clai tools` inspection sentence, and the README
index registration) and four do not — the Configuration section in `mcp.md` omits
`auth_timeout_seconds`; `cmd-dispatch.md`'s command map and table omit the new `mcp` subcommand
entirely (though the dynamically generated root usage text is unaffected); `tools-command.md` was
never touched by this worklog and still describes `clai tools` as a pure registry listing, with no
mention of the MCP cache source or the shadow-advisory marker; and `errors.md`'s sentinel table
lists only three of this worklog's new/affected MCP errors, missing four, while its own prose
(unchanged) still says "the four MCP errors," inconsistent with the three rows actually present.

Stopped at the phase's `Human required` gate: the fleet-level steady-state RSS comparison needs the
maintainer's own host and real agents. Automated process-count assertions confirmed passing; a
configuration pair and the exact figures to capture are proposed in the phase's Implementation
notes. Phase left `In Progress — human gate outstanding`, matching phases 5 and 6.

### 2026-10-03, phase 5 fix session

Fixed every finding that reopened phase 5 across both implementation reviews: R1-04 (bare `clai
mcp` panic), R1-05 (ban policy not threaded to credential resolution, both eager and lazy),
R1-09 (a token-store write failure discarding a valid token), R1-10 (the redaction test family,
whose real gap was a genuine leak — the DEBUG dump serialised `args`/`env` verbatim), R1-11 (the
OAuth `state` parameter minted and never read), R1-14's HTTP half (`connect_timeout_seconds` not
bounding the HTTP cache-miss connect), R1-18 (an error-coverage row that contradicted D15; the
code and its tests were already correct, only the row's text was wrong), R1-19 (a third config
parser), R1-24 (bare `fmt.Errorf` throughout `clai mcp auth`), R1-29 (three connection leaks),
R1-30's phase-5 share (verified compliant; the real violation is phase 6's file), R1-32
(`LoadEnvFile`'s redundant wrapper), R2-05 (a credential failure not reaching
`StrictMcpStartup`), R2-08 (`agent.Setup` able to hang and print to a library consumer's stdout),
and R2-22's phase-5 share (resolved as a side effect of R2-08). Promoted the eager/lazy credential
asymmetry, previously recorded only as an implementation-note comment, to decision **D45**.

Every fix drove through the real production composition root per invariant 10 — `agent.Setup`,
`setupMcpManager`, `setupTooling`, the real `clai mcp` command dispatch — rather than a hand-built
seam; the R2-08 hang-proof in particular was verified red-before-green (removing the `Interactive`
gate alone reproduces a real loopback-redirect timeout). Full gate run clean:
`go build ./...`, `go vet ./...`, `gofumpt -l .` (no output), `staticcheck ./...` (no output),
`go fix ./...` (no changes), `dupl -t 80 .` (35 clone groups; the one group beyond phase 8's
already-declared 34 is the same pre-existing `querier_setup_tools_test.go`/`pkg/agent/mcp_setup_test.go`
test-helper duplicate phase 8 already found, judged and accepted in writing — only its line numbers
moved), and `go test ./... -race -cover -count=3 -timeout=30s` (all packages passed; host load
~3 throughout, well under the documented load-8 threshold).

The phase's own `Human required` real-endpoint gate is untouched and still outstanding — not this
session's to satisfy. Phase left `In Progress — human gate outstanding`, matching phases 6 and 8's
own posture.

### 2026-10-03, phase 6 fix session

Fixed every finding that reopened phase 6 across both implementation reviews: R1-01 (the dominant
finding — a stdio connect failure reclassified into a never-memoised `AuthChallengeError` on a
bare `401`/`403` substring, costing two process spawns and a full auth-timeout block per tool call,
unbounded across calls), R1-06/D37 (the no-resolver branch's expiry misclassified as a resolved
wait, the exact mechanism of R1-01's second spawn), R1-07 (one auth window globally suspending
`mcpLogSink.Drain` for every server), R1-08 (the actionable result naming `clai mcp auth`, which
refuses every stdio server), R1-21 and R2-09 (folded into one fix: `OutputIsTerminal`'s production
wiring untested and unconditionally overwritten, and unreachable for any `pkg/agent` SDK consumer),
R2-01 (a 200 ms connect bound enclosing a `go run` compile, reproduced cold), and R1-30/R2-26
(folded into one fix: real wall-clock margins too tight for this host's documented load
sensitivity). R1-26 is owned by phase 1 and was not re-litigated here; it stays open there.

R1-01 was fixed with all three of the finding's corrective mechanisms together, per this task's own
instruction that the keyword-list tightening alone is insufficient: the sticky `authChallenged`
flag is now cleared on every definitive answer (`resolveAuthPending`, called from both `deliver`
and `Close`), the connector now caps blocked-outside-run re-dials at one per server per run (D36,
`connector.go`'s new `mcpBlockedRedialCap`), and reclassification now requires the stricter
`IsMcpLogAuthChallengeLine` classifier rather than the broader UI-only one. Every fix drove through
the real production composition root per invariant 10: `toolExecutor.invokeToolCall` against a real
spawned `TEST_SERVER_AUTH_HANG` process and a real spawn-log counter for R1-01
(`TestAuthChallengeStdioSpawnBoundedAcrossRepeatedToolCallsEndToEnd`), and the real `NewQuerier` →
`querier_setup.go` → `setupTooling` → `setupMcpManager` chain with a pre-warmed schema cache for
R1-21/R2-09 (`TestNewQuerierThreadsOutputIsTerminalIntoAuthTimeout`), rather than a hand-built
`Configurations` or a direct `newMcpAuthorizer`/`setupMcpManager` call that would have bypassed the
exact line under test.

Full gate run clean: `go build ./...`, `go vet ./...`, `gofumpt -l .` (one file needed realigning,
applied with `-w`, then clean), `staticcheck ./...` (no output), `go fix ./...` (no changes),
`dupl -t 80 .` (36 clone groups; the one group beyond the prior 35 is this session's own
`testServerBinary` helper, copied into `internal/tools/mcp/conn_stdio_test.go` from the
pre-existing `internal/text/querier_setup_tools_test.go` original — accepted in writing as the same
class of test-only fixture-build duplication phase 8's gate-sweep session already recorded, not
extracted into a shared package for 20 lines reused once more in a different test binary), and
`go test ./... -race -cover -count=3 -timeout=30s` (all packages passed; host load ~2-3 throughout).
R2-01's fix was additionally verified cold: `GOCACHE=<fresh empty dir> go test
./internal/tools/mcp/ -run TestStdioConnectReclassifiesAuthPromptAsChallenge -race -count=1`
compiled for 4.48 s and still passed, reproducing the exact R2-01 scenario and confirming the fix
rather than merely the absence of the old failure.

The phase's own `Human required` real mid-run authorization gate is untouched and still
outstanding — not this session's to satisfy. Phase left `In Progress — human gate outstanding`,
matching phases 5 and 8's own posture. `architecture/mcp.md`'s "When a human is needed mid-run"
section is now stale on two counts — read, not edited, by direction (the coordinating session owns
`architecture/`): it describes the authorization-failure exemption as unconditional, where D36 now
caps it at one re-dial per server per run; and it describes the actionable result as always naming
the authorizing command, where R1-08 now makes that true only for an endpoint-based server.

### 2026-10-03, phase 7 fix session

Fixed every finding that reopened phase 7: R1-16 (phase-7's test-design share — the root cause was
already fixed at the root by phase 3's D40, verified by reading rather than re-fixed here), R1-22
and its R2-17 restatement (the marker was keyed on the bare remote name with no reference to
transport, so a remote `read_file` was reported as shadowed by local `cat`), R1-23 and its R2-23
correction (`create_directory` → `mkdir` omitted on a true premise but a false inference), R1-35's
three suite gaps (two tests exercising one branch, an unasserted "no process started" invariant,
and an uncommented bare `_`), R2-12's phase-7 share (no e2e evidence for the cache-sourced tool set
or the marker above the unit level), and R2-17 (an unreachable assertion in
`TestShadowAdvisoryDoesNotAlterSelection`).

The marker now carries a transport check: `mcpListingEntries` (`internal/tools/cmd.go`) builds a
server-name-to-config map from the already-parsed configured servers and only calls
`shadowingBuiltin` when the owning server's `Command != ""`, closing the remote-`read_file`
misreport without touching the declared mapping itself. `create_directory` → `mkdir` needed one
new named constant, `pub_models.MkdirTool`, added beside its neighbours rather than an inline
string literal, per R2-23's own recommendation.

R1-16's phase-7 share and R2-12's phase-7 share were both, at root, the same class of defect the
README's dominant cross-phase invariant names: a test proving a fix must go through the real
production composition root on both sides of the seam it is proving, not a hand-built one.
`TestToolsListAgreesWithProductionSetupForAuthScopedCommandServer` (new,
`internal/text/mcp_listing_identity_test.go`) warms a command-based server's cache entry through
the real `setupMcpManager` → `resolveLazyServerViaCache` path — the same path a real run takes —
then reads it back through the real `tools.List()`, instead of warming the cache with the
listing's own `BuildIdentityWithScopes` call as `captureListingCacheEntry` still does for the
phase's other, legitimate unit tests of listing behaviour. Verified this test can actually fail:
temporarily reverting D40's `server.Command == ""` guard in `schemacache.go` to unconditional made
it fail, restoring the guard made it pass again.
`Test_goldenFile_TOOLS_lists_mcp_tools_from_cache` (new, `main_tools_e2e_test.go`) closes R2-12's
phase-7 half the same way: it writes a real `mcpServers/fs.json` under the e2e fixture's
`CLAI_CONFIG_DIR`, warms one real cache entry under its `CLAI_CACHE_DIR`, runs the real `clai
tools` command through `run()`, and asserts both the cache-sourced tool and its
`[shadowed by built-in: write_file]` marker appear in stdout — the first e2e evidence this
listing's cache-only source and its marker exist above the unit level.

R2-17's `TestShadowAdvisoryDoesNotAlterSelection` was rewritten to drive the real `List()` then the
real `Detail()` over one cached, shadowed entry, asserting the listing line is marked and the
`Detail` JSON's description survives exactly and unmarked; its predecessor drove `mcp.RegisterTools`
directly, which has no reference to the shadow map and could not fail for the reason the test
claimed to pin. `TestToolsDetailShowsMcpToolFromCache` and the removed test's `listingFakeConn`
double were folded into the rewrite rather than kept as a near-duplicate.

Full gate run clean: `go build ./...`, `go vet ./...`, `gofumpt -l .` (no files listed),
`staticcheck ./...` (no output), `go fix ./...` (no changes), `dupl -t 80 .` (36 pre-existing clone
groups, none touching this session's files), and `go test ./... -race -count=3 -timeout=30s -p 1`
(every package `ok`). Coverage of this session's touched packages, from the targeted `-cover` run:
`internal/tools` 81.3%, `internal/text` 85.3%, `pkg/text/models` 85.3%, root package 94.6%.

Phase returned to `Complete` on the status board; no architecture file was read or edited, per this
session's explicit instruction. `architecture/mcp.md`'s "The tool listing" section and
`architecture/tools-command.md`'s marker description are both now stale on one point: the marker's
scope is restricted to command-based servers (R1-22/R2-17), which neither file is known to state
either way without being read.

### 2026-10-03, phase 8 fix session (final gate re-sweep)

Picked up phase 8, `Reopened (review 2)`, as the last non-complete phase on the board. Fixed every
finding that reopened it except two, which cannot be fixed without editing `architecture/` (out of
this session's explicit instructions): R2-01 (phase 8's gate-row share), R2-04, R2-19, R2-20 and
R2-22 (re-confirmed closed), R2-24, R1-33, and the R1-04 cross-reference. Left open: R1-17 (major)
and R1-35b (note), both re-confirmed still true against today's source. Full detail, including the
gate commands, the cold-build-cache investigation and its baseline comparison, the duplication
re-ruling, the re-run documentation audit, and the coverage re-check, is in
[phase 8](phase-8-gate-sweep.md)'s own "Fix session, 2026-10-03" and "Documentation audit, re-run"
subsections, not restated here.

Three things worth a cross-session note. First, the cold-build-cache investigation (action item 2
of this session's brief) found two *different* findings, not one: the root package's cold-cache
timeout predates this worklog entirely (proven by running the identical command against this
branch's own base commit, `384e8d2`, extracted via `git archive` into a scratch directory — no
checkout, no commit, nothing touched in this working tree), while `internal/text`'s is new but is
this worklog's sheer added test volume compiling cold, not a tight bound wrapping a `go run` (the
one suspicious case found, `TestConnHandshakeTimeoutReturnsTypedError`'s 50ms `WithHandshakeBound`
in `internal/tools/mcp/manager_test.go`, was audited and confirmed safe: it is a response-wait
deadline, not a spawn-bound, so it fires on schedule regardless of compile time, and it passed
every cold-cache run including the one that reproduced the two real timeouts). Every direct `go
run ./testserver` test call site repository-wide now points at a prebuilt binary; `internal/text`'s
remaining cold-cache margin is a volume problem a future repackaging could address, not something
this phase's charter covers.

Second, the `mcp_http_config_test.go` self-duplicate the dupl re-run surfaced is not a new,
undeclared finding: this session briefly merged its two XOR tests into one table-driven test
(matching `serverconfig`'s own sibling convention), then reverted on discovering phase 4's own
2026-10-03 fix session had already considered and explicitly declined exactly that merge, in
writing, for a stated reason (test-name traceability). The worklog's rule against re-litigating a
phase's recorded decision applies here even though phase 4 is `Complete` and the decision lives in
its own phase file rather than the README's decisions log.

Third, `WithAuthPendingSink` (R1-26) is deleted, closing the one finding other phases deliberately
left for this final sweep (phase 6's fixer named it explicitly when declining to reach into phase
1's scope).

Status board and feedback index updated in this session. Phase 8 stays `In Progress`: the
fleet-memory human gate is still outstanding, and R1-17/R1-35b are confirmed still open and require
`architecture/` edits this session does not make.

Patch pass complete. All sixty-five findings from both implementation reviews are closed. Phases 1,
2, 3, 4 and 7 are `Complete`; phases 5, 6 and 8 are `In Progress` on their human gates alone, with
no code or documentation finding outstanding against any of them.

The coordinating session closed the two findings phase 8 correctly declined, R1-17 and R1-35b, since
both needed `architecture/` edits that phase was scoped out of. Each claim was verified against
source before editing: `tools-command.md` named `internal/tools/init.go`, which does not exist;
`tooling.md` still said MCP tools enter the same registry as built-ins, which the per-run registry
rule made false; and `errors.md` documented `NewMcpTransport` without its `endpoint` parameter. The
same session also widened the architecture index entry, gave `config.md` an MCP section that points
at `mcp.md` rather than duplicating it, and showed `auth_timeout_seconds` on the local-server example
since it bounds a stdio server's stderr prompt equally.

Two risks are recorded rather than fixed, both judged out of a patch pass's charter. The root package
times out on a cold Go build cache, which the phase-8 sweep confirmed is pre-existing by extracting
the base commit and reproducing it there. `internal/text` also times out cold, and that one is new:
it is this effort's own test volume rather than a tight bound, every direct `go run` call site having
been converted to a prebuilt binary. Warm CI is unaffected at twenty-one seconds, and fixing it
properly means splitting the package.

### 2026-10-03, sign-off fix session (B3, B4, S1, S2)

Picked up the two blockers and two smaller items the holistic sign-off review (below) filed against
phases 1–3 and 7, per the coordinating session's explicit instruction to fix exactly these four and
no others (phases 4, 5 and 6 stay held on B1/B2, untouched). Read the Sign-off verdict section first
as the authority for what to fix; filed the work as a new "Review 3"/"sign-off" round against phase
3 (B3, B4, S1) and phase 7 (S2), following how reviews 1 and 2 were recorded in each phase file.

Each fix was proved red (a failing test against a reverted copy of the production change) before
green, per CLAUDE.md. **B4:** `Cache.Capture` (`internal/tools/mcp/schemacache/schemacache.go`) now
refuses an empty tools array; `TestSchemaCacheRefusesEmptyToolsArray`. **B3:**
`Identity.LauncherOnlyFingerprint` gates a new setup-time notice on the cache-hit path in
`resolveLazyServerViaCache` (`internal/text/querier_setup_tools.go`);
`TestWarmCacheLauncherOnlyServerPrintsSetupNotice` plus a direct-binary control that must stay
silent. **S1:** `internal/text/main_test.go` adds a `TestMain` that builds the shared stdio fixture
before `m.Run()`, correcting `phase-8-gate-sweep.md`'s own stale "needs a package split"
conclusion in place; verified cold with an instrumented, temporary timestamp print (removed before
finishing): the portion of the run exposed to the `-timeout` alarm dropped from ~25.3s to ~19.9s,
headroom rising from ~4.7s to ~10.1s against the 30s bound. **S2:** the per-tool shadow marker is
demoted to a single footer line (`internal/tools/cmd.go`'s `mcpListingEntries`/`List`), and
`get_file_info` is removed from `builtinShadowMap` as a wrong mapping on its own merits; direct
regression test for the review's own Notion/`mcp-remote` probe,
`TestShadowFooterNeverAttributesToASpecificRemoteTool`.

One dupl clone group this session's own new tests introduced (near-identical setup-plus-assertion
bodies across `mcp_listing_test.go`) was extracted into two shared helpers before the final dupl
run, per the repository's own rule that duplicated code is abstracted; the full-repo clone count
stayed at the pre-existing 36. Three pre-existing schema-cache tests that seeded a warm cache with
an empty tools array purely to exercise unrelated mechanics (identity comparison, freshness,
invalidation) were updated to seed a one-element placeholder instead, since B4's new guard would
otherwise reject their seeding step for a reason unrelated to what each was testing.

Full gate run clean: `go build ./...`, `go vet ./...`, `gofumpt -l .` (no files listed),
`staticcheck ./...` (no output), `go fix ./...` (no changes), `dupl -t 80 .` (36 clone groups,
matching the pre-existing baseline), and `go test ./... -race -cover -count=3 -timeout=30s -p 1`
(every package `ok`, host load 0.9–2.1 throughout this session).

`architecture/mcp.md` and `architecture/tools-command.md` were not read or edited, per standing
instruction; both are now additionally stale on S2's footer shape, on top of the pre-existing
staleness phase 7's own 2026-10-03 fix session already recorded about the transport restriction —
flagged here for the coordinating session, which owns `architecture/`.

Status board, Decisions log (D46–D48) and Feedback index (new Sign-off review section) updated in
this session. Phases 1, 2, 3 and 7 move from "approved subject to two fixes" to fully approved; the
Sign-off verdict section's top line and Conditions paragraph are amended in place with a closure
note rather than rewritten, so the verdict's own historical finding stays legible. Phases 4, 5 and 6
remain held, unaffected.

### 2026-10-03, sign-off fix session (B1)

Picked up B1, the sole blocker holding phase 4, per the coordinating session's explicit instruction
to fix exactly this one finding (phases 5 and 6 stay held on B2 and their own conditions,
untouched). Read the Sign-off verdict section first as the authority for what to fix; filed the
work as a new "Review 3"/"sign-off" round against phase 4, following how reviews 1 and 2 were
recorded there and how the prior sign-off session recorded B3/B4 against phase 3 and S2 against
phase 7.

**B1:** `HttpConn.Call` (`internal/tools/mcp/conn_http.go`) called `consumeResponseBody`
synchronously, before the `select` on the waiter channel, so a server permitted by the
specification to hold its POST event-stream open after answering ("SHOULD" close it, not "MUST")
made every call hang to its context bound and fail, even though the answer had arrived from the
start. Proved red first: `TestHttpConnCallResolvesWhenFrameArrivesEvenIfStreamStaysOpen`
(`internal/tools/mcp/conn_http_test.go`) against a server fixture flushing its result frame and
then holding the stream open failed with `context deadline exceeded` at the full 1 s bound. Fixed
by running `consumeResponseBody` in its own goroutine, so the `select` resolves on the waiter
channel as soon as `deliver` places a result there, instead of waiting for the background read to
end; evaluated against every path through `Call` (plain JSON, event-stream, `initialize`'s
`captureSession`/`streamOnce`, and the `ctx.Done()` branch) for a race on `resp` or the waiter map —
none introduced, confirmed by `-race` across three runs. The same test now passes in well under the
context bound.

`httptestserver` gained `HoldPostStreamOpen` (`internal/tools/mcp/httptestserver/httptestserver.go`):
flushes the SSE result frame, then blocks (bounded at 2 s, `HangForever`'s own pattern) instead of
returning, the fixture mode the review named as missing from the tree — no prior test could express
"answer, then hold the stream open."

Full gate run clean: `go build ./...`, `go vet ./...`, `gofumpt -l .` (no files listed),
`staticcheck ./...` (no output), `go fix ./...` (no changes), `dupl -t 80 .` (36 clone groups,
matching the pre-existing baseline, no new group from this session's files), and `go test ./... -race
-cover -count=3 -timeout=30s -p 1` (every package `ok`, host load 2.27 at start).

`architecture/mcp.md` was read, not edited: its transport section already states the behaviour this
fix restores ("an event-stream body is parsed for frames until the awaited id arrives"), and it
reads true again now that the code matches it.

Status board, Decisions log (D49), Feedback index (Sign-off review section, B1 row) and the
Sign-off verdict section updated in this session. Phase 4 moves from "held" to fully approved.
Phases 5 and 6 remain held on B2 and their own conditions, unaffected.

### 2026-10-03, sign-off fix session (worklog-work, B2 — the last blocker)

`worklog-work`'s "Fixing findings" path again, for the one blocker still open: B2, which held
phases 5 and 6 together. B1, B3, B4 and both recommended items were already closed by the two
earlier sign-off fix sessions and were untouched here. Read the Sign-off verdict section as the
authority, then the README, `phase-5-oauth.md` and `phase-6-midrun-auth.md`; filed the work as
"Review 3"/sign-off rounds against both phases, following how phases 3, 4 and 7 recorded theirs.

**Order of work, which mattered.** The fixture was made hostile *first*, before a line of
production code changed, because B2's own diagnosis is that a permissive fixture is why the blocker
shipped green. That ordering produced the red evidence as a by-product rather than as an extra step:
making `oauthtestserver` require `resource` and compare `redirect_uri` failed eleven existing tests
immediately, which is item 1 and item 2 proved red in one run. The remaining four items were probed
individually before being fixed, and every probe's output is quoted in phase 5's implementation
notes. The two worth repeating here: a redirecting token endpoint received the unfixed client's PKCE
verifier (`sinkHits=1 verifierLeaked=true`), and the non-interactive case did not fail but **hung**,
running out a full 60 s bound inside `awaitRedirect` with a loopback listener accepting on a port
nothing would ever connect to — which is precisely the harm the review described a `pkg/agent`
consumer suffering.

**What was built.** One new file, `internal/tools/mcp/mcpauth/trust.go`, holding every trust check
and both redirect policies, plus six new typed errors in `errors.go` and a `Cause`/`Unwrap` pair on
three existing ones so a redirect refusal is recoverable through the stage's own error instead of
being flattened into a string. `resource` threaded through all three token grants and persisted on
`TokenEntry`; `redirect_uri` carried out of `authorizeViaFlow` on a new `authorizationFlow` struct,
with `exchangeCode`'s eight positional parameters collapsed into a `codeExchange` struct in the same
edit rather than growing to ten. `NewHttpConn` gained a `CheckRedirect`. `AuthorizeInteractive`
gained the root interactivity gate. Decisions D50 to D55 record the calls made while fixing,
including the two deliberate non-fixes (hostname-not-port on the metadata check; `serverconfig.go`
still admitting plain http) and the one deliberate non-closure (D54, mid-run re-authorization).

**Gates, all unedited.** `go build ./...`, `go vet ./...`, `gofumpt -l .` (no files listed),
`staticcheck ./...` (no output), `go fix ./...` (no changes), `dupl -t 80 .` (36 clone groups,
matching the baseline — the ~700 new lines of fixture, trust and test code added no group), and
`go test ./... -race -cover -count=3 -timeout=30s` (every package `ok`, host load 2.79 at start,
root package 23.5 s against the 30 s bound). `internal/tools/mcp/mcpauth` coverage rose to 85.8%.

`architecture/` was not touched, per the standing instruction. What it needs is enumerated in this
session's report: the Authorization section currently understates what clai validates (it describes
discovery and the five required fields but names no trust check), the external-specifications list
there has no RFC 8707 row, and the "Not yet implemented" list needs D54.

Status board (rows 5 and 6), external-specifications table, Decisions log (D50–D55), Feedback index
(Sign-off review section, new B2 row and a rewritten lead-in) and the Sign-off verdict section all
updated in this session. **No phase is held on a review finding any more.** Phases 5, 6 and 8 stay
In Progress on their three human-required gates, which were never findings — with one new, uncomfortable
observation recorded in the verdict: B2's missing `redirect_uri` is positive evidence that phase 5's
real-endpoint gate has never run, since a conformant server answers `invalid_grant` to that request.

## Sign-off verdict, 2026-10-03

**Not signed off as one unit. Phases 1, 2, 3 and 7 are approved subject to two fixes. Phases 4, 5
and 6 are held.** — **Superseded by the closure updates below: all four blockers and both
recommended items are now fixed and verified, so no phase is held on a review finding any more.
What remains is the three human-required gates, which were never findings.**

**Closure update, 2026-10-03 (sign-off fix session).** Both conditions for the approved phases are
met: B4 (`Capture` now refuses an empty tools array) and B3 (a warm launcher-only-fingerprinted hit
now prints a setup-time notice) are fixed and verified, filed against phase 3 as B3/B4 under the
Sign-off review entry in the Feedback index below. Both recommended items are also closed: the
shadow marker is demoted to a single footer line (S2, phase 7) rather than cut, and the
`internal/text` testserver build moved into a `TestMain` (S1, phase 3), closing — by correcting,
not by repackaging — the stale "needs a package split" conclusion `phase-8-gate-sweep.md` had
recorded for it. Phases 1, 2, 3 and 7 are therefore fully approved, not merely conditionally.
Phases 4, 5 and 6 remain held: B1, B2 and the held-phase conditions below are untouched by this
session.

**Closure update, 2026-10-03 (sign-off fix session, B1).** Phase 4's one condition is met: B1
(`Call` now resolves as soon as the awaited frame arrives, instead of when the POST response body
ends) is fixed and verified, filed against phase 4 as B1 under the Sign-off review entry in the
Feedback index below, with the fixture gap (a mode that flushes a response frame and then holds
the stream open) closed in the same session. Phase 4 is therefore fully approved, not held.
Phases 5 and 6 remain held: B2 and the held-phase conditions below are untouched by this session.

**Closure update, 2026-10-03 (sign-off fix session, B2).** The last blocker is closed. Every one of
B2's six items is fixed and verified, filed against phase 5 (and, for the mid-run gate, phase 6) as
B2 under the Sign-off review entry in the Feedback index, with decisions D50 to D53 and D55
recording the calls made while fixing. In order: RFC 8707 `resource` is sent on the authorization
request and on all three token grants and is persisted on the token store entry; the
authorization-code grant repeats the authorization request's `redirect_uri`; a new
`internal/tools/mcp/mcpauth/trust.go` validates the issuer (RFC 8414 §3.3), the resource identifier
(RFC 9728 §3.3), the challenge's metadata location against the server's own hostname, and the
scheme of every URL on the chain — the server endpoint, the issuer, all three advertised endpoints,
every discovery redirect hop, and the authorization URL immediately before the browser hand-off;
redirects are refused outright on the registration endpoint, both token grants and the MCP endpoint
itself, and bounded at three re-checked hops on discovery; and `AuthorizeInteractive` refuses a
non-interactive run at the root, so the mid-run `AuthResolver` inherits the gate the setup path got
as R2-08 instead of needing a second copy of it.

The fixture was the root cause and is fixed as such: `oauthtestserver`'s zero configuration now
requires `resource` on both request kinds and compares the exchange's `redirect_uri` against the
issued one, with the single relaxation phrased negatively so the strict posture is the default
(D55). Every fix has a fixture mode that makes its absence fail, and every red state was observed
before the fix rather than reasoned about: the issuer-mismatch and resource-mismatch fixtures both
reached dynamic client registration against the unfixed client (`registrations=1` — the review's own
"clai registers a client with the attacker"); the redirecting token endpoint received the PKCE
verifier (`sinkHits=1 verifierLeaked=true`); the two insecure-URL cases both issued their request
(`roundtrips=1`); the missing `redirect_uri` showed as `exchange redirect_uri seen=""` against an
authorization request that had sent one; and the non-interactive case did not fail at all but
**hung**, running out a full 60 s bound inside `awaitRedirect` with a loopback listener accepting on
a port nothing would ever connect to. The doc comment that presented the unvalidated fetch as a
feature is corrected to say what is actually checked.

Two things are deliberately left rather than fixed, both stated instead of hidden. The
metadata-location check compares hostname, not port, because a resource server may front its
metadata elsewhere on the same host and this repository's own composed fixtures do; the attack the
review composed is cross-host, so it is closed, and a same-host different-port variant is not.
`serverconfig.go` still admits a plain-http endpoint (D51): the refusal is scoped to the credential
clai mints itself, and a LAN server with a static `token_env` credential is a legitimate
configuration whose risk its operator already owns.

**Mid-run token expiry re-authorization is recorded, not closed (D54),** taking the option this
review itself offered. Verified against source first: `bearerDecorator` captures a token *string*
and is installed once on the `HttpConn` at connect time, and `resolveMcpAuthWait` inspects only
`resolver.ResolveForCall` — a connector resolution — never a call result, so a 401 on a later
`tools/call` is folded into a string by `tools.InvokeWith` while a valid refresh token sits unused.
Closing it needs a refreshable decorator seam on `HttpConn` (phase 4's surface), a typed
call-result path the executor can classify (phase 6's), and a re-entry rule for a connection
already handed out: a phase of its own, not a line in a security fix. It is named in both phase
files and in the cross-phase gap record below, and belongs in `architecture/mcp.md`'s "Not yet
implemented" list.

**One finding of this fix session, worth keeping next to the review's own process finding:** B2's
missing `redirect_uri` is positive evidence that phase 5's human-required real-endpoint
confirmation has never run. A conformant authorization server answers `invalid_grant` to that
request, so the flow cannot have completed against one. The gate was outstanding and honestly
recorded as outstanding — but its absence was also the only thing hiding this bug, and a worklog
that treats a human gate as "pending" rather than as "this claim is unverified" will keep producing
this shape of defect.

A final unbounded holistic review — the first pass to read the whole effort at once rather than one
phase at a time — found four blockers that thirteen prior passes missed. The coordinating session
verified each against source before accepting it:

| ID | Blocker | Verified |
| --- | --- | --- |
| B1 | `HttpConn.Call` returns when the response *body ends*, not when its answer arrives: `consumeResponseBody` is called synchronously at `conn_http.go:185` before the `select`. A server that holds its POST event-stream open — which the specification permits, saying SHOULD not MUST — makes every call, `initialize` included, hang to its bound and fail, killing the whole lazy HTTP path at 45 s. `architecture/mcp.md` documents the correct behaviour; no test covers it | Read at `conn_http.go:172-196` |
| B2 | The OAuth client is neither conformant nor safe: `resource` (RFC 8707) is never sent and `ProtectedResourceMetadata.Resource` has no readers; `redirect_uri` is absent from the authorization-code token request although the authorization request sets one, which a conformant server answers `invalid_grant`; and the discovery chain has no trust validation — `doc.Issuer` is never compared to the issuer fetched, `prm.Resource` never to the server, no scheme requirement, and no `CheckRedirect` on either client | `exchangeCode`'s form read directly; `grep '"resource"'` matches only a struct tag |
| B3 | A warm cache hides a dead server. For the dominant `npx`-launched shape only the launcher is fingerprinted, so a server broken by anything other than its launcher changing is invisible at setup for up to twelve hours, and never reported at all if the model does not call it. Probed end to end: `err=<nil>`, zero spawns, tool still advertised | Probe through real `setupMcpManager` |
| B4 | An empty tools array is captured and served as truth for twelve hours. `Capture` has no guard, so a server booting into a transient zero-tool state latches zero tools; neither invalidation signal can fire, because one needs a tool to call and the other needs a connection a cache hit never dials | Probe: capture accepted, lookup hit, `tools=[]` |

The separability that makes the split cheap: `url` and `McpServerAuth` did not exist at `c3867d3`,
so the HTTP transport and the OAuth client are greenfield. Holding them harms no existing user and
benefits none, because nobody is using them yet. The measured win lives entirely in phases 1 to 3.

**Conditions for the approved phases:** guard `Capture` against an empty tools array; restore a
setup-time notice when a warm-cache server's launcher is not fingerprintable, naming the server and
the fact that it was served without a handshake. Also recommended: cut or demote the per-tool shadow
marker, which is wrong for the dominant stdio-proxy shape, and move the testserver build into a
`TestMain` — the review measured that the recorded `internal/text` cold-cache risk is a `go build`
running inside the test clock, not package size, so it is cheaply closable rather than merely
recordable. **All four met, 2026-10-03 (see the Closure update above).**

**Conditions for the held phases:** B1 plus a fixture that holds its stream open; and for OAuth, send
`resource` and `redirect_uri`, validate the issuer and the resource, require `https` for discovery
and for any URL handed to a browser, refuse redirects on the token and registration endpoints, gate
`httpChallengeResolver` on `Interactive`, and make the OAuth fixture hostile so each of those has a
test that can fail. **Phase 4's condition (B1) met, 2026-10-03 (see the Closure update above);
phase 4 is fully approved. Every OAuth condition met too, 2026-10-03 (see the B2 Closure update
above); phases 5 and 6 are no longer held on a review finding, and are In Progress only on their
own human-required gates.**

**Recorded cross-phase gap, owned by nobody until now — now owned, and recorded as not implemented
(D54, 2026-10-03):** mid-run token expiry never re-authorizes.
`bearerDecorator` captures a token string at connect time, and the executor's auth wait inspects only
connector resolution, never a call result — so a valid refresh token sits unused while the model is
told authorization is required. Phase 5 built refresh, phase 6 built surfacing, and the two meet only
at connect time. This belongs in `architecture/mcp.md`'s "Not yet implemented" list.

**Process finding worth keeping.** Thirteen passes audited the implementation against this worklog's
own frame, and all four blockers live in the frame itself: a doc comment presenting an unvalidated
fetch as a feature, a cache contract treating a run-fact as content, a claim that a bug class was
eliminated when the compile had merely moved, and an invariant stated in `architecture/mcp.md` that
no test checks. A future round should be pointed at the invariants rather than at the phases.

## Sign-off, 2026-10-03 — approved

**Signed off. The feature is approved to ship as one unit.** The qualified non-approval recorded in
the verdict section above is superseded: every one of its four blockers is closed, each behind a test
that fails without the fix, and the conditions it set on both the approved and the held phases are
met.

| Blocker | Closed by |
| --- | --- |
| B1 — `Call` returned when the response body ended rather than when its answer arrived, so a server that holds its POST stream open (which the specification permits) hung every call to its bound | The body read moved off the synchronous path, every path through `Call` re-checked for races under `-race`, and a fixture mode added that flushes a frame then holds the stream open — the mode whose absence is why this passed two reviews and a gate sweep |
| B2 — the OAuth client was neither conformant nor safe: no `resource`, no `redirect_uri` on the token request, no issuer or resource validation, no scheme requirement, no redirect refusal, and an ungated interactive flow | All six items fixed, with the fixture made hostile **before** any production change so the red evidence fell out as a by-product. The sharpest: unfixed, the client POSTed its PKCE verifier to a redirecting token endpoint — `sinkHits=1 verifierLeaked=true` |
| B3 — a warm cache hid a dead server for the dominant launcher-launched shape | A setup-time notice gated on the identity, so only the shape that genuinely cannot detect its own breakage is noisy |
| B4 — an empty tools array was captured and served as truth for the full freshness bound | `Capture` refuses an empty array; it is a run-fact, not content |

Gates, run by the coordinating session at host load 1.25, flags unaltered: `gofumpt` clean,
`staticcheck` clean, `go vet` clean, `go build` clean, and
`go test ./... -race -cover -count=3 -timeout=30s` green across every package with zero failures.
Coverage on everything this effort touched clears the floor: `mcpauth` 85.9 percent, `serverconfig`
86.5, `internal/text` 85.3, `schemacache` 82.9, `internal/tools/mcp` 81.9, `internal/tools` 81.6,
`pkg/agent` 94.2.

Three items are approved **as recorded gaps, not as oversights**, each named in
`architecture/mcp.md` so a reader meets them before being surprised by them: mid-run token expiry
does not re-authorize (D54); the interactive credential chain is wired into the lazy endpoint path
and not the eager one (D45); and the metadata-location check compares hostname rather than port, so
a same-host different-port variant is out of its reach.

Three human gates remain and are the maintainer's: a live-endpoint OAuth confirmation, a real mid-run
authorization observation, and the fleet memory comparison. **Run the OAuth one first.** The missing
`redirect_uri` that B2 fixed is positive evidence that gate has never run, since a conformant server
answers `invalid_grant` to a token request without it — which is the sharpest lesson of this effort:
a worklog that records a human gate as "pending" rather than as "this claim is unverified" will keep
producing exactly this shape of defect.
