# Phase 5 — OAuth 2.1 and credential sources

**Status:** In Progress — human gate outstanding. Every finding that reopened the phase across
review 1, review 2 and the holistic sign-off review (B2) is fixed and verified (2026-10-03 fix
sessions); the automated suite is green. The phase cannot be marked Complete because the
human-required real-endpoint confirmation is still outstanding (see Implementation notes and the
Human required section) — that gate is not these fix sessions' to satisfy, and B2's finding that
`redirect_uri` was never sent is direct evidence it has never run.

Back to [README](README.md).

## Goal

Authorize an HTTP MCP connection through discovery, dynamic client registration, PKCE and refresh,
with a credential command and a static bearer as alternative sources, so a remote server's periodic
re-authorization becomes invisible to a run.

## Specification

### Why the full flow rather than a token store

The measurements under Strategy in the README settle this. All three probed authorization servers
expose a registration endpoint, advertise the challenge method named by the PKCE parameter, and list
the refresh grant. A design that stored tokens but could not register would have no client
identifier, forcing a hand-created OAuth application per vendor for nearly the same amount of code.
Decision D6 records the choice.

The whole client is built from the standard library. No OAuth package may be added, per the
dependency invariant in the README.

### Credential sources and their order

Four sources, tried in the order named by the credential-precedence parameter. The first that yields
a usable access token wins. Decision D15 fixes the distinction that makes the rule unambiguous:

- A source is **unset** when its configuration field is absent. An unset source is skipped
  silently and the next is tried.
- A source is **configured** when its field is set. A configured source that fails is a typed error
  and does not fall through, because a misconfigured secret manager must not degrade into an
  interactive browser prompt on a headless host.
- The token store is a **cache**, not a configured source. Its absence, unreadability or corruption
  is a miss and the next source is tried; only a write failure is surfaced.

| Source | Shape | Intended use |
| --- | --- | --- |
| Credential command | A command clai executes, whose trimmed standard output is the access token | A fleet, where the driver holds the refresh token and an agent receives only a short-lived access token. Composes with any external secret manager |
| Environment variable | A per-server variable name, resolved from the process environment or the configured envfile | A server issuing long-lived personal access tokens |
| Token store | A file under the token-store-directory parameter, written with the token-store-mode parameter | An interactive workstation that completed the browser flow once |
| Interactive flow | Discovery, registration, authorization, exchange | First-time authorization on a workstation |

A credential command is executed with the run's context, so cancellation reaps it. Its **standard
output** is the token: it is trimmed, used, and never logged, echoed, or included in an error. Its
**standard error** is captured, bounded, and included in the typed error when the command fails,
because that is the only diagnostic the operator gets; a helper that writes the token to standard
error is defeating its own purpose and clai does not defend against it. It is subject to the same command-ban policy already carried on the tool-call context,
so a banned command cannot be smuggled in as a credential helper.

### What the challenge is, and where the code comes from

The challenge arrives as the `AuthChallengeError` declared in the README shared interfaces, returned
by the transport phase. Its `Challenge` is the verbatim `WWW-Authenticate` header value and its
`ResourceMetadata` is the URL parsed out of that header's `resource_metadata` parameter. This phase
consumes that error and parses nothing out of a response body, and guesses nothing from the endpoint
URL.

Both discovery documents are fetched with `GET`, not `POST`, and their paths and required fields are
the README's external-specifications table: the protected-resource document at
`/.well-known/oauth-protected-resource` and the authorization-server document at
`/.well-known/oauth-authorization-server`. The five fields clai requires from the latter are listed
in that same table, and a document missing any of them is a typed error naming which.

The authorization code in the printed-URL fallback is read from the trusted input reader the
repository already threads through setup; the README's existing-code table names where it is
threaded. It reaches this phase through the connector, which receives it at setup. This phase adds no
new input path and never reads the process standard input directly.

The command-ban policy that governs a credential command is the one the README's existing-code table
names, carried on the tool-call context. This phase does not define a new policy.

The token store's file mode is a POSIX mode. Non-POSIX platforms are out of scope for this worklog,
consistent with the repository's existing async-tooling note being POSIX-only; on such a platform the
store is written with the platform default and the run warns once.

Dynamic registration may or may not issue a client secret. Both cases are supported: the secret is
stored when present and omitted when not, and it is a credential carrier in the redaction table
either way. The token store record, including exactly which fields are written, is the token store
entry in the README's record formats section.

### Schema cache identity extension

The requested scopes change which tools an endpoint exposes, so this phase adds them to the
endpoint-based cache identity owned by the transport phase, and updates that phase's endpoint-cache
tests to carry the new component. Existing entries written before the
extension no longer match and are therefore a miss, which is the correct and harmless outcome: a
miss reconnects and recaptures. No entry is rewritten and no failure is recorded.

### Fixtures introduced

This phase introduces exactly one fixture: the fake OAuth authorization server, as a test package
beside the HTTP MCP fake the transport phase owns. It is configurable per test to omit any required
metadata field, to reject registration, to reject an exchange, to reject a refresh grant, and to
count refresh requests. It also serves the protected-resource metadata document, since that document
is part of the discovery chain rather than part of the MCP server.

This phase introduces **no** protected-resource MCP fake. The transport phase's fake HTTP MCP server
already has a challenge mode and a bearer-token mode, and this phase configures that fixture rather
than duplicating an MCP-over-HTTP server into a second package, which the duplication gate would
rightly flag.

### The interactive flow

On an authorization challenge from phase 4, the client:

1. Reads the protected-resource metadata with a `GET` at the URL the error's `ResourceMetadata`
   carries, and takes from it the authorization servers and the supported scopes.
2. Reads the authorization-server metadata with a `GET` and requires every field the README's
   external-specifications table lists for it. A document missing any of them is a typed error
   naming which field is missing, rather than a partial flow.
3. Registers dynamically, retaining the issued client identifier alongside the tokens.
4. Generates a verifier and its challenge, opens the authorization URL, and listens for the
   redirect on the loopback-host and loopback-port parameters.
5. Exchanges the code with the verifier, and writes the tokens and the client identifier to the
   token store.

When a browser cannot be opened or the loopback listener cannot bind, the flow falls back to
printing the authorization URL and reading the resulting code from the trusted input reader the
repository already threads through setup. This keeps the flow usable over a remote shell, where a
loopback redirect cannot complete.

### Refresh

An access token is refreshed before it expires, by the margin named by the refresh-skew parameter,
so a request is never issued with a token about to expire. Refresh is single-flight per server,
implemented in this phase's own package and keyed by server. It is deliberately **not** the
connector's single flight from the lazy-connect phase, which resolves connections: a connection
already resolved still needs its token refreshed, so the two are separate layers. A run with several
in-flight calls performs one refresh, not one per call. A refresh failure is a
typed authorization error and triggers the interactive flow only when the run is interactive;
otherwise it is returned so the caller sees the absence.

### Redaction

| Carrier | Rule | Test |
| --- | --- | --- |
| Error strings | No access token, refresh token, client secret, verifier or code appears | `TestCredentialNeverAppearsInError` |
| Log and notice lines | Same five secrets never appear | `TestCredentialNeverAppearsInLog` |
| Schema cache entries | No secret is part of the identity or the record | `TestCredentialNeverReachesSchemaCache` |
| Debug output behind the existing debug flags | Redacted the same as normal output | `TestCredentialRedactedInDebugOutput` |
| Credential command output | Captured, used, never echoed | `TestCredentialCommandSuppliesToken` |

### Command surface

A new subcommand authorizes a server interactively and writes its token store entry. It lives in
its own domain package with a `cmd.go` and is injected from the composition root, matching the
placement convention visible in `internal/tools/cmd.go`. It performs no model call and spends
nothing.

### Invariants

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| Challenge handling | Protected-resource metadata is read from the challenge, not guessed | `TestOauthDiscoversProtectedResourceMetadataFromChallenge` |
| Authorization-server metadata | Required fields are checked before any request is issued | `TestOauthDiscoversAuthorizationServerMetadata` |
| Client identity | Obtained by dynamic registration, retained with the tokens | `TestOauthDynamicClientRegistration` |
| Authorization request | Carries a challenge computed with the method named by the PKCE parameter | `TestOauthPkceChallengeIsS256` |
| Code exchange | Stores access token, refresh token and client identifier | `TestOauthAuthorizationCodeExchangeStoresTokens` |
| Token nearing expiry | Refreshed before use, by the refresh-skew parameter | `TestOauthRefreshBeforeSkew` |
| Concurrent calls needing refresh | One refresh for the server, shared, in this phase's own single flight | `TestOauthRefreshIsSingleFlightPerServer` |
| Configured-but-failing source | Typed error, no fall-through to the next source | `TestCredentialSourcePrecedence` |
| Token store file | Written with the token-store-mode parameter | `TestTokenStoreFileModeIsRestrictive` |
| Every carrier in the redaction table | Carries none of the five secrets; the non-secret fields are explicitly permitted | `TestCredentialNeverAppearsInError`, `TestCredentialNeverAppearsInLog`, `TestCredentialNeverReachesSchemaCache`, `TestCredentialRedactedInDebugOutput` |
| The endpoint cache identity | Includes the requested scopes; a scope change is a miss | `TestScopeChangeInvalidatesSchemaCacheEntry` |
| An authorization failure | Nothing written to the schema cache | `TestSchemaCacheNeverPersistsAuthFailure` |

