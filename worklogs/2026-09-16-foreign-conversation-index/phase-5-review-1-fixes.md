# Phase 5 — Review one fixes

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Close every finding of the first review round, chiefly by making the shipped
code obey the README's promoted rule that an error is not a fact about the
file, without changing what a correct run discovers.

## Specification

This phase is an addendum: it re-opens no phase file and changes no design the
README did not already carry. Its reading contract is the README and this file.
The findings it closes are listed in the README feedback index; each subsection
below names the ones it closes.

### An open failure is not a completed scan (`R1-01`, blocker)

`scanJSONLFileRow` in `internal/vendors/jsonl_discover.go` returns the same
`(SourceRow{}, false)` for two different facts: the scan reached EOF and found
no session identity, and `OpenAbs` refused the file. `discoverJSONLFile` stores
that zero row in both cases, keyed by the stat taken before the read. Changing
a file's mode changes neither its size nor its mod time, so the negative row is
never invalidated and the conversation leaves `clai chat list` permanently.

That breaks the README invariant that the foreign index is derived, the
Definition of success row that says deleting the cache changes only timing, and
the Strategy table's promise that a lost cache is recoverable and never a data
loss. It is reachable in ordinary use: a descriptor exhaustion the worker pool
makes likelier, a mandatory-access-control denial, a network filesystem hiccup,
or the writing tool holding the file closed for a moment.

The fix separates the two facts. `scanJSONLFileRow` reports the open failure
distinctly — a third result, or a sentinel error — and `discoverJSONLFile`
skips `Store` for it while keeping the negative-caching branch exactly as it is
for a scan that completed and yielded no `SourceID`. Nothing about the hit path,
the stat order, or the row contents changes.

### A stat failure is not a missing file (`R1-04`, minor)

`ForeignIndex.liveRows` in `internal/chat/foreign_index.go` prunes an unmarked
row whenever `os.Stat` returns any error. Make an unreadable source directory
and the walk yields nothing, every row is unmarked, every stat fails with a
permission error, and the whole cache is written back empty although every file
still exists. The phase-two liveness table already says the opposite: a root
that was unreachable this run must not cost the whole cache.

The fix drops a row only when the error reports the file absent, and keeps it on
every other stat error. It is the same root cause as `R1-01` against the same
promoted rule.

### The index is built only when a chat verb runs (`R1-02`, major)

`chatDeps` in `main.go` constructs the `ForeignIndex` — which reads and decodes
the cache file — and `commands()` calls it while building the whole command map,
before dispatch. Every invocation therefore pays the load: a query, a photo
command, a version print. The README invariant says `internal/chat` loads the
index once per invocation; it is loaded once per *process*, including processes
that will never list a chat. Against a corpus-sized index the measurement in the
review put the cost of an unrelated verb at more than double, in a worklog whose
whole premise is invocation cost.

`CommandDeps.ForeignCache` becomes a factory — a function returning a
`vendors.SourceCache` — that `setChatQuerier` invokes, so only a chat verb pays.
The typed-nil safety `NewForeignIndex` provides must survive the change: on
failure the factory yields a nil interface, never an interface holding a nil
pointer, and `setChatQuerier` keeps its existing guard.

### The persist-failure warning follows read-only, not no-create-config (`R1-03`, major; D25)

`ForeignIndex.warn` returns early under `utils.NoCreateConfig`, and
`readOnlyChatSetup` in `internal/chat/cmd.go` sets that flag unconditionally for
`list`. The warning the phase-two error-coverage table promises therefore cannot
reach a `chat list` user: with the cache directory unwritable the verb prints
nothing, the cache is never written, and the user rescans the whole foreign
corpus on every listing forever with no way to learn why. The identical fault
under `chat continue` does warn, so the same defect is diagnosed differently by
which verb was typed.

D25 replaces the warning half of D24 and leaves its persist half intact. The
gate becomes `utils.ReadonlyConfig`, which is what actually marks a raw,
machine-readable or shell-hook run, set from the raw flag in
`internal/command.go`. An interactive listing that cannot write its cache says
so once; a raw run stays silent.

`architecture/config.md` states that a read-only mount produces no stderr noise
and names the foreign index as keeping that promise through the warning. That
sentence stops being true under D25, so amending it is part of this phase and
not optional — the document must say that the promise is kept for raw runs and
that an interactive listing warns once.

### `Locate` is deterministic (`R1-05`, minor)

`ForeignIndex.Locate` ranges a Go map and returns the first match, so when two
rows share a source and session identifier the answer varies between runs, while
the walk fallback in `FindJSONLSession` returns the lexically first match. The
invariant that lookup and discovery never disagree about which file holds a
session belongs to phase 1, which had no index to disagree with; phase 2's
locator inherited the obligation without inheriting the tie-break, so the
invariant row is promoted to this README by this phase. A project directory
restored from a backup produces exactly this: the
same identifier under two paths, and `chat continue` opening a different
transcript on consecutive runs.

The fix keeps the lexically smallest matching path, which is the walk's own
tie-break.

### What a cancellation really does to the cache (`R1-51`, minor)

