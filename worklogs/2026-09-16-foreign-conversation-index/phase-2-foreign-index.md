# Phase 2 — Foreign conversation index

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Give foreign conversations a derived index validated by one stat per file, so a
listing over an unchanged corpus opens no session file and continuing a foreign
conversation no longer scans one.

## Specification

### The seam

`vendors.SourceCache` is declared in the README. Both `SourceReader` methods
gain it: `Discover` because it builds the rows, `Read` because it resolves an
identifier to a file, which is the lookup the index exists to make cheap (D19).
A nil cache means "always scan" and is what the generic unit tests use, so every
phase-one test keeps working unchanged.

The protocol is fixed, one pass per walked file:

| Step  | Call                            | On hit                         | On miss                                                       |
| ----- | ------------------------------- | ------------------------------ | --------------------------------------------------------------- |
| One   | `StatAbs`                       | —                              | —                                                               |
| Two   | `cache.Lookup(path, info)`      | Row is used; row marked in use | Falls through                                                    |
| Three | The phase-one scan              | Not performed                  | Performed, using the same `info` for the mod-time fallback        |
| Four  | `cache.Store(path, info, row)`  | Not performed                  | Performed; row marked in use, **including a zero row**            |

The `fs.FileInfo` from step one is the only one taken. It is what `Lookup`
compares and what `Store` records, so a file appended to during step three is
recorded under the state that was actually read and is rescanned next run (D15).
Stat-after would record a state whose bytes were never read — a live corpus was
observed growing mid-measurement, which is what makes this concrete rather than
theoretical.

Storing a zero row for a file that yields no identity is deliberate: without it,
every unusable file is rescanned on every invocation forever.

### Liveness and pruning

A cached row survives a persist when it was **read**, **written**, or its file
**still exists**. Nothing else prunes:

| Row state at persist              | Action  | Why                                                                   |
| --------------------------------- | ------- | ----------------------------------------------------------------------- |
| Returned by `Lookup` this run     | Kept    | The walk reached it and it was current                                  |
| Written by `Store` this run       | Kept    | Freshly scanned                                                         |
| Untouched, its file still exists  | Kept    | A root that was unreachable this run must not cost the whole cache      |
| Untouched, its file is gone       | Dropped | The file was deleted; the row can never be validated again              |

The untouched set is empty on a normal run, so the prune costs nothing in the
common case. Pruning by "unmarked" alone would empty the cache whenever a walk
failed; pruning only on `Store` would empty it on the second run of an unchanged
corpus, which is the failure this rule exists to prevent.

### The index

`internal/chat/foreign_index.go` implements `vendors.SourceCache` and mirrors
`internal/chat/index.go` rather than inventing conventions:

| Concern             | Convention followed                                                                      |
| ------------------- | ------------------------------------------------------------------------------------------ |
| Location            | `foreign-index-file-name` inside `foreign-index-location`                                  |
| Directory creation  | `os.MkdirAll` before the first write. Neither `utils.GetClaiCacheDir` nor `utils.WriteFileAtomic` creates it, and nothing else in the repository creates the clai cache directory — the native index escapes this only because its file lives in a directory other code already creates |
| Format              | A versioned wrapper struct around a row slice, as `chatIndexCache` does                    |
| Version             | `foreign-index-version`; a lower or absent version discards every row                      |
| Write               | `utils.WriteFileAtomic`, as `writeChatIndex` does                                          |
| Read-only           | `utils.NoCreateConfig` suppresses the failure *warning* only; the write is attempted (D24). The flag is scoped to the config directory and this cache is not one |
| Embedded consumers  | `chat.SkipIndex` suppresses read and write alike, as the native index does                 |
| Construction        | `NewForeignIndex` returns an error rather than an unusable value, so no caller can hand `DiscoverJSONL` an interface holding a nil pointer — which would pass the "always scan" guard and then panic |
| Concurrency         | A mutex guards the row map; `Lookup`, `Store` and `Persist` are safe for concurrent use    |

A row carries the cached `SourceRow`, the absolute path, and the size and mod
time it was validated against. Rows for both vendors share the file; paths are
disjoint by construction and each row records its source name.

Failure is never fatal. A missing, unreadable, corrupt or version-stale cache
yields an empty in-memory index, the listing proceeds by scanning, and the cache
is rewritten when writable. The native index prints rebuild progress because a
rebuild there is slow and rare; a foreign rescan is the pre-existing behaviour
and prints nothing.

### Wiring

One read and one write per invocation, whatever the number of readers:

```
main.go builds the index from utils.GetClaiCacheDir() and passes it in
chat.CommandDeps → cmd.go assigns ChatHandler.foreignCache after chat.New,
which takes no deps struct → foreignChatRows passes it to every reader's
Discover, and the continue path passes it to Read → ChatHandler persists it
once, after the last reader
```

