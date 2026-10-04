# Phase 7 — Built-in shadow advisory

**Status:** Complete — reviews 1 and 2 reopened this phase on R1-16 (shared with phase 3), R1-22,
R1-23, R1-35, R2-12 (shared with phase 3), R2-17 and R2-23; every finding fixed and verified in the
2026-10-03 fix session (see Implementation notes below). A later holistic sign-off review reopened
this phase again on S2; fixed and verified in the 2026-10-03 sign-off fix session (see
Implementation notes below).

Back to [README](README.md).

## Goal

Give `clai tools` an MCP tool set it has never had, then mark the entries a clai built-in already
covers, so the cheapest reduction available is visible rather than folklore.

## Specification

### Why this is worth a phase

The census under Strategy in the README shows the local half of the ecosystem is dominated by Node
and Python packages in the measured cost class, and the usage figures show the filesystem server
among the most installed of them. Every capability that server provides is already a clai built-in
running in-process at no extra cost: the tool-name enumeration in `pkg/text/models/tools.go`
carries reading, writing, patching, listing, tree walking, searching, stream editing, head and tail
slicing, copying, syncing, temporary directory creation, type detection, line counting and row
slicing. A user who replaces that server with built-ins removes a process from every run.

This phase reports; it never resolves. Silently dropping a configured tool because clai believes it
has an equivalent would be a surprising behaviour change and would hide a real difference in
semantics. Decision D13 records that.

### Behaviour

**Amended by the sign-off review (S2, 2026-10-03).** This section originally specified a per-tool
marker, a trailing annotation on the tool's own listing line of the shape `[shadowed by built-in:
cat]`. The sign-off review found that gating it on `server.Command != ""` conflates transport with
locality: a command-based server can itself be a remote bridge (the documented `npx -y mcp-remote
https://...` shape, which `schemacache.go`'s own comments already name), so a bare remote-tool-name
match there is a claim about a capability the marker has no actual evidence for — cat cannot read a
Notion page. Cutting the marker outright was the review's other offered option; demoted to a single
footer line instead, since a footer can carry the same "a local built-in may already cover this"
information with no claim about any specific listed tool to be wrong about (the smaller of the two
corrective actions the review named).

The tool listing therefore gains one trailing advisory line, printed once after every tool's own
line, of the shape `Note: a configured MCP server's tool may already be covered by a local
built-in: cat, mkdir, write_file. Check each server's own tools before assuming redundancy.` —
naming every distinct, available built-in a declared mapping matched, deduplicated and sorted, with
no attribution to which server or tool triggered which entry. No per-tool line is ever annotated.

The mapping is a package-level `map[string]pub_models.ToolName` in the file the README code-layout
section names: the MCP tool's remote name without its server prefix, to the built-in that covers it.
A built-in is "available on this host" exactly when the process-global tool registry holds it, which
is how `registerLocalTools` already gates built-ins whose executable is absent; the footer asks the
registry rather than probing the filesystem itself.

The footer is contributed to only by a command-based server's tools (review 1/2 R1-22, corrected
again in R2-17: the Specification originally missed the remote branch). The declared mapping names
a capability of a local server; an endpoint-based server's `read_file` may read a file on a remote
host clai's local built-in cannot reach, so a url-based server's tools never contribute to the
footer regardless of a bare-name match against the mapping. This restriction is necessary but, per
the sign-off review, no longer sufficient on its own — see S2 above for why the footer, not a
per-tool claim, is the mechanism that makes the remaining imprecision safe.

### The listing has no MCP tools today, and this phase gives it some

This is the load-bearing half of the phase, and it is stated before the marker, because a marker
over an empty set marks nothing. The README's existing-code table records the verified facts: the
listing's `OnRun` calls `Init()` then `List()`, `List` reads the process-global registry, that
registry is populated only by `registerLocalTools`, and `setupMcpManager` is reached only from the
query path. The per-run registry rule forbids fixing this by writing MCP tools into the global
registry, and that rule exists for a good reason: concurrent setups would overwrite each other.

So this phase adds a **cache-only** listing source. For each configured server it builds the
identity and reads its schema-cache entry, exactly as the cache phase does, and lists the tools it
finds into a listing-scoped tool set that is never the global registry. It never connects and never
spawns, because a listing is not a run and must stay as cheap as it is today.

The listing shows **only servers that have succeeded**. A cache entry exists only where a handshake
completed, because the cache never records a run-fact, so an entry is itself the evidence of
success. A server with no entry — never run, or last run failed — contributes nothing and is not
listed and not flagged: a tool listing is not a diagnostic surface, and naming servers that have no
tools would be noise in the one output a user reads to find out what they can call. Its entry
appears after its first successful query run. Decision D21 records that this also
removes an untruth, since both the command's help text and `architecture/tooling.md` claim the
listing shows MCP tools, which it never has.

The mapping is declared data, not a heuristic on names: a guess that marks an unrelated tool as
redundant is worse than no marker. A tool with no declared mapping is simply unmarked.

Tool selection, registration, schemas sent to a model and execution are all unchanged. The marker
is presentation only, and the listing's existing alias grouping and sort order are preserved.

The listing must remain correct when no MCP server is configured, when the config directory is
absent, and when a server is configured but lazily unconnected, because after phase 3 the listing
may be served entirely from cached schemas.

### Invariants