The phase-three integration contract says a cancellation mid-flight has no
required side effects. It has one: files a worker finished before the cancel
were already committed through `cache.Store`, and `internal/chat` persists that
cache, while `DiscoverJSONL` returns no rows. The behaviour is benign and
arguably desirable — every stored row came from a complete scan keyed by its own
pre-read stat, so it is a valid hit next run — but it is neither written down
nor guarded, because the cancellation test runs with a nil cache.

This phase states the real rule in the contract row below and exercises it with
a live cache. No behaviour changes.

### Recorded figures the shipped tree contradicts (`R1-52`, `R1-06`, minor and note)

The README's phase-zero baseline table carries a cold-discovery figure under an
instruction telling the reader to rerun the command. The shipped tree gives a
different figure, and the original is not reproducible from any tree state any
more because the line cap it measured is deleted — which makes the regression
ceiling derived from it permanently unverifiable too. The same table's corpus
datum and the discoverable total it implies also disagree with what the shipped
reader reports.

Phase two's recorded warm figure came from a benchmark that was deleted after
measuring, and its cold figure predates the parallel uncapped discovery that now
runs through the same path. Neither is a defect; both must be marked so a later
round does not treat them as independently verified.

The fix is editorial and lands in the README and the two phase files: mark each
figure as pre-phase-three historical, record the shipped figure beside it, and
say plainly which of the corpus totals is the corpus fact and which is the
discoverable total.

### The prefilter's spelling assumption is documented, not engineered away (`R1-53`, minor; D26)

Both vendors' `MayContribute` search for literal byte spellings of JSON keys,
while `Fields` decodes with `encoding/json`, which matches struct tags without
regard to case and accepts escaped key characters. A line spelling a key
differently therefore yields a non-zero `LineFields` but is rejected by the
prefilter — the silent undercount `LinePrefilter`'s own doc comment forbids. It
is judged unreachable against the writers both vendors read today, and the
generated corpus emits only canonical spellings, so the conformance suite cannot
catch it by construction.

D26 records the assumption rather than re-engineering the prefilter, the way D23
recorded the one-identity assumption. The `MayContribute` doc comment in each
vendor and the conformance suite's own documentation state that the prefilter is
sound only for canonically spelled JSON keys. No case-variant fixture line is
added to the generated corpus: it would make the conformance suite fail by
construction, and the only fix would be a per-line case-insensitive scan whose
cost defeats the prefilter.

### The exactness claim gains its one caveat (`R1-54`, note)

`architecture/continue-from-claudex.md` says the count is exact from discovery
onward, unconditionally. `vendors.ReadMaxToken` still truncates a file's entire
scan at the first oversized line, and because the truncated scan's result is now
cached, the undercount is persisted rather than recomputed each run. Keeping the
token bound is accepted as pre-existing; the sentence gains one clause naming it,
so exact is not read as unconditional.

### Invariants

| Invariant                                                                           | Mechanism                                                                                   | Test                                                          |
| ------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| A file the filesystem refused to open is never written to the cache                  | `scanJSONLFileRow` reports the open failure distinctly; `discoverJSONLFile` skips `Store` for it | `TestDiscoverJSONL_openFailureIsNotCached`                    |
| A completed scan that yielded no identity is still cached negatively                 | `Store` is still reached on the completed-scan branch, with the zero row                     | `TestDiscoverJSONL_negativeCachingSurvivesTheSplit`            |
| Discovery stays cache-agnostic over a corpus that contains an unreadable file        | Nil-cache, cold-cache and warm-cache runs return identical rows                              | `TestDiscoverJSONL_cacheAgnosticWithUnreadableFile`            |
| A cached row is pruned only when the filesystem reports its file absent              | `liveRows` drops on a not-exist error and keeps the row on every other stat error            | `TestForeignIndex_statFailureKeepsRow`                         |
| A source root unreachable this run costs no rows at all                              | The same rule, reached through a root that cannot be walked or stat-ed                       | `TestForeignIndex_unreachableRootKeepsEveryRow`                |
| `Locate` returns the same path on every run                                          | One matching path always wins, never a map's iteration order. The winner is the walk's first, restated by the review-two addendum (`R2-02`) | `TestForeignIndex_locateReturnsLexicallySmallestPath`          |
| Lookup and discovery never disagree about which file holds a session                 | `Locate`'s tie-break is the walk fallback's tie-break. Within one directory only, as written here; the cross-directory case is the addendum's (`R2-02`) | `TestForeignIndex_locateAgreesWithWalkFallback`                |
| Only a chat verb that consults the cache constructs or reads the foreign index       | `CommandDeps.ForeignCache` is a factory; `commands()` does not invoke it, and neither does `setChatQuerier` — the handler resolves it where the cache is read (D27, `R2-03`) | `TestCommands_buildConstructsNoForeignIndex`                   |
| A chat verb that consults the cache invokes the factory exactly once                 | Restated by D27 as the two rows the README carries: the non-consulting verbs invoke it not at all, and a once-guard bounds the consulting ones (`R2-03`) | `TestChatCommand_cacheFactoryRunsOncePerVerb`                  |
| A factory that fails leaves a nil interface, never a typed nil                       | The factory returns a nil interface on failure; the handler's resolver keeps its non-nil guard, which is defence in depth and not the mechanism | `TestChatCommand_failedFactoryLeavesCacheNil`                  |
| An interactive listing whose cache cannot be written warns once                      | `warn` gates on `utils.ReadonlyConfig`, driven through the real verb                          | `TestChatList_unwritableCacheWarnsThroughTheVerb`              |
| A raw run whose cache cannot be written stays silent                                 | The same gate, with the raw flag set                                                         | `TestChatList_rawVerbStaysSilent`                              |
| Rows committed before a cancellation stay cached and are valid hits next run         | Each stored row came from a complete scan keyed by its own pre-read stat                     | `TestDiscoverJSONL_cancelMidFlightKeepsScannedRowsCached`      |

