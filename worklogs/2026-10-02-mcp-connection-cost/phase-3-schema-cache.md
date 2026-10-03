# Phase 3 — Tool schema cache

**Status:** Complete — reviews 1 and 2 reopened this phase on findings R1-03, R1-16, R1-28, R1-36
(review 1) and R2-03, R2-06, R2-12, R2-20, R2-21, R2-25, R2-27 (review 2); R1-02 and R1-14 are shared
with phase 2 and were fixed and verified there. Every blocker and major (R1-03, R1-16, R2-03, R2-06,
R2-12) is fixed and verified in the worklog-work session recorded in Implementation notes below.
R1-28 is accepted as a bounded consequence, R1-36 resolved as a side effect of the same fix, and
R2-25 is deferred (notes; none reopen the phase). A later holistic sign-off review reopened this
phase again on B3, B4 and S1; all three fixed and verified in the 2026-10-03 sign-off fix session
recorded in Implementation notes below.

Back to [README](README.md).

## Goal

Let setup advertise a command-based server's tool schemas from a delta-validated on-disk cache, so
that on a warm cache a lazy server contributes its tools to the model without any transport being
constructed. A miss still connects and captures, per decision D18.

## Specification

### Why a cache is required

Phase 2 defers the connection, but the model must still be told which tools exist before it can
call one. The only handshake output setup needs is the `tools/list` result, and the measurements in
the README show that result is a negligible fraction of the handshake cost: process birth is the
cost, and the tool list is the only thing worth keeping. Caching it is what makes laziness
observable, because without it every lazy server would still have to connect during setup.

### This phase owns the behaviour change

The previous phase built the lazy machinery and changed no default. This phase makes laziness the
default, per decision D16, because this is the first point at which a lazy server can contribute its
tool schemas without connecting. Three things therefore belong here and nowhere else.

First, flipping the startup-mode default. Second, pinning the eager posture in the tests that
actually spawn a server, which the README's fixture-posture parameter row names: the root end-to-end
fixture only creates an empty server directory and spawns nothing, so pinning there would pin
nothing. Third, injecting the cache directory everywhere `setupMcpManager` is exercised, so no test
run writes into the developer's real cache directory.

Because the eager posture is pinned in those tests, the lazy path gets no coverage under the race
gate from them. This phase therefore adds its own end-to-end fixture that configures a lazy server,
so the lazy path is exercised under the race detector rather than only in isolated unit tests.

### What a miss does at setup

A miss connects, exactly as setup does today, and captures the result. This is decision D18 and it
is the load-bearing policy of the phase, so it is stated before anything else: a miss that declined
to connect would ship a run whose MCP tool box is empty, and would break the existing
`Test_setupMcpManager_RegistersToolsAndNotifiesSuccess`, which writes a cold config directory and
asserts that `mcp_echo_echo` is registered.

The consequence is stated plainly rather than hidden: the zero-process outcome this worklog exists
to deliver is a **warm-cache** outcome. A first run after a server is configured pays its spawn once
and writes the entry; every run after that pays nothing until the tool is called. For a fleet this is
the common case by a wide margin, and `clai tools` warms the cache deliberately.

### Identity and storage

An entry is keyed by the content of the server's identity, not by its name. The file lives under
the schema-cache-directory parameter inside the clai cache directory, named by the
schema-cache-file-name parameter. This follows the convention already used for directory-scoped
chat bindings, where the file name is a digest and any human-readable path inside the record is
informational only.

Three things the identity must pin down, because getting any of them wrong silently changes the key:

- **Which environment is digested.** The `env` map from the server's config file, and only that.
  The inherited process environment is deliberately **excluded**, because digesting it would change
  the key with every shell and defeat the cache entirely, even though the spawned process does
  inherit it. An `envfile`'s **contents are not digested either**: its freshness is carried entirely
  by the size and modification time already in the identity, which keeps secret material away from
  a digest and matches invariant 6. The record-format section in the README is the authoritative
  algorithm.
- **An absent envfile.** When no envfile is configured, the identity's envfile component is a
  canonical absent marker rather than a zero size and a zero time, so "no envfile" and "an empty
  envfile" are distinguishable keys.
- **An unresolvable executable.** When the command does not resolve on the path, the executable
  component is the same canonical absent marker. The entry is then a miss, setup connects, and the
  spawn failure surfaces there rather than being pre-judged by the cache.

The identity covers everything that can change which tools a command-based server exposes:

