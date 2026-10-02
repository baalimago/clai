# Phase 3 — Tool schema cache

**Status:** Not Started

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

- **Which environment is digested.** Only the server's own configured environment: the `env` map
  from its config file, merged with the contents of its `envfile` when one is set. The inherited
  process environment is deliberately **excluded**, because digesting it would change the key with
  every shell and defeat the cache entirely, even though the spawned process does inherit it.
- **An absent envfile.** When no envfile is configured, the identity's envfile component is a
  canonical absent marker rather than a zero size and a zero time, so "no envfile" and "an empty
  envfile" are distinguishable keys.
- **An unresolvable executable.** When the command does not resolve on the path, the executable
  component is the same canonical absent marker. The entry is then a miss, setup connects, and the
  spawn failure surfaces there rather than being pre-judged by the cache.

The identity covers everything that can change which tools a command-based server exposes:

| Identity component | Included |
| --- | --- |
| Command and arguments | yes |
| Environment map, as a digest of sorted key and value pairs | yes |
| Envfile size and modification time | yes |
| Resolved executable path, size and modification time | yes |

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

The evidence for a command-based server is local and exact, so there is no time bound. The entry is
valid while the recorded sizes and modification times of the executable and the envfile still match,
and while the environment digest still matches. Any delta is a miss. Validating with a size and
modification-time delta rather than dropping a component from the key is the rule already
established for this repository's foreign conversation index.

The record carries its capture time, taken from the injected clock this phase owns, so a later phase
can add a time-bounded rule for a transport that has no local evidence without changing the record
format.

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
| Connect failure | Nothing written | `TestSchemaCacheNeverPersistsConnectFailure` |
| Corrupt entry | Treated as a miss | `TestSchemaCacheCorruptEntryIsTreatedAsMiss` |
| Cache write failure | Setup proceeds by connecting | `TestSchemaCacheWriteFailureDoesNotFailSetup` |
| The startup-mode default | Flipped to lazy in this phase | `TestStartupDefaultIsLazyAfterCache` |
| The cache constructor | Requires its directory, so a caller that omits it fails to construct rather than falling back to the real cache directory | `TestCacheConstructorRequiresDirectory` |
| The lazy path | Covered by its own end-to-end fixture under the race detector | `TestLazyStartupE2EUnderRace` |

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

Not started.

## Review findings

None.