### Parameters this phase owns

Values live in the README parameters table and are not restated here.

| Parameter                                                               | Why this phase owns it                                            |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| `chat.CommandDeps.ForeignCache` as a `func() vendors.SourceCache` factory | `R1-02` moves construction from the command map to the chat verb   |
| The persist-failure warning gate                                         | D25 moves it from `utils.NoCreateConfig` to `utils.ReadonlyConfig`  |

### Files

| File                                                | Change                                                                                                 |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `internal/vendors/jsonl_discover.go`                 | `scanJSONLFileRow` distinguishes an open failure from a completed empty scan; `discoverJSONLFile` skips `Store` on the open failure |
| `internal/vendors/jsonl_discover_test.go`            | `TestDiscoverJSONL_openFailureIsNotCached`, `TestDiscoverJSONL_negativeCachingSurvivesTheSplit`, `TestDiscoverJSONL_cacheAgnosticWithUnreadableFile`, `TestDiscoverJSONL_cancelMidFlightKeepsScannedRowsCached` |
| `internal/chat/foreign_index.go`                     | `liveRows` prunes only on a not-exist error; `Locate` returns the lexically smallest match; `warn` gates on `utils.ReadonlyConfig` |
| `internal/chat/foreign_index_test.go`                | `TestForeignIndex_statFailureKeepsRow`, `TestForeignIndex_unreachableRootKeepsEveryRow`, `TestForeignIndex_locateReturnsLexicallySmallestPath`, `TestForeignIndex_locateAgreesWithWalkFallback` |
| `internal/chat/cmd.go`                               | `CommandDeps.ForeignCache` becomes a factory; both chat setups invoke it inside `setChatQuerier`         |
| `internal/chat/cmd_test.go`                          | `TestChatCommand_cacheFactoryRunsOncePerVerb`, `TestChatCommand_failedFactoryLeavesCacheNil`, `TestChatList_unwritableCacheWarnsThroughTheVerb`, `TestChatList_rawVerbStaysSilent` |
| `main.go`                                            | `chatDeps` hands over the factory instead of a constructed index                                        |
| `main_foreign_cache_test.go`                         | New, root package: `TestCommands_buildConstructsNoForeignIndex`                                          |
| `internal/vendors/anthropic/source_reader.go`        | `MayContribute` doc comment states the canonical-spelling assumption (D26)                               |
| `internal/vendors/pi/source_reader.go`               | The same                                                                                                 |
| `internal/vendors/jsonltest/conformance.go`          | The suite's documentation states what it can and cannot catch (D26)                                      |
| `architecture/config.md`                             | The read-only sentence amended for D25                                                                   |
| `architecture/continue-from-claudex.md`              | The exactness claim gains its token-bound caveat (`R1-54`)                                               |
| `README.md`, `phase-2-foreign-index.md`, `phase-3-parallel-exact.md` | Historical figures marked, shipped figures recorded beside them (`R1-52`, `R1-06`)        |

The root-package test file is invisible to readiness checklist item `2a`, whose
grep matches test paths under `internal/` only. That is the residual phase four
already recorded, not a new gap.

## Integration contract

| Trigger                                                                                     | Collaborators                              | Observable result                                              | Required side effects                                                   | Prohibited side effects                                      |
| ----------------------------------------------------------------------------------------------- | -------------------------------------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------- | -------------------------------------------------------------- |
| A session file is unreadable during a cold listing, then readable again with its size and mod time unchanged | generated corpus, live `ForeignIndex`      | The second listing shows the row                               | The file is scanned on the second run                                    | No row written for it on the first run; no listing failure    |
| A non-chat verb runs                                                                         | the real command map, a populated cache dir | The verb's own output                                          | None                                                                     | No `ForeignIndex` constructed; no read of the cache file      |
| An interactive listing whose cache directory cannot be made                                  | the real `list` verb, a cache dir whose parent is a file | Every row listed                                 | One warning on stderr                                                    | No second warning; no partial cache file                      |
| The same fault under a raw run                                                               | the same, with the raw flag set             | Every row listed                                               | None                                                                     | Nothing on stderr                                             |
| A source root becomes unreachable between two listings                                       | warm `ForeignIndex`                         | The listing degrades to what the walk can reach                | The cache is rewritten holding every row                                 | No row pruned                                                 |
| Two files carry the same source and session identifier                                       | warm `ForeignIndex`                         | Continuing opens the file the walk would choose, on every run  | One stat to confirm the cached path                                      | No run-to-run variation; no disagreement with a cache-less run |
| The context is cancelled while workers are running, with a live cache                        | cancellable context, live `ForeignIndex`    | The context error is returned and no rows                      | Rows finished before the cancel stay stored and are valid hits next run   | No row stored from a partial scan                             |