| Identity component | Included |
| --- | --- |
| Command | yes |
| Arguments, as a digest, never stored verbatim (amended by D38/R1-03: the documented `mcp-remote --header "Authorization: Bearer ..."` shape puts a credential in args, and invariant 6 forbids a credential in a cached file) | yes |
| Environment map, as a digest of sorted key and value pairs | yes |
| Envfile size and modification time | yes |
| Resolved executable path, size and modification time | yes |
| First args entry that resolves to an existing file on disk (an interpreter-launched server's own script, which the resolved executable never sees), size and modification time, when one exists (amended by D40/R2-03) | yes, when present |

This phase owns the cache mechanism and the identity of a command-based server only. An
endpoint-based server has no executable to stat and no local evidence of change, so its identity
components, its freshness rule and its invalidation signals are owned by the transport phase that
introduces endpoint-based servers. Nothing in this phase refers to a transport that does not yet
exist.

The record stores the identity, the negotiated protocol version, the server info, the `tools/list`
result verbatim, and the capture time as a time value rather than a formatted string, so the encoder
owns its representation and emits RFC 3339.

The README's record-format section shows the record in its **final** shape, after every phase has
contributed. This phase writes the command-based identity components, the protocol version, the
server info and the tools; it leaves the endpoint and scopes components absent, and the phases that
own them fill them in. An entry written here and later read by a build that expects those components
is simply a miss, which is the correct outcome.

The write is synchronous: it happens after the handshake returns and before setup returns, so a run
that completes setup has either captured the entry or surfaced the write failure. Nothing is written
on a background goroutine, because a caller that cannot observe the failure cannot degrade from it. The exact shape, including the environment digest algorithm and what the
server-info field holds, is the schema cache entry in the README's record formats section; this
phase introduces no field that is not in that record.

### Freshness

**Amended by implementation review 2 (R2-03, ruling D40).** The evidence for a command-based server
is local, but it is no longer exact: `resolveExecutable(server.Command)` fingerprints the resolved
*launcher* (`node`, `npx`, `uvx`, `python`, `docker`, ...), not the server itself, so a delta on the
launcher alone cannot see a server upgrade that touches no local file the launcher's own path, size
or modification time depends on — by the worklog's own census this is roughly 85 percent of local
servers, including the exact `npx -y @modelcontextprotocol/server-filesystem` command the Strategy
section measured. Two things follow. First, the identity additionally fingerprints the first `args`
entry that resolves to an existing, regular file, which for an interpreter-launched server
(`node script.js`) is normally the script itself; a launcher invocation with no such entry (an npx
package name is not a path on disk) leaves this component absent and the identity still reacts only
to the launcher's own delta, exactly as before. Second, the schema-cache-freshness-bound parameter
now applies to **every** entry, command-based or endpoint-based, not only an endpoint-based one: time
is the backstop for exactly the class of change delta validation cannot see. The entry is valid while
every recorded size and modification time still matches, the environment and arguments digests still
match, and the entry is within the freshness bound. Any delta, or an expired bound, is a miss.
Validating with a size and modification-time delta rather than dropping a component from the key is
the rule already established for this repository's foreign conversation index.

The record carries its capture time, taken from the injected clock this phase owns. A clock that
moves backwards makes the bound fail-open (a negative duration can never exceed a positive bound);
this is an accepted optimisation consequence, not a correctness surface, since an entry that should
have expired but did not is only ever a cache lookup — it still only produces the same tool set a
live connect would (R1-36).

### What is never cached

Only content-determined data is written. A connect failure and a tool error are facts about one run,
not about the server, and neither is persisted. This is the same rule that keeps a run-fact out of
the foreign conversation index. Authorization has no failure mode until the phase that introduces
it, so the authorization case is specified there rather than asserted here against a hand-built
error that any error value would satisfy.

A cache write failure never fails setup. Setup degrades to connecting, the run proceeds, and the
failure is surfaced as a warning rather than returned as a setup error, because the cache is an
optimisation and its absence is not an absence of capability.

A corrupt or unparseable entry is a miss, never an error. The file is replaced on the next
successful capture.

### The setup-side call site

The cache is useless without the code that consumes it, so that path is specified here rather than
left to the code-layout table. On setup, for each configured command-based server:

1. Build the identity and look up its entry.
2. On a hit, register one tool per entry in the cached `tools/list`, under the existing
   `mcp_<server>_<tool>` prefix, each wired to that server's `Connector` and to nothing else. No
   transport is constructed and no process starts.
3. On a miss, resolve the connection, perform the handshake, register the tools from the live
   `tools/list` exactly as today, and capture the entry.

A hit and a miss must produce the **same** registered tool set for the same server state. That
equivalence is the phase's real contract: a cache that avoids a transport but registers nothing has
achieved nothing.

### Invariants

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| Entry file name | Digest of the identity record | `TestSchemaCacheKeyIsSha256OfIdentity` |
| Setup with a valid entry | Registers tools from the entry under the existing prefix, each wired to its connector, and constructs no transport | `TestSchemaCacheHitRegistersToolsWithoutTransport` |
| A hit and a miss for the same server state | Register an identical tool set | `TestCacheHitAndMissRegisterIdenticalToolSets` |
| Setup with no entry | Connects, registers tools, captures the entry | `TestSchemaCacheMissConnectsCapturesThenHits` |
| Executable size change | Entry is a miss | `TestSchemaCacheMissOnBinarySizeDelta` |
| Executable modification-time change | Entry is a miss | `TestSchemaCacheMissOnBinaryMtimeDelta` |
| Envfile size or modification-time change | Entry is a miss | `TestSchemaCacheMissOnEnvfileDelta` |
| Environment map change | Entry is a miss | `TestSchemaCacheMissOnEnvHashChange` |
| Args change, including a secret-shaped one | Entry is a miss; the secret never appears in the identity (D38, R1-03) | `TestSchemaCacheArgsAreDigestedNotStoredVerbatim` |
| Launched script file size or modification-time change, for an interpreter-launched server | Entry is a miss, closing the gap the resolved launcher alone cannot see (D40, R2-03) | `TestSchemaCacheMissOnScriptFileDelta` |
| Freshness bound | Applies to every entry, command-based or endpoint-based alike (D40, R2-03) | `TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo`, `TestHttpSchemaCacheEntryExpiresOnFreshnessBound` |
| A command-based server declaring `auth.scopes` | `BuildIdentity` and `BuildIdentityWithScopes` compute the same key, so setup and the cache-only listing never disagree (D40, R1-16) | `TestSchemaCacheCommandServerIgnoresAuthScopes` |
| Connect failure | Nothing written | `TestSchemaCacheNeverPersistsConnectFailure` |
| Corrupt entry | Treated as a miss | `TestSchemaCacheCorruptEntryIsTreatedAsMiss` |
| Cache write failure | Setup proceeds by connecting | `TestSchemaCacheWriteFailureDoesNotFailSetup` |
| The startup-mode default | Flipped to lazy in this phase | `TestStartupDefaultIsLazyAfterCache` |
| The cache constructor | Requires its directory, so a caller that omits it fails to construct rather than falling back to the real cache directory | `TestCacheConstructorRequiresDirectory` |
| The lazy path | Covered by its own end-to-end fixture under the race detector, asserting a positive tool match and no "doesn't exist" warning on the warm run, not exit status alone (R2-12) | `TestLazyStartupE2EUnderRace` |
| A per-file config parse or validation error | Warned unconditionally; also returned, typed, when the run is strict (D44, R2-06) | `Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers`, `Test_setupMcpManager_PerFileConfigErrorFailsStrictRun` |

### Limits

| Limit | Injectable field | README parameter | How a test triggers it |
| --- | --- | --- | --- |
| Cache location | Required constructor argument; the schema-cache-directory parameter is the value the production call site passes, not a fallback | schema-cache-directory parameter | Temporary directory passed per test |
| Capture time source | Cache field | injected-clock parameter | Fixed clock injected per test |

The clock is injected. No test sleeps, because the race gate for this repository is load-sensitive
and a sleeping test is the first thing to fail on a busy host.

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| First setup for a stdio server, empty cache | Fake stdio server, temporary cache directory | Tools registered | Server connected once, entry written | No second connection; no tool missing from the registration |
| Second setup, same server, unchanged on disk | Fake stdio server, warmed cache | The same tool set as the first setup, name for name | Entry read; every tool wired to its connector | No process started, no transport constructed, no tool missing |
| Second setup after the fake executable is rewritten | Fake stdio server with mutated size and modification time | Tools registered | Server reconnected, entry replaced | Stale tool list not served |
| Cache directory not writable | Temporary directory with writes denied | Setup succeeds | Warning surfaced, server connected | Setup not failed |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| Entries are keyed by an identity digest | `TestSchemaCacheKeyIsSha256OfIdentity` |
| A warm cache registers every tool from the entry and constructs no transport | `TestSchemaCacheHitRegistersToolsWithoutTransport` |
| A hit registers exactly what a miss would register | `TestCacheHitAndMissRegisterIdenticalToolSets` |
| A miss connects, captures, and the next setup hits without connecting | `TestSchemaCacheMissConnectsCapturesThenHits` |
| An executable size delta invalidates | `TestSchemaCacheMissOnBinarySizeDelta` |
| An executable modification-time delta invalidates | `TestSchemaCacheMissOnBinaryMtimeDelta` |
| An envfile delta invalidates | `TestSchemaCacheMissOnEnvfileDelta` |
| An environment map change invalidates | `TestSchemaCacheMissOnEnvHashChange` |
| An args change invalidates, and a secret placed in args never reaches the cache | `TestSchemaCacheArgsAreDigestedNotStoredVerbatim` |
| A launched script's size or modification-time delta invalidates an interpreter-launched server's entry | `TestSchemaCacheMissOnScriptFileDelta` |
| The freshness bound applies uniformly, command-based or endpoint-based | `TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo` |
| Setup and the cache-only listing compute the same key for a command-based server declaring `auth.scopes` | `TestSchemaCacheCommandServerIgnoresAuthScopes` |
| A per-file config error is reported and, under strict startup, returned | `Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers`, `Test_setupMcpManager_PerFileConfigErrorFailsStrictRun` |
| No run-fact reaches the cache | `TestSchemaCacheNeverPersistsConnectFailure` |
| Laziness becomes the default | `TestStartupDefaultIsLazyAfterCache` |
| A cache cannot be constructed without an explicit directory | `TestCacheConstructorRequiresDirectory` |
| The spawning tests pin the eager posture | Readiness checklist item nine, a grep over the two named test files |
| The lazy path is exercised under the race detector | `TestLazyStartupE2EUnderRace` |
| A corrupt entry is a miss | `TestSchemaCacheCorruptEntryIsTreatedAsMiss` |
| A write failure degrades instead of failing setup | `TestSchemaCacheWriteFailureDoesNotFailSetup` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Cache directory cannot be created | Warning, setup proceeds by connecting | `TestSchemaCacheWriteFailureDoesNotFailSetup` |
| Entry cannot be written | Warning, setup proceeds, nothing half-written left behind | `TestSchemaCachePartialWriteLeavesNoEntry` |
| Entry is truncated or not valid JSON | Miss, replaced on next capture | `TestSchemaCacheCorruptEntryIsTreatedAsMiss` |
| Entry identity does not match the server config | Miss | `TestSchemaCacheMissOnEnvHashChange` |
| Executable cannot be resolved on the path | Identity records the canonical absent marker for the executable and the entry is a miss, so the server is connected and the failure surfaces there | `TestSchemaCacheUnresolvableExecutableIsMiss` |
| Envfile named in config does not exist | Typed error naming the file, as today | `TestSchemaCacheMissingEnvfileIsTypedError` |
| Clock moves backwards | Capture time recorded verbatim; no validity decision in this phase depends on it | `TestSchemaCacheBackwardClockRecordsCaptureTimeVerbatim` |

## Implementation notes

Executed 2026-10-02 (clai-code session).

**Package layout.** `internal/tools/mcp/schemacache` (new): `Identity`, `FileFingerprint`,
`ExecutableFingerprint`, `Record`, `Cache`, `New`, `(*Cache).Lookup`, `(*Cache).Capture`,
`BuildIdentity`. `Identity.Key()` is the hex SHA-256 of the identity's own JSON encoding, so the
entry filename *is* the content key: an identity delta is a miss by construction, with no separate
comparison needed. `Lookup` still re-marshals and compares the stored identity against the one
passed in as a cheap defence against a hash collision or a corrupted write, but this is belt and
suspenders, not the mechanism.

**Resolved ambiguity (not a spec gap).** "Which environment is digested" named the env map merged
with the envfile's contents; the record-formats section's own algorithm statement says "a hex
SHA-256 over **the environment map**" with no mention of the envfile. Implemented per the latter,
precise statement: `env_digest` covers only `server.Env`; the envfile's freshness is carried
entirely by its separate size/mtime component, with no file read for cache purposes. This also
keeps a configured envfile's contents (potentially secret) out of a digest computation that
invariant 6 already forbids from a cached file in any other form.

**`BuildIdentity` is infallible.** Neither an unresolvable executable nor a configured-but-missing
envfile is an error from `BuildIdentity`: both collapse to the same canonical absent-marker (`nil`
pointer, never a zero-valued struct). This single mechanism satisfies both
`TestSchemaCacheUnresolvableExecutableIsMiss` and `TestSchemaCacheMissingEnvfileIsTypedError`: no
successful capture could ever have happened for either state (capture requires a prior successful
handshake), so the lookup is always a miss and the real failure surfaces exactly once setup
connects, via the pre-existing typed errors — no new error path was added to produce it.

**Mcp package additions (DRY extraction, no eager-path behaviour change).** `handleServer`'s
inlined initialize→tools/list→register sequence is now two exported seams the schema-cache call
site reuses instead of duplicating: `Handshake` (initialize + notified + tools/list, returning
`server_info` and the raw tools array) and `RegisterTools` (parses a tools array and registers one
`LLMTool` per entry under `mcp_<server>_<tool>`, wired to a given `Connector`). `NewTool` and
`NewResolvedConnector` are the matching exported constructors `RegisterTools` and the cache's
miss path need from outside the `mcp` package. `ProtocolVersion` and `HandshakeBoundOf` are
existing private values exported for the same reason (the cache's miss path drives its own
handshake and must advertise and bound it identically to the eager path). `initializeHandshake` now
returns the raw `initialize` result so `Handshake` can extract `serverInfo`; `handleServer` and
`dialStdio` both still just discard it. None of this changes `handleServer`'s or the connector's
observable behaviour — `internal/tools/mcp`'s own test suite passed unmodified.

**Setup-side wiring (`internal/text/querier_setup_tools.go`).** `effectiveStartupMode` gained a
`strictExplicit bool` parameter and the default flip: unset now resolves `lazy`, except
`strictExplicit` (explicit server, `StrictMcpStartup` on) which still resolves `eager`, per the
parameters table's conditional default and D14. The former dead-end lazy branch (`toolWg.Done();
continue`, phase 2) is replaced by `resolveLazyServerViaCache`, which does the hit/miss dance
specified in the phase and is reached only for the lazy-resolved case; the eager
`ControlEvent`/`Manager` path is untouched. A spawn or handshake failure from either path now
routes through one extracted `classifyServerFailure` helper instead of two copies of the same
explicit/ambient `if`. `connectorOptsFor` wires `server.ConnectTimeoutSeconds` onto a cache-hit's
`mcp.NewConnector` via `WithConnectBound` — this field has existed since phase 2 but nothing called
`mcp.NewConnector` in production until this phase, so this is the field's first real consumer.
`setupTooling` builds the production cache under `utils.GetClaiCacheDir()/mcpSchemas`
(`newMcpSchemaCache`); a resolution failure degrades to a warning and `cache == nil`, which
`resolveLazyServerViaCache` treats as an unconditional miss rather than refusing to run.

**Pre-existing test corrected, not silently.** `TestLazyConnectDegradesForAmbientServers` (phase 2)
asserted that an explicitly-lazy ambient server never connects at setup, full stop. D18 supersedes
that: a miss always connects, cold or warm, so a lazy ambient server now registers its tools on a
cold cache too; only a *warm* cache gets the zero-process outcome
(`TestSchemaCacheHitRegistersToolsWithoutTransport` proves that one). The test's body and doc
comment were rewritten in place to assert the corrected behaviour rather than left to rot or
deleted; this is a direct, textually-unambiguous consequence of D18 already in the README, not a
new decision.

**Fixture posture (readiness item nine).** `internal/text/querier_setup_tools_test.go`: every
config/literal that actually spawns `go run ../tools/mcp/testserver` (or the `sh -c touch` marker
helper) now carries an explicit `"startup":"eager"` / `Startup: pub_models.StartupEager`, so this
file's spawning tests stay on the pre-existing eager path regardless of the default flip, and the
lazy path gets no incidental coverage from them. `internal/text/mcp_log_sink_test.go`'s two
`mcp.NewStdioConn`-driving tests got the same field for documentation, though it is inert there
(that file bypasses `effectiveStartupMode` entirely) — checklist item nine's grep already matched
both files independently of this pin (the files are full of unrelated "startup window" prose), so
the pin's purpose is explicitness, not making the grep pass. Every test in both files was run and
still passes; `setupMcpManager`'s signature grew a required `*schemacache.Cache` parameter, so
every call site in `querier_setup_tools_test.go` now passes a fresh `testSchemaCache(t)` (a cache
rooted at `t.TempDir()`), per the parameters table's cache-directory-injection row.

**New fixture.** `internal/tools/mcp/testserver/main.go` gained one env-gated line:
`TEST_SERVER_SPAWN_LOG` appends one line per process birth to a named file, letting a test count real
spawns across repeated setup runs. Used by three `internal/text` tests and by the new root fixture
`main_mcp_lazy_e2e_test.go`'s `TestLazyStartupE2EUnderRace`, which runs a cold then a warm `query`
against a lazy, ambient, command-based server in-process (`run(...)`) under the race detector, with
`CLAI_CONFIG_DIR` and `CLAI_CACHE_DIR` both pointed at fresh temporary directories.

**Blast-radius check.** `schemacache.New`/`Lookup` never touch disk beyond a single read per lookup,
and `Capture` is the only write path; the per-server loop that reaches either is skipped entirely
when a run configures zero MCP servers. Audited every existing root e2e test and
`pkg/agent/mcp_setup_test.go`: none configures a real (non-empty) `mcpServers` directory outside the
ones this phase added, so no pre-existing test newly touches a developer's real cache directory.

**Verification.** `go build ./...`, `go vet ./...`, `go run mvdan.cc/gofumpt@latest -w -l .` (two
files reformatted, both mine), `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` (clean),
`go fix ./...` (no changes), `go run github.com/mibk/dupl@latest -t 80 .` (34 pre-existing clone
groups, none touching a file this phase added or changed — the one clone involving
`querier_setup_tools_test.go` is the pre-existing `startupErrorNames`/`mcpStartupServerNames` pair
shared with `pkg/agent/mcp_setup_test.go`, present before this phase). `go test ./... -race -cover
-count=3 -timeout=30s` passed in full at host load ~3.6–9. A later full-repo rerun (after two
additive coverage-only tests, no production change) hit a timeout solely in `internal/vendors`
—untouched by this diff—at host load 9.94 rising to 18; re-running `./internal/vendors/...` alone at
lower load passed cleanly in 8.6s, consistent with this repository's documented race-gate host-load
sensitivity, not a regression. Every package this phase touched
(`.`, `./internal/text/...`, `./internal/tools/mcp/...`) was independently re-run at
`-race -count=3 -timeout=30s` afterward and passed. Coverage:
`internal/tools/mcp/schemacache` 85.5%, `internal/text` 84.6%, `internal/tools/mcp` 86.0%, root
package 94.6%.

**Delta tests proved at the identity level, not through a live setup run.** The integration
contract's "fake executable is rewritten" row is proved by `TestSchemaCacheMissOnBinarySizeDelta`
and `TestSchemaCacheMissOnBinaryMtimeDelta` operating directly on `BuildIdentity` against a
throwaway shell-script "executable" in a temp dir, not by mutating a real server binary through a
live `setupMcpManager` run. The command every other integration test in this phase spawns is `go run
../tools/mcp/testserver`, which resolves to the shared, immutable system `go` binary — there is no
server binary of its own to rewrite without either faking a second fixture binary or risking the
host's toolchain. Proving the identity reacts to a size/mtime delta, and separately proving a miss
connects and registers (`TestSchemaCacheMissConnectsCapturesThenHits`), together cover the same
claim the integration row makes without conflating the two concerns.

**Out of scope, by instruction.** No `architecture/` file was read or edited; the coordinating
session owns documentation for this worklog.

### Fix session, 2026-10-03 (worklog-work, finding-fix pass after reviews 1 and 2)

Claude Sonnet 5, worklog-work skill, session identity `ef78c8b7-b6ce-4eb2-b334-3735c187e8a2`. Picked
up this phase because the README status board named it `Reopened (review 2)` with findings assigned
here from both rounds: R1-03, R1-16, R1-02 (shared with phase 2), R1-14 (shared with phase 2), R1-28,
R1-36 (review 1); R2-03, R2-06, R2-12, R2-20, R2-21, R2-25, R2-27 (review 2). Decisions D35–D44 were
read first and not re-litigated; D38 and D40 are what this session implements. Each finding's
resolution text is recorded inline under its own bullet in Review findings below; this note is the
session-level summary.

**Closed, fully, each proved red before green.** For every item below a failing test was run against
a reverted copy of the fix (full file swap for `schemacache.go`; the two specific hunks for
`querier_setup_tools.go`), observed to fail, then the fix was restored and the same test observed to
pass; the exact commands are under Verification.

- **R1-03/D38** — `Identity.ArgsDigest` (a hex SHA-256 over args, order preserved) replaces the
  verbatim `Identity.Args`; no raw arg is stored anywhere in the record, not even an informational
  copy, since any element of args can carry the documented `mcp-remote --header "Authorization:
  Bearer ..."` credential. `TestSchemaCacheArgsAreDigestedNotStoredVerbatim`.
- **R2-03/D40** — Two independent gaps closed together, since the finding's own corrective action
  named both. First, `Identity.Script` fingerprints the first `args` entry resolving to an existing
  file on disk, closing the gap where `resolveExecutable` alone only ever fingerprints the launcher
  (`node`, `npx`, `uvx`, ...) and never the interpreter-launched server's own script —
  `TestSchemaCacheMissOnScriptFileDelta` reproduces the review's exact probe (rewrite the launched
  script, confirm the previously byte-identical key now changes) and
  `TestSchemaCacheScriptFingerprintAbsentWhenNoArgResolvesToAFile` pins the npx-package-name case,
  where no args entry is a real path, as still infallible. Second, `Lookup`'s freshness bound
  (`schemacache.go`) lost its `identity.URL != ""` guard and now applies to every entry —
  `TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo` replaces the test that pinned the
  opposite rule phase 3 originally shipped. The two remaining mechanisms the finding enumerated (a
  tools-list-changed watcher and `newCacheInvalidatingConn` on the stdio path) are phase 4's to wire
  per R2-16, which this finding's own corrective action already scoped as optional; not required to
  close this reopening, and not touched here.
