# Phase 7 — Built-in shadow advisory

**Status:** Not Started

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

The tool listing gains a shadow marker: a trailing annotation on the tool's existing listing line
naming the covering built-in, of the shape `[shadowed by built-in: cat]`. It is appended to the line
the listing already prints; no column is added, no name is rewritten, and nothing about the existing
alias grouping or sort order changes.

The mapping is a package-level `map[string]pub_models.ToolName` in the file the README code-layout
section names: the MCP tool's remote name without its server prefix, to the built-in that covers it.
A built-in is "available on this host" exactly when the process-global tool registry holds it, which
is how `registerLocalTools` already gates built-ins whose executable is absent; the marker asks the
registry rather than probing the filesystem itself.

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
| An MCP tool with a declared covering built-in | Marked, naming the built-in | `TestToolsListMarksBuiltinShadowedMcpTools` |
| An MCP tool with no declared mapping | Unmarked | `TestToolsListMarksBuiltinShadowedMcpTools` |
| Tool selection and registration | Unchanged by the marker | `TestShadowAdvisoryDoesNotAlterSelection` |
| Schemas sent to a model | Unchanged by the marker | `TestShadowAdvisoryDoesNotAlterSelection` |
| A configured server with a cache entry | Its tools appear in the listing, from cache, with no process started | `TestToolsListShowsMcpToolsFromCache` |
| A configured server with no cache entry | Omitted entirely; no tool invented, nothing connected, nothing flagged | `TestToolsListOmitsServersWithoutCacheEntry` |
| The process-global registry | Never receives an MCP tool | `TestToolsListNeverWritesMcpIntoGlobalRegistry` |
| A run with no MCP servers configured | Listing succeeds with no marker | `TestShadowAdvisoryHandlesNoMcpServers` |
| A lazily unconnected server served from cache | Listed and marked from its cached schemas | `TestShadowAdvisoryMarksFromCachedSchemas` |
| An absent config directory | Listing succeeds, no error | `TestShadowAdvisoryAbsentConfigDirIsNotAnError` |

### Limits

This phase introduces no limit, threshold or tunable. The marker is presentation only: it reads the
registry and a declared mapping, bounds nothing, and consumes no parameter. A limit added here later
needs a README parameters row first.

### Documentation

`architecture/mcp.md` gains the listing's cache-only source and the marker. The MCP sentence in
`architecture/tooling.md` and the `tools` command's own help text are corrected in this phase, since
this is the phase that makes them true. It also gains a short section recording the recommended
posture that follows from the measurements: built-ins for local capability clai already has, the HTTP transport for services, and
stdio reserved for genuinely local capability clai lacks. The tools-command architecture note gains
the marker's description, since it documents the listing surface.

## Integration contract

| Trigger | Collaborators or fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Tool listing with a configured server exposing a file-reading tool | Fake stdio server whose tool name is in the declared mapping | Entry marked, naming the covering built-in | Marker rendered | Tool not removed from the listing |
| Tool listing with a configured server exposing an unmapped tool | Fake stdio server with an unrelated tool name | Entry unmarked | Listing rendered | No speculative marker |
| A run selecting that same MCP tool with an explicit selection | Fake stdio server, explicit selection | Tool is selected and callable | Tool registered as before | Marker does not suppress selection |
| Tool listing with no MCP servers configured | Empty config directory | Listing renders built-ins only | Listing succeeds | No error about a missing directory |
| Tool listing with a configured server and a warm cache | Warmed schema cache, no server running | Its tools listed and marked | Cache read | No process started; nothing written to the global registry |
| Tool listing with a configured server and no cache entry | Empty cache | That server contributes nothing and is absent from the output | Listing still succeeds | No connection attempted; no diagnostic line about the server |

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| MCP tools appear in the listing, read from cache, with no process started | `TestToolsListShowsMcpToolsFromCache` |
| A server with no cache entry is omitted, not flagged | `TestToolsListOmitsServersWithoutCacheEntry` |
| No MCP tool ever enters the process-global registry | `TestToolsListNeverWritesMcpIntoGlobalRegistry` |
| Shadowed MCP tools are marked with their covering built-in | `TestToolsListMarksBuiltinShadowedMcpTools` |
| The marker changes nothing about selection, registration or schemas | `TestShadowAdvisoryDoesNotAlterSelection` |
| The listing is correct with no servers configured | `TestShadowAdvisoryHandlesNoMcpServers` |
| A lazy server is listed and marked from its cached schemas | `TestShadowAdvisoryMarksFromCachedSchemas` |
| An absent config directory is not an error | `TestShadowAdvisoryAbsentConfigDirIsNotAnError` |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Config directory absent | Listing renders built-ins, no error | `TestShadowAdvisoryAbsentConfigDirIsNotAnError` |
| A configured server has no cache entry | Listing renders what it has and omits that server | `TestToolsListOmitsServersWithoutCacheEntry` |
| A cache entry is corrupt | Treated as absent, as the cache phase already specifies, so the server is omitted | `TestToolsListOmitsServersWithoutCacheEntry` |
| Declared mapping names a built-in that is not registered on this host, because its executable is absent | Entry unmarked rather than naming an unavailable built-in | `TestShadowMarkerSkipsUnregisteredBuiltin` |
| Two MCP servers expose tools mapping to the same built-in | Both marked independently | `TestShadowMarkerMarksBothServersIndependently` |

## Implementation notes

Not started.

## Review findings

None.