## Acceptance criteria

| Outcome                                                                                  | Test or command                                                                          |
| -------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| An unreadable file is never negatively cached                                             | `TestDiscoverJSONL_openFailureIsNotCached`                                                |
| A completed scan with no identity is still cached negatively                              | `TestDiscoverJSONL_negativeCachingSurvivesTheSplit`                                        |
| Deleting the cache changes no row, over a corpus containing an unreadable file            | `TestDiscoverJSONL_cacheAgnosticWithUnreadableFile`                                        |
| A stat error other than absence never prunes a row                                        | `TestForeignIndex_statFailureKeepsRow`                                                     |
| An unreachable root costs no rows                                                         | `TestForeignIndex_unreachableRootKeepsEveryRow`                                            |
| `Locate` answers identically on every run and agrees with the walk                        | `TestForeignIndex_locateReturnsLexicallySmallestPath`, `TestForeignIndex_locateAgreesWithWalkFallback` |
| A non-chat verb does no foreign-cache work                                                | `TestCommands_buildConstructsNoForeignIndex`                                               |
| A chat verb builds the index exactly once, and survives a factory failure                 | `TestChatCommand_cacheFactoryRunsOncePerVerb`, `TestChatCommand_failedFactoryLeavesCacheNil` |
| An interactive listing warns once when the cache cannot be written; a raw run does not     | `TestChatList_unwritableCacheWarnsThroughTheVerb`, `TestChatList_rawVerbStaysSilent`        |
| The cancellation cache rule is exercised rather than asserted in prose                    | `TestDiscoverJSONL_cancelMidFlightKeepsScannedRowsCached`                                  |
| `architecture/config.md` states the amended read-only rule                                | `grep -n 'ReadonlyConfig' architecture/config.md` names the foreign index warning           |
| The exactness claim carries its caveat                                                    | `grep -n 'ReadMaxToken' architecture/continue-from-claudex.md` returns the caveat clause     |
| Both vendors and the conformance suite state the spelling assumption                      | `grep -rn 'canonically spelled' internal/vendors/` names both readers and the suite          |
| Every recorded figure is either reproducible or marked historical                         | The phase-zero benchmark command at `benchmark-invocations`, its figure recorded beside the historical one |
| Every gate passes unedited                                                                | `make qa`; `go test ./... -race -cover -count=3 -timeout=30s`                               |
| Coverage of the touched packages stays at or above the floor                              | `go test ./internal/vendors/... ./internal/chat/ -cover`                                    |

## Error coverage

| Failure                                                            | Expected outcome                                                                     | Test                                                        |
| ---------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `OpenAbs` fails for a walked file                                   | The row is skipped for this run and nothing is cached; the next run retries the file    | `TestDiscoverJSONL_openFailureIsNotCached`                   |
| The scan completes and finds no session identity                    | The zero row is cached, exactly as before                                               | `TestDiscoverJSONL_negativeCachingSurvivesTheSplit`           |
| `os.Stat` fails with a permission error during the prune            | The row is kept                                                                         | `TestForeignIndex_statFailureKeepsRow`                        |
| Every row's file is unreachable during the prune                    | Every row is kept and the cache is rewritten intact                                     | `TestForeignIndex_unreachableRootKeepsEveryRow`               |
| The cache directory cannot be created under an interactive verb     | The listing succeeds and one warning reaches stderr                                     | `TestChatList_unwritableCacheWarnsThroughTheVerb`             |
| The cache directory cannot be created under a raw verb              | The listing succeeds and stderr stays empty                                             | `TestChatList_rawVerbStaysSilent`                             |
| The index cannot be constructed when the chat verb runs             | The factory yields a nil interface, the field stays unset, and the listing scans        | `TestChatCommand_failedFactoryLeavesCacheNil`                 |
| Two cached rows share a source and session identifier               | The lexically smallest path is returned, on every run                                   | `TestForeignIndex_locateReturnsLexicallySmallestPath`         |
| The context is cancelled mid-flight with a live cache               | No rows are returned; the rows already committed stay valid                             | `TestDiscoverJSONL_cancelMidFlightKeepsScannedRowsCached`     |
| A vendor spells a JSON key non-canonically                          | Out of contract by D26: documented in both readers and in the suite, caught by review   | The spelling-assumption grep in the acceptance table          |

## Implementation notes

Executed by clai on `2026-09-16` against the uncommitted working tree holding
every phase before this one. Deltas only.

### What the specification did not say