- **R1-16/D40** — `BuildIdentityWithScopes` now sets `Scopes` only when `server.Command == ""`, so a
  command-based server declaring an `auth` block (which `validateTransport` does not reject) computes
  the identical identity under `BuildIdentity` and `BuildIdentityWithScopes`; setup and the listing
  can no longer disagree on its key. `TestSchemaCacheCommandServerIgnoresAuthScopes`.
- **R2-06/D44** — `setupMcpManager` now warns on the joined error from `findConfiguredMcpServers`
  unconditionally, before the `len(mcpServers) == 0` early-return rather than only inside it, and
  additionally joins it (wrapped in `claierr.NewMcpServerStartup`, carrying the strict/degrade
  sentinel per invariant 14) into the explicit failures when the run is strict.
  `Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers` drives two real files on disk
  (one good, one with the concrete `"startup":"LAZY"` shape the finding named) through the real
  composition root and checks the good server's tools still register and the bad file is named in a
  warning; `Test_setupMcpManager_PerFileConfigErrorFailsStrictRun` checks the same error is also
  returned under strict startup. Phase 4's share of this finding (the same swallow, reached through
  `validateTransport` instead of `StartupMode.UnmarshalJSON`) closes at the same read site.
- **R2-12/D40 (plus R2-27, folded in)** — `main_mcp_lazy_e2e_test.go` now keeps both runs' captured
  stdout, sets `DEBUG=1` so `setupTooling`'s own per-tool debug line gives positive evidence
  `mcp_echo_echo` registered (rather than trusting exit status, which an unresolved `-t` name also
  returns as 0), and asserts the absence of "which doesn't exist" on both runs. The cold-run spawn
  assertion tightened from `!= 0` to `!= 1`. The config's `"startup"` field is dropped entirely, so
  the fixture now also proves the phase's flipped default (D16) instead of a pinned lazy posture,
  closing R2-27 in the same edit.