`chat.ReplayCommand` and `chat.DirscopeReplayCommand` take no deps and leave the
field nil, as does every test that does not supply one, which keeps the existing
chat-list tests scanning exactly as they do today.

### Session lookup through the index

`FindJSONLSession` consults the cache before walking: a row whose source and
`SourceID` match gives a candidate path, `StatAbs` confirms the cached size and
mod time still hold, and the path is returned without opening the file. Any
mismatch — missing file, changed pair, no matching row — falls back to the
phase-one walk. Continuing a foreign conversation therefore costs a map lookup
and one stat instead of a corpus scan, and because `Read` now receives the cache
that saving is reachable from the command rather than only from a unit test.

### Files

| File                                                   | Change                                                                          |
| ------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `internal/vendors/source.go`                            | Both methods gain the cache parameter; doc comment states the pull contract      |
| `internal/vendors/jsonl_discover.go`                    | Stat, lookup, store, and the cache-first branch of `FindJSONLSession`            |
| `internal/chat/foreign_index.go`                        | New: `NewForeignIndex`, `Lookup`, `Store`, `Persist`, the cache file format      |
| `internal/chat/foreign_index_test.go`                   | New                                                                              |
| `internal/chat/handler.go`                              | `foreignCache` field                                                             |
| `internal/chat/cmd.go`                                  | `CommandDeps` carries the cache and assigns it after `chat.New`; the read-only sub leaves it nil |
| `internal/chat/handler_list_chat.go`                    | Pass the cache to every `Discover` and to the continue path's `Read`; persist once |
| `internal/vendors/jsonl_discover_test.go`               | Cache-hit, cache-agnostic, negative-caching, growing-file and lookup tests       |
| `internal/chat/handler_list_chat_test.go`               | Stub signatures for both methods; the single-write and continue-path tests      |
| `main.go`                                               | Build the index and pass it in `chat.CommandDeps`; the replay commands pass none  |
| `internal/vendors/anthropic/source_reader.go`, `internal/vendors/pi/source_reader.go` | Signatures only                                    |
| `internal/vendors/anthropic/source_reader_test.go`      | Five `Discover` and four `Read` call sites gain a nil cache                       |
| `internal/vendors/pi/source_reader_test.go`             | Eight `Discover` and five `Read` call sites gain a nil cache                      |

## Integration contract

| Trigger                                                     | Collaborators                                       | Observable result                                             | Required side effects                              | Prohibited side effects                                         |
| ------------------------------------------------------------- | ----------------------------------------------------- | --------------------------------------------------------------- | ---------------------------------------------------- | ----------------------------------------------------------------- |
| First `chat list` over a generated corpus, no cache present | generated corpus, `jsonltest.CountingFS`, temp cache dir | Every row identical to a nil-cache run                        | The cache directory is created; the file is written once | No session file opened more than once; no write outside the cache dir |
| Second `chat list`, corpus untouched                        | same, warm cache                                     | Identical rows                                                 | Cache file rewritten once                           | **No session file opened at all**                                |
| Third `chat list` after one file gained a message           | same                                                 | That row's count grows; every other row is byte-identical      | Cache rewritten once                                | Only the changed file is opened                                  |
| `chat list` after a session file is deleted                 | same                                                 | Its row disappears from the listing                             | The cached row is pruned                            | No other row is pruned                                           |
| `chat list` with both vendors populated                     | both readers, one cache                              | Rows from both sources                                          | Exactly one cache read and one cache write          | No second write                                                  |
| `chat continue` for a cached foreign session                | warm cache, `jsonltest.CountingFS`                   | The conversation opens                                          | One stat to confirm the cached path                 | No walk; no file opened to find the session                      |
| `chat list` under read-only mode                            | `utils.NoCreateConfig` set                           | Every row present                                               | The cache is written when the cache directory is writable | Nothing on stderr when it is not                                 |
| `chat list` on a machine whose clai cache directory is absent | temp `CLAI_CACHE_DIR` that does not exist          | Every row present                                               | The directory is created and the cache written      | No silent permanent rescan                                       |

## Acceptance criteria