**A blocker fix that needed a signature, not a sentinel.** `scanJSONLFileRow`
takes the specification's first option: a third result. It returns
`(SourceRow, bool, error)`, where a non-nil error means the file was never
opened and `(SourceRow{}, false, nil)` keeps its old meaning — the scan
reached EOF and the file names no session. `discoverJSONLFile` returns early
on the error and reaches `Store` on every other path, so the negative-caching
branch is untouched.

**D25 invalidates a test the Files table does not list.**
`TestForeignIndex_readOnlyDegradesSilently` asserted silence under
`utils.NoCreateConfig`, which is exactly what D25 says must no longer be true,
so it failed the moment the gate moved. Its silence subtest now sets
`utils.ReadonlyConfig`, and a third subtest asserts the other half directly:
`utils.NoCreateConfig` alone no longer suppresses the warning. The write half
of D24 is untouched and its subtest is unchanged.

**`setChatQuerier`'s guard cannot enforce the typed-nil invariant.** The
invariant table gives the mechanism as "the factory returns a nil interface on
failure; `setChatQuerier` keeps its non-nil guard". Only the first clause does
any work. An interface holding a nil `*ForeignIndex` is not nil, so the guard
passes it straight through, and removing the guard entirely did not fail
`TestChatCommand_failedFactoryLeavesCacheNil` — the mutation table below
records it. The obligation belongs to the composition root, which is where the
assertion now lives: a subtest of `TestCommands_buildConstructsNoForeignIndex`
drives the real factory with a failing constructor and with an unresolvable
cache directory, and a mutation that ignores the constructor error does fail
it. The guard is kept, documented as defence in depth rather than as the
mechanism.

**Two seams were needed for the new criteria to be observable at all.**
`internal.Command` exposes no way to read back the querier it was given, so
`setChatQuerier` now delegates to `newChatQuerier`, which returns the handler;
the adapter keeps its name and its single responsibility. And `main.go` gained
a `newForeignIndex` package variable over `chat.NewForeignIndex`, following
the `newSummarizer` pattern the file already uses — without it "building the
command map constructs no index" has no observable, since a construction that
is never made leaves no trace on the filesystem.

**Two findings needed no production change.** `R1-51`'s cancellation rule and
the negative-caching branch were already correct; their tests passed before the
fixes and are regression guards for the split, not reproductions. `R1-53` is
documentation by decision (D26). `R1-52`'s phase-three figures were already
split into corpus and discoverable totals by phase 3 itself, so only the
shipped rerun row was added there; the marking work fell on the phase-two
table and the README Budgets note.

**`R1-07`'s note half is closed by the same tests as `R1-03`.**
`TestChatList_unwritableCacheWarnsThroughTheVerb` and
`TestChatList_rawVerbStaysSilent` drive the real `list` verb through
`Command` → `Setup` → `Run` against an isolated `CLAI_CONFIG_DIR`, a generated
corpus injected through `allSourceReaders`, and captured stdout. Nothing sets
a config global by hand: the verb sets them, which is the whole point of the
finding.

### Every fix was proved by a failing test first

| Finding | The test, run against the shipped code | Outcome |
| --------- | ---------------------------------------- | --------- |
| `R1-01` | `TestDiscoverJSONL_openFailureIsNotCached` | failed: "the unreadable file was cached as {...}" with a zero row under the pre-read stat |
| `R1-01` | `TestDiscoverJSONL_cacheAgnosticWithUnreadableFile` | failed: the cached run returned two rows where the cache-less run returned three — the derived-cache promise, broken |
| `R1-02` | `TestCommands_buildConstructsNoForeignIndex` | failed against a reinstated eager construction: `"building the command map constructed the foreign index 1 times"` |
| `R1-03` | `TestChatList_unwritableCacheWarnsThroughTheVerb` | failed against the `utils.NoCreateConfig` gate: `"an interactive listing printed 0 warnings"` |
| `R1-04` | `TestForeignIndex_statFailureKeepsRow` | failed: a row was pruned on a permission error |
| `R1-04` | `TestForeignIndex_unreachableRootKeepsEveryRow` | failed: the persisted cache came back empty although every file existed |
| `R1-05` | `TestForeignIndex_locateReturnsLexicallySmallestPath` | failed on the first round: the map answered with a middling path |
| `R1-05` | `TestForeignIndex_locateAgreesWithWalkFallback` | failed through `anthropic.SourceReader.Read`: continuing opened a different transcript than the cache-less walk |

### Mutation table

Each fix was reverted in place after it landed, to confirm the test that claims
it still fails. Every mutation was reverted before the gates were run.

| Mutation | Caught by |
| ---------- | ----------- |
| `warn` gates on `utils.NoCreateConfig` | `TestChatList_unwritableCacheWarnsThroughTheVerb` |
| `warn` gates on nothing | `TestChatList_rawVerbStaysSilent` and `TestForeignIndex_readOnlyDegradesSilently` — so the pair pins the gate from both sides, not only the permissive one |
| `liveRows` prunes on any stat error | `TestForeignIndex_statFailureKeepsRow`, `TestForeignIndex_unreachableRootKeepsEveryRow` |
| `Locate` returns the first map match | `TestForeignIndex_locateReturnsLexicallySmallestPath`, `TestForeignIndex_locateAgreesWithWalkFallback` |
| `chatDeps` constructs the index eagerly | `TestCommands_buildConstructsNoForeignIndex` |
| the composition root ignores the constructor error | the typed-nil subtest of `TestCommands_buildConstructsNoForeignIndex` |
| `newChatQuerier` drops its non-nil guard | **nothing** — see the note above; the guard is not the mechanism |