| Bound actor | Mechanism | Test |
| --- | --- | --- |
| An MCP tool with a declared, available covering built-in | No per-tool marker ever appears on its own line; the built-in is named once in List's trailing footer (amended by sign-off review S2) | `TestToolsListNeverEmitsPerToolShadowMarker`, `TestShadowFooterNamesAvailableBuiltin` |
| An MCP tool with no declared mapping | No footer contribution from it | `TestShadowFooterOmitsUnregisteredBuiltin` |
| Tool selection and registration | Unchanged by the advisory | `TestShadowAdvisoryDoesNotAlterSelection` |
| Schemas sent to a model | Unchanged by the advisory | `TestShadowAdvisoryDoesNotAlterSelection` |
| A configured server with a cache entry | Its tools appear in the listing, from cache, with no process started | `TestToolsListShowsMcpToolsFromCache` |
| A configured server with no cache entry | Omitted entirely; no tool invented, nothing connected, nothing flagged | `TestToolsListOmitsServersWithoutCacheEntry` |
| The process-global registry | Never receives an MCP tool | `TestToolsListNeverWritesMcpIntoGlobalRegistry` |
| A run with no MCP servers configured | Listing succeeds with no footer | `TestShadowAdvisoryHandlesNoMcpServers` |
| A lazily unconnected server served from cache | Listed, and contributes to the footer, from its cached schemas | `TestShadowFooterCoversLazyCachedServer` |
| An absent config directory | Listing succeeds, no error | `TestShadowAdvisoryAbsentConfigDirIsNotAnError` |
| An endpoint-based (url) server's remote tool whose bare name matches a declared mapping | Never contributes to the footer, regardless of the name match (review 1/2 R1-22, R2-17) | `TestShadowFooterSkipsEndpointBasedServers` |
| A command-based server that is itself a remote bridge (`npx -y mcp-remote https://...`) exposing a bare-name match | No per-tool claim ever appears on its listing line; the footer, if it fires at all, names no server or tool (sign-off review S2) | `TestShadowFooterNeverAttributesToASpecificRemoteTool` |
| Two servers' tools matching the same built-in | Named once in the footer, not once per server | `TestShadowFooterDeduplicatesAcrossServers` |
| `create_directory` | Contributes `mkdir` to the footer (review 1/2 R1-23, R2-23) | `TestShadowFooterCoversCreateDirectory` |
| `get_file_info` | Never mapped: it returns size/mtime/permissions, a different question than `file_type`'s `file(1)` (sign-off review S2, correcting the original mapping) | `TestBuiltinShadowMapExcludesGetFileInfo` |

### Limits

This phase introduces no limit, threshold or tunable. The advisory is presentation only: it reads
the registry and a declared mapping, bounds nothing, and consumes no parameter. A limit added here
later needs a README parameters row first.

### Documentation

`architecture/mcp.md` gains the listing's cache-only source and the advisory. The MCP sentence in
`architecture/tooling.md` and the `tools` command's own help text are corrected in this phase, since
this is the phase that makes them true. It also gains a short section recording the recommended
posture that follows from the measurements: built-ins for local capability clai already has, the HTTP transport for services, and
stdio reserved for genuinely local capability clai lacks. The tools-command architecture note gains
the advisory's description, since it documents the listing surface. **Stale as of the sign-off
review (S2):** this note described a per-tool marker; the advisory is now a single footer line
(see Behaviour above). Not corrected here on explicit instruction — `architecture/` is the
coordinating session's to reconcile.

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Tool listing with a configured server exposing a file-reading tool | Fake stdio server whose tool name is in the declared mapping | Tool's own line unmarked; footer names the covering built-in once (amended, S2) | Footer rendered | Per-tool marker never rendered; tool not removed from the listing |
| Tool listing with a configured server exposing an unmapped tool | Fake stdio server with an unrelated tool name | Entry unmarked, no footer contribution from it | Listing rendered | No speculative footer entry |
| A run selecting that same MCP tool with an explicit selection | Fake stdio server, explicit selection | Tool is selected and callable | Tool registered as before | Advisory does not suppress selection |
| Tool listing with no MCP servers configured | Empty config directory | Listing renders built-ins only | Listing succeeds | No error about a missing directory |
| Tool listing with a configured server and a warm cache | Warmed schema cache, no server running | Its tools listed, unmarked; footer names the covering built-in | Cache read | No process started; nothing written to the global registry |
| Tool listing with a configured server and no cache entry | Empty cache | That server contributes nothing and is absent from the output | Listing still succeeds | No connection attempted; no diagnostic line about the server |
| Tool listing with a command-based server that is itself a remote bridge, exposing a bare-name match | Fake stdio server shaped like `npx -y mcp-remote https://...` | Tool's own line carries no per-tool claim (sign-off review S2) | Listing rendered | No per-tool attribution of a capability the bridge's locality cannot back up |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| MCP tools appear in the listing, read from cache, with no process started | `TestToolsListShowsMcpToolsFromCache` |
| A server with no cache entry is omitted, not flagged | `TestToolsListOmitsServersWithoutCacheEntry` |
| No MCP tool ever enters the process-global registry | `TestToolsListNeverWritesMcpIntoGlobalRegistry` |
| A tool matching a declared, available built-in never carries a per-tool marker, and the footer names the built-in once (amended by sign-off review S2) | `TestToolsListNeverEmitsPerToolShadowMarker`, `TestShadowFooterNamesAvailableBuiltin` |
| The advisory changes nothing about selection, registration or schemas | `TestShadowAdvisoryDoesNotAlterSelection` |
| The listing is correct with no servers configured | `TestShadowAdvisoryHandlesNoMcpServers` |
| A lazy server is listed and contributes to the footer from its cached schemas | `TestShadowFooterCoversLazyCachedServer` |
| An absent config directory is not an error | `TestShadowAdvisoryAbsentConfigDirIsNotAnError` |
| A command-based remote bridge's tool never carries a per-tool claim it cannot back up | `TestShadowFooterNeverAttributesToASpecificRemoteTool` |
| `get_file_info` is never mapped (corrected, sign-off review S2) | `TestBuiltinShadowMapExcludesGetFileInfo` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Config directory absent | Listing renders built-ins, no error | `TestShadowAdvisoryAbsentConfigDirIsNotAnError` |
| A configured server has no cache entry | Listing renders what it has and omits that server | `TestToolsListOmitsServersWithoutCacheEntry` |
| A cache entry is corrupt | Treated as absent, as the cache phase already specifies, so the server is omitted | `TestToolsListOmitsServersWithoutCacheEntry` |
| Declared mapping names a built-in that is not registered on this host, because its executable is absent | Omitted from the footer rather than naming an unavailable built-in | `TestShadowFooterOmitsUnregisteredBuiltin` |
| Two MCP servers expose tools mapping to the same built-in | Both tools listed unmarked; the built-in named once in the footer, not once per server | `TestShadowFooterDeduplicatesAcrossServers` |