- **R2-20** — `setupMainTestConfigDir` (root `main_test_helpers_test.go`) now sets `CLAI_CACHE_DIR`
  under its own temp config dir; `main_mcp_lazy_e2e_test.go` dropped its own now-redundant line;
  `TestSetupTooling_injectedToolsRegisterWithoutWarning` (`internal/text`, which has no shared root
  helper) got its own line directly.
- **R2-21** — `internal/tools/mcp/testserver/main.go`'s spawn-log write now exits the fixture process
  non-zero on either the open or the close failing, instead of swallowing either with `err == nil`.

**Verified fixed elsewhere, not touched here, re-confirmed from this phase's own call site:**

- **R1-02/D35** and **R1-14** (stdio half) were fixed in phase 2's own fix session
  (`TestLazyConnectHonoursStrictMcpStartupForExplicitServers`,
  `TestLazyCacheMissConnectBoundAppliesToStdioHandshake`, both of which exercise this phase's
  `resolveLazyServerViaCache` call site directly). Re-run individually this session and still pass.
  R1-14's HTTP half stays open under phase 5's `internal/text/mcp_oauth.go`, unaffected by anything in
  this phase.

**Accepted or deferred, not fixed, each with a reason recorded against its own finding below:**

- **R1-28** (minor) — `exec.LookPath`'s `PATH` dependence is left as an accepted consequence: D40's
  freshness bound now backstops its worst case (one extra connect within the bound, never a
  permanently stale serve), so the open-ended risk the finding warned about is bounded.
- **R1-36** (note) — resolved as a side effect of D40's freshness-bound change rather than by a
  separate edit; the fail-open backward-clock behaviour is now stated directly in this phase's own
  Freshness section.
- **R2-25** (note) — deferred. A one-shot "entry exists but cannot be used" notice needs `Lookup` to
  report why it missed, which is a wider signature change (a third return value every caller would
  need to decide what to do with) than this note's severity warrants, and no blocker or major in
  either review round depends on it.

Repository rules followed: every fix above was preceded by a test proved to fail against a reverted
copy of the production change and to pass against the restored fix (see Verification); no test
spends money or reaches a vendor endpoint; no log-and-return — every new failure path returns a typed
error (`claierr.NewMcpServerStartup`) rather than being swallowed; no panic in a function returning an
error; every new return value is a named type (`*FileFingerprint`, `Identity`), never a bare bool or
int beside a value.

**Dupl.** One previously-undeclared clone was introduced while drafting
`TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo` against
`TestHttpSchemaCacheEntryExpiresOnFreshnessBound` (both built the same clock/capture/lookup sequence);
extracted into a shared `assertFreshnessBoundApplies(t, id)` helper before this session's final dupl
run, per the repository rule that duplicated code is abstracted. The final run reports 35 clone
groups, matching the count phase 2's own fix session already recorded as the shared tree's baseline
after phase 2's landing; the only clone group touching a file this session changed
(`querier_setup_tools_test.go:421,442` / `pkg/agent/mcp_setup_test.go`) pre-dates this session and was
already accepted in this phase's first execution.