### Verification

| Command | Result |
| --------- | -------- |
| `go test ./... -race -cover -count=3 -timeout=30s` | all green, `24.4` s wall at host load `5.71`. A first run after the edits took `31.0` s including compilation |
| `make qa` | exit `0` at host load `7.15`, forty-seven packages `ok` |
| `go run mvdan.cc/gofumpt@latest -w -l .` | no output |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | no output |
| `go vet ./...`, `go fix ./...` | no output |
| `go run github.com/mibk/dupl@latest -t 80 .` | the same `34` clone groups as the phases before it; none names a file this phase touched |
| `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x` | `34098838` ns/op, `24343347` B/op, `428053` allocs/op at load `1.12` — under the ceiling `107418967` ns/op |
| `go test ./internal/vendors/... ./internal/chat/ . -cover` | `internal/vendors` `70.7`, `internal/chat` `77.9`, `anthropic` `76.5`, `pi` `89.9`, `jsonltest` `93.3`, root `95.6` percent — every one at or above the floor, and `internal/chat` above where review one measured it. The `internal/vendors` figure is **not stable** and is quoted as the range `70.6` to `70.9`: it was first recorded here as `70.9`, both review-two reviewers measured `70.7` and both review-three reviewers measured `70.9`. Phase 6 found the cause — `scanJSONLFileSlots`'s `break feed` branch is covered or not per run — so no single value is the corrected one (`R2-58`, `R3-57`) |
| `go tool cover -func` over the touched files | `discoverJSONLFile`, `scanJSONLFileRow`, `liveRows` and `warn` at `100` percent; `Locate` `90.9`; `newChatQuerier` `85.7` |
| `grep -n 'ReadonlyConfig' architecture/config.md` | line `136` names the foreign index warning gate |
| `grep -n 'ReadMaxToken' architecture/continue-from-claudex.md` | line `308` carries the caveat clause |
| `grep -rn 'canonically spelled' internal/vendors/` | both readers and the conformance suite |
| readiness checklist items `1`, `2`, `6` | recorded here as passing; review two reproduced item `1` failing six times in this file and item `2` failing on one cross-phase duplicate. Both are corrected under `R2-54`. Item `6` matches only the phase that deletes the symbol and the one after it |

The two pre-existing flakes the README records — `anthropic`'s `Test_context`
and `internal/text`'s `TestNewQuerier_costManagerErrorUsesCostWarnf` — did not
reproduce at any load this session, the highest being `7.15`.

## Review findings

### Review 2 (`2026-09-16`)

One blocker, one major and three smaller findings, all routed to the addendum,
`phase-6-review-2-fixes.md`; this phase is not reopened. Each reviewer's
mutation testing confirmed that the tests this phase added are non-vacuous:
five mutations of this phase's fixes were all caught. The blocker is not a
regression — it is the class of `R1-01`, on the branch this phase did not
split.

| ID       | Severity | Where                                                                    | Finding                                                                   |
| -------- | -------- | ---------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| `R2-01`  | blocker  | `internal/vendors/jsonl_discover.go`, `scanJSONLFileRow` with `discoverJSONLFile` | A scan that could not be **read** is still cached as a fact about the file |
| `R2-03`  | major    | `internal/chat/cmd.go`, `setChatQuerier`                                  | The factory runs for chat verbs that never consult the cache — D27         |
| `R2-54`  | minor    | *Verification*, the readiness-checklist row                               | Items `1` and `2` fail, and this phase records them as passing             |
| `R2-02`  | minor    | `internal/chat/foreign_index.go`, `Locate` (filed against phase 2)        | `R1-05` is resolved for determinism; its stated mechanism is false        |
| `R2-58`  | note     | *Verification*, the coverage row                                          | The `internal/vendors` figure is one tenth optimistic                      |