| Outcome                                                                          | Test or command                                                      |
| ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------- |
| A warm-cache discovery opens no session file                                      | `TestDiscoverJSONL_cacheHitOpensNothing`                              |
| An unchanged corpus keeps its cache across repeated runs                          | `TestForeignIndex_unchangedCorpusSurvivesSecondRun`                   |
| The row is keyed by the pre-read stat, so a file that grew is not cached as current | `TestDiscoverJSONL_fileGrowingDuringScanIsNotCachedAsCurrent`       |
| Results are identical with a nil cache, a cold cache and a warm cache             | `TestDiscoverJSONL_cacheAgnosticResults`                              |
| A file yielding no identity is cached negatively and not rescanned                | `TestDiscoverJSONL_unusableFileCachedNegatively`                      |
| Rows for deleted files are pruned; rows for unwalked but existing files are kept  | `TestForeignIndex_prunesVanishedFiles`                                |
| Exactly one cache read and one cache write per listing                            | `TestForeignChatRows_singleCacheWrite`                                |
| A first run on a machine with no clai cache directory writes a usable cache       | `TestForeignIndex_createsMissingCacheDir`                             |
| Session lookup answers from the index without walking                             | `TestFindJSONLSession_usesCacheWithoutWalking`                        |
| A stale cached path falls back to the walk                                        | `TestFindJSONLSession_staleCachedPathFallsBack`                       |
| Continuing a foreign conversation reaches the cached path through `Read`          | `TestForeignChat_continueUsesCachedPath`                              |
| The index is safe under concurrent use                                            | `TestForeignIndex_concurrentAccess` under `-race`                     |
| Read-only mode still caches, and degrades in silence when it cannot               | `TestForeignIndex_readOnlyDegradesSilently`                           |
| Embedded consumers do no cache I/O                                                | `TestForeignIndex_skipIndexNoIO`                                      |
| Warm-cache discovery has not regressed beyond `benchmark-regression-band`         | The phase-zero benchmark command at `benchmark-invocations`, figure recorded below |
| The repository gate passes unedited, within its recorded band                     | `go test ./... -race -cover -count=3 -timeout=30s`                     |

## Error coverage

| Failure                                                       | Expected outcome                                                          | Test                                                |
| --------------------------------------------------------------- | --------------------------------------------------------------------------- | ----------------------------------------------------- |
| The cache file is absent                                      | Empty index, full scan, cache written                                       | `TestForeignIndex_missingStartsEmpty`                |
| The cache file is corrupt                                     | Empty index, full scan, cache rewritten, no error surfaced                  | `TestForeignIndex_corruptFallsBackToScan`            |
| The cache file has an older version                           | Every row discarded, full scan, cache rewritten at the current version      | `TestForeignIndex_versionMismatchRebuilds`           |
| The cache path is a directory, so it cannot be read as a file | Empty index; listing unaffected                                             | `TestForeignIndex_unreadableStartsEmpty`             |
| The cache directory's parent is a file, so no directory can be made | Listing succeeds; one warning; no partial file left behind             | `TestForeignIndex_undirectoryableParentWarnsOnce`    |
| The cache directory cannot be resolved                        | `NewForeignIndex` returns an error; the caller leaves the field unset and the listing scans | `TestForeignIndex_noCacheDirDisablesCaching`  |
| A cached row's file has vanished between lookup and persist   | The row is pruned; no error                                                 | `TestForeignIndex_fileVanishesMidRun`                |
| `Persist` is called with no prior lookups                     | A valid empty cache is written                                              | `TestForeignIndex_persistWithoutLookups`             |
| A cached row decodes with an empty path                       | The row is dropped at load                                                  | `TestForeignIndex_rowWithEmptyPathDropped`           |

The two directory failures are provoked by pointing at a path whose parent is a
file, the way `internal/utils/file_atomic_test.go` provokes its write error.
Permission bits are not used: they are a no-op when the tests run as root, which
would turn both rows into silent passes.

## Implementation notes

**Session:** `2026-09-16T14:45Z`, clai (worklog-work, phase `2`).

### Planning amendment, authorised mid-phase

The read-only finding below was handed back rather than fixed, because
rewriting an invariant row is planning and not execution. The maintainer
verified it independently and authorised the change, which is recorded as
**D24**: `Persist` gates on `chat.SkipIndex` alone, and `utils.NoCreateConfig`
now suppresses only the failure *warning*, so a read-only mount still
produces no stderr noise while a normal machine gets its cache. The invariant
row, this phase's read-only rows, and the named test — now
`TestForeignIndex_readOnlyDegradesSilently` — were updated in the same
change. `TestForeignIndex_skipIndexNoIO` is untouched and still owns the
genuine no-I/O switch.

The finding as originally reported is left standing below, because it is the
evidence D24 rests on.

### Specification gaps found (reported, not re-planned)