**Added by the sign-off review (B2, 2026-10-03).** The rows above describe a flow; none of them
describes a *trust* property, which is why the whole trust layer shipped unasserted. These do:

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| The authorization request and every token request | Carry RFC 8707 `resource`, naming the server the token is for | `TestOauthSendsResourceOnAuthorizationAndTokenRequests`, `TestOauthRefreshGrantSendsResource` |
| The authorization-code token request | Repeats the authorization request's `redirect_uri` (RFC 6749 section 4.1.3) | `TestOauthTokenRequestRepeatsRedirectUri` |
| An authorization-server metadata document | Must declare the issuer it was fetched for (RFC 8414 section 3.3) | `TestOauthIssuerMismatchIsRefused` |
| A protected-resource document | Must declare a resource identifier covering the server being authorized (RFC 9728 section 3.3) | `TestOauthResourceMismatchIsRefused`, `TestOauthAbsentResourceIdentifierIsRefused` |
| The challenge's `resource_metadata` location | Must be on the MCP server's own hostname | `TestOauthCrossHostResourceMetadataIsRefused` |
| Every URL on the chain, including the one handed to a browser and the server endpoint itself | Must be https, or http on a loopback host | `TestOauthPlainHttpDiscoveryUrlIsRefused`, `TestOauthInsecureAuthorizationEndpointIsNeverOpened`, `TestOauthInsecureTokenEndpointIsRefused`, `TestOauthPlainHttpServerEndpointIsRefused`, `TestRequireSecureURLAcceptsLoopbackAndHttps` |
| The token and registration endpoints | Never follow a redirect, because Go re-sends the POST body | `TestOauthRedirectingTokenEndpointIsRefused`, `TestOauthRedirectingRegistrationEndpointIsRefused`, `TestOauthRedirectingRefreshEndpointIsRefused` |
| A discovery GET's redirects | Bounded, and every hop checked | `TestSecureHopRedirectBoundsAndChecksEveryHop` |
| The MCP endpoint's own connection | Never follows a redirect either | `TestHttpConnRefusesEndpointRedirect` (`internal/tools/mcp`) |
| A run whose output is not a terminal | Never reaches the browser hand-off or the loopback listener at all | `TestOauthNonInteractiveRunIsRefused` |

### Limits

| Limit | Injectable field | README parameter | How a test triggers it |
| --- | --- | --- | --- |
| Refresh margin | Client field | refresh-skew parameter | Injected clock placing expiry inside the margin |
| Loopback listener address | Client field | loopback-host and loopback-port parameters | Fake authorization server redirecting to the bound address |
| Challenge method | Client field | PKCE parameter | Fake authorization server asserting the received method |
| Token store location and mode | Store field | token-store-directory and token-store-mode parameters | Temporary directory injected per test |
| Browser opener | Client field | the browser-opener row in the code-layout table | Fake opener that records the URL, or one that fails, to drive the printed-URL fallback |
| Source order | Client field | credential-precedence parameter | Fixture configuring several sources at once |

The clock is injected and no test sleeps, for the same load-sensitivity reason recorded in phase 3.

### Human required

Every automated test in this phase runs against an in-repo fake authorization server and a fake
resource server on loopback. No test reaches a vendor endpoint and no test spends money, per the
README invariant.

One manual confirmation is required before this phase can be marked complete, because no fake can
prove the real discovery chain:

- **Artifact the person produces:** a recorded confirmation that `clai` completes the interactive
  flow against one live vendor endpoint and that a later run reuses the stored tokens without a
  prompt. The endpoints whose metadata was already probed and recorded in the README are suitable
  candidates.
- **Where the agent stops:** after the automated suite passes, the agent stops and asks the
  maintainer to run the new subcommand against a server of their choice.
- **Before:** the agent ensures the subcommand is wired, the fakes pass, and the token store path is
  printed so the maintainer can inspect its mode.
- **After:** the agent records the outcome in the phase's implementation notes and the README session
  journal, including which endpoint was used and whether the fallback path was exercised. No
  credential is recorded.

### Documentation

`architecture/mcp.md` gains the authorization section: the source order, the flow, the refresh rule,
the redaction table, and the fleet guidance that a credential command keeps secrets out of a
sandbox. The configuration reference note gains the new fields, and the new subcommand is listed
wherever commands are enumerated.

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Call against a fake resource server returning a challenge, no stored token, interactive run | Fake resource server, fake authorization server, fake browser opener, loopback listener | Tool call succeeds after the flow | Registration, authorization, exchange performed; token store written | No credential in any output |
| Same, with the loopback listener unable to bind | Same fakes, listener denied | Flow completes through the printed-URL path | Code read from the trusted input reader | Flow not failed outright |
| Call with a stored token inside its validity | Fake resource server, warmed token store | Tool call succeeds | Token read from the store | No authorization request issued |
| Call with a stored token inside the refresh margin | Fake authorization server, injected clock | Tool call succeeds | One refresh performed | No interactive prompt |
| Several concurrent calls, all with a token inside the refresh margin | Fake authorization server counting refreshes | All calls succeed | Exactly one refresh request observed | No refresh storm |
| Call with a credential command configured | Fake command returning a token | Tool call succeeds | Command executed with the run context | Token not logged; store not consulted |
| Call with a credential command that exits non-zero | Fake command failing | Typed error naming the source | Error returned | No fall-through to another source |
| Call with a credential command that is on the command-ban list | Ban list fixture | Typed error naming the matched ban entry | Command never executed | Command not spawned |
| Call with a static bearer from the environment | Environment fixture | Tool call succeeds | Header carried | No authorization request issued |
| Refresh rejected by the authorization server, non-interactive run | Fake authorization server rejecting the grant | Typed authorization error returned to the caller | Error surfaced | No interactive prompt on a headless run |
| Authorization-server metadata missing the registration endpoint | Fake authorization server with reduced metadata | Typed error naming the missing capability | Error returned before any registration attempt | No partial flow |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| The challenge drives discovery | `TestOauthDiscoversProtectedResourceMetadataFromChallenge` |
| Authorization-server metadata is validated before use | `TestOauthDiscoversAuthorizationServerMetadata` |
| The client registers dynamically | `TestOauthDynamicClientRegistration` |
| The authorization request carries the required challenge method | `TestOauthPkceChallengeIsS256` |
| Exchange stores tokens and the client identifier | `TestOauthAuthorizationCodeExchangeStoresTokens` |
| A token inside the refresh margin is refreshed once | `TestOauthRefreshBeforeSkew` |
| Concurrent calls needing refresh produce exactly one refresh request | `TestOauthRefreshIsSingleFlightPerServer` |
| A challenge without resource metadata is a typed error | `TestOauthChallengeWithoutResourceMetadataIsTypedError` |
| Metadata missing a required field names which one | `TestOauthMetadataMissingRequiredFieldIsTypedError` |
| A path-bearing issuer resolves through the inserted form, then the appended form | `TestOauthMetadataUrlCompositionFallsBackToAppendedForm` |
| A refresh failure is a typed error, not a prompt on a headless run | `TestOauthRefreshFailureReturnsTypedAuthError` |
| Sources are tried in the configured order, with no silent fall-through | `TestCredentialSourcePrecedence` |
| A credential command supplies a token | `TestCredentialCommandSuppliesToken` |
| A failing credential command is a typed error | `TestCredentialCommandFailureIsTypedError` |
| A static bearer works from the environment and from an envfile | `TestStaticBearerFromEnvAndEnvfile` |
| The token store is written with a restrictive mode | `TestTokenStoreFileModeIsRestrictive` |
| No secret reaches any carrier in the redaction table | `TestCredentialNeverAppearsInError`, `TestCredentialNeverAppearsInLog`, `TestCredentialNeverReachesSchemaCache`, `TestCredentialRedactedInDebugOutput` |
| The subcommand writes a usable token store entry | `TestMcpAuthCommandWritesTokenStore` |
| An unbindable loopback listener falls back to the printed-URL path | `TestLoopbackRedirectFallsBackToPasteFlow` |
| A scope change invalidates the endpoint cache entry | `TestScopeChangeInvalidatesSchemaCacheEntry` |
| An authorization failure is never written to the schema cache | `TestSchemaCacheNeverPersistsAuthFailure` |
| The real discovery chain completes against a live endpoint | Human-required confirmation recorded in implementation notes |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Challenge carries no resource metadata location | Typed error naming the server and the missing metadata | `TestOauthChallengeWithoutResourceMetadataIsTypedError` |
| Resource metadata unreachable or unparseable | Typed error naming the stage | `TestOauthResourceMetadataFailureIsTypedError` |
| Authorization-server metadata lacks any of the five required fields | Typed error naming which field is missing | `TestOauthMetadataMissingRequiredFieldIsTypedError` |
| Issuer has a path component and its metadata is absent at the inserted URL | The appended form is tried; if that is absent too, a typed error naming both candidates | `TestOauthMetadataUrlCompositionFallsBackToAppendedForm` |
| Registration rejected | Typed error carrying the server's reason | `TestOauthRegistrationRejectionIsTypedError` |
| Browser cannot be opened | Printed-URL path used | `TestLoopbackRedirectFallsBackToPasteFlow` |
| Loopback listener cannot bind | Printed-URL path used | `TestLoopbackRedirectFallsBackToPasteFlow` |
| Redirect carries an error instead of a code | Typed error carrying the server's reason | `TestOauthRedirectErrorIsTypedError` |
| Code exchange rejected | Typed error, nothing written to the store | `TestOauthExchangeRejectionWritesNothing` |
| Refresh grant rejected | Typed authorization error; interactive run may re-authorize, headless run returns | `TestOauthRefreshFailureReturnsTypedAuthError` |
| Token store unreadable | Cache miss, next source tried | `TestTokenStoreUnreadableIsCacheMiss` |
| Token store unwritable | Typed error naming the path; the run continues with the token it already holds | `TestTokenStoreUnwritableIsTypedError` |
| Token store entry corrupt | Cache miss, next source tried | `TestTokenStoreCorruptEntryIsCacheMiss` |
| Credential command not found or non-zero exit | Typed error naming the source and the command | `TestCredentialCommandFailureIsTypedError` |
| Credential command returns empty output | Typed error naming the source | `TestCredentialCommandEmptyOutputIsTypedError` |
| Credential command matches the command-ban list | Typed error naming the matched entry, command never spawned | `TestCredentialCommandBannedIsNeverSpawned` |
| `auth.token_env` field itself absent (no `auth` block, or an `auth` block with every source unset) | Source is unset, so it is skipped and the next is tried | `TestUnsetCredentialSourceIsSkipped` |
| `auth.token_env` configured but the named variable absent from both the process environment and the envfile | D15 governs: the source is configured, so this is a typed error naming the source, with no fall-through — **corrected by R1-18**, replacing the row's earlier, incorrect "skipped" reading of this case | `TestConfiguredEnvVarAbsentIsTypedError` |
| Any authorization failure during setup or a call | Typed authorization error; the schema cache is left untouched | `TestSchemaCacheNeverPersistsAuthFailure` |
| Challenge points `resource_metadata` at another host | `*UntrustedMetadataHostError`, no request issued | `TestOauthCrossHostResourceMetadataIsRefused` |
| Any chain URL is plain http off loopback | `*InsecureURLError` naming the stage, no request issued and no browser opened | `TestOauthPlainHttpDiscoveryUrlIsRefused`, `TestOauthInsecureAuthorizationEndpointIsNeverOpened`, `TestOauthInsecureTokenEndpointIsRefused`, `TestOauthPlainHttpServerEndpointIsRefused` |
| Authorization-server metadata declares a different issuer | `*IssuerMismatchError`, no client registered | `TestOauthIssuerMismatchIsRefused` |
| Protected-resource metadata declares a different resource, or none | `*ResourceMismatchError`, no client registered, nothing stored | `TestOauthResourceMismatchIsRefused`, `TestOauthAbsentResourceIdentifierIsRefused` |
| Token or registration endpoint answers with a redirect | `*RedirectRefusedError` wrapped in the stage's own typed error; the target is never reached | `TestOauthRedirectingTokenEndpointIsRefused`, `TestOauthRedirectingRegistrationEndpointIsRefused`, `TestOauthRedirectingRefreshEndpointIsRefused` |
| A non-interactive run reaches the interactive flow | `*InteractiveAuthDisabledError` naming the subcommand to run from a terminal; no browser, no listener, no request | `TestOauthNonInteractiveRunIsRefused` (the mid-run caller's own proof is phase 6's row, so the name is declared there) |