- [x] `R2-01` — this phase split out the **open** failure only. `scanJSONLRawLines`
  returns `s.Err()` and the one caller that matters discards it with `_ =`, so a
  read failure yields `(SourceRow{...}, ..., nil)` and `discoverJSONLFile`
  reaches `cache.Store` with a row built from a partial read, keyed by the
  pre-read `(size, mtime)` — which the failure did not change, so the row can
  never be invalidated. The in-code comment "the scan reads to EOF, so every
  counted role is counted" is the false assumption. Unmet clauses: the README
  Strategy rule *An error is not a fact about the file*; the README invariant
  *the foreign index is derived: removing it changes results in no way except
  timing*; the Definition of success row *deleting `foreign_index.cache` changes
  only timing, never rows*; this phase's acceptance criterion *deleting the cache
  changes no row, over a corpus containing an unreadable file*; and this phase's
  integration-contract row *no row written for it on the first run*. This
  phase's own specification cited "a network filesystem hiccup" as motivation — a
  case that errors from `Read`, not `Open`, and precisely the branch it left
  unfixed. A read failure is also *more* likely than an open failure in practice,
  because the open is usually served from the dentry cache while the read goes to
  the wire: `NFS`/`SMB`, `ESTALE`, a device error, a `fuse` mount blipping.
  Both reviewers reproduced it against the pristine tree with an `fs.FS` whose
  `Open` succeeds and whose `Read` fails.
  **Scenario A, a conversation disappears permanently:** cache-less run three
  rows, warm run two rows, after the filesystem heals. The `R1-01` failure mode
  verbatim. **Scenario B, a persisted wrong count:** a session read-failing
  mid-file caches `MessageCount` `2` against a true `40` — one reviewer measured
  `2` against `20` on a twenty-message fixture. That reintroduces, through the
  failure branch, the exact defect D1 exists to remove, and unlike the
  oversized-line case it is not deterministic in the file's content. One
  reviewer enumerated every path to `Store` and found exactly one call site, so
  the two read-failure rows are the only remaining run-facts that can enter the
  cache. **Maintainer decision D28:** the fix must **not** be a blanket "any scan
  error skips `Store`". `bufio.ErrTooLong` stays cacheable — an oversized line is
  a property of the bytes, the same file truncates identically every run, and
  `(size, mtime)` invalidates it the moment the content changes, so
  cache-agnosticism holds; that is `R2-51`'s verdict and it is correct. Every
  other scanner error must skip `Store`. The fix therefore needs
  `errors.Is(err, bufio.ErrTooLong)` discrimination, not a bare nil-check; a
  blanket skip would silently revert the oversized-line behaviour and the cached
  counterpart of `TestDiscoverJSONL_oversizedLineTruncatesScan`.