## Implementation notes

Session 2026-10-02 (phase 7 executor).

**Deviation, discovered necessary: the listing's config parser had to move to a new leaf
package, not into `internal/tools/mcp`.** The code-layout table said the listing's cache-only
entry point lives partly in `internal/tools/cmd.go` and partly in `internal/tools/mcp/schemacache`.
Building that required reading the same configured-server parse setup already does
(`findConfiguredMcpServers`/`validateMcpTransport`, private to `internal/text/querier_setup_tools.go`),
since the listing's identity must match setup's byte-for-byte or a warm entry would look like a
miss. `internal/tools` cannot import `internal/text` (the reverse import already exists, `text`
imports `tools`), so the parser had to live somewhere both sides can reach. The obvious
destination, `internal/tools/mcp`, turned out to be unusable: that package's own tests
(`manager_test.go`, `tool_test.go`) import `internal/tools`, and `internal/tools/cmd.go` needs to
call this parser — so if it lived in `mcp`, `go test ./internal/tools/mcp/...` would hit "import
cycle not allowed in test" (`mcp` test → `tools` → `mcp`). Resolved by relocating the parser to a
new leaf package, `internal/tools/mcp/serverconfig`, added as a code-layout row under this phase
per the executor-adds-a-row convention phases 2–5 already established. `findConfiguredMcpServers`
in `internal/text/querier_setup_tools.go` is now a one-line delegate to
`serverconfig.FindConfiguredServers`; every existing caller and test in that file is unmodified
and still green. `schemacache.DefaultDirName` (the "mcpSchemas" subdirectory name, previously a
private constant in `internal/text`) moved the same way, for the same reason: both setup and the
listing must resolve the identical schema-cache directory, and `schemacache` is the one package
both already import. Both relocations got their own code-layout row.

**Second deviation, same root cause: `internal/tools/cmd.go` cannot import `internal/tools/mcp`
either**, for the identical cycle reason (that package's tests import `tools`, and `tools` would
be importing `mcp`). So the listing decodes a schema-cache record's verbatim `tools/list` array
with its own small local type, `cachedRemoteTool` (same two JSON fields `mcp.Tool` has: name,
description, inputSchema), rather than importing `mcp.Tool`. This is the only place this phase
reads that shape outside `mcp.RegisterTools` itself; `TestShadowAdvisoryDoesNotAlterSelection`
proves the real `mcp.RegisterTools`/`mcp.NewTool` path (which does use `mcp.Tool`) is untouched by
any of this, since that test lives in `internal/tools`'s test binary and imports `mcp` the same way
`mcp`'s own tests import `tools` — safe in that direction, because `mcp`'s test binary never pulls
in `tools`'s test files.

**Mapping chosen.** `internal/tools/builtin_shadow.go`'s `builtinShadowMap` covers the ten real
tool names of `@modelcontextprotocol/server-filesystem` (the server the Goal section measures) that
have an unambiguous clai built-in: `read_file`/`read_text_file`/`read_multiple_files` → `cat`,
`write_file` → `write_file`, `edit_file` → `apply_patch`, `list_directory`/
`list_directory_with_sizes` → `ls`, `directory_tree` → `file_tree`, `search_files` → `find`,
`get_file_info` → `file_type`. `move_file`, `create_directory`, `list_allowed_directories` and
`read_media_file` are deliberately left unmapped: per D13/the phase's own caution ("a guess that
marks an unrelated tool as redundant is worse than no marker"), none of them has a clai built-in
that is actually the same operation (`mkdir` lacks a `ToolName` constant and was not added — the
Goal section's capability list names "temporary directory creation", not plain directory creation,
so `create_directory` stays outside the declared, defensible set).

**Help text correction (D21), done; `architecture/` untouched on explicit instruction.** The `tools`
command's `HelpText` changed from "Lists mcp and built-in tools" (false before this phase: the
listing never showed an MCP tool) to "Lists built-in tools and any MCP tool cached from a prior
successful run, or one tool's specification." — true now, and precise about scope (cache-only, no
live connection). One e2e assertion pinned the old string
(`main_dispatch_e2e_test.go`, `Test_e2e_command_help`'s `"tools -h"` case) and was updated to match.
Per this session's explicit instruction, `architecture/mcp.md` and the MCP sentence in
`architecture/tooling.md` were **not** touched — the coordinating session owns those and will
reconcile them against this phase's shipped behaviour (cache-only listing source, the marker, and
the recommended posture section the phase specifies).

**Duplicate-test cleanup (dupl gate).** Relocating the parser without also moving its tests would
have left `serverconfig` at 0% direct coverage from its own test binary (cross-package execution
via `internal/text`'s tests doesn't count toward `serverconfig`'s own `-cover` figure). Ported
tests were added directly in `internal/tools/mcp/serverconfig/serverconfig_test.go`
(coverage 85.7%). `go run github.com/mibk/dupl@latest -t 80 .` then flagged three verbatim clones
against the now-redundant originals in `internal/text` (two in `querier_setup_tools_test.go`, one
in `mcp_http_config_test.go`). Per CLAUDE.md's "if some piece of code is written twice, it should
be abstracted", those three original test functions were removed (replaced with a one-line pointer
comment to their `serverconfig` equivalent); `findConfiguredMcpServers` itself stays exercised
indirectly by every `setupMcpManager` test already in that file, so no coverage was lost. The one
remaining dupl hit in that neighbourhood
(`querier_setup_tools_test.go` vs `pkg/agent/mcp_setup_test.go`) pre-exists this phase and is out of
scope.