**The declared `SourceCache` cannot serve the session lookup.** The README's
shared-interfaces block gives `SourceCache` exactly `Lookup(absPath, info)`
and `Store(absPath, info, row)`. Both are keyed by a path. "Session lookup
answers from the index without walking" needs the reverse direction — an
identifier to a candidate path — which neither method can express, so the
acceptance row and the integration contract's *no walk* prohibition were
unreachable as written. The seam gained a third method,
`Locate(source, sourceID) (absPath, ok)`, which answers from cached state
alone and touches no filesystem; `cachedSessionPath` then re-validates the
candidate through the *declared* `Lookup`, so the `(size, mod time)` rule
still has exactly one implementation. Evidence that this was an omission
rather than a design: the interface block predates D19, which validation
round two added when `Read` gained the cache.

**Read-only mode and the list verb cancel the feature out.** The invariant
table suppresses the write under `utils.NoCreateConfig`, and
`readOnlyChatSetup` sets `utils.NoCreateConfig` unconditionally for the
`list` verb — as `internal/chat/cmd_test.go`'s *list sub is structurally
read-only* case asserts. Implemented literally, as specified, and the
consequence is real rather than theoretical:

```
$ HOME=$SP/home CLAI_CACHE_DIR=$SP/cache clai -n -r c l q   # rows printed
$ ls $SP/cache                                              # empty
$ HOME=$SP/home CLAI_CACHE_DIR=$SP/cache clai -n -r c c no-such-chat q
$ ls $SP/cache
foreign_index.cache
```