**Verification**, run at host load 3.3–12.2 (`uptime`), below the load-8 threshold the README records
as making the race gate unreliable for most of the session; the one run at load 12.2 passed anyway,
recorded here rather than relied on:

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go run mvdan.cc/gofumpt@latest -l .` — no files need formatting.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — clean.
- `go fix ./...` — no changes.
- `go run github.com/mibk/dupl@latest -t 80 .` — 35 clone groups; see Dupl above.
- Red/green per finding, each run individually: `TestRedGreenFreshnessBoundAppliesToCommandIdentity`,
  `TestRedGreenCommandServerIgnoresAuthScopes` and `TestRedGreenArgsNeverStoredVerbatim` (scratch
  files, deleted after use) FAIL against a reverted `schemacache.go` and PASS against the fix;
  `Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers` and
  `Test_setupMcpManager_PerFileConfigErrorFailsStrictRun` FAIL against a reverted
  `querier_setup_tools.go` and PASS against the fix.
- `go test ./internal/tools/mcp/schemacache/... ./internal/text/... ./internal/tools/... . -race -cover -count=3 -timeout=30s`
  — all pass. `schemacache` coverage 86.8%, `internal/text` 84.3%.
- `go test ./... -race -cover -count=3 -timeout=30s -p 1` — full repository, run twice in this
  session (host load 3.3–9 and again at 12.17), both exit code 0.
- `TestLazyStartupE2EUnderRace` individually at `-race -count=3` — passes in 1.3 s total; the `go run`
  compile it spawns is already warm from the gate runs above, and no assertion in it encloses that
  compile with a tight bound (D41 is phase 6/8's to implement in full; this fixture was not made to
  depend on it).

### Sign-off fix session, 2026-10-03 (worklog-work, B3/B4/S1)

A later, unbounded holistic review read the whole effort at once rather than one phase at a time
and found two blockers this phase's own two prior review rounds missed, plus one correction to a
conclusion phase 8 recorded about this phase's own test file. Full text in the README's Sign-off
verdict section and the Sign-off review entry under the Feedback index.

**B4 — an empty tools array is captured and served as truth for the freshness bound.** `Cache.Capture`
(`internal/tools/mcp/schemacache/schemacache.go`) had no guard against an empty tools array:
`extractToolsArray` normalises an absent `tools` key to `[]`, and `RegisterTools` returns nil for an
empty array, so the capture was reached. A server that boots into a transient zero-tool state
latched zero tools for the full freshness bound, with neither invalidation signal able to fire
(unknown-tool needs a tool to call; the list-changed watcher needs a connection a cache hit never
dials). Fixed by a new `toolsArrayIsEmpty` helper and a guard at the top of `Capture` that refuses
(returns an error) when `tools` decodes to a zero-length JSON array — an error, never a panic, and
both production call sites (`querier_setup_tools.go`, `mcp_http_schema_cache.go`) already degrade a
`Capture` failure to a warning-and-connect, so neither needed a code change.
`TestSchemaCacheRefusesEmptyToolsArray`, proved red (against a reverted `schemacache.go`) before
green. Three pre-existing tests (`TestScopeChangeInvalidatesSchemaCacheEntry`,
`TestCredentialNeverReachesSchemaCache`, and the shared `assertFreshnessBoundApplies` helper plus
`TestSchemaCacheInvalidateRemovesEntry`, `TestSchemaCacheBackwardClockRecordsCaptureTimeVerbatim`)
seeded a warm cache with `[]byte("[]")` purely to exercise identity/freshness/invalidation mechanics
unrelated to the tools array's content; each now seeds a one-element placeholder tool instead, since
an empty array was never the point of any of them.

**B3 — a warm cache hides a server broken by anything other than its launcher changing.** For the
dominant `npx -y <package>` shape, only the launcher (`npx` itself) is locally fingerprinted
(`resolveExecutable`); a package upgrade or breakage is invisible at setup for the whole freshness
bound, and never reported at all if the model never calls the tool. Before this worklog, eager setup
warned on this every run. The fix does not reconnect: `Identity.LauncherOnlyFingerprint()`
(`schemacache.go`) reports true exactly when `Command`'s basename is one of a small, documented set
of generic interpreter/package-runner commands this worklog's own census and D40 already single out
(`node`, `npx`, `npm`, `uv`, `uvx`, `pipx`, `python`, `python3`, `docker`, `bunx`, `deno`) **and**
`Script` is absent (no args entry resolved to an on-disk file) — the shape where `Executable` alone
fingerprints a stable launcher binary, never the content it launches. A direct-binary server
(`Command` resolves to the server's own binary; D40's own text: "the design already wins") stays
silent, since its `Executable` already fingerprints the thing that would have changed.
`resolveLazyServerViaCache`'s cache-hit branch (`querier_setup_tools.go`) now calls
`warnIfLauncherOnlyFingerprint` before registering the cached entry's tools, printing one
`ancli.Noticef` line naming the server and its launcher. **Decision on where it belongs:** gated on
the identity, not printed unconditionally on every warm hit, so a direct-binary server's quiet warm
run (the common case for the ~15 percent of local servers this already covers correctly) stays
quiet, and the notice fires only for the shape that actually cannot detect its own breakage.
`TestWarmCacheLauncherOnlyServerPrintsSetupNotice` (positive case, proved red before green) and
`TestWarmCacheDirectBinaryServerPrintsNoNotice` (control, using the package's existing
`testServerBinary` fixture, already passed before the fix and continues to).

**S1 — the testserver build was inside the test clock, not the package.** `internal/text/querier_setup_tools_test.go`'s
`testServerBinary` built the shared stdio fixture behind a `sync.Once` *inside* a test function, so
the first test to call it paid the `go build`'s ~4s against the `-timeout=30s` race-gate clock.
Measured cold (`GOCACHE` pointed at a fresh throwaway directory, before this fix): `go test
./internal/text/ -race -count=3 -timeout=30s` passed at 25.353s, 84 percent of budget — matching
phase 8's own gate-sweep measurement. Phase 8's gate-sweep session recorded this as needing a
repackaging of `internal/text`'s MCP tests into their own package to fix properly; that conclusion
is corrected here, in place (`phase-8-gate-sweep.md`'s cold-cache table and the paragraph below it):
Go arms the `-timeout` alarm inside `(*testing.M).Run()`, which a package's `TestMain` calls
explicitly, so any work a `TestMain` does *before* calling it never counts against that alarm at
all — it does not need to be smaller, it needs to move. `internal/text` had no `TestMain`. Fixed by
adding one (`internal/text/main_test.go`): it builds the fixture once, before calling `m.Run()`, and
`testServerBinary` now only reads the result (`testServerBinPath`/`testServerBinErr`, package-level
vars set exactly once, before any test runs). The `sync.Once` and its two vars were removed from
`querier_setup_tools_test.go`, along with the now-unused `sync` and `os/exec` imports.
**Verified cold, as required:** instrumented `TestMain` with timestamps for one run (removed after
measuring), `GOCACHE` pointed at a fresh throwaway directory each time: the build itself takes
4.31s; `m.Run()` (the portion exposed to the `-timeout` alarm) takes 19.93s. Total `go test` time is
essentially unchanged (25.352s after vs. 25.353s before, since the same work happens either way —
the fix moves *where* the clock starts relative to the build, not the total wall time), but headroom
against the 30s bound rises from ~4.7s (30 − 25.353) to ~10.1s (30 − 19.93), consistent with the
review's own ~4.7s-to-~8.7s estimate and better than it. No new test: the evidence is the cold-cache
timing itself, run twice (instrumented, then clean) and reported above and in the README's
Sign-off-review feedback-index row.

**Verification, sign-off fix session:**

```
go build ./...                                             # clean
go vet ./...                                                # clean
go run mvdan.cc/gofumpt@latest -l .                         # no files listed
go run honnef.co/go/tools/cmd/staticcheck@latest ./...      # clean
go fix ./...                                                # no changes
go run github.com/mibk/dupl@latest -t 80 .                  # 36 clone groups, none new from this session's files
go test ./internal/tools/mcp/schemacache/... ./internal/text/... -race -cover -count=3 -timeout=30s
                                                             # ok; schemacache 82.9%, internal/text 85.3%