- [x] `R2-03` (upgraded from the reviewer's note) — `setChatQuerier` invokes the
  factory for `continue`, `delete`, `list`, `dir`, `dirv2` and `help`, while only
  `list` (through `handleListCmd` into `foreignChatRows`) and `continue` (through
  `readForeignChat`) touch `foreignCache`. `help`, `dir`, `dirv2` and `delete`
  pay the whole `os.ReadFile` plus `json.Unmarshal` for nothing. The reviewer
  filed it as information because this phase's invariant literally says *a chat
  verb invokes the factory exactly once* and the test asserts `1` for those
  verbs — so it is in contract. The contract is what is wrong:
  `readOnlyChatSetup`'s own comment calls `dir` and `dirv2` shell-prompt hot
  paths, so `clai -r c dirv2` in a precmd hook now decodes a corpus-sized index
  on every prompt render. That is the same cost `R1-02` was raised to remove,
  relocated from every verb to most chat verbs. **Maintainer decision D27:** the
  index construction becomes **lazy** — only a verb that actually consults the
  cache constructs it — preserving "at most one construction per invocation"
  with a once-guard, preserving the typed-nil protection at the composition root
  that `R1-02`'s fix established, and keeping the factory's error handling. This
  phase's invariant row and `TestCommands_buildConstructsNoForeignIndex` are
  amended to assert `0` constructions for `help`, `dir`, `dirv2` and `delete`
  and exactly `1` for `list` and `continue`. Amending a phase-five test is
  expected here and is authorised.
- [x] `R2-54` — the *Verification* table claimed readiness-checklist items `1`,
  `2` and `6` pass. Rerun verbatim from the worklog directory, item `1` failed on
  this file six times — an unbackticked date where every other phase backticks
  its date, a `"through 4"` whose phase reference the strip pattern eats, two
  quoted test outputs carrying digits, and a `"phases 2, 3 and 4"` and a
  `"phases 3 and 4"` that the same pattern truncates — and item `2` failed with
  two cross-phase duplicates: `TestForeignIndex_readOnlyDegradesSilently`,
  declared by phase two's acceptance table and rewritten and described by this
  phase, and `TestNewQuerier_costManagerErrorUsesCostWarnf`, cited by two
  phases as a pre-existing flake this worklog did not write. Item `6` passes as claimed. All
  six item-`1` violations are fixed in the review's own change and the
  *Verification* row now records what was actually true. Item `2` itself was also
  repaired, because it had become structurally unable to police what it claims:
  the `costManagerErrorUsesCostWarnf` hit is pure noise while the
  `readOnlyDegradesSilently` hit is a genuine double-declaration, and phase 4 had
  already recorded that the check cannot separate a test a phase declares from
  one a phase cites. A check whose output is majority noise stops being read. The
  rule now qualifies "declared" as naming in a phase's contract half and stops at
  `## Implementation notes`; both duplicates fall out of the repaired rule, and
  item `2a` gained root-package test files so it can see `main_profile_test.go`
  and `main_foreign_cache_test.go` — the residual recorded twice already, now
  fixed rather than recorded a third time.
- [x] `R2-02` (filed against phase 2, landing on this phase's fix) — `R1-05` is
  resolved for determinism, but the mechanism this phase wrote into the README
  invariant is false: the lexically smallest full path is not
  `filepath.WalkDir`'s tie-break across sibling directories.
  `TestForeignIndex_locateAgreesWithWalkFallback` writes both duplicates into one
  directory, where full-path order and walk order coincide, so it structurally
  cannot observe it. See phase 2's review-two section.
- [x] `R2-58` (note) — the *Verification* table claimed `internal/vendors` rose
  to `70.9`. Both reviewers measured `70.7`, unchanged from review one. Every
  other figure this phase recorded reproduces exactly. Corrected in place, here
  and in the README's phase-five journal entry. **Superseded by `R3-57`:** the
  figure oscillates and neither value is the corrected one, so the row now
  quotes the range.

**Verified good in this phase.** `R1-01`'s original branch is correctly and
minimally fixed, with the negative-caching branch pinned by its own test. The
signature change is clean through the concurrency path. Cancellation is fully
specified and guarded, and the rows stored before a cancel are field-identical
to a full cache-less scan. The typed-nil obligation sits at the composition root
with a real assertion that a mutation fails, and this phase's honest note that
`newChatQuerier`'s guard is not the mechanism is accurate. D25's gate is verified
on every listing path with a real built binary — one warning interactive, silence
under `-r` — and `warn` fires at most once with no deadlock from `liveRows` into
`Persist`. One index construction, one cache read and at most one write per
invocation, confirmed through the real dispatcher. Every recorded figure
reproduces except the one `R2-58` corrects.

### Review 3 (`2026-09-16`)

Two minors and one note of this phase's own. All three are the rule review three
promoted — a claim corrected in one copy and left standing in another — and all
three are closed in the review's own change, because each is a worklog edit
rather than a code change. This phase is not reopened, and the round's verdict
is **ready**.

| ID       | Severity | Where                                                                  | Finding                                                                    |
| -------- | -------- | ------------------------------------------------------------------------ | ---------------------------------------------------------------------------- |
| `R3-50`  | minor    | README, *Invariants* and *Parameters and owners*, the zero-cache-IO rows | Both state the pre-D27 wiring as the mechanism                             |
| `R3-53`  | minor    | README, *Parameters and owners*; `main.go`'s `newForeignIndex`         | Readiness item `3` genuinely fails: this phase's seam has no owner row      |
| `R3-57`  | note     | README, *Feedback index*, the `R2-58` row, and this file's coverage row | The index carries a characterisation its own later phase refuted           |

- [x] `R3-50` — the invariant row read "`CommandDeps.ForeignCache` is a factory
  the chat setups invoke; `commands()` only builds the map". Under D27 no chat
  setup invokes anything: `internal/chat/cmd.go` only carries the factory to the
  handler, and the sole invocation is `foreignCacheOrNil` in
  `internal/chat/handler.go`. This phase's corresponding row **was** amended for
  D27; the README's was not, and the parameters table carried the same falsehood
  a second time in the words "invoked in `setChatQuerier`", mitigated only by the
  later D27 row sitting below it. The consequence is not cosmetic: a contributor
  asked to "restore the mechanism" reintroduces `R2-03` — the cost `R1-02` was
  raised to remove and D27 removed a second time. Both rows restated in the
  review's own change.
- [x] `R3-53` — `newForeignIndex` in `main.go` is a package-level injectable
  seam this phase introduced, and `main_foreign_cache_test.go` replaces it to
  count constructions. It had no row in the README parameters table, while the
  comparable `discoverWorkers` seam has one and `cpu-profile-file-name` has one.
  Readiness item `3` — every config field, flag and injectable field has one
  owner — therefore does not pass as written. Phase `4` recorded item `3` as
  passing before this seam existed, so nothing is falsely recorded; the item
  simply became false when the seam landed and no one re-ran it against the new
  surface. A `newForeignIndex` row, owner this phase, added in the review's own
  change. Item `3` passes again.
- [x] `R3-57` — the README feedback index summarised `R2-58` as "phase 5's
  `internal/vendors` coverage figure is one tenth optimistic; every other figure
  reproduces exactly", and this file's *Verification* row recorded `70.7` as the
  corrected value. Phase `6` then established that the figure **oscillates** —
  `70.9` six times of twelve, `70.6` or `70.7` the rest — because
  `scanJSONLFileSlots`'s `break feed` branch is covered or not per run. Both
  round-three reviewers measured `70.9`: the "wrong" figure reproduces and the
  "corrected" one does not. The index was therefore carrying a characterisation
  its own later phase had refuted, which is the feedback index's one job to
  avoid. Both the summary and this file's row now quote the range.

**Verified good in this phase.** `R1-01`'s original branch stays correctly and
minimally fixed, with the negative-caching branch pinned. The typed-nil
obligation still sits at the composition root, and this phase's honest note that
`newChatQuerier`'s guard is not the mechanism is what `R3-51` promotes into the
README invariant table. The `R1-03` command-level tests still drive the real
`list` verb end to end, and D25's gate still fires once interactive and never
raw.
