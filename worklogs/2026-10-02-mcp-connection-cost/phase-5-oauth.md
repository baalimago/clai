# Phase 5 — OAuth 2.1 and credential sources

**Status:** Not Started

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
| Named environment variable unset | Source is unset, so it is skipped and the next is tried | `TestUnsetCredentialSourceIsSkipped` |
| Any authorization failure during setup or a call | Typed authorization error; the schema cache is left untouched | `TestSchemaCacheNeverPersistsAuthFailure` |

## Implementation notes

Not started.

## Review findings

None.