## Implementation notes

Phase 5 implemented 2026-10-02. `internal/tools/mcp/mcpauth` lands as specified: discovery
(`discoverProtectedResource`, `discoverAuthorizationServer`, the RFC 8414 inserted-then-appended
well-known composition), dynamic registration, PKCE (`newPKCE`, `newState`), the interactive flow
(loopback listener plus printed-URL fallback, both driving through `AuthorizeInteractive`), the
authorization-code exchange, single-flight refresh, the credential-precedence chain
(`ResolveCached`/`ResolveStaticCredential`), and the token store (`store.go`). All thirty-four
declared test names exist and pass (verified by name against the phase file and the repository's
existing tests — no collisions). `internal/tools/mcp/oauthtestserver` is the one new fixture the
phase owns; it also serves the protected-resource document, exactly as specified, and the
transport phase's `httptestserver` is configured for the challenge and bearer-token roles with no
changes to its behaviour beyond a new `FixedAccessToken` config field (below). The fake browser
opener (`AutoFollowOpener`, `FailingOpener`) also lives there, per the code-layout table's explicit
"the fake in internal/tools/mcp/oauthtestserver" — it was initially duplicated across the three
test files that needed it before being consolidated into `oauthtestserver/browser.go` during this
same session, the table's own wording being the thing that caught the duplication.

Deviations and additions beyond the explicit code-layout table, each with its reason:

- **`internal/text/mcp_oauth.go`** (new file) wires `mcpauth` into the two existing setup paths
  phases 3–4 own (`querier_setup_tools.go`'s eager HTTP branch, `mcp_http_schema_cache.go`'s lazy
  cache-hit and cache-miss branches). The table had no row for this glue; one is added below rather
  than treating its absence as a blocking gap, following the precedent phases 2–4's executors set
  for symbols discovered necessary during implementation (e.g. phase 3's `mcp.Handshake`/`RegisterTools`
  extraction).
- **`mcp.NewConnectorFromDial` / `mcp.DialFunc` / `mcp.WithHttpRequestDecorator`** (new exports in
  `internal/tools/mcp/connector.go` and `conn_http.go`; `dialHttp` gained a `decorate RequestDecorator`
  parameter). The authorization phase needed a way to wrap a dial with its own credential resolution
  and interactive fallback while still reusing the package's existing single-flight memoisation,
  which `newConnector` already provides but did not expose for an externally-supplied `dialFunc`.
- **`mcp.LoadEnvFile`** (renamed up from the already-existing private `loadEnvFile`). The
  `auth.token_env` source's envfile fallback needs the same parser `conn_stdio.go` already uses for
  spawning a process; duplicating it would have been flagged by the duplication gate.
- **`pkgtools.ValidateCmdNotBanned`** (new exported wrapper around the existing private
  `validateCmdNotBannedWithContext` in `pkg/tools/cmd_ban.go`). The credential command source must
  enforce the command-ban policy from outside `pkg/tools`, which had no exported check before this
  phase.
- **`schemacache.BuildIdentityWithScopes`** adds the scopes component additively; `BuildIdentity`'s
  existing signature and behaviour are unchanged, so phase 3's and phase 4's own call sites and
  tests needed no further change beyond the two assertions this phase updated to use the new
  constructor (`internal/text/mcp_http_setup_test.go`), per the phase's own instruction.
- **`internal/text/conf.go`** gained `Configurations.TrustInput io.Reader`, and `SetupQuerier` now
  assigns it from its existing `trustInput` parameter. This is the "reaches this phase through the
  connector, which receives it at setup" path the spec names; no new input path was added.