go test ./... -race -cover -count=3 -timeout=30s -p 1       # ok, every package, host load 0.9–2.1
```

## Review findings

### Review 1, 2026-10-02 — implementation review

**Status: Reopened (review 1).** The cache mechanism itself is sound and README invariant 2 holds
on every branch I could enumerate; the findings are about the identity's reach and one credential
carrier.

**Verified good, traced rather than taken on trust:**

- **README invariant 2 — no run-fact is ever persisted — holds on every path.** There are exactly
  two production `Capture` call sites, `internal/text/querier_setup_tools.go:337` and
  `internal/text/mcp_http_schema_cache.go:77`, and neither is reachable unless the connection
  construction, `mcp.Handshake` **and** `mcp.RegisterTools` all returned nil. A connect failure, a
  handshake failure, a registration failure, a credential-source failure, an `AuthChallengeError`
  and an interactive-flow failure all return before it. A tool error cannot reach it at all,
  because `Capture` only ever runs during setup. `Invalidate` only removes. The persisted payload
  is identity, protocol version, `serverInfo` and the tools array — all content-determined.
  `TestSchemaCacheNeverPersistsConnectFailure` and `TestSchemaCacheNeverPersistsAuthFailure` both
  drive the real `setupMcpManager` and scan the real cache directory; they are substantive, not
  seam tests.
- **Delta validation is real and structural.** The entry's filename *is* `sha256(json(Identity))`
  (`internal/tools/mcp/schemacache/schemacache.go:67-74`), so any component change is a miss with
  no comparison needed, and `Lookup` re-compares the marshalled identity as defence in depth. An
  unresolvable executable or unreadable envfile collapses to a nil fingerprint that can never
  match an entry written after a successful spawn, so it degrades to a miss and the real failure
  surfaces at connect, exactly as `BuildIdentity`'s doc comment claims.
- Cache files are 0600, written temp-plus-rename, with no temp file left on any failure branch.
  The command-based path carries no time bound and
  `TestSchemaCacheCommandIdentityHasNoFreshnessBound` pins that.
- `TestSchemaCacheHitRegistersToolsWithoutTransport` counts **real** process births through the
  fixture's own `TEST_SERVER_SPAWN_LOG`, and asserts the cold run spawned before asserting the
  warm run did not, so it cannot pass by never spawning. This is the model the tests named in
  R1-25 should follow.

**Findings**

- [x] **R1-03** (blocker) — **a credential written into `args` is persisted verbatim into a cache
  file, breaking README invariant 6** ("A credential never appears in a log line, an error string
  **or a cached file**"). `BuildIdentity` (`internal/tools/mcp/schemacache/schemacache.go:79-88`)
  digests `Env` through `envDigest` but stores `Args` verbatim, and the README record format
  specifies that. Verified by probe: a server configured as
  `{"command":"npx","args":["-y","mcp-remote","https://x/mcp","--header","Authorization: Bearer sk-live-…"]}`
  produces a cache entry containing `"Authorization: Bearer sk-live-…"` in plain text, while the
  *same* secret placed in `env` is absent. The asymmetry is the tell: `Env` was digested precisely
  because it carries secrets. The precondition is honest — clai expands nothing, so the operator
  must have written the token literally into the server JSON — but that is the documented
  `mcp-remote` shape, and the README's own census names `mcp-remote` at 732,960 weekly downloads
  while stating "clai is currently in the group that needs such a bridge". The same verbatim args
  also reach the `DEBUG` dump at `querier_setup_tools.go:148`, which is the carrier R1-10 shows is
  covered only by a tautological test.
  Corrective action: digest `Args` the way `Env` is digested — the identity must still react to an
  args change, so the field cannot simply be dropped — and keep at most a non-secret informational
  copy, following the dirscope convention the README already cites ("abs_path is informational
  only"). If the maintainer prefers to accept the exposure, invariant 6 and the record format must
  both say so in writing.

  **Resolved (fix session, 2026-10-03), per D38.** `Identity.Args` is gone; `Identity.ArgsDigest`
  (`schemacache.go`'s `argsDigest`, a hex SHA-256 over args joined one per line, order preserved)
  now carries it, exactly as `EnvDigest` already does, and no raw arg is stored anywhere in the
  record, not even an informational copy, since every element of args can carry a secret in the
  documented shape and no single element is reliably safe.
  `TestSchemaCacheArgsAreDigestedNotStoredVerbatim` plants a secret in the exact
  `mcp-remote --header "Authorization: Bearer ..."` shape, marshals the identity, and asserts the
  secret's absence while asserting the digest still changes when args changes. The README record
  format's `"args": ["server.js"]` field is replaced with `"args_digest": "<hex sha256>"` to match
  (D38's own Replaces column names this edit).
- [x] **R1-16** (major, shared with phase 7) — **setup and the `clai tools` listing compute
  different cache keys for a command-based server that declares `auth.scopes`.** Setup uses
  `BuildIdentity` (`internal/text/querier_setup_tools.go:298`); the listing uses
  `BuildIdentityWithScopes` (`internal/tools/mcp/schemacache/schemacache.go:332`), which sets
  `id.Scopes` whenever `server.Auth != nil` with no command/url discrimination (`:98-104`).
  `serverconfig.validateTransport` does not reject an `auth` block on a command-based server, so
  `{"command":"node","args":["s.js"],"auth":{"scopes":["read"]}}` parses. Setup then writes under
  the scopeless key, the listing looks up the with-scopes key, misses permanently, and per D21
  omits the server **silently** — a server the operator just used vanishes from `clai tools`
  forever with no diagnostic. The doc comment at `schemacache.go:319` claims it "builds the
  identity exactly as a live setup would", which is false in this case. The phase-7 suite cannot
  catch it: `captureListingCacheEntry` (`internal/tools/mcp_listing_test.go:50`) warms the cache
  with the listing's *own* builder, so every test is self-consistent by construction on the one
  property that matters.
  Corrective action: have both sides call one function — either make
  `BuildIdentityWithScopes` ignore `Auth.Scopes` when `Command != ""`, or switch
  `querier_setup_tools.go:298` to it — and add a test that warms through production setup and
  reads back through `ListCachedServers`.

  **Resolved (fix session, 2026-10-03), per D40.** `BuildIdentityWithScopes` now sets `Scopes`
  only when `server.Command == ""` (i.e. an endpoint-based server) and `server.Auth != nil`; a
  command-based server's identity is therefore byte-identical whichever builder computes it, so
  setup and the listing can never disagree on its key again. `TestSchemaCacheCommandServerIgnoresAuthScopes`
  pins the two builders' agreement directly; `Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers`
  and the pre-existing schema-cache setup suite exercise the production call site this shares with
  the hit path unchanged.
- [x] **R1-02** (blocker, owned with phase 2) — this phase flipped the default to `lazy` (D16),
  which is what makes the strict-startup hole in `effectiveStartupMode` reachable with a warm
  cache. Full detail and corrective action in phase 2's R1-02.
- [x] **R1-14** (major, owned with phase 2) — `connect_timeout_seconds` is ignored on this phase's
  cache-miss path. Detail in phase 2's R1-14.

  **Verified fixed (2026-10-03), owned and closed by phase 2.** `TestLazyConnectHonoursStrictMcpStartupForExplicitServers`
  (R1-02) and `TestLazyCacheMissConnectBoundAppliesToStdioHandshake` (R1-14's stdio half, which is
  the half this phase's own cache-miss path reaches) both pass at `-race -count=3`. The HTTP half of
  R1-14 stays open under phase 5's `internal/text/mcp_oauth.go`, per the feedback index; nothing in
  this phase's own call site is affected.
- [ ] **R1-28** (minor) — `resolveExecutable` (`schemacache.go:139`) resolves the command through
  `exec.LookPath`, reintroducing the `PATH` dependence the identity design deliberately excluded
  on the grounds that it "would change the key with every shell". A different shell resolving
  `node` to a different path is a spurious miss. It fails in the safe direction, but it quietly
  erodes the warm-cache claim the worklog's headline rests on, so it should be stated as an
  accepted consequence or keyed on the basename plus fingerprint instead.

  **Accepted as a known consequence, not fixed (minor; does not reopen the phase).** Left on
  `exec.LookPath` unchanged: D40's freshness bound now backstops exactly this risk (a spurious miss
  from a different shell's `PATH` only ever costs one extra connect within the bound, never a stale
  serve), so the severity this finding warned about is now bounded rather than open-ended. Keying on
  basename plus fingerprint instead remains a reasonable future improvement but is not required to
  close this reopening.
- [x] **R1-36** (note) — `schemacache.go:242`: freshness is judged with
  `clock().Sub(capturedAt) > bound`, so a clock that moves backwards makes an endpoint entry
  immortal (a negative duration can never exceed the bound). This phase's row says "no validity
  decision in this phase depends on [the clock]", which phase 4 made false when it added the
  12 h bound. One sentence in phase 4 recording the fail-open behaviour is enough; it is an
  optimisation, not a correctness surface.

  **Resolved as a side effect of D40 (2026-10-03).** The freshness bound now applies to every
  entry, command-based included, so the fail-open backward-clock behaviour this finding named is no
  longer specific to phase 4's endpoint half; the Freshness section above and `Lookup`'s own doc
  comment both state it directly in this phase now, which is what the finding asked for.

### Review 2, 2026-10-02 — implementation review, round 2

Status: **Reopened (review 2)**, in addition to the round-1 reopening. One blocker.

Round 1 found that `args` is persisted verbatim (R1-03) and that `exec.LookPath` reintroduces a
`PATH` dependence (R1-28). Round 2 asked the next question: given that the identity resolves an
executable at all, *which* executable does it resolve, and what happens to an entry that should
have been invalidated.

**Verified good:**

- A cache failure is never fatal on any path. `utils.GetClaiCacheDir` failing, `schemacache.New`
  failing, `MkdirAll` failing inside `Capture`, `CreateTemp` failing, the rename failing — every
  one degrades to a warning and a connect (`querier_setup_tools.go:358-364`, `:394-397`,
  `:336-340`; `schemacache.go:269-305`). `New` deliberately does not create the directory, so a
  run that never captures never touches the filesystem.
- `Capture` is atomic (temp file plus rename, `schemacache.go:290-305`), so a caller that returns
  has either a durable entry or a reported failure.
- `Lookup`'s defence-in-depth re-comparison of the stored identity against the requested one
  (`schemacache.go:236-247`) is correct and would catch a corrupted write.

**Findings:**

- [x] **R2-03** (blocker) — **A command-based entry cannot be invalidated at all for the spawn form
  this worklog measured.** Four mechanisms are supposed to keep an entry honest. For a
  command-based server, all four are absent or blind:

  1. *Delta validation.* `resolveExecutable(server.Command)` (`schemacache.go:135-147`)
     fingerprints the **resolved command**, not the server. For `{"command":"node","args":["…/dist/index.js"]}`
     that is the `node` binary; for `npx`, `uvx`, `python` or `docker` it is the launcher. Probe:
     built `BuildIdentity` for a `node`-launched script, rewrote the script with a different size
     and mtime, rebuilt the identity — the key was byte-identical (`cbc23603a0aa98ff…`), and the
     fingerprint was `{Path:/home/lorkin/.local/bin/node Size:122889056 …}`. Per this worklog's own
     census (60.8 percent npm, 23.9 percent PyPI) that is roughly 85 percent of local servers, and
     it includes `npx -y @modelcontextprotocol/server-filesystem`, the exact command the Strategy
     section measured.
  2. *Freshness bound.* `Lookup` applies it only when `identity.URL != ""` (`schemacache.go:242`),
     by the parameters table's own "no time bound" rule for command-based servers.
  3. *tools-list-changed.* `watchForToolsListChanged` is started only from the HTTP paths
     (`mcp_http_schema_cache.go:68`, `:107`). `resolveLazyServerViaCache` starts no watcher.
  4. *Unknown-tool failure.* `newCacheInvalidatingConn` wraps only the HTTP paths
     (`mcp_http_schema_cache.go:47`, `:71`). The stdio path hands `mcp.NewConnector` and
     `mcp.NewResolvedConnector` through unwrapped (`querier_setup_tools.go:304`, `:331`).

  Concrete failure: a user runs `clai` once with `npx -y @modelcontextprotocol/server-filesystem`,
  the entry is captured. The package publishes a release that renames `read_file` to
  `read_text_file`. Every subsequent run advertises `read_file` from cache forever; each call
  returns the server's unknown-tool error; nothing invalidates; no command clears the cache. The
  Definition-of-success row "A changed server binary or envfile invalidates the cache" is met only
  when `command` *is* the server binary.

  Corrective action: fingerprint the first `args` entry that resolves to an existing file, in
  addition to the resolved command, and apply the freshness bound to every entry rather than only
  endpoint-based ones. Wiring `newCacheInvalidatingConn` onto the stdio path is worth doing too,
  but on its own it is inert — see **R2-16**.

  **Resolved (fix session, 2026-10-03), per D40.** Both corrective-action items are done: (1)
  `Identity.Script` (`schemacache.go`'s `resolveScriptFingerprint`) fingerprints the first `args`
  entry that resolves to an existing, regular file, closing the exact `node script.js` gap the
  probe demonstrated — `TestSchemaCacheMissOnScriptFileDelta` reproduces that probe directly: it
  rewrites the launched script and asserts the identity key changes, where it was previously
  byte-identical. (2) `Lookup`'s freshness bound now applies unconditionally
  (`c.clock().Sub(rec.CapturedAt) > c.freshnessBound`, no more `identity.URL != ""` guard), closing
  mechanism 2 for every command-based entry, including an `npx`-launched one with no on-disk script
  argument at all (`TestSchemaCacheFreshnessBoundAppliesToCommandIdentityToo`). Mechanisms 3 and 4
  (a tools-list-changed watcher and `newCacheInvalidatingConn` on the stdio path) remain phase 4's
  to wire, per R2-16, which this finding's own corrective action already scoped as optional ("worth
  doing too, but on its own it is inert"); they are not required to close this reopening.

- [x] **R2-06** (major, shared with phase 4) — **A per-file config error is dropped whenever any
  other server parses, so this phase's and phase 4's new validation silently deletes a server.**
  `serverconfig.FindConfiguredServers` correctly collects per-file errors and returns them joined
  (`serverconfig/serverconfig.go:34-67`), but `setupMcpManager` reads `err` only inside
  `if len(mcpServers) == 0` (`querier_setup_tools.go:150-156`). With two or more config files the
  joined error is discarded: not returned, not warned, not logged. That swallow predates this
  worklog, but this worklog adds two new ways to land in it — `StartupMode.UnmarshalJSON` and
  `validateTransport` — and so turns it into a user-visible regression. Concrete failure: a user
  with `linear.json` and `filesystem.json` adds `"startup": "LAZY"` to one of them. That server
  vanishes entirely, `clai tools` stops listing it, the model silently loses its tools, and no
  message is printed. At `c3867d3` an unknown `startup` value was simply ignored and the server
  started. Corrective action: warn on the joined error unconditionally, and return it when the run
  is strict.

  **Resolved (fix session, 2026-10-03), per D44.** `setupMcpManager` now warns on the joined error
  from `findConfiguredMcpServers` unconditionally (`ancli.Warnf("failed to parse mcp server
  config(s): %v", err)`), before the `len(mcpServers) == 0` branch rather than only inside it, and
  additionally joins it into the explicit failures (wrapped in `claierr.NewMcpServerStartup`, so it
  carries the strict/degrade sentinel per invariant 14) when the run is strict. Two new tests drive
  the real composition root with two real files on disk, one good and one carrying the exact
  invalid-`startup`-value shape from the concrete failure: `Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers`
  (ambient, ensures the good file's tools still register and the bad file is named in a warning)
  and `Test_setupMcpManager_PerFileConfigErrorFailsStrictRun` (strict, ensures the error is also
  returned). Phase 4's own share (the same swallow reached through `validateTransport`) is covered
  by the same fix, since both land in the one joined `err` this change now reads unconditionally.

- [x] **R2-12** (major, shared with phase 7) — **The lazy end-to-end fixture's central claim is
  unasserted.** `main_mcp_lazy_e2e_test.go:32-33` says the warm run "must register the same tools
  without spawning the server again". The test asserts exactly two things: exit status 0 (`:66`)
  and an unchanged spawn count (`:69`). Exit status cannot carry the claim, because an unresolved
  `-t` name is a warning and a `continue` (`querier_setup_tools.go:446-451`) and `setupTooling`
  returns nil. A warm run that registered **zero** tools — an empty cached `tools` array, a
  `RegisterTools` regression, a schema-patch rejection — passes both assertions. The property is
  tested at the seam (`schema_cache_setup_test.go:77-100`,
  `TestCacheHitAndMissRegisterIdenticalToolSets`) and dropped on crossing to the composition root,
  which is the one place it would catch a wiring fault. Corrective action: the test already
  captures stdout and stderr and discards stdout with `_` at `:51` and `:63`; keep them and assert
  the warm run printed no "which doesn't exist" warning, plus assert the tool's presence
  positively.

  Two smaller points in the same fixture, filed here rather than as their own rows: `:58` asserts
  only `coldSpawns != 0`, so a regression that spawned five times on a cold cache passes; and `:43`
  pins `"startup":"lazy"` explicitly, so the fixture cannot prove the flipped default even though
  dropping the field would prove it at no cost.

  **Resolved (fix session, 2026-10-03).** `main_mcp_lazy_e2e_test.go` now keeps both captured
  stdout buffers (`coldStdout`, `warmStdout`) instead of discarding them, and sets `DEBUG=1` so
  `setupTooling`'s existing per-tool debug line gives positive, production-printed evidence that
  `mcp_echo_echo` registered on both runs; both runs also assert the absence of "which doesn't
  exist". The cold-run spawn assertion is now `coldSpawns != 1` → fail (exactly one connect for one
  server), not merely non-zero. The config's `"startup"` field is dropped entirely, so the test now
  also proves the phase's flipped default (D16) rather than a pinned lazy posture (closing R2-27 in
  the same edit, per the feedback index's note that it folds into this row).

- [x] **R2-20** (minor) — **The cache-injection parameters row is unmet outside `internal/text`.**
  The row reads "Cache directory injection wherever `setupMcpManager` is exercised by a test → a
  per-test temporary directory, never the developer's real cache directory". `setupMainTestConfigDir`
  (`main_test_helpers_test.go:21-98`) sets `CLAI_CONFIG_DIR` but not `CLAI_CACHE_DIR`, so every root
  end-to-end test that reaches `setupTooling` or `clai tools` resolves the developer's real
  `~/.cache/clai/mcpSchemas`. The same holds for
  `TestSetupTooling_injectedToolsRegisterWithoutWarning` (`internal/text/querier_setup_tools_test.go:636`).
  Nothing is written today, because an empty `mcpServers` directory returns from `setupMcpManager`
  before any `Capture` and `New` does not create the directory — so this is a latent hazard, one
  config file away from a test writing into a developer's cache, not a present fault. Corrective
  action: one `t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))` in the shared root
  helper, which also lets `main_mcp_lazy_e2e_test.go:39` drop its own line.

  **Resolved (fix session, 2026-10-03), exactly as proposed.** `setupMainTestConfigDir`
  (`main_test_helpers_test.go`) now sets `CLAI_CACHE_DIR` to a directory under its own `t.TempDir()`-rooted
  config dir, and `main_mcp_lazy_e2e_test.go` no longer sets it itself. `TestSetupTooling_injectedToolsRegisterWithoutWarning`
  (`internal/text`, which has no shared root helper) got its own `t.Setenv("CLAI_CACHE_DIR", ...)` line
  directly.

- [x] **R2-21** (note) — **The spawn counter the warm-cache proof rests on is fail-open.**
  `internal/tools/mcp/testserver/main.go:23` is `if f, err := os.OpenFile(...); err == nil`. A spawn
  whose log write fails is indistinguishable from no spawn, and "no spawn" is precisely what the
  warm half of every spawn-count test asserts. The cold assertion validates the mechanism within
  the same run, which is the saving grace, but only before the thing under test. Corrective action:
  make the write fatal to the fixture process (exit non-zero on failure) so a broken counter fails
  loudly rather than silently proving the claim.

  **Resolved (fix session, 2026-10-03), exactly as proposed.** `internal/tools/mcp/testserver/main.go`
  now exits non-zero (after a stderr line) on either the open or the close failing, rather than
  swallowing either with `err == nil`. The full `-race -count=3` suite was re-run afterward with no
  regression, confirming no test relies on a silently-broken spawn log.

- [ ] **R2-25** (note, deferred) — **`Lookup` is silent where `Capture` is loud.** A corrupt, unreadable or
  mismatched entry is a miss with no diagnostic (`schemacache.go:220-247`), while a write failure
  warns on every run (`querier_setup_tools.go:338`). A permanently unreadable cache directory
  therefore produces a silent connect every run with nothing to tell the user why their warm cache
  never warms. The silence is right for the hit/miss decision; what is missing is a one-shot notice
  when an entry exists but cannot be used.

  **Deferred, not fixed (note; does not reopen the phase).** A one-shot notice needs `Lookup` to
  report *why* it missed, which is a wider signature change (today `Lookup` returns `(Record,
  bool)`, correctly typed per the repository's own rule against a bare bool beside a value, but a
  diagnostic reason is a third thing every caller would need to decide what to do with) rather than
  a one-line fix, and no blocker or major in either review round depends on it. Left for a session
  that owns the call sites that would consume the diagnostic.

### Review 3, 2026-10-03 — sign-off review (holistic)

**Status: Reopened (sign-off).** The first pass to read the whole effort at once rather than one
phase at a time, after reviews 1 and 2 had already closed this phase. Found two blockers neither
prior round caught, both specific to this phase's cache mechanism, plus one correction to a
conclusion phase 8 recorded about this phase's own test file. Full detail in the README's Sign-off
verdict section and the Sign-off review feedback-index entry.

**Findings**

- [x] **B4** (blocker) — `Cache.Capture` had no guard against an empty tools array, so a server that
  booted into a transient zero-tool state latched zero tools for the full freshness bound, with
  neither invalidation signal able to fire. Probe: `Capture(empty) accepted=true, Lookup
  hit=true, tools=[]`.

  **Resolved (sign-off fix session, 2026-10-03).** `Capture` now refuses an empty tools array via
  `toolsArrayIsEmpty`. `TestSchemaCacheRefusesEmptyToolsArray`. Full detail in Implementation notes
  above.

- [x] **B3** (blocker) — For the dominant `npx`-launched shape, only the launcher is locally
  fingerprinted, so a server broken by anything else is invisible at setup for up to twelve hours,
  and silent forever if the model never calls the tool. Probed end to end through
  `setupMcpManager`: `err=<nil>`, zero spawns, tool still advertised.

  **Resolved (sign-off fix session, 2026-10-03).** `Identity.LauncherOnlyFingerprint` gates a new
  setup-time notice on the cache-hit path, restoring (for this shape) the warning eager setup always
  gave. `TestWarmCacheLauncherOnlyServerPrintsSetupNotice`,
  `TestWarmCacheDirectBinaryServerPrintsNoNotice`. Full detail, including the decision on where the
  notice is gated, in Implementation notes above.

- [x] **S1** (minor) — `querier_setup_tools_test.go`'s `testServerBinary` built the shared fixture
  inside a test's `sync.Once`, charging its ~4s `go build` against the `-timeout=30s` race-gate
  clock; measured cold at 25.3s, 84 percent of budget. Phase 8's gate-sweep session recorded this as
  needing a package split; that conclusion was wrong.

  **Resolved (sign-off fix session, 2026-10-03).** A new `internal/text/main_test.go` `TestMain`
  builds the fixture before calling `m.Run()`, moving the cost outside the `-timeout` alarm's window
  rather than shrinking it. Verified cold: alarm-exposed duration drops from ~25.3s to ~19.9s,
  headroom rises from ~4.7s to ~10.1s. Phase 8's own stale conclusion is corrected in place in
  `phase-8-gate-sweep.md`. Full detail in Implementation notes above.