The full-setup verbs warm the cache and every listing reads it, but a
machine that only ever runs `clai chat list` never writes one, so the
worklog's headline outcome does not reach the verb it was written for. The
native index escapes this because its writer is the save path, which runs
under full config; the foreign index has no second writer. Resolving it is a
decision (does a *cache* directory fall under a *config* creation flag at
all — the flag's own doc comment scopes it to "config dir and default config
file creation"), so it is reported here rather than fixed. **The phase is
left In Progress for that reason**; every other contract is met.

**`Persist` takes no error return.** The phase's error table wants "one
warning" and "listing succeeds"; the README's parameter row writes
`ForeignIndex.Persist()` with no result. Both are satisfied by owning the
message inside `Persist` behind a warn-once guard, which also makes the
undirectoryable-parent row observable at the index layer where the phase
files it. `errOut` is an unexported field so the in-package test reads it.

**The file list undercounts the call sites.** Phase zero added
`internal/vendors/anthropic/bench_test.go`, which holds a sixth `Discover`
call the table does not mention. Pi's count (eight and five) is exact.
The table's `internal/chat/cmd.go` row says "the read-only sub leaves it
nil", which contradicts the Wiring block two sections earlier —
`foreignChatRows` is reached only from the list verb, which is the read-only
sub. The Wiring block was taken as normative: both setups receive the cache.

**The prune rule and one error row disagree.** *Liveness and pruning* keeps a
row `Returned by Lookup this run`; the error table prunes a row "vanished
between lookup and persist". Implemented per the table with its own section,
and `TestForeignIndex_fileVanishesMidRun` provokes the case the error row
actually describes: the file is gone before the walk reaches it, so no
`Lookup` happens, the row is untouched, and it is pruned.

### Decisions made while implementing

- An open failure on a cache miss stores the zero row, following the protocol
  table's step four literally ("Performed ... **including a zero row**"). The
  cost is that a transiently unreadable file is not retried until it changes;
  the benefit is the one the section names — no file is rescanned forever.
- `scanJSONLFileRow` takes the step-one `fs.FileInfo` instead of taking its
  own stat for the mod-time fallback, which is what "using the same `info`"
  requires and which removes a second stat per timestamp-less file.
- `load` discards rows at any version other than the current one, not only
  lower ones: a row written by a newer clai is equally unvalidatable.
- The walk fallback in `FindJSONLSession` never writes to the cache. An
  identity is not a discovered row, and storing a partial one would poison
  the next listing.
- `readForeignChat` exists so the continue path's `Read` call is reachable
  from a test without driving the table UI; `listChats` routes through it.

### Measured figures

Same machine as the phase-zero baselines (`Intel Core Ultra 7 155H`,
`NumCPU=22`, warm page cache), at the host load each row names.

**Every discovery figure below is historical and pre-phase-three** (`R1-06`,
`R1-52`). The cold row was measured against a line-capped scan that phase 3
deleted, so the same command on any current tree returns a different figure —
`34098838` ns/op when phase 5 reran it at load `1.12`, against the ceiling
`107418967` ns/op. The warm row came from a throwaway benchmark deleted after
measuring, so it cannot be rerun at all. Both are kept because they are what
this phase was judged against; neither is independently verifiable, and a
later round must not treat them as if it were.

| Figure | Command | Result |
| -------- | --------- | -------- |
| Cold discovery, this phase — **historical** | `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x`, load `1.79` | `90399212` ns/op, `26262680` B/op, `598960` allocs/op |
| Against the phase-zero figure | the same command's recorded `89515806` ns/op, ceiling `107418967` ns/op | `+0.99` percent — inside `benchmark-regression-band`. The cold path is unchanged bar one stat per file |
| Warm discovery — **historical, not reproducible** | a throwaway benchmark over the same corpus with a warm `ForeignIndex`, deleted after measuring, load `1.6` | `230698` ns/op, `62080` B/op, `452` allocs/op — `392` times the cold figure, which is the worklog's premise reproduced against the generated corpus |
| Repository gate | `go test ./... -race -cover -count=3 -timeout=30s`, load `1.54` before the D24 amendment, load `3.17` after | `23.9` s then `26.4` s, both all-green. A run at load `6.1` timed out the root and `internal/audio` packages at the `30` s bound — neither is touched by this phase, and both pass at lower load, which is the host-load flake the validation policy already records |

### Verification

| Check | Command | Result |
| ------- | --------- | -------- |
| Generic seam | `go test ./internal/vendors/ -run 'TestDiscoverJSONL_cache\|TestDiscoverJSONL_unusable\|TestDiscoverJSONL_fileGrowing\|TestFindJSONLSession_usesCache\|TestFindJSONLSession_stale' -v` | all pass |
| Index and wiring | `go test ./internal/chat/ -run 'TestForeignIndex\|TestForeignChat' -v` | seventeen cases, all pass, including both subtests of the rewritten read-only case |
| New-code coverage | `go tool cover -func` over `./internal/chat ./internal/vendors` | `foreign_index.go` `75`–`100` percent per function, `jsonl_discover.go` `92`–`100` percent |
| Format | `go run mvdan.cc/gofumpt@latest -w -l .` | no output |
| Static analysis | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | no output |
| Lint | `go vet ./...` | no output |
| Fix | `go fix ./...` | no output |
| Duplication | `go run github.com/mibk/dupl@latest -t 80 .` | thirty-four groups; none names a file this phase wrote. The one `handler_list_chat_test.go` pair is pre-existing fixture setup, far above the lines this phase appended |
| End-to-end, before D24 | `clai -n -r c l q` and `clai -n -r c c no-such q`, against a generated `~/.claude/projects` | rows correct; cache written by the full-setup verb, not by `list` — the finding |
| End-to-end, after D24 | the same listing, then a second warm listing, then one with the cache dir at mode `555` | `foreign_index.cache` written by `list`, read by the second run with identical rows, and the unwritable run printed zero bytes to stderr |

## Review findings

### Review 1 (`2026-09-16`)

Five defects and two notes. All are routed to the addendum,
`phase-5-review-1-fixes.md`; this phase is not reopened. The blocker and one
minor share a single root cause, now promoted into the README Strategy section
as *An error is not a fact about the file*.

| ID       | Severity  | Where                                                 | Finding                                                                      |
| -------- | --------- | ------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `R1-01`  | blocker   | `internal/vendors/jsonl_discover.go`, `scanJSONLFileRow` with `discoverJSONLFile` | An unreadable file is negatively cached                 |
| `R1-02`  | major     | `main.go`, `chatDeps` reached from `commands()`        | The index is loaded on every invocation, not only on chat verbs               |
| `R1-03`  | major     | `internal/chat/foreign_index.go`, `warn`, with `internal/chat/cmd.go`, `readOnlyChatSetup` | The persist-failure warning cannot reach a `chat list` user |
| `R1-04`  | minor     | `internal/chat/foreign_index.go`, `liveRows`           | Pruning on any stat error, not only on absence                                |
| `R1-05`  | minor     | `internal/chat/foreign_index.go`, `Locate`             | A map range makes the answer non-deterministic                                |
| `R1-06`  | note      | Implementation notes, *Measured figures*               | The recorded benchmark figures are not independently reproducible             |
| `R1-07`  | note      | `TestForeignIndex_readOnlyDegradesSilently`            | Nothing drives the read-only rule through the real verb                       |

- [x] `R1-01` — `scanJSONLFileRow` returns `(SourceRow{}, false)` for two
  different facts: the scan reached EOF and found no session identity, and
  `OpenAbs` failed. `discoverJSONLFile` stores the zero row in both cases, keyed
  by the stat taken *before* the read. Changing a file's mode changes neither
  size nor mod time, so the negative row is never invalidated and the
  conversation leaves `clai chat list` permanently. This breaks the README
  invariant *the foreign index is derived*, the phase invariant *a file that
  yields no usable row is cached as such* — an unreadable file never *yielded*
  anything — the Definition of success row *deleting `foreign_index.cache`
  changes only timing, never rows*, and the Strategy table's *a lost cache is
  recoverable, never a data loss*. Reproduced against the built binary: with one
  session file unreadable, the listing showed two rows and cached the file with
  an empty `SourceID`; restoring the file with its mod time unchanged still gave
  two rows; deleting the cache gave three. Also reproduced with a synthetic
  `fs.FS` failing the first `Open`. Real triggers include descriptor exhaustion —
  made *more* likely by the worker pool phase 3 added — mandatory-access-control
  denials, network-filesystem hiccups, and the writing tool briefly taking a file
  unreadable. **Found independently by a second reviewer as `R1-50`, by a
  different method**; filed once here.
- [x] `R1-01` test gap — phase 1's unreadable-file-skipped test and phase 3's
  parallel unreadable-file-isolation test both run with a **nil** cache through
  `discoverStub`; `TestDiscoverJSONL_unusableFileCachedNegatively`
  covers only the no-identity case; `TestDiscoverJSONL_cacheAgnosticResults` uses
  a fully readable corpus. The unreadable-by-cache cell is unexercised, which is
  why a passing suite ships the blocker.
- [x] `R1-02` — `commands()` builds the whole command map before dispatch, so
  `chatDeps` runs `NewForeignIndex` → `load()` → `os.ReadFile` plus
  `json.Unmarshal` for `clai q`, `clai version`, `clai p` and everything else.
  This breaks the invariant *one cache read and one cache write per invocation,
  whatever the number of readers*: the index is loaded once per **process**,
  including processes that will never list a chat. Measured: `strace -e openat`
  over `clai version` shows the cache opened, and a twenty-run mean moves from
  `4` ms per run with no index to `9`–`11` ms per run with a `400`-row,
  `367` KB index. A corpus-sized index more than doubles the process cost of
  unrelated verbs, in a worklog whose entire premise is invocation cost. The fix
  makes the deps field a factory invoked inside `setChatQuerier`, preserving the
  typed-nil safety `NewForeignIndex`'s error return currently provides.
- [x] `R1-03` — `warn` returns early under `utils.NoCreateConfig`;
  `readOnlyChatSetup` sets that flag unconditionally and routes `list|l` through
  it. The Error coverage row *the cache directory's parent is a file → listing
  succeeds; one warning; no partial file left behind* is therefore unreachable
  for the verb this worklog exists for. Verified: with the cache directory at
  mode `555` and no raw or no-create flags, `clai c l q` printed **zero bytes**
  to stderr. The cache is never written, the user rescans the whole foreign
  corpus on every listing forever, and nothing says why. The identical fault
  under `clai c c …`, which takes the full setup, *does* warn — one defect,
  two diagnoses, decided by which verb was typed.
  `TestForeignIndex_undirectoryableParentWarnsOnce` passes only because it never
  touches the global. **Maintainer decision D25**: the warning is gated on
  `utils.ReadonlyConfig` instead. D24's ungating of the *write* was correct and
  stands; gating the *warning* on the same flag was not, because that flag marks
  "this verb creates no config", not "this is a read-only mount".
  `architecture/config.md`'s *a read-only mount produces no stderr noise*
  sentence stops being true of the foreign index and is amended in the same
  change — part of the addendum, not optional.
- [x] `R1-04` — `liveRows` prunes an unmarked row on **any** stat error.
  Reproduced by taking the project directory unreadable: the walk yields nothing,
  every row is unmarked, every `os.Stat` fails with a permission error, and the
  persisted cache drops from three rows to none although all three files still
  exist. This breaks the phase's own Liveness row *untouched, its file still
  exists → kept … a root that was unreachable this run must not cost the whole
  cache* and the README invariant *rows whose file no longer exists are dropped*.
  Fix: drop only on a not-exist error, keep the row on every other. Same root
  cause as `R1-01`, against the promoted Strategy rule.
- [x] `R1-05` — `Locate` ranges a Go map, whose iteration order is randomised,
  while the walk fallback in `FindJSONLSession` returns the lexically first
  match, so the two disagree. That breaks the phase-one invariant *lookup and
  discovery never disagree about which file holds a session*, which this phase's
  locator inherited without inheriting the tie-break; it is promoted to a README
  invariant row by the addendum. Scenario: a project directory restored from
  backup, so the same `sessionId` exists under two paths — `clai c c <n>` opens a
  different transcript on consecutive runs and disagrees with a cache-less run.
  Fix: keep the lexically smallest matching path.
- [x] `R1-06` (note) — the *Measured figures* table records a cold and a warm
  benchmark. The warm figure came from a throwaway benchmark since deleted, and
  the cold figure predates phase 3's parallel uncapped discovery, which now runs
  through the same path. Not a defect; recorded so the next review round does not treat
  either as independently verified. The README's Budgets table now marks the
  figures this worklog can and cannot reproduce.
- [x] `R1-07` (note) — `TestForeignIndex_readOnlyDegradesSilently` sets the
  global by hand, so a future change to `readOnlyChatSetup` could re-break D24
  and D25 with every test still green. Folded into `R1-03`'s command-level test
  in the addendum.

**Verified good in this phase.** D15's stat-before-read holds on every path:
exactly one `fs.FileInfo` per file flows into `Lookup` and the matching `Store`,
and `Store` has no other call site, so no partially scanned row can be cached.
The typed-nil hazard is closed as specified. `ForeignIndex` is fully
mutex-guarded and `Persist` does not self-deadlock. The warm-cache invariant was
verified by `strace` over a built binary — no session file opened, one stat per
file — and one cache write per listing, with `foreignChatRows` called once. The
prune rule's *untouched but still present* branch is correct in intent; only its
error handling is wrong, which is `R1-04`.

### Review 2 (`2026-09-16`)

Two minors of this phase's own, plus the round-one checkbox sweep. All are
routed to the addendum, `phase-6-review-2-fixes.md`; this phase is not
reopened. `R1-05`, filed here in round one, is resolved for determinism but its
stated mechanism is false — that is `R2-02`, and the invariant it belongs to is
the one this phase's locator inherited.

| ID       | Severity | Where                                                      | Finding                                                                 |
| -------- | -------- | ------------------------------------------------------------ | ------------------------------------------------------------------------- |
| `R2-02`  | minor    | `internal/chat/foreign_index.go`, `Locate`, with its doc comment | The tie-break is not the walk's tie-break across sibling directories |
| `R2-04`  | minor    | README, *Shared interfaces*, the `SourceCache` block       | The block shows two methods; the shipped interface has three             |
| `R2-53`  | minor    | This section's round-one checkboxes                        | Every one was still open while the README declared them closed           |

- [x] `R2-02` — `Locate` keeps the lexically smallest matching **full path**,
  and its doc comment asserts that this is the walk's own tie-break. It is not.
  `WalkJSONLFiles` uses `filepath.WalkDir`, which orders **entries within each
  directory**, depth first — not whole paths. `'-'` sorts below `'/'`, so for
  sibling project directories `proj` and `proj-bak` the walk visits
  `root/proj/s.jsonl` first while `root/proj-bak/s.jsonl` is the lexically
  smaller full path, and `Locate` answers with the file the walk would not have
  opened. This breaks the README invariant *lookup and discovery never disagree
  about which file holds a session, cache or no cache*, whose stated mechanism
  is exactly the false claim. Not exotic: Claude Code's real layout is
  `~/.claude/projects/<cwd-slug>/<uuid>.jsonl` with separators replaced by `-`,
  so suffix-related sibling directories are the norm, and `R1-05`'s own
  motivating case — a project directory restored from a backup — produces it.
  Proved end to end through `anthropic.SourceReader.Read`: the cache-less walk
  returned one transcript and the warm index returned the other, so
  `clai chat continue` opens a different transcript depending on whether the
  cache is warm. The disagreement the invariant forbids was made deterministic
  rather than removed. Test gap:
  `TestForeignIndex_locateAgreesWithWalkFallback` writes both duplicates into
  **one** directory, where full-path order and walk order coincide, so it
  structurally cannot observe this. **Maintainer decision:** rank candidates by
  walk order — compare path segment by segment, which is `filepath.WalkDir`'s
  ordering for files — and add a cross-directory fixture. The invariant's
  wording is right and the implementation is wrong; do not narrow the wording
  instead.
- [x] `R2-04` — the README's *Shared interfaces* `SourceCache` block shows
  `Lookup` and `Store`; `internal/vendors/source.go` has `Lookup`, `Store` and
  `Locate`. It matters because `R1-05` and `R2-02` are entirely about `Locate`'s
  contract, and that block is also where the typed-nil rule is stated, so it is
  the one place an executor reads both. Phase 4 recorded it as a residual an
  executor may not fix; two review rounds later it is still wrong in the
  document every executor reads. Fixed in the addendum. **Found independently
  by the second reviewer as the first item of `R2-56`**; filed once here.
- [x] `R2-53` — all eight round-one checkboxes in this file were still `- [ ]`
  while the README's feedback index declared every finding closed by phase 5. A
  contributor routing off the phase files — which is how the board tells them to
  work — saw eight open action items on a `Complete` phase. The skill's rule is
  that a finding carries a checkbox so the fixer can close it directly, which
  makes an unticked box a claim. Closed in the review's own change, here and in
  the two phases after this one; the round-two boxes are ticked as the
  addendum lands them.

**Verified good in this phase.** There is exactly one `cache.Store` call site in
`internal/vendors`, and `cachedSessionPath` and `FindJSONLSession` never write,
so the enumeration of what can enter the cache is complete. The stat-failure
path stores nothing and has no nil dereference. `ForeignIndex` remains fully
mutex-guarded with no self-deadlock from `liveRows` into `Persist`, and `warn`
fires at most once. One index construction, one cache read and at most one cache
write per invocation, confirmed through the real dispatcher rather than through
a stub. `StatAbs`/`OpenAbs` symmetry is real and `jsonltest.CountingFS`
implements `fs.StatFS`, which is what makes the never-opened assertions
meaningful.

### Review 3 (`2026-09-16`)

One minor and one note of this phase's own. The minor is a claim in the README
invariant table that this phase's work made inert two rounds ago and that no
round has restated; it is corrected in the review's own change, not deferred.
The note is routed to the addendum, `phase-7-review-3-fixes.md`. This phase is
not reopened, and the round's verdict is **ready**.

| ID       | Severity | Where                                                                   | Finding                                                                   |
| -------- | -------- | ------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `R3-51`  | minor    | README, *Invariants*, *a cache that cannot be constructed is absent, never a typed nil* | The row names a mechanism this worklog has proved inert, in the table where it reads as verified |
| `R3-02`  | note     | `internal/chat/handler_list_chat.go`, `persistForeignCache`             | An unguarded field read that is a latent race the current call graph hides |

- [x] `R3-51` — the row's mechanism read "`NewForeignIndex` returns an error;
  the caller assigns the field only on success", with
  `TestForeignIndex_noCacheDirDisablesCaching` as its test. Neither holds the
  invariant. Phase `6`'s own mutation table records "`foreignCacheOrNil` assigns
  the factory result without its non-nil guard → caught by **nothing**", and
  phase `6`'s implementation notes explain why it cannot be caught: a failed
  factory already returns a genuinely nil interface, so the guard is a no-op,
  and a typed nil defeats a non-nil check anyway. The named test exercises the
  constructor and a handler with *no factory at all*, never a *failed* factory.
  What actually holds the invariant is the composition root's nil-interface
  contract in `main.go`, asserted by
  `TestCommands_buildConstructsNoForeignIndex`, which phase `6` confirms **does**
  fail under mutation. The row named neither. This is the third instance of the
  "guard that catches nothing" pattern — after round one's typed-nil guard and
  readiness item `2` — and the first in the invariant table, where a reader takes
  it as verified. Closed in the review's own change: the mechanism is restated as
  the composition root's contract and the row names the test that fails under
  mutation, with `TestForeignIndex_noCacheDirDisablesCaching` kept as what it
  really covers, the constructor's own error.
- [x] `R3-02` — `persistForeignCache` reads `cq.foreignCache` directly, and its
  doc comment advertises "it reads the resolved field rather than the resolver"
  as a deliberate safety property. It is safe only because the single call site
  is a `defer` on the goroutine that just resolved the cache. A reviewer proved
  the hazard with a probe: one goroutine calling `foreignCacheOrNil()` while
  another calls `persistForeignCache()` is reported by `-race` as a write/read
  on `ChatHandler.foreignCache`, the write being inside `sync.Once.doSlow`.
  Mutating `persistForeignCache` to go through `foreignCacheOrNil()` breaks no
  test, so neither the current property nor its replacement is pinned by
  anything. A future `defer cq.persistForeignCache()` at a higher level, or the
  same call from a worker, would introduce a real race that no existing test
  would catch. **Maintainer decision:** state the call contract on
  `persistForeignCache` explicitly — it must run on the goroutine that resolved
  the cache — or read the field through a non-constructing guarded accessor.
  Prefer the guarded accessor where it costs nothing, since a stated contract is
  one more copy of a claim that can rot.

**Verified good in this phase.** `persistForeignCache` is called from exactly
one place, outside the interactive loop, so there is no skipped and no doubled
write, and the `continue` path mutates nothing, which is why having no persist
there is correct rather than an omission. `Locate` filters by source before it
ranks, and `cachedSessionPath` re-validates the winner. `liveRows` still prunes
on `fs.ErrNotExist` alone. `ChatHandler` is never copied: `go vet`'s copylocks
analysis is clean with the `sync.Once` in place. Lazy resolution is race-free
under a sixty-four goroutine probe — exactly one index built, the same non-nil
interface observed by all of them, a panicking factory leaving later askers
undeadlocked, and a memoised failure that does not retry.