- **`oauthtestserver.Config.FixedAccessToken`** (new fixture field, this phase's own fixture) lets a
  resource-server integration test pre-configure `httptestserver.RequireBearerToken` to the exact
  token the authorization flow is about to issue, since the production exchange otherwise mints a
  random one. Used only by the integration tests proving the real wiring boundary
  (`TestSetupAuthorizesHttpServerAfterChallenge`, `TestMcpAuthCommandWritesTokenStore`); it changes
  no default behaviour.
- **`mcpauth.EnvFileLoader`** (an injected function type, not a direct import of `mcp.LoadEnvFile`)
  exists because `internal/tools/mcp` imports `internal/tools/mcp/mcpauth` for its own `cmd.go`
  subcommand, and the reverse import `mcpauth` → `mcp` would have cycled. Production callers
  (`internal/text`'s `newMcpAuthorizer`, `internal/tools/mcp/cmd.go`'s `RunAuth`) pass `mcp.LoadEnvFile`
  explicitly via `mcpauth.WithEnvFileLoader`.
- **Eager HTTP servers get only the static half of the credential-precedence chain**
  (`auth.token_command`, `auth.token_env` — `staticHttpDecorator` in `mcp_oauth.go`), not the token
  store or the interactive fallback. The full chain is wired into the lazy path only
  (`dialHttpServerWithAuth`/`handshakeHttpServerWithAuth`/`newAuthenticatingHttpConnector`), because
  D16/D18 already make `lazy` the default for an ambient endpoint-based server — the common case —
  and because the eager path runs through `mcp.Manager`'s `ControlEvent`/channel machinery, which has
  no retry-on-challenge seam without reopening phases 1–2. The gap this leaves: an explicit,
  strict-startup HTTP server configured `"startup":"eager"` that needs the interactive flow is not
  wired; its challenge surfaces as a plain typed connect failure through the existing strict-startup
  path (`claierr.AuthChallengeError`, now wrapped into `claierr.NewMcpServerStartup` by
  `failureCollector.addExplicit`'s funnel, R2-05's fix, so it reaches `StrictMcpStartup` the same as
  any other explicit failure), never silently swallowed. Flagged here rather than fixed silently,
  since closing it properly belongs with phases 1–2's own machinery. Promoted to README decision
  **D45** (2026-10-03 fix session), per this round's instruction that a cross-phase ruling accepted
  "as declared" belongs in the Decisions log, not only in a code comment.
- **The endpoint cache identity's scopes component is the static `auth.scopes` config value**, not
  the scopes actually negotiated during a live OAuth flow. The negotiated scopes are only known
  after a connection succeeds, while the cache lookup that decides whether to connect at all happens
  first — using the live, post-flow value would be circular. `auth.scopes`'s own documented default
  ("whatever the server advertises") still governs the authorization *request* itself
  (`requestedScopes` in `mcpauth.go`), just not the cache key.

Two real, load-exposed issues were caught and fixed during a `go test ./... -race -cover -count=3
-timeout=30s` run, both consistent with the repository's documented host-load sensitivity but
differing in kind:

1. The root `github.com/baalimago/clai` package timed out once at 30s during the full run (host
   load observed in the 4–6 range across cores); it was re-run alone immediately afterward and
   passed in 9.2s. This package is untouched by this phase; the same class of transient failure is
   recorded by phases 1, 2 and 4's own execution notes.
2. `TestOauthRefreshIsSingleFlightPerServer`, as first written, launched eight goroutines and
   trusted the Go scheduler to interleave them ahead of a real (if fast, loopback) HTTP round trip —
   this is a genuine test-design flaw, not host noise, and it surfaced as `refresh count = 2` once
   under load. Fixed by adding two unexported, test-only fields to `Authorizer`
   (`testJoined func()`, `testBarrier chan struct{}`) that let the test force every caller to join
   `refresh()`'s locked winner-or-follower check before the winner is released to do any real work —
   deterministic regardless of scheduler timing or host load. Re-run clean ten times under `-race`
   after the fix. No production code path reads either field outside this one test.

Verification commands actually run, each clean:

- `go build ./...`
- `go vet ./...`
- `go run mvdan.cc/gofumpt@latest -l .` (no output)
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` (no output)
- `go fix ./...` (applied two pre-existing-style modernisations inside this phase's own new files —
  a manual contains loop to `slices.Contains` in `discovery.go`, and `for i := 0; i < n; i++` to
  `for range n` in a test loop — no behavioural change, confirmed by re-running the affected tests)
- `go run github.com/mibk/dupl@latest -t 80 .` — 34 pre-existing clone groups, none touching any
  file this phase added or edited
- `go test ./... -race -cover -count=3 -timeout=30s` — clean on the second full run (first run's
  two failures addressed above); per-package coverage for this phase's own new code:
  `internal/tools/mcp/mcpauth` 83.7%, `internal/tools/mcp/schemacache` 85.6% (was already above
  floor; the new `BuildIdentityWithScopes`/redaction tests added coverage here too),
  `internal/tools/mcp` 81.0% (includes the new `cmd.go`), `internal/text` 82.8%. All clear the
  coverage-floor parameter's 70% must; `mcpauth` falls short of the 90% preferred figure — the
  uncovered remainder is almost entirely `Error()`/`Unwrap()` boilerplate already exercised once by
  `TestErrorMessagesNameTheirServer` plus `browser.go`'s real `exec.Command` branches (os-specific,
  shells to a real browser binary a CI host has none of; its failure path, which is the one that
  matters, is exercised through `TestLoopbackRedirectFallsBackToPasteFlow`).
- Manual smoke test of the real binary: `clai mcp -h`, `clai mcp auth -h`, and
  `CLAI_CONFIG_DIR=/tmp/... clai mcp auth nonexistent-server` (confirms argument parsing and the
  typed "read server config" error reach the CLI layer correctly, exit code 0 with the error printed
  via the standard command-error path).

`architecture/` was not touched, per this session's explicit instruction that the coordinating
session owns all architecture documentation for this worklog; the phase's own Documentation section
(the authorization section of `architecture/mcp.md`, the configuration reference, the subcommand
listing) is therefore outstanding and is not claimed as done here.

**Human required gate — outstanding, blocking `Complete`.** The automated suite is green, the
subcommand is wired (`clai mcp auth <server>`), and a successful run prints the token store's path
and mode for inspection (confirmed in `TestMcpAuthCommandWritesTokenStore`'s captured output:
`Authorized "httpecho". Token stored at .../mcpAuth/<hash>.json (mode 0600).`). No fake was
substituted for the real-endpoint confirmation and none of the probed vendor endpoints (Linear,
Notion, Intercom, Sentry) were contacted by this session. The maintainer needs to run
`clai mcp auth <a real server of their choice>` against one of them (or any other OAuth-protected
MCP server), confirm the interactive flow completes, confirm a second run reuses the stored token
without a prompt, and report which endpoint was used and whether the printed-URL fallback was
exercised, back into this file and the README session journal. No credential should be recorded.

### Implementation notes delta — 2026-10-03 fix session

Fix session for the `worklog-work` skill's "Fixing findings" workflow, run by a Claude Code agent
(Sonnet 5) against the working tree left by implementation reviews 1 and 2.

**Findings closed, and how** (full detail and corrective action under each finding below; this is
the summary):

- **R1-04** — `internal/tools/mcp/cmd.go`'s `Command()` now assigns `c.OnRun`. New test:
  `Test_e2e_mcp_bare_invocation_does_not_panic` (root package); help row added to
  `Test_e2e_command_help`.
- **R1-05** — `setupTooling` (`internal/text/querier_setup_tools.go`) now threads
  `pkgtools.WithCmdBanContext(ctx, userConf.CmdBan)` into the one `ctx` `setupMcpManager` receives.
  New tests: `pkg/agent/mcp_setup_test.go`'s `Test_AgentSetup_CmdBanAppliesToMcpCredentialCommand`
  (explicit/eager) and `Test_AgentSetup_CmdBanAppliesToAmbientLazyHttpCredentialCommand`
  (ambient/lazy).
- **R1-09** — `mcpauth.AuthorizeInteractive`, `doRefresh` and `ResolveCached` now return a usable
  decorator/entry alongside a `*TokenStoreWriteError` instead of discarding it; the three call
  sites in `internal/text/mcp_oauth.go` tolerate that specific case. New test:
  `TestSetupToolCallSucceedsDespiteUnwritableTokenStore` (`internal/text`).
- **R1-10** — the real, provable leak was `querier_setup_tools.go`'s DEBUG dump
  (`debug.IndentedJsonFmt(mcpServers)` serialised `args`/`env` verbatim); fixed with a new
  `redactMcpServersForDebug`/`debugRedactedMcpServer` view. New tests:
  `TestDebugDumpNeverLeaksArgsOrEnvSecret` (`internal/text`),
  `TestSecretsNeverLeakIntoUncoveredErrorTypes` (6 subtests, `internal/tools/mcp/mcpauth`),
  `TestCredentialCommandOutputNeverEchoed` (`mcpauth`), `TestMcpHttpStatusErrorNeverLeaksAccessToken`
  (`internal/text`).
- **R1-11** — `prepareAuthorization` returns its minted `state`; the loopback branch of
  `authorizeViaFlow` compares it to the redirect's and returns `*RedirectError` on mismatch; the
  loopback handler now 404s any path but `/callback`. New tests:
  `TestOauthLoopbackStateMismatchIsTypedError`, `TestLoopbackHandlerIgnoresOtherPaths` (`mcpauth`).
- **R1-14 (HTTP half)** — `handshakeHttpServerWithAuth`'s new `runBoundedHandshake` applies
  `mcp.ConnectBoundOf`/`mcp.ReportAsConnectStage`, mirroring the stdio fix exactly. New test:
  `TestLazyHttpCacheMissConnectBoundAppliesToHandshake` (`internal/text`).
- **R1-18** — the code and its tests already matched D15 from an earlier session this review round
  predates; only the phase file's own Error coverage row was wrong. Row split into two, below.
- **R1-19** — `loadNamedServer` now calls `serverconfig.FindConfiguredServers` instead of its own
  `json.Unmarshal`. New tests: `TestLoadNamedServerRejectsBothCommandAndUrl`,
  `TestLoadNamedServerExpandsEnvfileRelativeToConfigDir` (`internal/tools/mcp`).
- **R1-24** — seven new typed errors in a new file, `internal/tools/mcp/cmd_errors.go`
  (`McpAuthUsageError`, `McpAuthConfigDirError`, `McpAuthServerConfigError`,
  `McpAuthNotEndpointBasedError`, `McpAuthNoChallengeError`, `McpAuthConnectError`,
  `McpAuthFlowError`), replacing every bare `fmt.Errorf` in `cmd.go`. The existing test now asserts
  the type; two new tests cover the two previously-untested branches
  (`TestMcpAuthCommandNonEndpointServerIsTypedError`, `TestMcpAuthCommandNoChallengeIsTypedError`).
- **R1-29** — all three leaked connections are now `Close`d. Verified by
  `TestMcpAuthCommandNoChallengeIsTypedError` running the fixture twice.
- **R1-30 (phase 5's share)** — audited; no real timing-bound sleep exists in phase 5's own files
  (the one `time.Sleep` is a liveness poll with no elapsed-time assertion). The actual violation is
  phase 6's `tool_executor_auth_test.go` (confirmed by R2-26's identical citation) — not fixed here,
  correctly out of scope.
- **R1-32** — `loadEnvFile` renamed to `LoadEnvFile`; the one-line wrapper deleted;
  `conn_stdio.go`'s call site updated.
- **R2-05** — `failureCollector.addExplicit` (the renamed `classifyServerFailure` funnel) now wraps
  any explicit failure not already carrying `claierr.ErrMcpServerStartup`. New test:
  `Test_AgentSetup_ExplicitMcpCredentialFailure_FailsSetup` (`pkg/agent`).
- **R2-08** — `newMcpAuthorizer` now takes `outputIsTerminal bool` (`userConf.OutputIsTerminal`,
  the same signal D22's own bound uses) and sets `WithInteractive`; the setup-path flow
  (`handshakeHttpServerWithAuth`) branches on `authz.Interactive` before attempting it at all, and
  bounds the attempt by the resolved auth-timeout when it proceeds; `WithPrintURL` now routes
  through a new `AuthPrintWriter` capability on the sink instead of the Authorizer's raw-stdout
  default. New tests: `TestSetupNonInteractiveSkipsAuthorizeInteractive` (`internal/text`, verified
  red-before-green — removing the `Interactive` gate alone reproduces a real loopback-redirect
  timeout), `TestAuthPrintURLRoutesThroughSinkNotStdout`,
  `TestAuthPrintURLFallsBackToDefaultForAPlainSink`.
- **R2-22 (phase 5's share)** — resolved as a side effect of R2-08: both options now have a real
  production setter whose value is read.
- **Eager/lazy credential asymmetry** — promoted from an implementation-note comment to decision
  **D45** in the README.

**Findings left open, and why:** none, among those assigned to phase 5. The phase's `Human
required` real-endpoint gate remains legitimately outstanding; it is not a finding and this
session did not attempt to satisfy it.

**Architecture note made stale:** none beyond what phase 5's own prior implementation note already
disclosed (the authorization section of `architecture/mcp.md`, the configuration reference, and the
subcommand listing remain outstanding). `architecture/` was not touched by this session, per the
standing instruction that the coordinating session owns it.

**Verification commands actually run, each clean:**

- `go build ./...`
- `go vet ./...`
- `go run mvdan.cc/gofumpt@latest -l .` (no output)
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` (no output)
- `go fix ./...` (no changes)
- `go run github.com/mibk/dupl@latest -t 80 .` — 35 clone groups (34 pre-existing per phase 8's own
  gate-sweep record, plus the one test-helper duplicate between
  `internal/text/querier_setup_tools_test.go` and `pkg/agent/mcp_setup_test.go` that phase 8 already
  found, judged and accepted in writing — its line numbers simply moved when this session added an
  import to the latter file. No new duplication introduced.
- `go test ./... -race -count=1 -timeout=30s` — clean, run repeatedly between individual fixes.
- `go test ./... -race -cover -count=3 -timeout=30s` — clean on the full suite, host load ~3
  throughout (well under the documented load-8 race-gate threshold). Per-package coverage for this
  phase's own code: `internal/tools/mcp/mcpauth` 84.2%, `internal/text` 84.9%,
  `internal/tools/mcp` 81.3%, `pkg/agent` 94.2% — all clear the 70% floor.

### Sign-off fix session, 2026-10-03 (worklog-work, B2)

A later, unbounded holistic review read the whole effort at once and judged this phase's
authorization client "an OAuth-shaped object, not an OAuth client": the PKCE and `state` halves are
genuinely correct and genuinely tested, the token store is right, and **everything else on the
trust layer had never been asserted by anything**, which is exactly why it shipped green through two
review rounds and a gate sweep. Full text in the README's Sign-off verdict section and the Sign-off
review entry under the Feedback index.

The fixture is the root cause, not a contributing factor. `oauthtestserver` was permissive at every
one of the points below — it recorded `issuedCode.redirectURI` and never compared it, declared a
`resource` nothing had to send, and would redirect nowhere — so a client that did none of these
things passed. Each fix below is paired with a fixture mode that makes its absence fail.

**B2.1 — RFC 8707 `resource` was never sent.** It is a MUST on the authorization request and on
every token request in the MCP authorization specification, and it is the mechanism that stops a
token minted for one MCP server being replayed against another behind the same authorization
server. `ProtectedResourceMetadata.Resource` was parsed and had zero readers in the tree.

*Fixed:* `prepareAuthorization` sets `resource`, `exchangeCode` sets it on the authorization-code
grant, and `doRefresh` sets it on the refresh grant. The value is the protected-resource document's
own (now validated) identifier; `TokenEntry` gained a `Resource` field so a refresh names the same
one without re-walking discovery. An entry written before this fix carries none, and
`refreshResource` then falls back to the server's own endpoint, which the MCP specification makes
its canonical resource identifier.

*Red before green:* the fixture now requires `resource` on both, and the unfixed client failed the
entire existing suite — eleven tests, `invalid_target` on the authorization request and
`resource parameter absent or not this resource` on the refresh grant.

**B2.2 — `redirect_uri` was missing from the authorization-code token request.** RFC 6749 section
4.1.3 requires it whenever the authorization request carried one, which `prepareAuthorization`
always does. A conformant server answers `invalid_grant`. **This is direct evidence the interactive
flow has never completed against a real authorization server**, and it is recorded as such against
the Human required gate below.

*Fixed:* `authorizeViaFlow` now returns an `authorizationFlow` struct carrying the `redirectURI`
the loopback-or-paste decision settled on (the two paths use different ones), and `exchangeCode`
repeats it. `exchangeCode`'s eight positional parameters became one `codeExchange` struct in the
same edit rather than growing to ten.

*Red before green:* the fixture now compares the token request's `redirect_uri` against the one the
code was issued against. Isolated from B2.1 by relaxing the resource requirement, the unfixed
client produced `exchange redirect_uri seen="" authorize redirect_uri="http://127.0.0.1:45173/callback"`.

**B2.3 — the discovery chain had no trust validation at all.** Four distinct gaps, and the doc
comment on `discoverProtectedResource` presented the absence of validation as a feature —
*"Nothing about the fetch is guessed: the URL comes from the challenge, verbatim"* — which is the
sentence that let it through two reviews.

*Fixed*, in a new `trust.go` whose only job is this:

- `requireIssuerMatch` (RFC 8414 section 3.3) compares `doc.Issuer` to the issuer whose well-known
  URL was fetched, tolerating only a trailing-slash difference. `AuthorizationServerMetadata.Issuer`
  had zero readers before this.
- `requireResourceCovers` (RFC 9728 section 3.3) compares the document's `resource` to the server
  being authorized: same scheme, same host and port after default-port normalisation, and the
  server's path at or under the resource's. An absent `resource` is a refusal, since RFC 9728
  requires the field.
- `requireMetadataFromServerHost` refuses a `resource_metadata` location that is not on the MCP
  server's own hostname. `conn_http.go` lifts that location out of the server's own
  `WWW-Authenticate` with no host check, which is fine — the check belongs where `server.Url` is
  known. **Known limit, stated rather than hidden:** the hostname is compared, not the port, because
  a resource server may front its metadata elsewhere on the same host and this repository's own
  composed fixtures do exactly that (the MCP fake and the authorization fake are separate loopback
  servers). The attack the review composed is cross-*host*, so this closes it; a same-host,
  different-port variant is not refused.
- `requireSecureURL` requires https, or http on a loopback host, and runs on the server endpoint,
  the `resource_metadata` location, the issuer, all three advertised endpoints, every discovery
  redirect hop, and the final authorization URL immediately before it is handed to `xdg-open`. The
  loopback exception is what keeps a local MCP server usable. `fetchJSON` enforces it as the funnel.
  `serverconfig.go` still admits a plain-http endpoint deliberately: a LAN server with a static
  `token_env` credential is a legitimate configuration, and the refusal is scoped to the credential
  clai mints itself, where the token would be clai's own to leak.

The misleading doc comment is rewritten to say what is actually checked.

*Red before green:* the issuer-mismatch and resource-mismatch fixtures both reached dynamic client
registration against the unfixed client (observed: `registrations=1` on each), which is the
review's own scenario — clai registering a client with the attacker. The two insecure-URL probes
both issued the request (`roundtrips=1`).

**B2.4 — no `CheckRedirect` on either `http.Client`.** Go re-sends a POST body across a 307 or 308
and only ever strips the `Authorization` header cross-domain, never the body.

*Fixed:* `NewAuthorizer` derives two clients from whatever `WithHTTPClient` installed, copying it
rather than mutating a client the caller owns: a discovery client that may follow up to
`maxDiscoveryRedirects` hops, each one re-checked by `requireSecureURL` (a real issuer may
legitimately redirect a well-known URL for trailing-slash normalisation), and a strict client that
refuses outright, used by `registerDynamicClient` and both token grants. `NewHttpConn`'s own client
gained `refuseEndpointRedirect`: the MCP endpoint is a fixed property of the server config, so there
is no case for following a redirect away from it. `RegistrationRejectedError`,
`ExchangeRejectedError` and `RefreshError` gained a `Cause` and `Unwrap` so the refusal is
recoverable with `errors.As` through the stage's own typed error instead of being flattened into a
string.

*Red before green, and this is the sharpest proof in the set:* the fixture's redirecting token
endpoint, against the unfixed client, delivered the PKCE verifier to the redirect target —
`sinkHits=1 verifierLeaked=true`. The same proof for the transport: removing
`refuseEndpointRedirect` alone makes `TestHttpConnRefusesEndpointRedirect` fail with the request
having reached the target.

**B2.5 — `httpChallengeResolver` was not gated on `authz.Interactive`.** The setup path was fixed
for precisely this (R2-08) and the mid-run path was not, so a headless `pkg/agent` consumer that
merely set `auth_timeout_seconds` opened a browser and bound a loopback listener inside its own
process.

*Fixed at the root rather than at that one call site:* `AuthorizeInteractive` itself refuses with
`*InteractiveAuthDisabledError` when `!a.Interactive`, before the challenge is even read, so every
caller present and future inherits the gate. `httpChallengeResolver` deliberately still returns a
non-nil resolver, because `resolveMcpAuthWait` reads `authResolver != nil` to choose between the
endpoint-based actionable result ("run: clai mcp auth <server>", which is the correct advice for a
headless run) and the command-based wording; returning nil would have made the message wrong and
burned the full `auth_timeout` waiting for nothing. R2-08's own setup-path branch is now redundant
defence and is left in place.

*Red before green:* against the unfixed code this case does not fail, it **hangs** — the probe ran
out the full 60 s test bound inside `awaitRedirect`, with the goroutine stack showing the loopback
listener accepting on a port nothing was ever going to connect to.

**B2.6 — the fixture is now hostile by default.** `oauthtestserver`'s zero configuration requires
`resource` on the authorization request and on every token request, and compares the
authorization-code grant's `redirect_uri` against the issued one. Added configuration: `Resource`,
`OmitResource`, `IssuerOverride`, `RegistrationEndpointOverride`, `AuthorizationEndpointOverride`,
`TokenEndpointOverride`, `RegistrationRedirectTo`, `TokenRedirectTo` and the single relaxation
`AllowMissingResourceParam` (negative, so the zero value stays hostile). Added observability:
`ResourceID`, `LastAuthorizeResource`, `LastAuthorizeRedirectURI`, `LastTokenResource`,
`LastExchangeRedirectURI`, `RegistrationCount` and `RequestCount` — the last two are what let a
test assert that a refusal happened *before* the chain was walked, which a fixture cannot otherwise
show, since being refused means it is never reached. `httptestserver` gained `RedirectPostTo`.

The composed-chain call sites in `internal/text` and `internal/tools/mcp` now declare
`c.Resource = hs.URL`, because the resource server in that composition is the MCP fake, not the
authorization fake.

**Judged out of scope, with reasoning — mid-run token expiry never re-authorizes.** The review
recorded this as a cross-phase gap and offered the option of recording rather than closing it. It is
recorded, and it is not closed here. Verified against source: `bearerDecorator` captures a token
*string* and is installed once on the `HttpConn` at connect time, and
`toolExecutor.resolveMcpAuthWait` only ever inspects `resolver.ResolveForCall` — a connector
resolution — never a call result, so a 401 on a later `tools/call` is folded into a string by
`tools.InvokeWith` and the model is told authorization is required while a valid refresh token sits
unused. Closing it properly needs three changes this session has no mandate for: a refreshable
decorator seam on `HttpConn` (phase 4's surface), a typed call-result path the executor can
classify (phase 6's), and a re-entry rule for a connection already handed out. That is a phase of
its own, not a line in a security fix. It is named in the README's cross-phase gap record and
belongs in `architecture/mcp.md`'s "Not yet implemented" list, which the coordinating session owns.

**Verification commands, run at the end of this session:**

```
go build ./...                                                               # clean
go vet ./...                                                                 # clean
go run mvdan.cc/gofumpt@latest -l .                                          # no files listed
go run honnef.co/go/tools/cmd/staticcheck@latest ./...                       # clean
go fix ./...                                                                 # no changes
go run github.com/mibk/dupl@latest -t 80 .                                   # 36 clone groups, matching the baseline
go test ./... -race -cover -count=3 -timeout=30s                             # clean, host load ~2.8
```

Coverage for the code this session touched: `internal/tools/mcp/mcpauth` 85.8% (up from 84.2%),
`internal/tools/mcp` 81.9%, `internal/text` 85.3% — all clear the 70% floor. Root package 23.5 s
against the 30 s race-gate bound.

`architecture/` was not touched, per the coordinating session's standing instruction for this
worklog. What it needs is named precisely in this session's report and in the README.

## Review findings

### Review 1, 2026-10-02 — implementation review

**Status: Fixed, 2026-10-03 fix session.** Every finding below is checked off and verified; see each
finding's own fixed-note and the phase's Implementation-notes delta. The human gate is legitimately
outstanding and is not a defect.
The findings below are separate from it: one command panics, the command-ban control does not reach
any production path, the CSRF parameter is generated and thrown away, and the redaction invariant's
four tests do not establish it.

**Verified good:**

- Discovery follows the specification table: the protected-resource URL is taken verbatim from the
  challenge and nothing is guessed (`internal/tools/mcp/mcpauth/mcpauth.go:202-217`); RFC 8414's
  inserted form is tried before the OIDC-style appended form
  (`discovery.go:298-310`); all five required fields are checked before any further request
  (`:315-330`). Every discovery body is read under a 64 KiB bound.
- PKCE is real `S256` over a 32-byte `crypto/rand` verifier (`pkce.go:122-131`).
- Refresh single flight is correct and is this package's own layer, keyed per server, with the
  in-flight entry removed before the result is published (`refresh.go:78-115`). The test hooks are
  nil in production and are honestly declared as such on the struct.
- The token store is the right shape: 0600, atomic temp-plus-rename, `MkdirAll` at 0700, and
  absence/unreadability/corruption/empty-token are all misses rather than errors
  (`store.go:361-407`), exactly as D15 requires of a cache.
- Error strings for the token and registration endpoints carry only the server's own stated reason
  (`token.go:49-61`, `registration.go:205-217`), never the request form — so the verifier, the
  code and the client secret do not leak through those two carriers.
- `TestMcpAuthCommandWritesTokenStore` is a genuine end-to-end exercise against both in-repo fakes,
  asserting the stored token value *and* the 0600 mode. `TestSchemaCacheNeverPersistsAuthFailure`
  drives real setup and scans the real cache directory.
- Invariant 5 holds: no automated test reaches a vendor endpoint or spends anything.

**Findings**

- [x] **R1-04** (blocker) — **`clai mcp` panics with a nil pointer dereference.** `Command()`
  (`internal/tools/mcp/cmd.go:29-60`) builds the parent with `Subs` but never assigns
  `c.OnRun`, and `internal/command.go:135-139` then falls through to `c.querier.Query(ctx)` on a
  command that has no querier. Reproduced against a build of the working tree: bare `clai mcp`
  →
  `panic: runtime error: invalid memory address or nil pointer dereference … internal.(*Command).Run … internal/command.go:139`.
  `clai mcp -h` and `clai mcp auth <server>` both work; only the bare invocation crashes. It is the
  only parent-with-`Subs` command in `main.go`'s map that omits `OnRun` —
  `internal/audio/cmd.go`, `internal/chat/cmd.go`, `internal/profiles/cmd.go` and
  `internal/tools/cmd.go:110` each assign exactly one. Breaks CLAUDE.md's "Never panic in a
  function which returns an error" and README invariant 3.
  Corrective action: assign `OnRun` on the `mcp` parent to print help or return a usage error,
  mirroring `internal/tools/cmd.go:110-116`, and add bare-`mcp` plus a `{"mcp -h", …}` row to
  `Test_e2e_command_help` (`main_dispatch_e2e_test.go:304`), which this worklog edited without
  adding a row for its new command.
  **Fixed, 2026-10-03 fix session:** `Command()` now assigns `c.OnRun` to print `c.Help()` and
  return nil (`internal/tools/mcp/cmd.go`); `{"mcp -h", ...}` added to `Test_e2e_command_help`, and
  a new `Test_e2e_mcp_bare_invocation_does_not_panic` drives bare `clai mcp` through the real
  composition root (`run([]string{"mcp"})`) and asserts no panic, exit 0.
- [x] **R1-05** (major) — **the credential command's ban policy never reaches any production call
  site, so the declared "banned command is never spawned" invariant holds on no real path.**
  `pkgtools.ValidateCmdNotBanned` reads the policy from the context
  (`pkg/tools/cmd_ban.go:28-30` → `validateCmdNotBannedWithContext`), and
  `pkgtools.WithCmdBanContext` is applied in exactly one place in the whole repository:
  `internal/text/tool_executor.go:178`. Every credential resolution runs under a context that has
  never been through it — `staticHttpDecorator(ctx, …)` from `setupMcpManager`
  (`internal/text/querier_setup_tools.go:209`), and `dialHttpServerWithAuth` from the connector,
  whose dial runs under `c.runCtx` (`internal/tools/mcp/connector.go:99`), not the caller's
  tool-call context. `userConf.CmdBan` exists at setup time — `querier_setup.go:201` copies it onto
  the querier — so the data is available and simply is not threaded. Scenario: a server config
  with `"auth": {"token_command": ["sh","-c","…"]}` spawns `sh` at setup even on a run started with
  `--cmd-ban sh`. `TestCredentialCommandBannedIsNeverSpawned`
  (`internal/tools/mcp/mcpauth/credential_test.go:62-76`) builds the ban context *itself* and so
  passes while production supplies no policy — the archetype of a row proved against its seam.
  Corrective action: attach `pkgtools.WithCmdBanContext(ctx, userConf.CmdBan)` to the context
  handed to `setupMcpManager` (or to each credential-resolution call site), and add one test that
  asserts the ban through `setupMcpManager` with a `CmdBan` entry in `Configurations`.
  **Fixed, 2026-10-03 fix session:** `setupTooling` now attaches
  `pkgtools.WithCmdBanContext(ctx, userConf.CmdBan)` to the one `ctx` it threads into
  `setupMcpManager` (`internal/text/querier_setup_tools.go`), which reaches both the eager
  (`staticHttpDecorator`) and lazy (`c.runCtx`-inherited `dialHttpServerWithAuth`) call sites from
  the single composition-root boundary. Verified through the real public surface:
  `Test_AgentSetup_CmdBanAppliesToMcpCredentialCommand` (explicit/strict/eager, `pkg/agent`) and
  `Test_AgentSetup_CmdBanAppliesToAmbientLazyHttpCredentialCommand` (ambient/lazy, `pkg/agent`).
- [x] **R1-11** (major) — **the OAuth `state` parameter is generated, sent, received and silently
  discarded.** `prepareAuthorization` mints it (`internal/tools/mcp/mcpauth/mcpauth.go:303-313`)
  and puts it in the authorization URL, `awaitRedirect` captures it into `redirectResult.state`
  (`flow.go:46`), and `authorizeViaFlow` returns only `res.code` (`:268`) — `redirectResult.state`
  has **zero readers** in the package (`grep -rn "\.state\b" internal/tools/mcp/mcpauth/` returns
  nothing). The loopback handler also ignores `r.URL.Path` entirely, so any request to the
  ephemeral port resolves the wait. PKCE does mitigate authorization-code injection here, because
  an injected code was issued against a different `code_challenge` and the exchange would fail —
  which is why this is major and not a blocker — but the code as written claims a CSRF defence it
  does not implement, and a captured-then-discarded value is precisely the "capture is not
  recording" failure a review is supposed to catch.
  Corrective action: return the state from `prepareAuthorization`, compare it to `res.state`, and
  return a `*RedirectError` on mismatch; restrict the handler to the `/callback` path. Add a test
  driving a mismatched state. If the maintainer prefers to rely on PKCE alone, delete `newState`
  and the `state` field rather than leaving a dead defence in place.
  **Fixed, 2026-10-03 fix session:** `prepareAuthorization` now returns the `state` it minted;
  `authorizeViaFlow`'s loopback branch compares it to `res.state` and returns a `*RedirectError`
  ("state parameter mismatch") on a mismatch; the loopback handler now rejects any request whose
  path is not `/callback` with `http.NotFound` instead of resolving the wait on any path.
  `TestOauthLoopbackStateMismatchIsTypedError` drives a `forgedStateOpener` that submits a code
  straight to the redirect URI with a forged state, bypassing the real `/authorize` endpoint
  entirely; `TestLoopbackHandlerIgnoresOtherPaths` proves an unrelated request to the ephemeral port
  does not resolve the wait.
- [x] **R1-10** (major) — **the redaction invariant's four named tests do not establish it.**
  The invariant row is "Every carrier in the redaction table | Carries none of the five secrets",
  and the redaction table names five carriers. What the tests actually do:
  `TestCredentialRedactedInDebugOutput` (`internal/tools/mcp/mcpauth/redaction_test.go:82-99`)
  **cannot fail** — it runs a real flow to obtain random secrets, then asserts those secrets are
  absent from a freshly built, unrelated `serverWithAuth(...)` struct literal that could never
  contain them; its own comment concedes the struct "never carries a resolved secret in the first
  place". `TestCredentialNeverAppearsInLog` (`:62-72`) re-asserts the *same* `refreshErr` string
  that `TestCredentialNeverAppearsInError` already asserted, wrapped in a `fmt.Sprintf`, and
  exercises no production logging call site. Between them the two cover one error type plus one
  hand-built `CredentialSourceError`; nothing covers `ExchangeRejectedError`,
  `RegistrationRejectedError`, `TokenStoreWriteError`, `DiscoveryError`, `MetadataURLError`,
  `RedirectError`, `claierr.McpHttpStatusError` (whose `Message` is a verbatim response body), or
  the real production carriers `ancli.Warnf("failed to setup: '%v', err: %v")`
  (`internal/text/querier_setup_tools.go:284`) and the `DEBUG` dump at `:148`. R1-03 is the proof
  that this matters: a secret placed in a carrier-reachable config field reaches both a cached file
  and the debug dump, and the test family designed to catch exactly that is the one that cannot
  fail. The "Credential command output | Captured, used, **never echoed**" row is also unmet:
  `TestCredentialCommandSuppliesToken` (`credential_test.go:19-35`) asserts only that the token is
  supplied and set as a header.
  Corrective action: rewrite the family so each row's test places a known secret into that
  carrier's real input and asserts its absence from that carrier's real output, including the
  `DEBUG` config dump with the secret in `args` and `env`, and each typed error constructed from a
  fake server response that echoes the secret back.
  **Fixed, 2026-10-03 fix session:** the DEBUG dump was the real, proven leak this finding warned
  about — `debug.IndentedJsonFmt(mcpServers)` serialised `pub_models.McpServer` verbatim, so `args`
  and `env` values reached stdout whole. `querier_setup_tools.go`'s DEBUG call site now dumps a new
  `debugRedactedMcpServer` view (arg count, env key names only, no values) built by
  `redactMcpServersForDebug`; `TestDebugDumpNeverLeaksArgsOrEnvSecret` places a known secret in both
  `args` and `env` and drives the real `setupMcpManager` DEBUG path, asserting absence from captured
  stdout while the server name and env key names survive. `TestCredentialRedactedInDebugOutput`
  (the tautological test named in this finding) is unchanged — it still documents the config-struct
  case — but the real production carrier is now covered separately.
  `TestCredentialCommandOutputNeverEchoed` closes the "never echoed" row by capturing this
  process's own stdout around a successful `ResolveCached` call.
  `TestSecretsNeverLeakIntoUncoveredErrorTypes` (`redaction_test.go`) adds the six previously-named
  gaps — `ExchangeRejectedError`, `RegistrationRejectedError`, `TokenStoreWriteError`,
  `DiscoveryError`, `MetadataURLError`, `RedirectError` — each driven through the real fakes (never
  hand-built) and checked against secrets minted by a separate, successful flow, the realistic
  multi-tenant risk invariant 6 actually guards against.
  `TestMcpHttpStatusErrorNeverLeaksAccessToken` (`internal/text`) closes `claierr.McpHttpStatusError`:
  a real access token authenticates a connection, the fixture is then reconfigured mid-test to fail
  with a generic body, and the resulting error is asserted not to carry the token the failing
  request still sent as its own `Authorization` header — proving `conn_http.go`'s message source is
  the response body only, never the request.
- [x] **R1-09** (major) — **a token-store write failure discards a valid, freshly issued token**,
  contradicting the declared error-coverage row "Token store unwritable | Typed error naming the
  path; **the run continues with the token it already holds**". Both
  `AuthorizeInteractive` (`internal/tools/mcp/mcpauth/mcpauth.go:232-234`) and `doRefresh`
  (`refresh.go:157-159`) return the `*TokenStoreWriteError` and throw the `TokenEntry` away.
  Scenario: a container with a read-only `HOME` — every OAuth flow completes, the access token is
  valid, and clai fails the tool call anyway. The named test
  (`store_test.go:38-53`) exercises `store.Save` in isolation and never touches the
  continue-with-the-token half of its own row.
  Corrective action: return `(decorator, err)` / `(entry, err)` together so the caller can proceed
  with the in-memory token while surfacing the degraded state, and test that the tool call still
  succeeds with an unwritable store.
  **Fixed, 2026-10-03 fix session:** `AuthorizeInteractive` and `doRefresh` now return the valid
  `TokenEntry`/decorator alongside the `*TokenStoreWriteError` instead of a zero value;
  `ResolveCached`, `dialHttpServerWithAuth`, `httpConnOptsFor` and `handshakeHttpServerWithAuth`'s
  interactive-retry branch all tolerate that specific, decorator-carrying case (warning rather than
  failing) while any other error still fails as before.
  `TestSetupToolCallSucceedsDespiteUnwritableTokenStore` (`internal/text`) drives the full
  `setupMcpManager` boundary with a token store whose parent path is a regular file (deterministic
  `MkdirAll` failure) and asserts the tool call still succeeds.
- [x] **R1-18** (major) — **a declared error-coverage row contradicts both D15 and the code, and
  its named test covers a different case.** The row is "Named environment variable unset | Source
  is unset, so it is **skipped and the next is tried**", but `resolveEnvToken`
  (`internal/tools/mcp/mcpauth/credential.go:287-291`) returns a hard `*CredentialSourceError`
  with no fall-through, which is what D15 ("a source that is configured and fails is a typed error
  with no fall-through") actually requires. `TestUnsetCredentialSourceIsSkipped`
  (`credential_test.go:140-158`) tests a nil `auth` block and an `auth` block with *both* sources
  unset — neither is the row's case. Scenario as shipped: a server with
  `auth.token_env: "LINEAR_MCP_TOKEN"` in a shell where that variable happens to be absent returns
  a typed error and never consults a perfectly valid token-store entry or the interactive flow.
  Ruling: D15 governs, so the **row is wrong** and should be rewritten to "Named environment
  variable unset → typed error naming the source, no fall-through"; the test should then be
  retargeted to that case and the existing two sub-cases kept under a correctly named row.
  **Fixed, 2026-10-03 fix session:** the code already matched D15 (`resolveEnvToken` returns a hard
  `*CredentialSourceError`, no fall-through) and `TestUnsetCredentialSourceIsSkipped` already
  covered only the two true-unset sub-cases, with `TestConfiguredEnvVarAbsentIsTypedError` already
  covering the configured-but-failing case correctly — both were fixed in an earlier session this
  review round predates. Only the documentation gap this finding names remained: the Error coverage
  table's row below is rewritten to D15's actual rule, split into the two distinct cases, each
  naming the test that already proves it.
- [x] **R1-19** (minor) — **`loadNamedServer` is a third MCP config parser.**
  `internal/tools/mcp/cmd.go:111-123` unmarshals the server JSON itself instead of calling
  `serverconfig.FindConfiguredServers`, which the README code-layout table names as "the single
  parsing primitive … so the two can never resolve a server's identity differently". Consequences:
  a config setting **both** `command` and `url` is accepted here (only `Url == ""` is checked,
  `:89`) while setup rejects it (`serverconfig.go:76-78`); and `server.EnvFile` is left unexpanded,
  so a relative envfile resolves against the process CWD instead of the config file's directory
  (`serverconfig.go:51-61`), which silently breaks `auth.token_env`'s envfile fallback
  (`mcpauth/credential.go:278-282`).
- [x] **R1-24** (minor) — `clai mcp auth`'s failures are all bare `fmt.Errorf`
  (`internal/tools/mcp/cmd.go:54, 69, 90, 96, 100, 103, 115, 119`), against README invariant 3 and
  CLAUDE.md's typed-error rule. `TestMcpAuthCommandMissingServerIsTypedError`
  (`cmd_test.go:81-90`) promises a typed error in its name and asserts only `err != nil`, so it
  cannot fail for the reason it states. The `server.Url == ""` branch and the "answered with no
  authorization challenge" branch have no test at all.
  **Fixed, 2026-10-03 fix session:** every `cmd.go` failure path now returns one of seven new typed
  errors (`internal/tools/mcp/cmd_errors.go`: `McpAuthUsageError`, `McpAuthConfigDirError`,
  `McpAuthServerConfigError`, `McpAuthNotEndpointBasedError`, `McpAuthNoChallengeError`,
  `McpAuthConnectError`, `McpAuthFlowError`). `TestMcpAuthCommandMissingServerIsTypedError` now
  asserts `errors.As(err, &cfgErr)` against `*McpAuthServerConfigError`; the two previously
  untested branches each get their own new test,
  `TestMcpAuthCommandNonEndpointServerIsTypedError` and `TestMcpAuthCommandNoChallengeIsTypedError`.
- [x] **R1-29** (minor) — `handshakeHttpServerWithAuth` (`internal/text/mcp_oauth.go:127-152`)
  leaks the first `HttpConn` when it falls back to the interactive flow, and leaks `conn2` when the
  retry fails; neither is `Close`d, so each is cleaned up only transitively when the caller's
  `connCtx` ends. `runAuthWith` (`internal/tools/mcp/cmd.go:93-97`) likewise leaks the probe
  connection on the "nothing to authorize" path.
  **Fixed, 2026-10-03 fix session:** `handshakeHttpServerWithAuth` now closes the bare probe `conn`
  once it commits to the interactive fallback, and closes `conn2` on a failed retry; `runAuthWith`
  closes its probe connection on the "nothing to authorize" path.
  `TestMcpAuthCommandNoChallengeIsTypedError` runs the no-challenge path twice against the same
  fixture, proving the first run's connection was not held open.
- [x] **R1-32** (note) — `mcp.LoadEnvFile` (`internal/tools/mcp/envfile.go:16-18`) is a one-line
  wrapper that exists only to export `loadEnvFile`, giving one function two names and two doc
  comments. Rename `loadEnvFile` to `LoadEnvFile` and update its two internal call sites instead.
  **Fixed, 2026-10-03 fix session:** `loadEnvFile` renamed to `LoadEnvFile` directly; the wrapper
  deleted; `conn_stdio.go`'s one internal call site updated to the new name.
- [x] **R1-30** (minor, owned with phase 6) — this phase's statement "The clock is injected and no
  test sleeps, for the same load-sensitivity reason recorded in phase 3" does not hold across the
  authorization family. Detail in phase 6's R1-30.
  **Verified, 2026-10-03 fix session, phase 5's share:** audited every file this phase owns
  (`internal/tools/mcp/mcpauth`, `internal/text/mcp_oauth*.go`) for a real wall-clock sleep used as
  a correctness bound. The one `time.Sleep(time.Millisecond)` in
  `TestOauthRefreshIsSingleFlightPerServer` is a liveness poll waiting for an atomic counter to
  reach `n` (no timeout, no pass/fail decision made on elapsed time), not a timing-bound sleep; the
  clock is injected everywhere a refresh-skew decision is made. The actual real-sleep-against-a-bound
  violation R1-30 describes is phase 6's own file (`tool_executor_auth_test.go`, confirmed by
  R2-26's identical citation), so phase 5's statement is accurate for phase 5's own files. No code
  change required on phase 5's side; phase 6's fix session owns the rest.

### Review 2, 2026-10-02 — implementation review, round 2

Status: **Fixed, 2026-10-03 fix session.** Every finding below is checked off and verified. The
human-required live-endpoint gate remains legitimately outstanding and is not a finding.

Round 1 covered the ban policy's unreachable call site (R1-05), the discarded token (R1-09), the
redaction tests (R1-10), the unread `state` (R1-11), the third config parser (R1-19), the bare
errors (R1-24) and the connection leaks (R1-29). Round 2 looked at what the authorization family
does to a caller that is not a human at a terminal.

**Verified good:**

- `pkg/claierr`'s new vocabulary is mechanically complete: every new type has a sentinel, `Unwrap`
  returns the cause wherever a cause is stored, `errors.Is` reaches the sentinel from a wrapped
  error, and every constructor has at least one production producer. Nothing was exported without
  one.
- `TestSchemaCacheNeverPersistsAuthFailure` (`internal/text/mcp_oauth_setup_test.go:72`) asserts
  over the raw directory listing rather than through `Lookup`, so it is the one cache-negative
  assertion in this change that an identity-derivation bug cannot fake. That is the right shape and
  worth copying.
- `mcpauth.CredentialSourceError`, `RefreshError` and `TokenStoreWriteError` each carry the server
  name and the source, and `CredentialSourceError.Unwrap` reaches the cause
  (`mcpauth/errors.go:95-115`).

**Findings:**

- [x] **R2-05** (major) — **An explicit server's credential failure under strict startup degrades to
  a warning instead of failing `Setup`.** `setupTooling` has exactly one classifier for the
  strict/degrade fork: `errors.Is(err, claierr.ErrMcpServerStartup)` (`querier_setup_tools.go:404`).
  `staticHttpDecorator` (`mcp_oauth.go:79-85`) returns `*mcpauth.CredentialSourceError`, whose
  `Unwrap` reaches the command's own error and never the MCP startup sentinel. That error reaches
  `classifyServerFailure` (`querier_setup_tools.go:224`), is joined into `explicitFailures`, is
  returned — and then fails the `errors.Is` test, so `setupTooling` takes the degrade branch at
  `:411-412` and returns nil. Concrete failure:
  `agent.WithMcpServers([]models.McpServer{{Name:"linear", Url:"https://…/mcp", Auth:&models.McpServerAuth{TokenCommand:[]string{"op","read","…"}}}})`
  where `op` exits non-zero. `Setup` returns nil, the server's tools are absent, and the agent runs
  a task that depends on them — exactly what D13 and `pkg/agent/mcp_setup_test.go:41-46` promise
  cannot happen. The same hole applies to `httpConnOptsFor` (`mcp_oauth.go:161`) and
  `AuthorizeInteractive` (`:142`) errors on the lazy path. No test combines `StrictMcpStartup` with
  an authorization failure. Corrective action: wrap at the one boundary rather than trusting each
  producer — `classifyServerFailure` is the single funnel every explicit failure passes through, so
  wrapping there in `claierr.NewMcpServerStartup(name, stage, err)` closes the class.
  **Fixed, 2026-10-03 fix session:** the funnel is `failureCollector.addExplicit`/`addIfExplicit`
  today (renamed from `classifyServerFailure` by an earlier fix session; same single-boundary
  shape). `addExplicit` now wraps any error that does not already carry `claierr.ErrMcpServerStartup`
  into `claierr.NewMcpServerStartup(serverName, "credential", err)` before appending it, so every
  explicit failure — however it was typed by its own producer — reaches `setupTooling`'s
  `errors.Is` check. Verified through the real public surface:
  `Test_AgentSetup_ExplicitMcpCredentialFailure_FailsSetup` (`pkg/agent/mcp_setup_test.go`)
  reproduces the exact scenario this finding names (`WithMcpServers` plus a failing
  `auth.token_command`) and asserts `Setup()` fails with `errors.Is(err, claierr.ErrMcpServerStartup)`
  naming the server.

- [x] **R2-08** (major) — **`Authorizer.Interactive` is set twice in production and read nowhere, so
  `agent.Setup` can block on a browser redirect and can print to a library consumer's stdout.**
  Three facts compose:

  1. `mcpauth.WithInteractive` is applied at `internal/text/mcp_oauth.go:68` and
     `internal/tools/mcp/cmd.go:74`, but `grep -rn '\.Interactive\b'` over non-test code finds only
     the assignment in `WithInteractive` itself (`mcpauth/mcpauth.go:106-109`) and a comment at
     `refresh.go:77`. Nothing branches on it. `WithInteractive(false)` is inert, so the non-terminal
     fail-fast posture D22 describes is not enforced at setup at all.
  2. `AuthorizeInteractive` is called on the **setup** path (`mcp_oauth.go:142`) with no bound of
     its own, and `awaitRedirect` (`mcpauth/flow.go:38-59`) is bounded only by the caller's context.
     `agent.Setup(context.Background())` therefore waits indefinitely for a redirect that may never
     arrive.
  3. `WithPrintURL` is test-only, so production always uses `defaultPrintURL`
     (`mcpauth/flow.go:63-67`), which is `fmt.Printf` — the **process** stdout. `pkg/agent` sets
     `Out: io.Discard` and documents that library mode is silent on stdout; this path writes to it.

  Concrete failure: an SDK consumer with an ambient `mcpServers/linear.json` and no cached schema
  entry. `Setup` misses the cache, connects, gets a `401`, enters `AuthorizeInteractive`, prints an
  OAuth URL into the host application's stdout and then either opens a browser or hangs. Corrective
  action: branch on `a.Interactive` before `AuthorizeInteractive` — the field exists for exactly
  this — bound the setup-path call by the resolved auth-timeout, and route `printURL` through the
  sink's injectable writer.
  **Fixed, 2026-10-03 fix session, all three facts:**
  (1) `newMcpAuthorizer` now takes `outputIsTerminal bool` — the same `userConf.OutputIsTerminal`
  D22's own auth-timeout bound already resolves, rather than a second, independently-derived check
  of the real process stdout — and sets `WithInteractive(outputIsTerminal)`.
  `handshakeHttpServerWithAuth`'s setup-path branch now reads `authz.Interactive` before attempting
  the interactive flow at all: false (every pkg/agent SDK consumer, since `OutputIsTerminal` is
  always false there per R2-09, phase 6's finding) returns the original challenge immediately, no
  browser, no print, no wait.
  (2) When `authz.Interactive` is true, the call is now wrapped in
  `context.WithTimeout(ctx, resolveAuthTimeout(server, authz.Interactive))` before calling
  `AuthorizeInteractive`, so the setup-path flow is bounded exactly as the mid-run one already is.
  (3) `newMcpAuthorizer` now also passes `mcpauth.WithPrintURL(authPrintURLFor(sink))`: a new
  `authPrintWriter` feature-detection interface lets `*mcpLogSink` expose its own injectable
  `errOut` writer (a new `AuthPrintWriter() io.Writer` method on it), and `defaultPrintURL`'s raw
  `fmt.Printf` is only ever reached when no such sink is configured.
  Verified: `TestSetupNonInteractiveSkipsAuthorizeInteractive` (`internal/text`) proves the
  non-interactive skip does not hang, using a poison `io.Pipe` reader that would block the test
  forever if the printed-URL fallback were ever reached, bounded by an outer deadline that is the
  failure signal, not a correctness bound — and red-before-green verified: removing the
  `!authz.Interactive` gate alone reproduces the hang (a real loopback redirect wait that times out),
  confirming the gate itself, not merely the non-terminal auth-timeout default, is what the test
  exercises. `TestAuthPrintURLRoutesThroughSinkNotStdout` and
  `TestAuthPrintURLFallsBackToDefaultForAPlainSink` (`internal/text`) pin the third fact directly.

- [x] **R2-22** — filed against phase 1; `mcpauth.WithInteractive` and `mcpauth.WithPrintURL` are
  this phase's two entries in it, and R2-08 is why the first one matters beyond tidiness.
  **Resolved as a side effect of R2-08, 2026-10-03 fix session:** both options now have a real,
  non-test production setter (`newMcpAuthorizer`) whose value is actually read
  (`authz.Interactive` branches the setup-path flow; `authPrintURLFor`'s result is what
  `WithPrintURL` installs). Neither is a dead, test-only option with a doc comment describing a
  README parameter production cannot set; phase 1's own entries in this finding are unaffected.

### Review 3, 2026-10-03 — sign-off review (holistic)

**Status: Reopened (sign-off), resolved.** The first pass to read the whole effort at once rather
than one phase at a time, after reviews 1 and 2 had already closed every finding against this
phase. It found the blocker that held this phase and phase 6 in the final verdict. Full detail in
the README's Sign-off verdict section and the Sign-off review feedback-index entry.

**Findings**

- [x] **B2** (blocker) — the OAuth client was neither conformant nor safe: *"this is an
  OAuth-shaped object, not an OAuth client."* RFC 8707 `resource` was never sent and
  `ProtectedResourceMetadata.Resource` had no readers; `redirect_uri` was absent from the
  authorization-code token request although the authorization request always set one, which a
  conformant server answers `invalid_grant`; the discovery chain had no trust validation at all —
  `doc.Issuer` was never compared to the issuer fetched, `prm.Resource` never to the server, no
  scheme requirement anywhere, and no `CheckRedirect` on either `http.Client`; and
  `httpChallengeResolver` was not gated on `authz.Interactive` although the setup path had been
  fixed for precisely that (R2-08). Composed: a malicious server, or an on-path attacker forging
  the 401, controlled the resource metadata, the issuer, the registration, `/authorize` and
  `/token`; clai registered a client with the attacker, handed the attacker's
  `authorization_endpoint` to `xdg-open`, and stored whatever the attacker's token endpoint
  returned. The root cause of it shipping green is the fixture: `oauthtestserver` was permissive at
  every one of those points.

  **Resolved (sign-off fix session, 2026-10-03).** `resource` is sent on the authorization request
  and all three token grants, carried forward on the stored entry; `redirect_uri` is repeated on
  the exchange; a new `trust.go` validates the issuer (RFC 8414 §3.3), the resource identifier
  (RFC 9728 §3.3), the metadata location's host and the scheme of every URL on the chain including
  the one handed to a browser and the server endpoint itself; redirects are refused on the token
  and registration endpoints and on the MCP endpoint, and bounded-and-rechecked on discovery;
  `AuthorizeInteractive` refuses a non-interactive run at the root. The fixture is hostile by
  default and each fix has a test that fails without it. Every red state was observed first, the
  sharpest being `verifierLeaked=true` — the unfixed client POSTing its PKCE verifier to a redirect
  target — and the non-interactive case hanging for a full 60 s inside `awaitRedirect` rather than
  merely failing. The misleading doc comment that presented the unvalidated fetch as a feature is
  corrected. Full detail, item by item with its red evidence, in the Implementation notes above.

- [ ] **Recorded, not closed: mid-run token expiry never re-authorizes.** Verified against source
  this session. Judged a phase of its own rather than a line in a security fix, with reasoning in
  the Implementation notes above and in the README's cross-phase gap record.

## Review findings — fix verification, sign-off round

B2 is checked off above with its fix, its fixture mode and its red-before-green evidence cited in
place. The only item left open against this phase is the recorded mid-run re-authorization gap,
which is a scope reduction rather than a defect in what was built, and the human-required
real-endpoint confirmation — which B2.2 shows has never run.