**`schemacache.ListCachedServers`/`ListEntry`** got direct unit tests in `schemacache_test.go`
(`TestListCachedServers_ReturnsOnlyHits`, `TestListCachedServers_EmptyDirectoryYieldsNoEntries`)
beyond the phase's declared set, for the same direct-coverage reason (it showed 0% before those
existed, since the phase's own `internal/tools` tests only exercise it cross-package).

**One supplementary test beyond the declared set:** `TestToolsDetailShowsMcpToolFromCache`, proving
the code-layout row's other stated half — `Detail` extended, not just `List` — actually works, and
that its JSON output carries no shadow marker (the marker is listing-line presentation only, never
part of a tool's specification).

**Host-load note (not a regression).** The first full-suite run at `-race -count=3 -timeout=30s`
hit the already-documented host-load sensitivity: `internal/audio`, `internal/tools/mcp` and the
root package timed out at the 30s wall clock under concurrent package load (host load 8–20 during
this session). Each was re-run in isolation and passed cleanly
(`TestStdioConnectReclassifiesAuthPromptAsChallenge` 3/3 at `-count=3`;
`TestAssemblerCleansUpOnSuccessAndError` 3/3; root package standalone in 12.6s) — neither test nor
file touched by this phase. One failure in that first full run was real, not load noise: the root
package's `Test_e2e_command_help` asserted the old help-text substring and was fixed as described
above. A clean full-suite confirmation was obtained with `go test ./... -race -count=3
-timeout=30s -p 1` (forcing sequential package builds so this session's own test run did not
compound the host's ambient load): every package `ok`, in ~23s for the slowest (root) and ~16s for
`internal/tools/mcp`. `-p` is a build-parallelism flag, not one of the race/count/timeout flags the
repo requires to stay unmodified.

**Verification run, final:**

```
go build ./...                                             # clean
go vet ./...                                                # clean
go run mvdan.cc/gofumpt@latest -l .                         # no files listed
go run honnef.co/go/tools/cmd/staticcheck@latest ./...      # clean
go fix ./...                                                # no changes
go run github.com/mibk/dupl@latest -t 80 .                  # no new clones from this phase's files
go test ./... -race -count=3 -timeout=30s -p 1              # ok, every package
```

Coverage of this phase's own new/touched packages (from the `-cover` run cited above):
`internal/tools` 80.9%, `internal/tools/mcp/schemacache` 86.0%, `internal/tools/mcp/serverconfig`
85.7% — all above the repository's 70% floor and close to the 90% preferred figure.

### 2026-10-03, fix session (review 1 and review 2 findings)

Session identity: worklog-work fixer, 2026-10-03.

**Findings closed, this session:**

- **R1-16 (phase-7 share)** — root cause was already fixed at the root by phase 3 (D40,
  `BuildIdentityWithScopes` now ignores `Auth.Scopes` whenever `server.Command != ""`); verified by
  reading `internal/tools/mcp/schemacache/schemacache.go:108-128`, not re-done here. This phase's
  own share — a test design flaw, not a code bug — is closed: `captureListingCacheEntry` still
  exists for the phase's other, legitimate unit tests, but the identity-agreement property now has
  its own test, `TestToolsListAgreesWithProductionSetupForAuthScopedCommandServer`
  (`internal/text/mcp_listing_identity_test.go`), which warms the cache through the real
  `setupMcpManager` → `resolveLazyServerViaCache` path and reads it back through the real
  `tools.List()` — the production composition root on both sides of the seam, per the README's
  cross-phase invariant. Sanity-checked that it actually catches the regression: reverting D40's
  `server.Command == ""` guard in `schemacache.go` to unconditional made the test fail; restoring
  it passed again.
- **R1-22 / R2-17 (marker transport scope)** — `mcpListingEntries` (`internal/tools/cmd.go`) now
  looks up each hit's originating server by name and applies `shadowingBuiltin` only when
  `server.Command != ""`. New test: `TestShadowMarkerSkipsEndpointBasedServers`.
- **R1-23 / R2-23 (`create_directory` → `mkdir`)** — added `MkdirTool ToolName = "mkdir"` to
  `pkg/text/models/tools.go` and a `"create_directory": pub_models.MkdirTool` entry to
  `builtinShadowMap` (`internal/tools/builtin_shadow.go`). New test:
  `TestShadowMarkerCoversCreateDirectory`.
- **R1-35(a)** — `TestShadowAdvisoryHandlesNoMcpServers` now creates an empty `mcpServers/` so it
  reaches the `filepath.Glob`-returns-nothing branch, distinct from
  `TestShadowAdvisoryAbsentConfigDirIsNotAnError`'s `os.Stat`-missing branch.
- **R1-35(b)** — `TestToolsListShowsMcpToolsFromCache` now uses `spawnSentinelCommand`, a real
  executable script that touches a sentinel file if ever run, in place of the inert
  `unreachableCommand`, and asserts the sentinel stays untouched — a spawn would now actually fail
  the test.
- **R1-35(c)** — added a one-line comment at `configuredMcpServersForListing`'s `servers, _ :=`
  naming D21 as the reason the parse error is intentionally dropped.
- **R2-12 (phase-7 share)** — new e2e test `Test_goldenFile_TOOLS_lists_mcp_tools_from_cache`
  (`main_tools_e2e_test.go`) writes a real `mcpServers/fs.json`, warms a real cache entry under the
  fixture's `CLAI_CACHE_DIR`, runs the real `clai tools` command through `run()`, and asserts both
  the cache-sourced tool and its shadow marker appear in stdout. Phase 3's fixture half
  (`main_mcp_lazy_e2e_test.go`) was already closed in an earlier 2026-10-03 fix session; not
  redone here.
- **R2-17** — see R1-16/R1-22 above for the mechanism; the test itself,
  `TestShadowAdvisoryDoesNotAlterSelection`, was rewritten to drive `List()` then `Detail()` over
  one cached, shadowed entry and assert the cached description survives unmarked. The old
  `mcp.RegisterTools`-based body and its `listingFakeConn` double were removed, and
  `TestToolsDetailShowsMcpToolFromCache` was folded into the merged test (removing the duplication
  the review implicitly asked for by naming both tests).
- **R2-23** — closed together with R1-23: the constant was added rather than an inline string
  literal, per this finding's own recommendation.

**Findings left open:** none. Every finding that reopened this phase is checked off above.

**Verification commands, this session (all from the repository root):**

```
go build ./...                                             # clean
go vet ./...                                                # clean
go run mvdan.cc/gofumpt@latest -l .                         # no files listed
go run honnef.co/go/tools/cmd/staticcheck@latest ./...      # clean
go fix ./...                                                # no changes
go run github.com/mibk/dupl@latest -t 80 .                  # 36 pre-existing clone groups, none touching this session's files
go test ./internal/tools/... ./internal/text/... ./pkg/text/models/... . -race -cover -count=3 -timeout=30s -p 1
                                                             # ok, every package
go test ./... -race -count=3 -timeout=30s -p 1              # ok, every package (full repo, background run)
```

**Architecture staleness note (not acted on, per this session's explicit instruction not to touch
`architecture/`):** the marker's new transport restriction (command-based servers only) belongs in
`architecture/mcp.md`'s "The tool listing" section if that section documents the marker's scope;
`architecture/tools-command.md`'s description of the marker should likewise note it never applies
to an endpoint-based server. Neither file was read or edited by this session.

### Sign-off fix session, 2026-10-03 (worklog-work, S2)

A later, unbounded holistic review found that the per-tool marker this phase shipped gates on
transport (`server.Command != ""`), not locality: a command-based server can itself be a remote
bridge (the documented `npx -y mcp-remote https://...` shape, which `schemacache.go`'s own comments
already name), so a bare-name match against `builtinShadowMap` produces a per-tool claim the
marker has no actual evidence for. The review's own probe: a server shaped like this exposing a
`read_file` tool that actually reads a Notion page would be marked `[shadowed by built-in: cat]`.
`cat` cannot read a Notion page. Full text in the README's Sign-off verdict section and the
Sign-off review feedback-index entry.

**Decision: demoted to a single footer line, the smaller of the two corrective actions the review
offered** (the other being to cut the marker outright). A footer can carry the same "a local
built-in may already cover this" information with no claim about any specific listed tool to be
wrong about, which is exactly the property a per-tool marker cannot have once "command-based"
stopped being a reliable proxy for "local." Cutting the advisory outright was rejected because it
would also discard the information for the (still dominant) case where the marker was correct —
the phase's own Goal section measures the filesystem server precisely because that case is common
— and the footer's generality is what makes it safe to keep.

**Implementation.** `mcpListingEntry` lost its `marker` field; `mcpListingEntries`
(`internal/tools/cmd.go`) now returns `([]mcpListingEntry, []string)`, the second value a
deduplicated, sorted slice of built-in names matched across command-based hits only (the url-based
restriction, R1-22/R2-17, is unchanged — it is necessary but, per this finding, no longer
sufficient on its own). `List` prints the footer as one trailing line after every tool's own line,
only when the slice is non-empty, naming every matched built-in with no attribution to which
server or tool triggered which entry. `findMcpListingEntry`/`Detail` are unaffected: they never had
a marker of their own to carry.

**The same session also corrects one mapping entry wrong on its merits (S2's second half):**
`get_file_info` was mapped to `pub_models.FileTypeTool`, but the MCP filesystem server's
`get_file_info` returns size, modification time and permissions — a `stat(1)`-shaped answer —
while `FileTypeTool` wraps `file(1)`, which answers a different question (a file's content type).
The two were never equivalent; `builtin_shadow.go`'s original "Mapping chosen" implementation note
above is superseded by this correction, not edited in place, since it is this session's own prior
record of what was shipped at the time. Removed rather than repointed: no built-in in this
repository answers what `get_file_info` actually returns.

**Tests, every one proved red (against a reverted copy of `cmd.go`/`builtin_shadow.go`) before
green:** `TestToolsListNeverEmitsPerToolShadowMarker`, `TestShadowFooterNamesAvailableBuiltin`,
`TestShadowFooterOmitsUnregisteredBuiltin`, `TestShadowFooterDeduplicatesAcrossServers`,
`TestShadowFooterCoversLazyCachedServer`, `TestShadowFooterSkipsEndpointBasedServers`,
`TestShadowFooterCoversCreateDirectory`, `TestBuiltinShadowMapExcludesGetFileInfo`, and the direct
regression for this finding's own probe, `TestShadowFooterNeverAttributesToASpecificRemoteTool`
(a command-based server shaped exactly like the review's `npx -y mcp-remote https://mcp.notion.com/mcp`
example, exposing a `read_file` tool described as "Read a Notion page": its listing line carries no
per-tool marker naming `cat`). `TestShadowAdvisoryDoesNotAlterSelection` was updated in place to
assert the new footer shape rather than the old per-tool marker string, since that is what it was
always proving — the invariant, not the exact rendering. Three dupl clone groups this session's own
new test functions introduced (near-identical setup-plus-footer-assertion bodies) were extracted
into two shared helpers, `listWithBuiltinRegistered` and
`listSingleCachedToolWithBuiltinRegistered`, before the final dupl run, per the repository rule
that duplicated code is abstracted.

**Documentation.** `architecture/tools-command.md`'s marker description and
`architecture/mcp.md`'s tool-listing section are both now additionally stale on the advisory's
shape (footer, not per-tool marker), on top of the pre-existing staleness note above about the
transport restriction; neither was read or edited this session either, per the same standing
instruction.

**Verification commands, this session (all from the repository root):**

```
go build ./...                                             # clean
go vet ./...                                                # clean
go run mvdan.cc/gofumpt@latest -l .                         # no files listed
go run honnef.co/go/tools/cmd/staticcheck@latest ./...      # clean
go fix ./...                                                # no changes
go run github.com/mibk/dupl@latest -t 80 .                  # 36 clone groups, none new from this session's files
go test ./internal/tools/... -race -cover -count=3 -timeout=30s     # ok; internal/tools 81.6%
go test . -run Test_goldenFile_TOOLS -v                     # ok, both golden-file tests
go test ./... -race -cover -count=3 -timeout=30s -p 1       # ok, every package, host load 0.9–2.1
```

## Review findings

### Review 1, 2026-10-02 — implementation review

**Status: Reopened (review 1).** The listing really is cache-only and the marker really is emitted
by the production path; the findings are about which key the listing computes and what the marker
says about a remote tool.

**Verified good:**

- **The marker is emitted by production, not by a test helper.** `internal/tools/cmd.go:190-192`
  inside `mcpListingEntries`, consumed by `List()` at `:68-92`; `Detail` shares the same source via
  `findMcpListingEntry` (`:201-208`) and correctly carries no marker in its JSON.
- **The listing is genuinely cache-only and connects nothing.** `mcpListingEntries` →
  `configuredMcpServersForListing` → `schemacache.ListCachedServers` → `Cache.Lookup`, which is
  `os.ReadFile` plus `json.Unmarshal`. There is no `exec.Command`, no dial and no `Conn`
  construction anywhere in the path; the only `exec` reference is `exec.LookPath` in
  `resolveExecutable`, which starts nothing.
- **D21's omission behaviour is correct on all three branches** — never cached, corrupt entry
  (`Lookup` miss, `schemacache.go:225-227`) and identity mismatch.
  `TestToolsListOmitsServersWithoutCacheEntry` asserts the absence of the *server name*, not merely
  of its tools.
- **The per-run registry rule holds.** The only `Registry` touch in the shadow path is the
  read-only `Registry.Get` in `builtinRegistered` (`internal/tools/builtin_shadow.go:38`), and
  `TestToolsListNeverWritesMcpIntoGlobalRegistry` asserts both size invariance and the absence of
  any `mcp_`-prefixed key, so it would catch a regression that added a `Registry.Set`.
- All ten mapped built-ins exist in the `pub_models.ToolName` enum
  (`pkg/text/models/tools.go:105-120`); **no mapping names a non-existent built-in.** The omissions
  of `mv`, `list_allowed_directories` and `read_media_file` are correct.
- `TestShadowAdvisoryDoesNotAlterSelection` drives real `mcp.RegisterTools`, `mcp.NewTool` and
  `tool.Call`, proving the marker never reaches a specification. `builtinRegistered` gating on the
  registry rather than probing the filesystem matches `registerLocalTools`' availability rule, and
  `TestShadowMarkerSkipsUnregisteredBuiltin` is a real assertion.
- `serverconfig.FindConfiguredServers` preserves the pre-relocation contract: per-file failures
  collected, parsed servers returned alongside the joined error, name from the base filename,
  envfile expanded and joined relative to the config file.

**Findings**

- [x] **R1-16** (major, owned with phase 3) — the listing computes `BuildIdentityWithScopes` while
  command-based setup captures under `BuildIdentity`, so a command-based server declaring
  `auth.scopes` is omitted from `clai tools` permanently and silently. The phase-7 suite cannot
  catch it because `captureListingCacheEntry`
  (`internal/tools/mcp_listing_test.go:50`) warms the cache with the listing's own builder rather
  than through production setup. Full detail and corrective action in phase 3's R1-16; the test-side
  half of the fix belongs here.
  **Closed, 2026-10-03 fix session.** The root cause is phase 3's (D40:
  `BuildIdentityWithScopes` now ignores `Auth.Scopes` whenever `server.Command != ""`, so it equals
  `BuildIdentity` for every command-based server, auth or not — verified by reading
  `internal/tools/mcp/schemacache/schemacache.go:108-128`). This phase's own share — the suite's
  inability to catch a divergence because it warms the cache with the listing's own builder — is
  closed by a new test that drives the real production composition root instead:
  `TestToolsListAgreesWithProductionSetupForAuthScopedCommandServer`
  (`internal/text/mcp_listing_identity_test.go`) configures a command-based server declaring
  `auth.scopes`, warms the cache through the real `setupMcpManager`/`resolveLazyServerViaCache`
  path (keyed by `BuildIdentity`), then calls the real `tools.List()` (keyed by
  `BuildIdentityWithScopes`) and asserts the cached tool is visible. Confirmed this test actually
  catches the regression it names: temporarily reverting D40's guard in `schemacache.go` and
  rerunning made it fail, then restoring made it pass again.
- [x] **R1-22** (minor) — **the marker is keyed on the bare remote tool name with no reference to
  transport, so it marks remote tools misleadingly.** `internal/tools/cmd.go:190` calls
  `shadowingBuiltin(t.Name)` and `mcpListingEntries` iterates `url`-based servers too. Scenario: an
  endpoint-based server exposing `read_file` — reading files on the **remote** host — is listed as
  `[shadowed by built-in: cat]`, telling the operator to delete a capability local `cat` cannot
  replace. The code faithfully implements the prescribed table; it is the Specification that missed
  the remote branch, against this phase's own statement that "a guess that marks an unrelated tool
  as redundant is worse than no marker".
  Corrective action: restrict the marker to command-based servers, or key it on
  `(transport, remoteName)` and add a row saying so.
  **Closed, 2026-10-03 fix session** (took the smaller of the two corrective actions): `mcpListingEntries`
  (`internal/tools/cmd.go`) now builds a `serverByName` map from the configured servers and only
  applies `shadowingBuiltin` when the owning server's `Command != ""`; the Behaviour section above
  now states the restriction. `TestShadowMarkerSkipsEndpointBasedServers` pins an endpoint-based
  server exposing `read_file` staying unmarked.
- [x] **R1-23** (minor) — **`create_directory` → `mkdir` is omitted on a false premise.**
  `internal/tools/builtin_shadow.go:13-24` leaves `create_directory` unmapped and this phase's
  notes justify it with "`mkdir` lacks a `ToolName` constant and was not added". `mkdir` is a real,
  unconditionally registered native built-in (`pkg/tools/bash_tool_mkdir.go:13`, in
  `registerLocalTools`' `nativeTools` list at `internal/tools/handler.go:41`). The missing constant
  is a one-line addition to `pkg/text/models/tools.go`, a file this worklog already edits, and
  `builtinShadowMap`'s value type is a string alias so `pub_models.ToolName("mkdir")` would work
  unchanged. The phase under-reports a real, unambiguous shadow against its own Goal.
  **Closed, 2026-10-03 fix session, per R2-23's corrected reasoning below:** added
  `MkdirTool ToolName = "mkdir"` to `pkg/text/models/tools.go` beside its neighbours, and
  `"create_directory": pub_models.MkdirTool` to `builtinShadowMap`.
  `TestShadowMarkerCoversCreateDirectory` pins it marked.
- [x] **R1-35** (note) — three small test-and-coverage gaps in this phase's suite:
  (a) `mcp_listing_test.go:301` and `:326` claim to pin two distinct branches — "an existing, empty
  config directory" and "the other half … distinct from an existing empty one" — but neither
  creates `mcpServers/`, so both fail at the same `os.Stat` (`internal/tools/cmd.go:221`) and
  return from the same line; the `filepath.Glob`-returns-nothing branch (`:224`) is never reached.
  Fix: `os.MkdirAll` an empty `mcpServers/` in one of them.
  (b) The invariant row "…with no process started | `TestToolsListShowsMcpToolsFromCache`" is
  asserted by nothing: that test's only mechanism is an `unreachableCommand` whose comment claims a
  spawn "would fail loudly", which it would not — an ignored spawn error leaves the test green. The
  invariant is in fact true by structure (see Verified good), so this is a missing assertion rather
  than a broken property; a spawn counter would make it real.
  (c) `configuredMcpServersForListing` discards a parse error with `servers, _ :=`
  (`internal/tools/cmd.go:228`). Partial results survive, so only diagnostics are lost, but the
  bare `_` is against CLAUDE.md's "Return an error on every failure" and deserves at least an
  explicit comment.
  **Closed, 2026-10-03 fix session, all three:**
  (a) `TestShadowAdvisoryHandlesNoMcpServers` now `os.MkdirAll`s an empty `mcpServers/`, so it
  reaches the `filepath.Glob`-returns-nothing branch; `TestShadowAdvisoryAbsentConfigDirIsNotAnError`
  is unchanged and keeps pinning the `os.Stat`-missing branch.
  (b) Replaced the inert `unreachableCommand` in `TestToolsListShowsMcpToolsFromCache` with
  `spawnSentinelCommand`, a real executable script that touches a sentinel file if ever run; the
  test now asserts the sentinel was never touched, making the "no process started" invariant a
  real, failable assertion.
  (c) Added a one-line comment at the discard site in `configuredMcpServersForListing` naming D21
  as the reason the parse error is intentionally dropped rather than propagated.

### Review 2, 2026-10-02 — implementation review, round 2

Status: **Reopened (review 2)**, in addition to the round-1 reopening.

**Round 1's phase-7 findings re-verified:**

- **R1-16** upheld, and it is the sharpest finding of either round against this phase.
  `schemacache.ListCachedServers` keys every server with `BuildIdentityWithScopes`
  (`schemacache.go:332`), while `resolveLazyServerViaCache` keys a command-based capture with
  `BuildIdentity` (`querier_setup_tools.go:298`). They coincide only when `server.Auth` is nil
  (`schemacache.go:98-104`), and nothing rejects an `auth` block on a command-based config —
  `validateTransport` enforces only the XOR and URL absoluteness. So `{"command":"node","auth":{"scopes":["x"]}}`
  is written under one key and read under another, and the server is silently and permanently
  omitted from `clai tools`. The comment at `internal/tools/cmd.go:153-154` ("builds the identity …
  exactly as setup does") is false for that case. The smaller of the two fixes is to key the
  command-based capture with `BuildIdentityWithScopes` unconditionally, which also makes that
  comment true.
- **R1-22** upheld. `shadowingBuiltin(t.Name)` (`internal/tools/cmd.go:190`) is keyed on the bare
  remote name, and `builtinShadowMap` (`internal/tools/builtin_shadow.go:13-24`) is derived from one
  specific server, so any server exposing a `read_file` with different semantics is marked.

**Verified good:**

- The listing never writes into the process-global registry and never connects: `mcpListingEntries`
  (`internal/tools/cmd.go:159-195`) reads the cache and builds a listing-scoped set, and every
  failure to resolve a config or cache directory returns an empty result rather than an error, per
  D21.
- `builtinRegistered` asks the registry rather than probing the filesystem
  (`builtin_shadow.go:37-40`), which correctly inherits `registerLocalTools`' own
  executable-presence gate.

**Findings:**

- [x] **R2-17** (minor) — **`TestShadowAdvisoryDoesNotAlterSelection`'s central assertion is
  unreachable by construction.** `internal/tools/mcp_listing_test.go:398-425` claims (comment at
  `:392-397`) to pin "the invariant that the marker changes nothing about tool selection,
  registration or the specification sent to a model". It calls `mcp.RegisterTools` (`:402`) and
  asserts `spec.Description == "Read a file"` (`:411`) and
  `!strings.Contains(spec.Description, "shadowed")` (`:414`). But the marker exists only inside
  `mcpListingEntries` (`internal/tools/cmd.go:190-193`), which this test never invokes;
  `mcp.RegisterTools` has no reference to `shadowingBuiltin` and no access to the shadow map, and
  `spec.Description` is the verbatim `description` from the input JSON at `:399`. The "shadowed"
  check cannot fail for the reason the test names. What it legitimately proves — that
  `RegisterTools` round-trips a description — is already covered by
  `internal/tools/mcp/tool_test.go`. Corrective action: make it differential. Run `List()` over a
  cached entry that *is* shadowed, then assert `Detail("mcp_fs_read_file")`'s JSON `description`
  equals the cached description exactly, with no marker.
  `TestToolsDetailShowsMcpToolFromCache` (`:348`) already does half of that at `:369`; merging the
  two gives one test that can fail.
  **Closed, 2026-10-03 fix session,** exactly as suggested: `TestShadowAdvisoryDoesNotAlterSelection`
  now warms one cached, shadowed entry, runs the real `List()` and asserts the marked line, then
  runs the real `Detail()` and asserts its JSON `description` equals the cached value with no
  marker. `TestToolsDetailShowsMcpToolFromCache` and the now-unreachable `mcp.RegisterTools`-based
  body (and its `listingFakeConn` double) were removed as redundant with the merge and with
  `internal/tools/mcp/tool_test.go`.

- [x] **R2-23** (note) — **Correction to R1-23, so the fixer does not go looking for a symbol that
  does not exist.** R1-23 says the `create_directory` → `mkdir` entry was omitted "on the false
  premise that `mkdir` has no `ToolName` constant". The premise is *true*: there is no `MkdirTool`
  in `pkg/text/models/tools.go` — the constant block runs from `FileTreeTool` to `ClaiResultTool`
  and contains no `mkdir`. What is false is the inference, because `ToolName` is a defined string
  type, so `pub_models.ToolName("mkdir")` is a valid map value, and `mkdir` *is* a registered
  native built-in (`internal/tools/handler.go:43` registers `tools.Mkdir`, specified at
  `pkg/tools/bash_tool_mkdir.go:12`). R1-23's recommendation stands; only its reason needs
  restating. The clean fix is to add the missing `MkdirTool ToolName = "mkdir"` constant beside its
  neighbours and then add the map entry, rather than inlining a string literal into a table whose
  every other value is a named constant.
  **Closed together with R1-23 above**, following this correction's own clean-fix recommendation
  (named constant, not a string literal).

- [x] **R2-12** — filed against phase 3; the half of it that belongs here is that the only
  end-to-end evidence for a cache-sourced tool set — `main_mcp_lazy_e2e_test.go` — asserts nothing
  about the tool set, and the only end-to-end evidence for this phase's listing path,
  `main_tools_e2e_test.go:11-29`, asserts only that the footer string is present. The new listing
  source and the shadow marker that the reworded `tools -h` text now advertises are both
  unexercised above the unit level. One cached entry plus `CLAI_CACHE_DIR` in that test would close
  it.
  **Phase 7's share closed, 2026-10-03 fix session**, exactly as suggested: a new
  `Test_goldenFile_TOOLS_lists_mcp_tools_from_cache` in `main_tools_e2e_test.go` writes a real
  `mcpServers/fs.json` config under the e2e fixture's `CLAI_CONFIG_DIR`, warms a real cache entry
  under its `CLAI_CACHE_DIR` for a `write_file` remote tool, runs the real `clai tools` command
  through `run()`, and asserts the cache-sourced tool and its `[shadowed by built-in: write_file]`
  marker both appear in stdout. Phase 3's fixture half (`main_mcp_lazy_e2e_test.go`) was already
  fixed in the 2026-10-03 fix session referenced on the README status board and is not reopened by
  this phase.

### Review 3, 2026-10-03 — sign-off review (holistic)

**Status: Reopened (sign-off).** The first pass to read the whole effort at once rather than one
phase at a time, after reviews 1 and 2 had already closed this phase. Found one finding, in two
parts, that neither prior round caught. Full detail in the README's Sign-off verdict section and
the Sign-off review feedback-index entry.

**Findings**

- [x] **S2** (minor) — **The marker gates on transport, not locality, and one mapping entry is
  wrong on its merits.** `commandBased := serverByName[hit.ServerName].Command != ""`
  (`internal/tools/cmd.go`) treats "command-based" as a proxy for "local," which the documented
  `npx -y mcp-remote https://...` bridge shape disproves: a command-based server can be genuinely
  remote, so a bare-name match against `builtinShadowMap` produced a per-tool claim with no actual
  evidence behind it — a Notion page read marked `[shadowed by built-in: cat]`. Separately,
  `get_file_info → file_type` was wrong on its own merits: the MCP tool returns size/mtime/
  permissions, `file_type` wraps `file(1)`, a different question. The review offered two corrective
  actions for the first half (cut the marker, or demote it to a footer with no per-tool claim) and
  asked for the second half to be fixed or removed.

  **Resolved (sign-off fix session, 2026-10-03).** Demoted to a single footer line (the smaller
  corrective action); `get_file_info` removed from `builtinShadowMap` outright. Decision rationale,
  full implementation detail and the test list are in Implementation notes above. Direct regression
  test for the review's own probe: `TestShadowFooterNeverAttributesToASpecificRemoteTool`.
