# Foreign conversation index — `chat list` without rescanning other tools' logs

`clai chat list` spends nearly all of its wall time re-reading other tools'
session logs. `anthropic.SourceReader.Discover` walks `~/.claude/projects/**/*.jsonl`
on **every invocation**, `json.Unmarshal`s each line into a `map[string]any`
to extract five scalars, and caches nothing between runs. The native side
solved this years ago: `chat_index.cache` exists precisely so that listing
never opens a conversation file.

Foreign sources never got the equivalent for one structural reason: clai
**owns** the native write path and upserts the index at save time, but is a
**spectator** to files Claude Code and pi write whenever they like. This
worklog gives foreign conversations their own index, validated by one `stat`
per file instead of a rescan, and extracts the JSONL discovery algorithm —
currently written twice per vendor across two vendors — into the generic
`internal/vendors` layer so the cost rule lives in code instead of prose.

## Status board

| #   | Phase                                                           | Status      | Summary                                                                                                      |
| --- | --------------------------------------------------------------- | ----------- | ------------------------------------------------------------------------------------------------------------ |
| 0   | [Measurement gate](./phase-0-measurement-gate.md)               | Complete    | Repair `DEBUG_CPU`; add the `jsonltest` corpus generator; record the baselines every later phase asserts against |
| 1   | [Generic JSONL discovery](./phase-1-generic-jsonl-discovery.md) | Complete    | `LineFields`, `LineRole`, `JSONLSchema`, `DiscoverJSONL`, `FindJSONLSession`, `StatAbs`; vendors keep only `Fields`. Review 3 routed `R3-01` to [phase 7](./phase-7-review-3-fixes.md), which closed it  |
| 2   | [Foreign conversation index](./phase-2-foreign-index.md)        | Complete — review 1 findings closed by [phase 5](./phase-5-review-1-fixes.md) | `SourceCache` seam, `foreign_index.cache`, `(size, mtime)` validation, index-first session lookup. R1-01, R1-02, R1-03, R1-04, R1-05, R1-06, R1-07; review 2 routed R2-02 and R2-04 to [phase 6](./phase-6-review-2-fixes.md); review 3 routed `R3-02` to [phase 7](./phase-7-review-3-fixes.md), which closed it, and corrected R3-51 in place |
| 3   | [Parallel and exact discovery](./phase-3-parallel-exact.md)     | Complete — review 1 findings closed by [phase 5](./phase-5-review-1-fixes.md) | Bounded worker pool, `MayContribute` prefilter, `DiscoverMaxLines` deleted, `MessageCount` exact. R1-51, R1-52, R1-53; review 2 routed R2-01, R2-51, R2-52 and R2-55 to [phase 6](./phase-6-review-2-fixes.md); review 3 routed R3-52 and R3-56 to [phase 7](./phase-7-review-3-fixes.md), which closed both |
| 4   | [Docs and quality-gate sweep](./phase-4-docs-and-gates.md)      | Complete — review 1 findings closed by [phase 5](./phase-5-review-1-fixes.md) | `continue-from-claudex.md` push/pull contract and cost budget; stale Pi row; every repository gate green. R1-54; review 2 routed R2-55, R2-56 and R2-57 to [phase 6](./phase-6-review-2-fixes.md); review 3 corrected R3-55 in place |
| 5   | [Review one fixes](./phase-5-review-1-fixes.md)                 | Complete — review 2 findings closed by [phase 6](./phase-6-review-2-fixes.md) | Addendum: an error is never cached; the index is built per chat verb, not per process; D25 and D26; the figures corrected. R2-01, R2-03, R2-53, R2-54, R2-58; review 3 corrected R3-50, R3-53 and R3-57 in place |
| 6   | [Review two fixes](./phase-6-review-2-fixes.md)                 | Complete    | Addendum: a read failure is not a fact about the file either; `Locate` ranks by walk order; the index is built lazily; D27 and D28; the promoted rule reaches the architecture tree. R2-01, R2-02, R2-03, R2-04, R2-51, R2-52, R2-55, R2-56, R2-57; review 3 routed `R3-03` to [phase 7](./phase-7-review-3-fixes.md), which closed it, and corrected R3-54 and R3-58 in place |
| 7   | [Review three fixes](./phase-7-review-3-fixes.md)               | Complete    | Addendum: the identity scan's discarded error; `persistForeignCache`'s guarded accessor; D29's intelligible token-bound error; the surviving copies of two corrected claims, plus two more the acceptance grep found. R3-01, R3-02, R3-03, R3-52, R3-56 — all closed |

**Phase order.** Complete in order. Phase 0 is a gate: it repairs the
profiler and produces the fixtures and baselines, and no later phase may
cite a figure it did not produce. Phase 1 is behaviour-preserving and lands
with the vendor test files unedited. Phase 2 makes listing cheap without
touching the cold path. Phase 3 changes the cold path, and only it may
delete the line cap — see D14. Phase 4 is the last phase of the original plan;
phases 5 and 6 are review addenda that follow it.

Phase 6 is a second addendum on the same rationale, routing review two. Phases
2, 3, 4 and 5 keep their `Complete` status for the work they did; their board
rows point at phase 6.

Phase 7 is the third and final addendum, routing review three. Review three's
verdict is **ready**, so phase 7 closes no blocker and no major: it carries the
three code findings the review filed and the copies of two already-corrected
claims that escaped the documents the review corrected in place. One reviewer
proposed a plain documentation sweep instead; the worklog's own lifecycle —
finding, phase, evidence — carries them instead, because three of the findings
are code plus tests and an untracked sweep leaves them unproved. Phases 1
through 6 keep their `Complete` status; their board rows point at phase 7.

Phase 5 is an addendum, not a reopening. The first review round produced many
small findings that cross phase boundaries and share one root cause, so the
fixes are consolidated into a single phase rather than scattered across three
reopened ones; the executing agent's reading contract stays two files, this
README and the addendum. Phases 2, 3 and 4 keep their `Complete` status for the
work they did, and their board rows point at where the review findings are now
tracked.

**Next eligible work: none — the worklog is finished.** Every phase, zero
through seven, is `Complete`; phase 0 was a gate and its decision was
**proceed**. Review 3 returned **ready** from both reviewers before phase 7 ran,
and phase 7 closed the three code findings and the four surviving documentation
copies it carried, none of which blocked a release. Every finding of every
validation and review round is closed. What remains is not work this worklog
owns: an optional review round 4 over phase 7 itself, and the maintainer's own
release, which no agent performs. The out-of-scope items under *Out of scope* —
chiefly the native index floor — are the natural successors.

## Strategy

### Why foreign differs from native

This is the whole design, and every invariant below follows from it.

|                                     | Native conversations                | Foreign conversations                             |
| ----------------------------------- | ----------------------------------- | ------------------------------------------------- |
| Who writes the source of truth      | clai                                | Claude Code, pi                                   |
| How the index learns of a change    | `upsertChatIndex` at save — **push** | nobody notifies clai — **pull**                   |
| Cost of knowing a row is current    | zero; clai was the writer           | one `stat` per file                               |
| What invalidates a row              | nothing; the writer maintains it    | `size` or `mtime` differs from the cached pair    |
| What a lost cache costs             | a full rebuild over every chat      | a full rescan; recoverable, never a data loss     |

`chat_index.cache` is a **durable record**. `foreign_index.cache` is a
**derived cache**: deletable at any moment with no effect beyond timing.
They do not share a file (D2) because they do not share invalidation rules,
and a corrupt foreign row must never trigger a rebuild across the native
corpus.

### Measured baseline

Measured 2026-09-16 on the maintainer's machine against the live Claude Code
corpus (`362` files, `575` MB, `223664` lines, `NumCPU=22`, warm page cache),
by a harness that called the real `SourceReader`. Recorded as motivating
evidence; phase 0 reproduces the shape against the generated corpus, and
that reproduction — not this table — is what later phases assert on.

| Discovery strategy                          | Serial  | Eight-way | `MessageCount`         |
| ------------------------------------------- | ------- | --------- | ---------------------- |
| Today: line cap, `map[string]any`           | 3.10 s  | —         | 28216 — **3.8x under** |
| Uncapped `map[string]any`                   | 9.63 s  | —         | exact                  |
| Uncapped typed struct                       | 5.26 s  | 1.55 s    | exact                  |
| Byte prefilter, decode only candidate lines | 4.44 s  | 0.80 s    | exact                  |
| Head decode, byte-count the tail            | 1.35 s  | 0.35 s    | approximate            |
| **Cache hit: `stat` sweep only**            | 5.6 ms  | —         | from the cache         |
| Decode a corpus-sized index (`122` KB)      | 1.3 ms  | —         | —                      |

Three findings shaped the plan.

**The warm win is larger than a prototype suggested.** `stat` sweep plus
cache decode is about `7` ms against `3100` ms, roughly `440x`. The earlier
`191` MB figure was the line-capped prefix; the corpus is three times that.

**Exact counting costs more than today, serially.** `4.44` s against
`3.10` s. Deleting the line cap therefore *regresses* the cold path unless
parallel discovery lands in the same change; at eight workers it is
`0.80` s. Hence D14, and hence the phase order. This also settles the
previously open D12 without a cold-page-cache study.

**The corpus moves while it is read.** Three successive exact-count runs
returned `108424`, `108438` and `108446` messages: the measuring session was
itself appending to `~/.claude/projects`. That is the pull problem
demonstrated rather than argued, and it fixes the validation rule — `stat`
**before** the read, key the row on that `stat` (D15).

### An error is not a fact about the file

Promoted by review 1, which found the same root cause behind a blocker and a
minor finding in code written by two different phases. It is a rule about what
may enter durable state, and every phase that writes to the cache inherits it.

> A row may only be **cached**, or **pruned**, on something the filesystem
> actually reported about the file's *content* — "the scan completed and found
> nothing", "the file does not exist". "I could not open it" and "I could not
> stat it" are facts about *this run*. They must never be written into a cache
> that outlives it.

The distinction is exactly the derived-cache promise. A fact about the content
stays true until the content changes, and `(size, mtime)` detects that change.
A fact about the run is invisible to `(size, mtime)` — a permission denial, a
descriptor exhaustion, an unreachable mount change neither — so a row written
from one can never be invalidated, and the cache stops being derived. At that
point deleting the cache changes rows rather than timing, which is the property
the Definition of success is built on.

Applied, the rule reads: a scan that could not start is not a scan that found
nothing, a scan that could not be **read to its end** is not a scan that found
what it managed to read, and a stat that failed is not a file that is gone. The
first and third readings were violated in the shipped tree (`R1-01`, `R1-04`)
and are fixed in phase 5; the second was still violated there (`R2-01`) and is
fixed in phase 6. The rule now also lives in
`architecture/continue-from-claudex.md`, with D28's refinement beside it, so it
survives this worklog's deletion (`R2-56`).

**A content-determined bound is a fact about the file; a transport error is
not** (D28). The rule's test is not "did an error occur" but "would a
cache-less rescan of the same bytes reach the same answer, and does
`(size, mtime)` invalidate it when the bytes change". A line longer than
`vendors.ReadMaxToken` ends its file's scan every run, in the same place, for
the same bytes, and any edit that removes the oversized line changes the file's
`size` or `mod time` — so `bufio.ErrTooLong` **is** a fact about the content and
its truncated row stays cacheable. Every other scanner error — a read that
failed on the wire, a device error, a stale handle, a mount that blipped — is a
fact about this run alone, invisible to `(size, mtime)`, so a row written from
it can never be invalidated. A blanket "any scan error skips the cache" would
therefore be wrong in the other direction: it would silently revert the
oversized-line behaviour that phase 3 specified and phase 4 documented.

**Known limitation: a truncated row is listable but not continuable** (D29,
`R3-03`). D28's correctness has a user-visible edge. A session file holding a
line longer than `vendors.ReadMaxToken` discovers a truncated row, which D28
correctly calls a fact about the file and caches; selecting that row runs
`Read`, which routes the same `bufio.ErrTooLong` through its scan-error wrapper
and fails the verb. Because the row is now cached, the dead end is permanent
until the file's bytes change — before the cache existed, the deleted line cap
meant discovery never formed an opinion about such a file at all. `Read` must
**not** tolerate the truncation: feeding a silently shortened conversation to a
model is worse than failing, and it would contradict D1's "fix the field, do not
drop it" stance. A marker column on the listing is scope this worklog does not
own. What is owed is an **intelligible** error naming the cause and the bound,
so the dead end is explained rather than cryptic; phase 7 landed it in
`vendors.ScanJSONLLines`, generically, keeping the sentinel behind `%w` so D28's
gate is unaffected and both vendors inherit the message through their own
unchanged wrap. A future effort that wants the row marked in the listing can
decide with these facts in hand.

### A corrected claim is not corrected until every copy of it is

Promoted by review 3, which found both reviewers tracing five of the round's
findings to one root cause. It is a rule about documentation state, and it binds
every phase that corrects a claim anywhere.

> **A corrected claim is not corrected until every copy of it is.** The copies
> are enumerable, and the enumeration is short: the README invariant row, the
> README parameters row, the owning phase's contract table, the Go doc comment
> on the symbol, and the architecture section. Correcting a subset leaves the
> rest reading as verified, in exactly the documents a contributor reaches
> first.

Three review rounds have each spent at least one finding on a single surviving
copy: round 1 on the typed-nil guard's mechanism, round 2 on `Locate`'s lexical
tie-break (`R2-02`) and on the conformance suite's resolution (`R2-55`), round 3
on five at once. `R3-50`, `R3-52`, `R3-53`, `R3-56` and `R3-57` are all the same
rule instantiated: a pre-D27 mechanism left in the README while phase 5's row
was amended; a conformance overstatement qualified in two documents and
surviving in three; a seam with tests but no parameters row; "lexical order"
surviving in the Go file a contributor edits after the README was fixed; and a
feedback-index summary its own later phase refuted.

The rule's practical form is a checklist, not a virtue: when a phase corrects a
claim, it walks the five copies before it writes its verification table, and
records the walk. A correction that names only the document the finding cited
has closed the finding and not the defect.

### Invariants (non-negotiable)

| Invariant                                                                                   | Mechanism                                                                                        | Owner   | Test                                                          |
| ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ | ------- | -------------------------------------------------------------- |
| A file whose `size` and `mtime` match its cached row is never opened                        | `DiscoverJSONL` stats, calls `SourceCache.Lookup`, and scans only on a miss                       | Phase 2 | `TestDiscoverJSONL_cacheHitOpensNothing`                       |
| A row is keyed by the `stat` taken before the read, never after                             | One `fs.FileInfo` per file flows into `Lookup` and the matching `Store`                            | Phase 2 | `TestDiscoverJSONL_fileGrowingDuringScanIsNotCachedAsCurrent`  |
| An unchanged corpus keeps its cache across runs                                             | `Lookup` marks a hit in use; `Store` marks a miss; only unmarked rows face the prune `stat`        | Phase 2 | `TestForeignIndex_unchangedCorpusSurvivesSecondRun`            |
| Rows whose file no longer exists are dropped, never resurrected                             | `Persist` stats every unmarked row and keeps only those that still exist                          | Phase 2 | `TestForeignIndex_prunesVanishedFiles`                          |
| A row is pruned only when the filesystem reports its file **absent**; any other stat error keeps it | `liveRows` drops on a not-exist error alone — a run-level failure is not a fact about the file | Phase 5 | `TestForeignIndex_statFailureKeepsRow`, `TestForeignIndex_unreachableRootKeepsEveryRow` (`R1-04`) |
| A file that yields no usable row is cached as such and is not rescanned until it changes    | `Store` accepts a zero row; `DiscoverJSONL` drops empty `SourceID` after the cache consult         | Phase 2 | `TestDiscoverJSONL_unusableFileCachedNegatively`               |
| The foreign index is derived: removing it changes results in no way except timing           | Discovery with a nil cache and with a warm cache produce identical rows                            | Phase 2 | `TestDiscoverJSONL_cacheAgnosticResults`; `TestDiscoverJSONL_cacheAgnosticWithUnreadableFile` (`R1-01`, closed); `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile` (`R2-01`, closed by phase 6) |
| A file the filesystem refused to **open** is never cached, positively or negatively          | `scanJSONLFileRow` reports an open failure distinctly; `discoverJSONLFile` skips `Store` for it, and only a completed scan may cache a zero row | Phase 5 | `TestDiscoverJSONL_openFailureIsNotCached`, `TestDiscoverJSONL_cacheAgnosticWithUnreadableFile` (`R1-01`) |
| A scan that **failed to read** its file is never cached either                               | `scanJSONLFileRow` propagates the scanner's error; `discoverJSONLFile` skips `Store` unless the error is the content-determined token bound (D28) | Phase 6 | `TestDiscoverJSONL_readFailureIsNotCached`, `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile` (`R2-01`) |
| A scan truncated by the content-determined token bound **stays** cacheable                   | `discoverJSONLFile` discriminates with `errors.Is(err, bufio.ErrTooLong)`; the same bytes truncate identically every run and `(size, mtime)` invalidates the row when they change (D28) | Phase 6 | `TestDiscoverJSONL_oversizedLineRowStaysCacheable` |
| A missing, unreadable, corrupt or version-stale cache never fails a listing                 | Every read error falls through to a full scan; the cache is rewritten when writable                | Phase 2 | `TestForeignIndex_corruptFallsBackToScan`, `TestForeignIndex_versionMismatchRebuilds` |
| Only `chat.SkipIndex` suppresses index I/O; the write is attempted under `utils.NoCreateConfig` | `SkipIndex` suppresses read and write alike. `NoCreateConfig` is scoped to the config dir and this cache is not, so the write is attempted under it (D24) | Phase 2 | `TestForeignIndex_skipIndexNoIO`, `TestForeignIndex_readOnlyDegradesSilently` |
| A persist failure warns **once** on an interactive run and never on a raw run | `warn` gates on `utils.ReadonlyConfig`, which is what marks a raw, machine-readable or shell-hook run (D25, replacing D24's `NoCreateConfig` gate) | Phase 5 | `TestChatList_unwritableCacheWarnsThroughTheVerb`, `TestChatList_rawVerbStaysSilent` (`R1-03`) |
| One cache read and one cache write per invocation, whatever the number of readers           | `internal/chat` loads the index once, passes it to every `Discover`, persists once                  | Phase 2 | `TestForeignChatRows_singleCacheWrite`                          |
| **Zero** cache reads and writes when the invocation is not a chat verb                       | `CommandDeps.ForeignCache` is a factory; `commands()` only builds the map, and the chat setups only carry the factory to the handler — under D27 no setup invokes it (`R3-50`) | Phase 5 | `TestCommands_buildConstructsNoForeignIndex`, `TestChatCommand_cacheFactoryRunsOncePerVerb` (`R1-02`) |
| **Zero** cache reads and writes for a chat verb that never consults the cache                | The factory is invoked lazily by the verbs that read the cache — `list` and `continue` — and not by `help`, `dir`, `dirv2` or `delete`. `readOnlyChatSetup` serves `list`, `dir`, `dirv2` and `help`, of which `dir` and `dirv2` are shell-prompt hot paths and `list` is itself a consulting verb; `delete` goes through `fullChatSetup` (D27, `R3-54`) | Phase 6 | `TestChatCommand_onlyConsultingVerbsConstructTheIndex` (`R2-03`) |
| At most one index construction per invocation, however many times the verb asks              | A once-guard around the lazy factory; the typed-nil protection stays at the composition root | Phase 6 | `TestChatCommand_lazyFactoryRunsAtMostOnce` (`R2-03`) |
| Lookup and discovery never disagree about which file holds a session, cache or no cache      | `Locate` ranks candidates in **walk order** — segment by segment, which is what `filepath.WalkDir` orders entries by — not by whole-path byte order. Lexical full-path order is not the walk's tie-break across sibling directories (`R2-02`) | Phase 6 | `TestForeignIndex_locateFollowsWalkOrder`, `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs` (`R2-02`); determinism alone: `TestForeignIndex_locateReturnsLexicallySmallestPath`, `TestForeignIndex_locateAgreesWithWalkFallback` (`R1-05`, phase 5) |
| A session file has exactly one session identity; the first line that yields one decides it  | `FindJSONLSession` stops at the first line whose `Fields` carries a `SessionID`                     | Phase 1 | `TestFindJSONLSession_stopsAtFirstIdentityLine`, `TestFindJSONLSession_agreesWithDiscover` |
| A vendor package cannot read more than the algorithm decides                                | `JSONLSchema` exposes no file handle, path or reader; its only data method is `Fields(line []byte)` | Phase 1 | `TestJSONLSchema_surfaceIsLineOnly`; review of the interface     |
| `LineFields` is a closed set: a field enters only when the chat list gains a capability      | Every field maps to an eager `SourceRow` field consumed by sort, dedup, group collapse or dir filter | Phase 1 | `TestLineFields_closedSet` reflects over the struct and asserts the exact field set |
| A prefilter never hides a line the schema would have used, **over the fixture corpus the suite is run with** | `RunSchemaConformance` asserts `MayContribute` is true for every fixture line whose `Fields` is non-zero. It detects a narrowing only when the corpus holds a line the narrowed filter misses; it does not police the marker set, and five of the six anthropic markers can be deleted individually without failing it (`R2-55`) | Phase 3 | `TestSchemaConformance_anthropic`, `TestSchemaConformance_pi`; the qualification is documented by phase 6 |
| Parallel discovery returns exactly what serial discovery returns, in the same order         | The walk fixes a deterministic order — `filepath.WalkDir`'s per-directory entry order, which is **not** whole-path byte order (`R2-02`); workers write indexed slots and never `append` | Phase 3 | `TestDiscoverJSONL_parallelMatchesSerial` under `-race`         |
| Concurrent discovery of several sources shares one cache safely                             | `ForeignIndex` guards its map with a mutex; `Lookup`/`Store`/`Persist` are safe for concurrent use   | Phase 2 | `TestForeignIndex_concurrentAccess` under `-race`                |
| The foreign cache directory is created before the first write                               | `os.MkdirAll` before `WriteFileAtomic`; neither `GetClaiCacheDir` nor `WriteFileAtomic` creates it   | Phase 2 | `TestForeignIndex_createsMissingCacheDir`                        |
| A cache that cannot be constructed is absent, never a typed nil                              | The composition root's nil-interface contract: `chatDeps`'s factory in `main.go` returns a **nil interface** on any constructor error and never a nil `*ForeignIndex` behind a non-nil interface. The handler's own non-nil guard is not the mechanism and cannot be — a failed factory already yields a nil interface, and a typed nil defeats a non-nil check (`R3-51`) | Phase 5 | `TestCommands_buildConstructsNoForeignIndex`, whose typed-nil subtest fails under mutation; `TestForeignIndex_noCacheDirDisablesCaching` covers the constructor's own error only |
| Discovery reads only files; it never writes to a foreign root                                | No code path in `internal/vendors` opens a source file for writing                                   | Phase 1 | `TestDiscoverJSONL_neverWritesToSourceRoot` (read-only FS)       |
| A file the filesystem refused to **read** is never reported as a file that names no session  | `jsonlFileIdentity` returns the scanner's error and `FindJSONLSession` fails the lookup instead of walking on to a candidate the walk would not have chosen. A refused *open* stays a skip (`R3-01`) | Phase 7 | `TestFindJSONLSession_readFailureIsNotAMissingSession`, `TestFindJSONLSession_readFailureNeverAnswersWithASibling` |
| Persisting the foreign cache never constructs one, and never races the resolver              | `persistForeignCache` reads through `resolvedForeignCache`, a non-constructing accessor holding the same mutex the resolver writes the field under. `sync.Once` alone cannot serve a reader that must not call `Do` (`R3-02`) | Phase 7 | `TestChatHandler_persistForeignCacheDoesNotConstruct`, `TestChatHandler_persistForeignCacheRacesResolution` under `-race` |
| A scan ended by the token bound fails with an error naming the cause and the bound, and is still the same sentinel | `ScanJSONLLines` wraps `bufio.ErrTooLong` once and generically with `%w`; discovery's own `scanJSONLRawLines` is untouched, so D28's `errors.Is` gate is unaffected (D29) | Phase 7 | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`, `TestSourceReaderRead_tokenBoundErrorIsIntelligible`, `TestPiSourceReaderRead_tokenBoundErrorIsIntelligible`, plus the two oversized-line tripwires |

### Budgets

Timing is compared, never asserted absolutely. Every performance row in every
phase cites `benchmark-regression-band` and the phase-zero benchmark command,
run at the fixed `benchmark-invocations` so two runs are comparable.

| Budget                                                          | Injectable / parameter                                   | Owner   | How a test or command reaches it                                  |
| ----------------------------------------------------------------- | ---------------------------------------------------------- | ------- | ------------------------------------------------------------------- |
| Worker pool bound                                               | `discoverWorkers` seam; `discover-workers`                 | Phase 3 | The test replaces the seam and counts peak concurrent scans        |
| Cold discovery cost against the phase-zero figure                | `benchmark-regression-band`, `benchmark-invocations`       | Phase 0 | The phase-zero benchmark command, rerun and compared per phase      |
| Repository gate wall-clock                                      | `jsonltest.CorpusOptions.Sessions`, `.LinesPerSession`     | Phase 0 | `go test ./... -race -cover -count=3 -timeout=30s`, band recorded per phase in the session journal |

Every later phase re-asserts the last two rows; phase 0 recorded the numbers
they are compared against, reproduced here because a phase reads only this
document and its own phase file.

**Phase-zero baselines** (maintainer's machine, `Intel Core Ultra 7 155H`,
`NumCPU=22`, warm page cache, `2026-09-16`). Rerun the commands; do not quote
these figures as your own measurement.

| Baseline                      | Command                                                                                                    | Figure                                              |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| Cold discovery, line-capped   | `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x`        | `89515806` ns/op, `33044104` B/op, `623968` allocs/op — **historical, pre-phase-3** |
| Regression ceiling from it    | the same command, times `benchmark-regression-band`                                                          | `107418967` ns/op — historical, derived from the row above |
| Cold discovery, as shipped    | the same command on the current tree                                                                         | `31032276` ns/op, `24344167` B/op, `428055` allocs/op |
| Repository gate wall-clock    | `go test ./... -race -cover -count=3 -timeout=30s`                                                            | `24` s to `25` s at host load below `4`               |

**The first two rows are no longer reproducible** (`R1-52`). The instruction
above says to rerun the commands, but the line cap those figures measured was
deleted by phase 3, so the same command on any current tree state returns the
third row instead, and the ceiling derived from the first row cannot be
verified again either. They are kept because phases 2 and 3 were compared
against them, and marked so a contributor who follows the instruction is not
left with an unexplained threefold discrepancy. A phase comparing itself
against the band from here on cites the shipped row.

The default corpus behind the discovery figures is `64` files and `4191484`
bytes, holding `15699` messages — the **corpus** total. Of those, `250` live in
the identity-less fixture that discovery drops by design, so the **discoverable**
total is `15449` across `61` rows from `64` files, which is what the shipped
reader reports; today's line-capped discovery reported `12159` across the same
rows. The corpus and discoverable totals were previously stated as one figure
(`R1-52`).

### Shared interfaces

Defined in `internal/vendors`. Phase 1 owns them; phases 2 and 3 consume and
extend them.

```go
// LineRole classifies one JSONL line for counting and preview extraction.
type LineRole uint8

const (
	LineRoleNone      LineRole = iota // not a message line
	LineRoleUser
	LineRoleAssistant
	LineRoleTool
	LineRoleSkip // explicitly excluded, e.g. Claude sidechains
)

// LineFields is everything one JSONL line may contribute to a SourceRow.
// Closed set: every field is an eager SourceRow field that the chat list
// needs before it can sort, dedup, collapse groups or apply the dir filter.
// When Role is LineRoleSkip every other field is ignored.
type LineFields struct {
	SessionID string
	Cwd       string
	Timestamp time.Time
	Model     string
	Role      LineRole
	UserText  string // full text; the generic layer owns truncation
}

// JSONLSchema is all a JSONL-backed source implements. It never receives a
// path, handle or reader, so it cannot decide how much of a file is read.
type JSONLSchema interface {
	SourceName() string
	Root() string
	SkipDirs() []string
	Fields(line []byte) LineFields
}

// LinePrefilter is an optional upgrade of JSONLSchema. A schema that
// implements it lets the scan skip decoding lines that cannot contribute.
// Not implementing it is always correct, only slower.
type LinePrefilter interface {
	MayContribute(line []byte) bool
}

// SourceCache is the pull-validation seam. A nil cache means "always scan"
// and is what unit tests use. Implementations must be safe for concurrent
// use. Lookup marks a hit as in use for this run; see the prune invariant.
//
// A caller must never hand over a typed nil: an interface holding a nil
// *ForeignIndex is not nil, and the "always scan" branch would be skipped.
// NewForeignIndex therefore returns an error instead of an unusable value.
type SourceCache interface {
	// Lookup returns the row cached for absPath when the pair it was stored
	// under still matches info, and marks that row in use for this run.
	Lookup(absPath string, info fs.FileInfo) (SourceRow, bool)
	// Store records row for absPath keyed by info, and marks it in use. A
	// zero row is a valid entry: it records that absPath yields nothing.
	Store(absPath string, info fs.FileInfo, row SourceRow)
	// Locate names the file that last held (source, sourceID). It answers
	// from cached state alone and touches no filesystem, so the caller must
	// still validate the candidate with Lookup before trusting it. Its
	// tie-break is the walk's own order — see the lookup invariant.
	Locate(source, sourceID string) (absPath string, ok bool)
}

// discoverWorkers is the pool bound, injectable the way internal/chat already
// makes allSourceReaders injectable. Tests replace it; nothing else may.
var discoverWorkers = func() int { ... } // README: discover-workers
```

Generic entry points, all in `internal/vendors`:

- `DiscoverJSONL(ctx, schema JSONLSchema, fsys fs.FS, cache SourceCache) ([]SourceRow, error)`
- `FindJSONLSession(ctx, schema JSONLSchema, fsys fs.FS, cache SourceCache, sourceID string) (string, error)`
- `StatAbs(fsys fs.FS, absPath string) (fs.FileInfo, error)` — the `OpenAbs`
  sibling. It calls `fs.Stat`, which falls back to `Open` plus `File.Stat` on a
  filesystem that does not implement `fs.StatFS`. Every test filesystem in this
  worklog must implement `fs.StatFS`, or the never-opened invariant fails
  against correct production code.

Test-support entry points, in `internal/vendors/jsonltest`:

- `WriteCorpus(tb testing.TB, root string, opts CorpusOptions) Corpus` — the
  `tb.Fatal` wrapper — over `WriteCorpusErr(root string, opts CorpusOptions) (Corpus, error)`
- `RunSchemaConformance(t *testing.T, schema JSONLSchema, lines [][]byte)` over
  `CheckSchemaConformance(schema JSONLSchema, lines [][]byte) error`
- `CountingFS(root string) (fs.FS, *FSCounts)` — an `fs.StatFS` recording opens
  and stats, so "never opened" is asserted the same way everywhere

Each pair exists because `testing.TB` cannot be implemented outside `testing`
and `*testing.T` cannot be intercepted: a helper that only fatals has no
failure a test can observe (D21).

`SourceReader` gains the cache as an explicit collaborator on **both** methods.
`Read` needs it because it resolves a session identifier to a file, which is
the lookup the index exists to make cheap (D19):

```go
Discover(ctx context.Context, cache SourceCache) ([]SourceRow, error)
Read(ctx context.Context, cache SourceCache, sourceID string) (pub_models.Chat, error)
```

Three implementors change — `anthropic.SourceReader`, `pi.SourceReader`, and
the stub in `internal/chat/handler_list_chat_test.go` — plus the existing call
sites in the two vendor test files: thirteen of `Discover` and nine of `Read`.
Phase 2 lists them per file. What `Read` *reads* does not change; only how it
finds the file.

### Rejected alternatives

**Fold foreign rows into `chat_index.cache` as v3.** Conceptually tidier, but
merges a durable record with a derived cache; a corrupt foreign row would then
trigger a rebuild across every native conversation.

**Keep `MessageCount` but estimate it from file size.** Cheap and always
wrong, replacing a truncation bug with an approximation bug.

**Head-only discovery with `Provides()` and a per-file byte ceiling.** The
machinery existed only to make a head-only scan safe. Indexing removes the
need for a head-only scan, and with it the ceiling, the `Provides()`
declaration and the failure mode where a misdeclared schema silently reads to
EOF.

**Byte-counting the tail without verifying candidates.** Fastest of the exact
alternatives' neighbours, but it miscounts when a message body quotes a role
marker. Shipping an approximate count is the defect this worklog removes.

**A `CountJSONLMessages` generic entry point.** Proposed, then dropped: no
caller exists. `DiscoverJSONL` already yields the count and `Read` is out of
scope (D16).

**Re-bounding session lookup with a line cap.** `FindJSONLSession` instead
stops at the first line that establishes the file's identity, which is line
one for both vendors (D17).

### Out of scope

- The native index floor. Decoding all of `chat_index.cache` to render one
  page is the next bottleneck once this lands, and it touches the save path
  for every conversation. It deserves its own effort.
- SQLite or API-backed sources (Cursor, Codex) — D9.
- Any behavioural change to `SourceReader.Read` or the two full-read scan
  sites. Phase 2 gives `Read` the cache parameter and phase 3 drops the scan
  sites' now-constant line-bound argument; what either one reads does not
  change.
- The host-side walk. `WalkJSONLFiles` uses `filepath.WalkDir` while opens go
  through the injectable `fs.FS`; that split predates this worklog and stays.
- Conversation content, grouping semantics, and every existing list toggle.

## Parameters and owners

| Parameter                                                                                   | Default                  | Owner   |
| ------------------------------------------------------------------------------------------- | ------------------------ | ------- |
| `DEBUG_CPU` profile flush (`main` stops the profile before `os.Exit`)                        | —                        | Phase 0 |
| `cpu-profile-file-name`                                                                      | `cpu_profile.prof`       | Phase 0 |
| `benchmark-regression-band` (a phase's figure must not exceed the phase-zero figure by more) | one fifth                | Phase 0 |
| `benchmark-invocations` (`-benchtime` for every comparison run)                              | ten                      | Phase 0 |
| `jsonltest.CorpusOptions.Shape`                                                              | caller-chosen, no default | Phase 0 |
| `jsonltest.CorpusOptions.Seed` (fixture content and timestamps derive from it)               | one                      | Phase 0 |
| `jsonltest.CountingFS` (records opens and stats; implements `fs.StatFS`)                     | —                        | Phase 0 |
| `jsonltest.CorpusOptions.Sessions` (generated fixture session count)                         | 64                       | Phase 0 |
| `jsonltest.CorpusOptions.LinesPerSession` (exceeds the line cap deleted in phase 3)          | 250                      | Phase 0 |
| `vendors.LineRole`, `vendors.LineFields`, `vendors.JSONLSchema`                              | —                        | Phase 1 |
| `vendors.DiscoverJSONL`, `vendors.FindJSONLSession`, `vendors.StatAbs`                       | —                        | Phase 1 |
| `vendors.ReadMaxToken` (scanner token bound, unchanged). A compile-time constant, **not** part of the cache key: a row truncated at this bound is cached at its undercount, so **raising or lowering it requires a `foreign-index-version` bump** (`R2-51`) | 10 MiB                   | Phase 1 |
| `source-preview-runes` (one-line preview width, existing behaviour)                          | 100                      | Phase 1 |
| `source-missing-preview` (placeholder when no user text was found)                           | `(no preview)`           | Phase 1 |
| `vendors.SourceCache` interface; nil means always scan                                       | nil                      | Phase 2 |
| `vendors.SourceReader.Discover` and `.Read` cache parameter                                   | —                        | Phase 2 |
| `foreign-index-file-name`                                                                    | `foreign_index.cache`    | Phase 2 |
| `foreign-index-version` (bump invalidates every row; the documented lever for any incompatible change, including a change to `vendors.ReadMaxToken`) | 1                        | Phase 2 |
| `foreign-index-location` (`utils.GetClaiCacheDir()`; derived state, not config)              | —                        | Phase 2 |
| `chat.NewForeignIndex(dir string) (*ForeignIndex, error)`, `ForeignIndex.Persist() error` (the result and the announcement boundary date from the 2026-09-17 code-style pass) | —                        | Phase 2 |
| `ChatHandler.foreignCache` (injectable; nil disables caching in tests)                        | nil                      | Phase 2 |
| `chat.SkipIndex` gates foreign index I/O; the write is attempted under `utils.NoCreateConfig` (D24) | —                        | Phase 2 |
| The persist-failure warning gate: `utils.ReadonlyConfig`, not `utils.NoCreateConfig` (D25)    | —                        | Phase 5 |
| `chat.CommandDeps.ForeignCache` as a `func() vendors.SourceCache` factory, carried to the handler by `setChatQuerier` and — since D27 — invoked by no setup at all (`R3-50`) | nil                      | Phase 5 |
| `newForeignIndex` (`main.go`), the composition-root construction seam the root test binary replaces to count constructions (`R3-53`) | `chat.NewForeignIndex`   | Phase 5 |
| `MayContribute` soundness assumption: canonically spelled JSON keys (D26)                     | —                        | Phase 5 |
| `vendors.LinePrefilter` optional interface                                                    | not implemented          | Phase 3 |
| `vendors.DiscoverMaxLines`                                                                    | **deleted** (D6)         | Phase 3 |
| `vendors.ScanJSONLLines` line-bound parameter (no non-zero caller remains)                     | **deleted**              | Phase 3 |
| `discover-workers` (bounded pool inside `DiscoverJSONL`, via the `discoverWorkers` seam)      | `min(8, runtime.NumCPU())` | Phase 3 |
| `chat.CommandDeps.ForeignCache` invoked **lazily**, by the consulting verb rather than by `setChatQuerier`, behind a once-guard (D27) | nil                      | Phase 6 |
| The cacheable-scan-error rule: `errors.Is(err, bufio.ErrTooLong)` alone may reach `Store` (D28) | —                        | Phase 6 |
| `jsonlFileIdentity` propagates its scanner error, so `FindJSONLSession` tells "this file names no session" from "this file could not be read" (`R3-01`) | —                        | Phase 7 |
| `ChatHandler.persistForeignCache`'s access to the resolved cache: a non-constructing guarded accessor, not a bare field read (`R3-02`) | —                        | Phase 7 |
| The token-bound read failure's user-facing message: names the cause and the bound, never the raw scanner sentinel (D29) | —                        | Phase 7 |

## Readiness checklist

Run by the author before requesting validation; outcome recorded in the
session journal. Run from this directory.

1. No numerals in phase prose. Backticked spans are verbatim commands, code
   and file names, so they are stripped before the check rather than excused
   one pattern at a time:
   ```
   for f in phase-*.md; do
     awk '/^```/{b=!b; print ""; next} b{print ""; next} {print}' "$f" |
     sed -e 's/`[^`]*`//g' -e 's/[Pp]hases\? [0-9]\+\(.[0-9]\+\)\?//g' \
         -e 's/\bD[0-9]\+//g' -e 's/^#.*//' |
     grep -nE '[0-9]' | sed "s|^|$f:|"
   done
   ```
   Fenced blocks, backticked spans, phase references, decision ids and headings
   are stripped first; anything left is a number this worklog owns and must not
   restate.
2. Every test name is **declared** in exactly one phase. A phase *declares* a
   test in its contract half — specification, invariant, limit,
   integration-contract, acceptance and error-coverage tables, and the Files
   table. Everything from `## Implementation notes` onward — deltas, mutation
   tables, verification rows and review findings — only ever **cites** a test,
   including a test another phase owns and a pre-existing test this worklog did
   not write. An `### Amends earlier phases` subsection is the one place in a
   phase's contract half that may name another phase's test, and it too is a
   citation. The check therefore stops at the heading and skips that subsection:
   ```
   for f in phase-*.md; do
     awk '/^## Implementation notes/{exit}
          /^### Amends earlier phases/{skip=1; next}
          /^#/{skip=0} !skip{print}' "$f" |
     grep -ohE '\bTest[A-Z][A-Za-z0-9_]+' | sort -u
   done | sort | uniq -d
   ```
   The unqualified form this check used until review 2 could not tell a
   declaration from a citation, so it reported a pre-existing flake named in two
   phases' verification prose as a violation and buried the one real
   double-declaration beside it (`R2-54`). A name may still appear several times
   inside its own phase — once in a contract table, once in acceptance — but
   never in two phases' contract halves.
2a. Every test named by a phase's contract half has a home in that phase's file
   list, and every listed test file belongs to a phase. Root-package test files
   count: the check is not scoped to `internal/`, which is what made it blind to
   `main_profile_test.go` and `main_foreign_cache_test.go` (`R2-54`). List the
   pairs and read them together:
   ```
   for f in phase-*.md; do
     echo "== $f"
     awk '/^## Implementation notes/{exit}
          /^### Amends earlier phases/{skip=1; next}
          /^#/{skip=0} !skip{print}' "$f" |
       grep -ohE '\bTest[A-Z][A-Za-z0-9_]+' | sort -u
     awk '/^## Implementation notes/{exit} {print}' "$f" |
       grep -oE '`[a-z][A-Za-z0-9_/.-]*_test\.go`' | sort -u
   done
   ```
3. Every config field, flag, and injectable field has one owner in the
   parameters table above.
4. Every invariant and limit is a table with a test per row.
5. Any phase mentioning listening, manual, or paid steps has a
   `Human required` subsection. (Target: none.)
6. No phase references text scheduled for deletion before the phase that
   deletes it: `grep -ln 'DiscoverMaxLines' phase-*.md` must list no phase
   before phase 3.
7. New conventions do not contradict existing code conventions — checked
   against `internal/chat/index.go` (cache versioning, atomic write, rebuild
   reporting, read-only degradation, `SkipIndex`), `internal/vendors/jsonl.go`
   (walk and scan helpers, `OpenAbs`), `internal/vendors/source.go` (reader
   contract), `internal/chat/cmd.go` (`CommandDeps` injection), and
   `internal/utils/path.go` (cache directory resolution).

## Decisions

| ID  | Date       | Decision                                                                    | Rationale                                                                                                                                                             | Replaces                                              |
| --- | ---------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| D1  | 2026-09-16 | Index foreign conversations; keep `MessageCount` exact                      | Head-only discovery reached its speed only by deleting a user-visible field. Indexing is faster still and fixes the field instead of removing it                        | The benchmark-driven "no persistent cache" conclusion |
| D2  | 2026-09-16 | A separate `foreign_index.cache`, not `chat_index.cache` v3                 | A derived, rebuildable cache must not share invalidation rules with the durable record of every native conversation                                                     | —                                                     |
| D3  | 2026-09-16 | The foreign index lives in the cache directory                              | It is derived state, reconstructible from other tools' files; the config directory holds records                                                                        | —                                                     |
| D4  | 2026-09-16 | Validate rows by `(size, mtime)`                                            | clai does not own the write path, so currency can only be pulled; one `stat` per file is the cheapest sound check                                                        | —                                                     |
| D5  | 2026-09-16 | The delta pass is synchronous                                               | Sort, dedup, group collapse and the dir filter all gate page one, so there is nothing to overlap; backgrounding adds a handshake plus race-gate exposure                 | Async cache preparation joined before `[/]`           |
| D6  | 2026-09-16 | Delete `DiscoverMaxLines`; a cache miss reads to EOF                        | The cap made `MessageCount` wrong for three quarters of sessions while still costing a large read. With a cache, misses are rare                                         | —                                                     |
| D7  | 2026-09-16 | Generic owns the scan loop; vendors supply `Fields(line []byte)`            | The same walk/scan/aggregate is written twice per vendor, which is how the documented "read only the head" rule was lost                                                 | —                                                     |
| D8  | 2026-09-16 | `Discover` takes a `SourceCache` collaborator                               | Explicit injection over a reader-held field or a package global; keeps one read and one write for the whole invocation                                                  | —                                                     |
| D9  | 2026-09-16 | SQLite-backed sources are out of scope                                      | `SourceReader` stays unchanged in shape so a non-line-oriented source is not forced through a line abstraction later                                                     | —                                                     |
| D10 | 2026-09-16 | No lazy fields, no per-page count resolver, no bounded paginator            | With counts in the index every row is fully populated before the table is built, so the `[/]` filter path formats in-memory data and cannot trigger I/O                  | An earlier lazy-`MessageCount` design                 |
| D11 | 2026-09-16 | Repair `DEBUG_CPU` in phase 0                                               | `os.Exit` skips the deferred `StopCPUProfile`, so the mode has always written an empty profile; no phase can cite a measurement the tool cannot produce                  | —                                                     |
| D12 | 2026-09-16 | **Closed.** No async cold-start rebuild                                     | Measurement showed the cold path is fixed by width, not by backgrounding: eight workers put an exact rebuild below today's line-capped cost                              | The open question left for phase 0                    |
| D13 | 2026-09-16 | Phase order: index before parallelism; exactness with parallelism           | Phase 2 alone is a pure win with no cold-path change. Phase 3 pairs the regression with its remedy in one change                                                         | The earlier order, where parallelism trailed          |
| D14 | 2026-09-16 | The line cap may only be deleted by a phase that also lands parallel discovery | Exact counting is slower serially than the capped scan it replaces. Splitting them ships a user-visible regression for the length of one phase                         | D6 as an unconditional deletion                       |
| D15 | 2026-09-16 | `stat` before the read; the row is keyed by that `stat`                     | Foreign corpora are appended to while being read — observed during measurement. Stat-after would cache a count for bytes that were never read                            | An unstated validation order                          |
| D16 | 2026-09-16 | No `CountJSONLMessages` entry point                                         | `DiscoverJSONL` already yields the count and `Read` is out of scope, so the function would ship with no caller                                                            | The three-entry-point API                             |
| D17 | 2026-09-16 | `FindJSONLSession` stops at the first line that establishes identity        | A session file has one identity, so reading further cannot change the answer. This removes the lookup path's dependence on the line cap instead of re-bounding it        | Lookup inheriting `DiscoverMaxLines`                  |
| D18 | 2026-09-16 | The byte prefilter is an optional interface, guarded by a conformance suite | Keeps `JSONLSchema` minimal, makes non-implementation correct-but-slower, and makes a wrong prefilter a test failure rather than a silent undercount                      | A mandatory `Provides()`-style declaration            |
| D19 | 2026-09-16 | `Read` takes the cache too                                                  | `Read` resolves an identifier to a file, which is the lookup the index makes cheap. Without it the only legal call is `FindJSONLSession(..., nil, ...)` and continuing a conversation keeps scanning the corpus                | "`Read` is untouched"                                 |
| D20 | 2026-09-16 | The worker bound is a package-level function variable, not a constant       | `internal/chat` already injects `allSourceReaders` this way. A constant computed from `runtime.NumCPU()` is unreachable from a test, so both the bound and its degenerate cases would be untestable                              | `min(8, NumCPU())` computed inline                    |
| D21 | 2026-09-16 | Every fatal-ing test helper wraps an error-returning core                   | `testing.TB` cannot be implemented outside `testing` and `*testing.T` cannot be intercepted, so a helper that only fatals has no observable failure. The core is what the error rows test                                        | Helpers that only take a `testing.TB`                 |
| D22 | 2026-09-16 | Goroutine lifetime is structural, not observed                              | `goleak` is a new dependency the repository forbids, and polling `runtime.NumGoroutine()` is flaky under the race gate. `DiscoverJSONL` owns its workers under a `WaitGroup` and returns only after `Wait`                        | A leak-detector assertion                             |
| D23 | 2026-09-16 | D17's one-identity assumption is backed by measurement, not symmetry        | Measured over the maintainer's corpus: no file of three hundred sixty three carries more than one distinct session identity. A guard fixture keeps the assumption honest if a vendor ever changes                                 | An unverified assumption                              |
| D24 | 2026-09-16 | `foreign_index.cache` is written under `utils.NoCreateConfig`; only `chat.SkipIndex` suppresses it. The flag still silences the failure warning | `NoCreateConfig` is documented as scoped to the config dir, the default config file, migration callbacks and the model querier, and D3 puts this cache in the *cache* dir, so the guard never applied to it by its own terms. It is set unconditionally by `readOnlyChatSetup`, so honouring it made the index permanently dead for `chat list` — the one verb this worklog targets. The native index survives the same rule only because the save path writes it; this one has no second writer. Keeping the warning suppressed preserves the read-only promise that a read-only mount produces no stderr noise | "`utils.NoCreateConfig` suppresses the foreign cache write" |
| D25 | 2026-09-16 | The foreign persist-failure warning is gated on `utils.ReadonlyConfig`, not `utils.NoCreateConfig`. D24's ungating of the *write* stands unchanged | D24 was half right. `NoCreateConfig` marks "this verb creates no config", not "this is a read-only mount", and `readOnlyChatSetup` sets it unconditionally for `list` — so the warning phase 2's error-coverage table promises could never reach a `chat list` user, who then silently pays a full rescan of the foreign corpus forever. `ReadonlyConfig` is what actually marks a raw, machine-readable or shell-hook run, which is the run that must stay silent. The same fault under `chat continue` already warns, so the gate also made one defect diagnose itself differently by verb. `architecture/config.md` is amended in the same change | The warning half of D24 (`R1-03`) |
| D26 | 2026-09-16 | The byte prefilter's soundness assumption — canonically spelled JSON keys — is **documented**, not engineered away | `MayContribute` searches literal key spellings while `Fields` decodes with `encoding/json`, which matches struct tags case-insensitively and accepts escaped key characters, so a non-canonical spelling yields a non-zero `LineFields` the prefilter rejects. Judged unreachable against today's Claude Code and pi writers. A case-variant fixture line would make the conformance suite fail by construction, and the only fix would be a per-line case-insensitive scan whose cost defeats the prefilter. Stating the assumption in both `MayContribute` doc comments and the suite's documentation — as D23 handled the one-identity assumption — makes a vendor change a review finding rather than a silent undercount | D18's implied "a conformance suite closes this class" (`R1-53`) |
| D27 | 2026-09-16 | The foreign index is constructed **lazily**, by the chat verb that actually consults it, not by `setChatQuerier` for every chat verb | `R1-02` moved the construction off every process and onto every chat verb, which is not the same thing. `setChatQuerier` serves `continue`, `delete`, `list`, `dir`, `dirv2` and `help`, while only `list` and `continue` ever touch `foreignCache`; `readOnlyChatSetup`'s own comment calls `dir` and `dirv2` shell-prompt hot paths, so `clai -r c dirv2` in a precmd hook decodes a corpus-sized index on every prompt render. That is the cost `R1-02` was raised to remove, relocated rather than removed. The construction moves behind a once-guard so "at most one construction per invocation" survives, the typed-nil protection stays at the composition root where phase 5 proved it belongs, and the factory keeps its error handling. Phase 5's invariant row and `TestCommands_buildConstructsNoForeignIndex` are amended to assert zero constructions for `help`, `dir`, `dirv2` and `delete` — amending a phase-5 test is expected here and is authorised | The "a chat verb invokes the factory exactly once" half of `R1-02`'s fix (`R2-03`) |
| D28 | 2026-09-16 | A content-determined bound is a fact about the file; a transport error is not. `bufio.ErrTooLong` stays cacheable; **every other scanner error skips `Store`** | `R2-01` shows the promoted Strategy rule is still violated: `scanJSONLRawLines` returns `s.Err()` and `scanJSONLFileRow` discards it, so a read that failed mid-file caches a partial row under the pre-read `(size, mtime)` the failure did not change — a row that can never be invalidated. The fix must **not** be a blanket nil-check on the scan error, because an oversized line is a property of the bytes: the same file truncates identically every run, a cache-less rescan returns the same row, and `(size, mtime)` invalidates it the moment the content changes. A blanket skip would silently revert that behaviour and the cached counterpart of `TestDiscoverJSONL_oversizedLineTruncatesScan`. The discrimination is therefore `errors.Is(err, bufio.ErrTooLong)`, not `err != nil`. `R2-51` reached the same verdict on the truncation path independently and it is correct | The unqualified reading of *An error is not a fact about the file* (`R2-01`, `R2-51`) |
| D29 | 2026-09-16 | `Read` must **not** tolerate `bufio.ErrTooLong`; the token-bound dead end is made **intelligible** instead, and the residual is recorded as a known limitation | D28 makes a truncated row durable, so a D28-cached oversized-line row is listed and can never be continued: selecting it runs `Read`, which routes the same sentinel through `fmt.Errorf("scan jsonl %q: %w", ...)` and fails the verb with a raw scanner message. Three repairs were weighed. Tolerating the truncation in `Read` would feed a silently shortened conversation to a model, which is worse than failing and contradicts D1's "fix the field, do not drop it" stance. A marker column on the listing is scope this worklog does not own. What is proportionate is an error naming the cause and the bound, so a dead end that is genuine is explained rather than cryptic; the residual — a listable, non-continuable row — is stated in Strategy with its cause, so a future effort can decide about a marker with the facts in hand | Nothing; it is D28's user-visible edge, previously masked by the line cap phase 3 deleted (`R3-03`) |

## Definition of success

| Outcome                                                                           | Evidence                                            |
| --------------------------------------------------------------------------------- | --------------------------------------------------- |
| A second `chat list` with an unchanged foreign corpus opens no session file       | Phase 2 counting-FS test                            |
| An unchanged corpus keeps its cache indefinitely across runs                      | Phase 2 second-run test                             |
| Deleting `foreign_index.cache` changes only timing, never rows                    | Phase 2 cache-agnostic test                         |
| A corrupt, stale or unwritable cache still lists successfully                     | Phase 2 error-coverage rows                         |
| Continuing a foreign conversation no longer scans the corpus, through the command path | Phase 2 index-first lookup and continue tests  |
| A first run on a machine with no clai cache directory produces a usable cache     | Phase 2 directory-creation test                     |
| `MessageCount` is exact for sessions longer than the deleted line cap             | Phase 3 count test over the generated corpus        |
| A cold, uncached listing is not slower than the line-capped scan it replaces      | Phase 3 acceptance measurement, generated corpus    |
| Parallel discovery is race-free and byte-identical to the serial result           | Phase 3 tests under `-race`                         |
| Both vendors' existing discovery tests pass unedited through phase 1              | Phase 1 acceptance: no diff in the vendor test files |
| `DEBUG_CPU=1 clai c list` writes a non-empty, parseable profile                   | Phase 0 acceptance                                  |
| `continue-from-claudex.md` states the push/pull contract and a checkable cost budget, and lists pi accurately | Phase 4                          |
| `make qa` passes unedited; new code at or above seventy percent statement coverage | Phase 4 sweep                                       |

## Validation policy

Repository gates apply unchanged: `gofumpt`, `staticcheck`, `go vet`,
`go test ./... -race -cover -count=3 -timeout=30s`, `go fix`, `dupl`. No
phase requires a network call, a vendor key, or a paid run. No
`Human required` steps.

**Race-gate care.** The gate runs about twenty-one seconds on `main` here and
is sensitive to host load; phase 3 introduces the only concurrency in this
worklog. Fixtures must use the generated corpus at fixture scale — never the
developer's real `~/.claude/projects` — and every phase records its gate
rerun in the session journal.

**Performance claims.** No phase may assert a timing figure it did not
produce from the generated corpus. The measured baseline above is motivating
evidence about one machine, not an acceptance threshold.

## Feedback index

### Validation round 1 (2026-09-16)

| ID     | Severity | Closed by                                                                                                       |
| ------ | -------- | ----------------------------------------------------------------------------------------------------------------- |
| V1-01  | blocker  | Phase files 0–4 authored                                                                                          |
| V1-02  | blocker  | `SourceCache` contract rewritten: `Lookup` marks in use, `Persist` stats only unmarked rows; three invariant rows added |
| V1-03  | major    | D17 and the one-identity invariant; `FindJSONLSession` no longer depends on the line cap                          |
| V1-04  | major    | Phase 1 "tests unedited" scoped to phase 1; the thirteen call sites listed in phase 2's file list                  |
| V1-05  | major    | D16: `CountJSONLMessages` dropped                                                                                 |
| V1-06  | minor    | Baseline table replaced by a measured one; wall-clock and profile shares no longer mixed                           |
| V1-07  | minor    | Definition of success names the coverage floor                                                                    |
| V1-08  | minor    | `ScanJSONLLines`'s `maxLines` parameter fate stated in phase 3                                                    |
| V1-09  | minor    | `ChatHandler.foreignCache` wiring assigned to `CommandDeps` in phase 2                                            |
| V1-10  | note     | Checklist items 1, 2 and 6 now carry commands; run from the worklog directory                                     |
| V1-11  | note     | D15 and the stat-before-read invariant, with a growing-file test                                                  |

### Validation round 2 (2026-09-16)

| ID    | Severity | Closed by                                                                                                     |
| ----- | -------- | --------------------------------------------------------------------------------------------------------------- |
| V2-01 | major    | D20 and the `discoverWorkers` seam; phase 3 gains a limit table with both bounds reachable through it           |
| V2-02 | major    | D19: `Read` takes the cache; phase 2 wires the continue path and adds `TestForeignChat_continueUsesCachedPath`   |
| V2-03 | major    | Phase 4's review rows replaced by one checkable phrase per documentation change                                 |
| V2-04 | major    | `TestLineFields_closedSet` replaces "checked at review"; the race-gate row became a budget row owned by phase 0  |
| V2-05 | major    | D21: every fatal-ing helper wraps an error-returning core; error rows target `WriteCorpusErr` and `CheckSchemaConformance` |
| V2-06 | major    | Verified against `internal/utils/path.go` and `internal/utils/file.go`: nothing creates the cache directory. Phase 2 gains an `os.MkdirAll` convention row, an invariant row and `TestForeignIndex_createsMissingCacheDir` |
| V2-07 | major    | **Refuted by measurement, then closed:** no file of three hundred sixty three in the maintainer's corpus carries two identities. D23 records it; phase 0 re-runs the check as a gate; a two-identity fixture guards it |
| V2-08 | major    | The preview rule now says first user line *with non-empty text*, matching both vendors; the count/preview divergence is stated and fixtured |
| V2-09 | minor    | `main.go` added to phase 2's file list; the post-construction assignment and the nil-leaving replay commands are named |
| V2-10 | minor    | `cpu-profile-file-name` parameter row                                                                           |
| V2-11 | minor    | Checklist item 1 now strips fenced blocks, backticked spans, phase references and decision ids before flagging any digit; item 2a added for file lists |
| V2-12 | minor    | Phase 4's grep restricted to the owning document with an expected empty result; the stale future-readers comment added to the table |
| V2-13 | minor    | Phase 3 says each vendor's **one** full-read call site                                                          |
| V2-14 | minor    | `StatAbs` documented as `fs.Stat` with the `fs.StatFS` requirement; `jsonltest.CountingFS` owned by phase 0 and used by every never-opened assertion |
| V2-15 | minor    | `benchmark-regression-band` and `benchmark-invocations` parameters; all four performance rows cite them          |
| V2-16 | minor    | D22: `WaitGroup` ownership makes the guarantee structural; `TestDiscoverJSONL_returnsAfterWorkersFinish` replaces leak detection |
| V2-17 | minor    | `jsonltest.CorpusOptions.Shape` and `.Seed` parameter rows; later phases use the default seed                    |
| V2-18 | note     | The race-gate invariant became a budget row owned by phase 0, re-asserted by later phases                        |
| V2-19 | note     | Both directory failures provoked by a parent that is a file, following `internal/utils/file_atomic_test.go`      |
| V2-20 | note     | `NewForeignIndex` returns an error; the typed-nil hazard is an invariant row and a named test                    |

### Review 1 (2026-09-16) — verdict **not ready**

Severity taxonomy as used by the validation rounds above: `blocker` and `major`
stop a sign-off, `minor` is a real defect that does not, `note` is information
for the next round. Everything but a `note` is routed to
[phase 5](./phase-5-review-1-fixes.md); the phases themselves are not reopened.

| ID     | Severity | Phase                                              | Summary                                                                                              |
| ------ | -------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| R1-01  | blocker  | [2](./phase-2-foreign-index.md)                     | An unreadable file is negatively cached under the pre-read `(size, mtime)`, so deleting the cache changes rows, not just timing |
| R1-02  | major    | [2](./phase-2-foreign-index.md)                     | The index is loaded and JSON-decoded on every `clai` invocation, not only on chat verbs                |
| R1-03  | major    | [2](./phase-2-foreign-index.md)                     | The persist-failure warning is gated on a flag `chat list` always sets, so it can never reach a listing user — D25 |
| R1-04  | minor    | [2](./phase-2-foreign-index.md)                     | `liveRows` prunes on any stat error, so an unreachable root empties the whole cache                    |
| R1-05  | minor    | [2](./phase-2-foreign-index.md)                     | `Locate` ranges a map, so two rows sharing a `(source, sourceID)` give a different answer per run       |
| R1-06  | note     | [2](./phase-2-foreign-index.md)                     | The phase's recorded benchmark figures are not independently reproducible; round 2 must not treat them as verified |
| R1-07  | note     | [2](./phase-2-foreign-index.md)                     | Nothing drives D24/D25 through the real verb; the read-only test sets the global by hand               |
| R1-50  | —        | —                                                    | **Duplicate of R1-01**, found independently by the second reviewer through a different method           |
| R1-51  | minor    | [3](./phase-3-parallel-exact.md)                    | Cancellation mid-flight has an unstated, untested cache side effect — benign, but neither written down nor guarded |
| R1-52  | minor    | [3](./phase-3-parallel-exact.md)                    | Two README figures are contradicted by the shipped tree and one of them is no longer reproducible at all |
| R1-53  | minor    | [3](./phase-3-parallel-exact.md)                    | Prefilter soundness rests on literal byte spellings `encoding/json` does not require — D26              |
| R1-54  | note     | [4](./phase-4-docs-and-gates.md)                    | "The count is exact from discovery onward" is stated unconditionally; `ReadMaxToken` still truncates    |

Every row but `R1-06`, `R1-50` and the note half of `R1-07` is closed by phase 5;
`R1-07` is folded into `R1-03`'s command-level test there, and `R1-06` is closed
by the figure annotations under **Budgets** above.

**Closed by phase 5**, each with a test shown to fail against the shipped code
before the fix landed and to pass after it:

| ID     | Closed by                                                                                                     |
| ------ | --------------------------------------------------------------------------------------------------------------- |
| R1-01  | `scanJSONLFileRow` returns a third result; an open failure never reaches `Store`. `TestDiscoverJSONL_openFailureIsNotCached`, `TestDiscoverJSONL_cacheAgnosticWithUnreadableFile`, `TestDiscoverJSONL_negativeCachingSurvivesTheSplit` |
| R1-02  | `CommandDeps.ForeignCache` is a factory; `main.go` gained a construction seam. `TestCommands_buildConstructsNoForeignIndex`, `TestChatCommand_cacheFactoryRunsOncePerVerb`, `TestChatCommand_failedFactoryLeavesCacheNil` — all three amended by phase 6 when D27 made the construction lazy |
| R1-03  | D25: `warn` gates on `utils.ReadonlyConfig`; `architecture/config.md` amended. `TestChatList_unwritableCacheWarnsThroughTheVerb`, `TestChatList_rawVerbStaysSilent` |
| R1-04  | `liveRows` prunes only on a not-exist error. `TestForeignIndex_statFailureKeepsRow`, `TestForeignIndex_unreachableRootKeepsEveryRow` |
| R1-05  | `Locate` keeps the lexically smallest match, the walk's own tie-break. `TestForeignIndex_locateReturnsLexicallySmallestPath`, `TestForeignIndex_locateAgreesWithWalkFallback` |
| R1-06  | The Budgets annotations, plus the phase-two table marked historical and the shipped figure recorded beside it |
| R1-07  | Both `R1-03` tests drive the real `list` verb end to end; no test sets a config global by hand |
| R1-51  | The cancellation rule is stated in phase 5's contract and exercised with a live cache. `TestDiscoverJSONL_cancelMidFlightKeepsScannedRowsCached` |
| R1-52  | Historical figures marked in the Budgets table and in phase 2; the shipped figure recorded beside them in phase 3 |
| R1-53  | D26: the canonical-spelling assumption documented in both readers' `MayContribute` and in `jsonltest/conformance.go` |
| R1-54  | `architecture/continue-from-claudex.md`'s exactness claim carries its `ReadMaxToken` clause |

### Review 2 (2026-09-16) — verdict **not ready**

Two reviewers audited the phase-5 fixes independently. Severity taxonomy
unchanged. Everything but a `note` is routed to
[phase 6](./phase-6-review-2-fixes.md); no phase is reopened.

**Round-1 status.** `R1-02`, `R1-03`, `R1-04`, `R1-07`, `R1-51`, `R1-52` with
`R1-06`, `R1-53` and `R1-54` are resolved with no regression, and each
reviewer's mutation testing confirmed the phase-5 tests that close them are
non-vacuous. Two carry a qualification. `R1-05` is resolved for **determinism** —
`Locate` answers identically on every run — but its stated mechanism is false:
the lexically smallest full path is not the walk's tie-break across sibling
directories, which is `R2-02`. `R1-01`'s **instance** is resolved and its
**class** is still open: phase 5 split out the open failure only, and the read
failure reaches `Store` exactly as the open failure used to, which is `R2-01`.

| ID     | Severity | Phase                                              | Summary                                                                                              |
| ------ | -------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| R2-01  | blocker  | [5](./phase-5-review-1-fixes.md), [3](./phase-3-parallel-exact.md) | A scan that could not be **read** is still cached as a fact about the file: `scanJSONLRawLines`'s error is discarded, so a partial read is stored under the pre-read `(size, mtime)` — D28 |
| R2-02  | minor    | [2](./phase-2-foreign-index.md), [5](./phase-5-review-1-fixes.md) | `Locate`'s lexical full-path tie-break is not `filepath.WalkDir`'s order across sibling directories, so lookup and discovery disagree by whether the cache is warm |
| R2-03  | major    | [5](./phase-5-review-1-fixes.md)                    | The index factory runs for `help`, `dir`, `dirv2` and `delete`, which never consult the cache — D27 |
| R2-04  | minor    | [2](./phase-2-foreign-index.md), [4](./phase-4-docs-and-gates.md) | The README `SourceCache` code block still shows two methods; the shipped interface has three |
| R2-50  | —        | —                                                    | **Duplicate of R2-01**, found independently by the second reviewer through a different probe |
| R2-51  | note     | [3](./phase-3-parallel-exact.md)                    | Verdict on the `ReadMaxToken` truncation path: **not** the same defect class; one residual obligation on the parameters table |
| R2-52  | minor    | [3](./phase-3-parallel-exact.md)                    | Phase 3's cancellation contract row still states the falsehood `R1-51` reported, contradicting phase 5's row |
| R2-53  | minor    | [2](./phase-2-foreign-index.md), [3](./phase-3-parallel-exact.md), [4](./phase-4-docs-and-gates.md) | Every round-1 finding checkbox was still open while the README declared them closed |
| R2-54  | minor    | [5](./phase-5-review-1-fixes.md)                    | Readiness checklist items 1 and 2 fail, phase 5 records them as passing, and item 2 is itself structurally unable to police what it claims |
| R2-55  | note     | [3](./phase-3-parallel-exact.md), [4](./phase-4-docs-and-gates.md) | The conformance suite is non-vacuous but lower-resolution than two documents claim; D26's own loop is not closed in the document a new vendor author reads |
| R2-56  | note     | [4](./phase-4-docs-and-gates.md)                    | The promoted rule *An error is not a fact about the file* never reached the architecture tree, and the worklog is deleted by convention once the effort ships |
| R2-57  | minor    | [4](./phase-4-docs-and-gates.md)                    | Stale Go doc comments in the four files this worklog rewrote — the "cost rule lives in prose" failure D7 exists to remove |
| R2-58  | note     | [5](./phase-5-review-1-fixes.md)                    | Phase 5's and review 2's `internal/vendors` coverage figures disagree. **Neither is the wrong one** (`R3-57`): the figure oscillates over `70.6` to `70.9` because `scanJSONLFileSlots`'s `break feed` branch is covered or not per run, so both are real measurements of the same tree and the figure is quoted as a range from here on. Every other figure reproduces exactly |

**Routed to [phase 6](./phase-6-review-2-fixes.md):** `R2-01`, `R2-02`, `R2-03`,
`R2-04`, `R2-52`, `R2-53`, `R2-54`, `R2-57`, and the documentation obligations
inside the notes `R2-51`, `R2-55` and `R2-56`. Every one is now closed; the
table below names what closed each. `R2-58` and `R2-53` are closed in
the review's own change — the figure is corrected in phase 5's verification
table and in the phase-5 journal entry, and all thirteen round-one checkboxes
across the three phase files are ticked. `R2-54`'s checklist repair and phase
5's six item-1 violations are also corrected in this change; the two item-2
duplicates fall out of the repaired rule.

**Closed by phase 6**, each production fix proved by a test shown to fail
against the shipped code before it landed:

| ID     | Closed by                                                                                                     |
| ------ | --------------------------------------------------------------------------------------------------------------- |
| R2-01  | `scanJSONLFileRow` propagates the scanner's error; `discoverJSONLFile` stores only on a nil error or `bufio.ErrTooLong` (D28). `TestDiscoverJSONL_readFailureIsNotCached`, `TestDiscoverJSONL_partialReadNeverPersistsACount`, `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile`, `TestDiscoverJSONL_oversizedLineRowStaysCacheable` |
| R2-02  | `Locate` ranks candidates with `compareWalkOrder`, segment by segment. `TestForeignIndex_locateFollowsWalkOrder`, `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs` |
| R2-03  | D27: `ChatHandler.foreignCacheOrNil` resolves the factory at the point of consultation behind a `sync.Once`. `TestChatCommand_onlyConsultingVerbsConstructTheIndex`, `TestChatCommand_lazyFactoryRunsAtMostOnce`, `TestChatCommand_lazyFactoryFailureLeavesCacheUnset`, and the two amended phase-5 tests |
| R2-04  | Verified: the *Shared interfaces* `SourceCache` block and `internal/vendors/source.go` agree on `Lookup`, `Store` and `Locate`. Landed by the review's own change |
| R2-51  | Verified: the coupling sentence sits on both the `vendors.ReadMaxToken` and `foreign-index-version` parameter rows, and is now also in the architecture document |
| R2-52  | `phase-3-parallel-exact.md`'s mid-flight cancellation row states the real side effect and points at phase 5's row |
| R2-55  | `architecture/continue-from-claudex.md` qualifies the conformance claim and carries the canonical-spelling sentence in *Implementing a new JSONL source*; the README invariant's qualification landed by the review's own change |
| R2-56  | The promoted rule and D28's refinement are a section of `architecture/continue-from-claudex.md`'s source-reader contract |
| R2-57  | The four stale doc comments state the shipped rule; the acceptance grep returns nothing |

### Review 3 (2026-09-16) — verdict **ready**

Two reviewers audited phase 6 and the whole worklog independently. Both returned
**ready**: no blocker, no major. Severity taxonomy unchanged. The two halves of
a sign-off were assessed separately and both hold — the work **ships clean
through the gates**, and the work is **correct**. Round 2's substantive open
question is closed with it: the promoted rule now holds on *both* branches in
code, and it has escaped the worklog into the architecture tree.

**Round-2 status.** `R2-01` is resolved and complete on every branch this time,
not only the instance. `R2-02`, `R2-03`, `R2-04`, `R2-51`, `R2-52`, `R2-53`,
`R2-54`, `R2-55` (as scoped), `R2-56` and `R2-57` are all resolved. `R2-58` is
resolved as a **measurement** — the oscillation has a cause and it is phase 3's
cancellation branch — but not as a **record**, because the feedback index still
carried the refuted characterisation: that is `R3-57`. No round-1 finding
regressed either.

**Root cause of this round.** Both reviewers independently traced five findings
to one rule, now promoted into Strategy as *A corrected claim is not corrected
until every copy of it is*. `R3-50`, `R3-52`, `R3-53`, `R3-56` and `R3-57` are
each one surviving copy of a claim an earlier round already corrected somewhere
else.

| ID     | Severity | Phase                                              | Summary                                                                                              |
| ------ | -------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| R3-01  | minor    | [1](./phase-1-generic-jsonl-discovery.md)           | `jsonlFileIdentity` discards the scanner error, so a transport failure silently changes which file the walk says holds a session |
| R3-02  | note     | [2](./phase-2-foreign-index.md)                     | `persistForeignCache`'s unguarded field read is a latent race the current call graph hides, and neither the property nor its replacement is pinned |
| R3-03  | note     | [6](./phase-6-review-2-fixes.md)                    | A D28-cached oversized-line row is listed but can never be continued, and the verb fails with a raw scanner message — D29 |
| R3-50  | minor    | [5](./phase-5-review-1-fixes.md)                    | The zero-cache-IO invariant states the pre-D27 wiring as its mechanism, in the README invariant row and again in the parameters row |
| R3-51  | minor    | [2](./phase-2-foreign-index.md)                     | The typed-nil invariant names a mechanism this worklog has proved inert, in the table where it reads as verified |
| R3-52  | minor    | [3](./phase-3-parallel-exact.md)                    | The conformance overstatement `R2-55` qualified in two documents survives in two Go files and in phase 3's own row |
| R3-53  | minor    | [5](./phase-5-review-1-fixes.md)                    | Readiness item 3 genuinely fails: `main.go`'s `newForeignIndex` seam has no parameters row            |
| R3-54  | note     | [6](./phase-6-review-2-fixes.md)                    | The D27 invariant row misdescribes which verbs `readOnlyChatSetup` serves; D27's own decision entry is correct |
| R3-55  | note     | [4](./phase-4-docs-and-gates.md)                    | The recorded flake threshold is the wrong model: the `internal/text` collision is stdout contention between parallel tests, not host load |
| R3-56  | note     | [3](./phase-3-parallel-exact.md)                    | "Lexical order" survives in the file a contributor edits — the fourth copy of the claim `R2-02` corrected |
| R3-57  | note     | [5](./phase-5-review-1-fixes.md)                    | The feedback index carries a characterisation its own later phase refuted; the "wrong" coverage figure is the one that reproduces |
| R3-58  | note     | [6](./phase-6-review-2-fixes.md)                    | Two board and pointer mismatches: phase 3's board row understates its own review-2 table, and a phase-6 verification row points below itself at outcomes recorded in the README |

**Routed to [phase 7](./phase-7-review-3-fixes.md):** `R3-01`, `R3-02`, `R3-03`
and the Go-file and phase-file copies inside `R3-52` and `R3-56`. Three of them
are code plus tests, which is why they are a phase and not an untracked
documentation sweep — one reviewer proposed the sweep, and the worklog's own
lifecycle of finding, phase and evidence is what makes them provable. **Every
one is now closed**; the table below names what closed each. Phase 7 is the last
phase of this worklog and every round's findings are closed with it.

**Closed by the review's own change**, because each is a worklog edit the filing
itself performs: `R3-50` (both README rows restated against D27), `R3-51` (the
invariant's mechanism restated as the composition root's nil-interface contract,
naming `TestCommands_buildConstructsNoForeignIndex`), `R3-53` (a `newForeignIndex`
parameters row, owner phase 5), `R3-54` (the D27 row's verb list corrected),
`R3-55` (the flake's cause restated here and in phase 4), `R3-57` (the `R2-58`
summary corrected and the coverage figure quoted as a range), and `R3-58` (phase
3's board row and phase 6's verification row).

| ID     | Closed by                                                                                                     |
| ------ | --------------------------------------------------------------------------------------------------------------- |
| R3-50  | The invariant row now says the setups only carry the factory and D27 leaves no setup invoking it; the parameters row says the same |
| R3-51  | The invariant's mechanism is the composition root's nil-interface contract, asserted by `TestCommands_buildConstructsNoForeignIndex`, which phase 6's mutation table confirms does fail under mutation |
| R3-53  | A `newForeignIndex` row in the parameters table, owner phase 5, beside the `discoverWorkers` seam row           |
| R3-54  | The D27 invariant row lists `list`, `dir`, `dirv2` and `help` for `readOnlyChatSetup` and puts `delete` on `fullChatSetup` |
| R3-55  | The review-1 journal entry and `phase-4-docs-and-gates.md` state stdout contention between parallel tests inside the package as the cause, with the load model withdrawn |
| R3-57  | The `R2-58` row above, and the range quoted wherever the `internal/vendors` figure appears                      |
| R3-58  | Phase 3's board row names all four findings its own review-2 table carries; phase 6's readiness row points at the README journal |

**Closed by [phase 7](./phase-7-review-3-fixes.md)**, each production fix proved
by a test shown to fail against the tree before it landed:

| ID     | Closed by                                                                                                     |
| ------ | --------------------------------------------------------------------------------------------------------------- |
| R3-01  | `jsonlFileIdentity` returns its scanner error and `FindJSONLSession` fails the lookup instead of walking on to a file the walk would not have chosen. `TestFindJSONLSession_readFailureIsNotAMissingSession`, `TestFindJSONLSession_readFailureNeverAnswersWithASibling` — the second drives `anthropic.SourceReader.Read` and returned the sibling's transcript before the fix |
| R3-02  | `ChatHandler.resolvedForeignCache` is a non-constructing accessor behind the same mutex the resolver writes under; `persistForeignCache` goes through it and its doc comment states the mechanism. `TestChatHandler_persistForeignCacheRacesResolution` reproduced the reviewer's race before the fix; `TestChatHandler_persistForeignCacheDoesNotConstruct` is the mutation guard the finding asked for |
| R3-03  | D29: `vendors.ScanJSONLLines` wraps `bufio.ErrTooLong` once, generically, naming the cause and the bound and keeping the sentinel behind `%w`, so D28's discovery gate is untouched and both vendors inherit the message through their unchanged wrap. `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`, `TestSourceReaderRead_tokenBoundErrorIsIntelligible`, `TestPiSourceReaderRead_tokenBoundErrorIsIntelligible`, with the two oversized-line tripwires passing unedited |
| R3-52  | The qualification reaches `internal/vendors/schema.go`, `internal/vendors/jsonltest/conformance.go` and `phase-3-parallel-exact.md`'s prefilter invariant row, in the words the two corrected documents already use |
| R3-56  | `discoverSlot`, `walkJSONLPaths` and phase 3's discovery-steps row state per-directory entry order. Phase 7's five-copy walk found two further copies the finding did not name — `jsonltest.Corpus`'s doc comment and `phase-0-measurement-gate.md`'s declaration of the same field — and corrected both |

## Session journal

### 2026-09-16 — Investigation and README authored (clai)

Profiled `chat list` after discovering `DEBUG_CPU` writes an empty file
(`os.Exit` skips the deferred `StopCPUProfile`); measured with a patched
binary. Established that discovery dominates and that nothing is cached
between runs. Prototyped four discovery strategies.

Initially proposed head-only discovery with a lazy `MessageCount`, which
introduced a `[/]`-filter hazard and a bounded page resolver. The maintainer
rejected the framing: the native index already demonstrates the right
pattern, and dropping a field to hit a timing target is the wrong trade.
Reworked around a foreign index with pull validation (D1, D10).

### 2026-09-16 — Validation round 1 and redesign (clai)

Validation returned **Not ready**: no phase files existed, and the
`SourceCache` contract as written would have pruned every row on the second
run. Re-measured against the real corpus with a temporary in-module harness
rather than trusting the earlier prototype numbers, which produced the three
findings in the measured baseline: the warm win is larger than stated, exact
counting is slower than today's scan unless parallelised, and the corpus
mutates during measurement. Added D12–D18, rewrote the strategy, reordered
the phases so no phase ships a regression, and mapped every round-1 finding
in the feedback index.

### 2026-09-16 — Round 3 aborted; author self-check and handoff (clai)

An independent round-3 validation was launched and did not complete: the
validating session's login expired while it was verifying the worklog's claims
against the repository. **The round-2 fixes therefore carry no independent
audit.** In its place the author re-ran the readiness checklist and a
cross-reference pass, which found and fixed: the decisions log listing D18 after
D23; phases 0 and 4 having no `Files` table, so several named tests had no
declared home; and phases 1 through 3 naming tests whose test file was not in
their file list. Items 1, 2, 2a, 5 and 6 pass; every test named in the README's
invariants appears in a phase; every parameter cited by a phase has a row.

Residual risk is concentrated in phase 2 (the wiring through `CommandDeps` and
the continue path) and in phase 3's performance claim, neither of which a
document check can settle. Phase 0 is unaffected: it is a gate, it touches no
discovery code, and completing it produces the fixtures and figures the later
phases are written against.

### 2026-09-16 — Validation round 2 and fixes (clai)

Round 2 verified all eleven round-one findings closed with no regressions, and
returned **Not ready** with twenty new findings, eight major. Fixed by class
rather than line by line: two seams that had no injectable owner (the worker
bound, and the cache's route into `Read`), four classes of criterion that no run
could fail, and three claims about the existing code.

Two claims were re-checked against the repository before accepting them. The
cache-directory finding is real and was the most serious defect in the worklog:
nothing in clai creates `<UserCacheDir>/clai`, so the index would have failed to
persist on every fresh machine, silently, forever. The one-identity finding was
a fair challenge to an unverified assumption, and measurement refuted the risk:
no file of three hundred sixty three carries two identities, which D23 now
records and phase 0 re-checks.

### 2026-09-16 — Phases authored (clai)

Phase files 0–4 written from the revised README. Two README amendments were
needed while writing them and were made before the phases referring to them:
the out-of-scope rule now forbids a *behavioural* change to the full-read scan
sites rather than any change, so phase 3 may drop their now-constant line-bound
argument; and the concurrency invariant moved to phase 2, which writes the
mutex, rather than phase 3, which only exercises it.

Readiness checklist run from this directory. Items 1, 2, 5 and 6 pass. Items 1
and 2 were themselves corrected first: item 1 flagged `-timeout=30s` inside the
repository's verbatim gate command, and item 2 flagged names that appear twice
inside a single phase (contract table and acceptance), which the rule allows.
Item 6 matches phases 3 and 4 only; phase 4's match is the grep that proves the
symbol is gone from the documentation. Items 3, 4 and 7 checked by reading:
every parameter has one owner, every invariant and limit is a table row with a
test, and the conventions were checked against the five files named in the
checklist. All five phase files carry the seven required sections and every
status-board link resolves. Every phase `Not Started`; handing to validation
round 2.

### 2026-09-16 — Phase 1 executed (clai)

The extraction landed with both vendor test files unedited, which is the
phase's own acceptance signal, and with a second one the phase did not ask for:
`TestDiscoverJSONL_matchesCorpusFacts` was written and run against the
*unmodified* readers first, so behaviour preservation has a before-and-after
run instead of an argument. `dupl` confirms the point of the exercise — the two
clone groups it used to report between `anthropic/source_reader.go` and
`pi/source_reader.go` are gone, and no new group involves any file this phase
wrote. Discovery cost is unchanged (`+0.96` percent against a same-session
control, well inside `benchmark-regression-band`) while allocated bytes fell a
fifth, because the generic scanner hands out bytes where the old loop allocated
a string per line. The gate ran `24.0` s at host load `1.7`.

Four specification gaps are recorded as deltas in the phase file. One matters
to the next phase: the README's shared-interfaces block gives `DiscoverJSONL`
and `FindJSONLSession` a `SourceCache` parameter, but that type and every
invariant about it belong to phase 2, so both functions ship without it and
phase 2 adds it. The others are smaller — the aggregation table's "whose
`UserText` is non-empty" describes a guard that both vendors actually apply to
the *preview*, `ScanJSONLLines` had to become a wrapper over the new raw
scanner to avoid writing the clone the phase forbids, and the mod-time fallback
now goes through the injectable filesystem that both vendors were bypassing.

One maintenance contract outside the phase's Files table was honoured in the
same change: `architecture/continue-from-claudex.md` told a new source to
implement `discoverOne`, a symbol this phase deletes. Its shared-skeleton
section now names the real entry points. Phase 4 still owns the rest of that
document.

### 2026-09-16 — Phase 0 executed (clai)

The gate is **proceed**. The one-identity assumption still holds — a single
line reading `1` over `365` files, two more than when D23 was recorded — so
D17 stands unchanged.

`DEBUG_CPU` is repaired: `main` now calls `os.Exit(runProfiled(...))` and every
profiling failure falls through to an unprofiled run, so the mode writes a real
profile for the first time. `internal/vendors/jsonltest` ships the corpus
generator, its oracle and the counting filesystem, and
`BenchmarkSourceReaderDiscover` gives every later phase a command instead of a
remembered number. All gates green; the figures are in the phase file and, for
the two the later phases re-assert, in Budgets above.

Four specification defects were found and are recorded as deltas in the phase
file rather than fixed silently. One is a genuine contradiction that a future
reader will hit: the parameters table gives `CorpusOptions.Sessions` a default
while the phase's error table requires a zero count to mean an empty corpus.
Taking `Sessions` literally and zero-substituting only `LinesPerSession` and
`Seed` satisfies both intents, and `DefaultSessions` is now an exported
constant a caller names. The others are smaller: `Shape` needed a non-shape
zero value to have "no default" at all, a working directory cannot be a file so
the profile-create failure is provoked with a directory in the file's place,
and two error rows described the same failure and were split.

Two additions were needed for acceptance to be checkable rather than asserted:
`SessionFact.Kind`, without which "every fixture kind is present" has no
oracle, and one fact per written file, so the identity-less and empty fixtures
— which exist precisely to be dropped — have a fact to be dropped against.

The corpus was cross-checked against both real readers before being trusted: a
temporary test discovered the fixture-scale corpus with `anthropic.SourceReader`
and `pi.SourceReader` and matched every field of every row to its fact,
dropping exactly the three identity-less files. That test belongs to phase `1`
and was deleted rather than committed.

Readiness checklist items `1` and `2` rerun over the phase files after the
edit: both still pass.

### 2026-09-16 — Phase 2 executed; held on one decision (clai)

The seam, the index, the wiring and the index-first session lookup are
implemented and every named test passes, under the gate unedited at `23.9` s
and host load `1.54`. Cold discovery moved `+0.99` percent against the
phase-zero figure — one stat per file — while warm discovery costs
`230698` ns/op against `90399212` ns/op cold, which reproduces the premise of
this worklog against the generated corpus rather than the maintainer's real
one.

**The phase is not `Complete`, and the reason needs a decision rather than
more code.** The invariant table suppresses the cache write under
`utils.NoCreateConfig`; `readOnlyChatSetup` sets `utils.NoCreateConfig`
unconditionally for the `list` verb, which `internal/chat/cmd_test.go`
already asserts it must. Together they mean `clai chat list` reads the index
and never writes one. Confirmed with the built binary against a generated
`~/.claude/projects`: `clai c l` printed correct rows and left the cache
directory empty, while `clai c c …` — a full-setup verb falling through to
the same listing — wrote `foreign_index.cache` correctly. The native index
survives the same rule only because its writer is the save path; the foreign
index has no second writer, so the integration contract's *no silent
permanent rescan* fails for the verb the worklog was written for. The flag's
own doc comment scopes it to "config dir and default config file creation",
and D3 puts this cache in the *cache* directory deliberately, so the
narrowest fix is probably to let the foreign index ignore
`NoCreateConfig` — but that rewrites an invariant row and a named test, which
is planning work, not execution.

Four smaller specification gaps are recorded as deltas in the phase file. One
is structural: the declared `SourceCache` has no way to turn a session
identifier into a candidate path, so the cache-first lookup the phase
requires was unreachable through the two declared methods. `Locate` was added
as a third method and the `(size, mod time)` rule still lives only in
`Lookup`. The interface block predates D19, which is when `Read` gained the
cache, so this reads as an omission. The others: `Persist` owns its own
warn-once message because the README gives it no error result; the file list
misses phase zero's `bench_test.go` call site and its `cmd.go` row
contradicts the Wiring block about whether the read-only sub gets the cache
(the Wiring block was taken as normative); and one error row prunes a row the
liveness table keeps, which the implemented test resolves by provoking the
case the row actually describes.

`architecture/continue-from-claudex.md` was updated in the same change for
the signature it now states wrongly — the two interface methods and the
lookup sentence. Phase 4 still owns that document's push/pull narrative and
cost budget.

### 2026-09-16 — Phase 2 completed after an authorised planning amendment (clai)

The maintainer verified the read-only finding independently and authorised the
fix, so the amendment below is **planning granted mid-phase, not an execution
decision**: an executor may not rewrite an invariant row on its own, which is
why the phase was handed back `In Progress` rather than quietly corrected.

`ForeignIndex.Persist` now gates on `chat.SkipIndex` alone. Under
`utils.NoCreateConfig` the write is attempted and only the failure *warning*
is suppressed, which keeps the documented read-only promise — a read-only
mount produces no stderr noise and no failed write — while letting the cache
exist on a normal machine. D24 records it, the invariant row is rewritten,
and `TestForeignIndex_readOnlyNoWrite` became
`TestForeignIndex_readOnlyDegradesSilently`, which now asserts both halves:
a writable cache directory is written even under the flag, and an
undirectoryable one lists in silence. `TestForeignIndex_skipIndexNoIO` is
unchanged and still owns the genuine no-I/O switch.

The evidence that motivated the finding reverses cleanly: the same
`clai -n -r c l q` that left the cache directory empty now writes
`foreign_index.cache`; a second listing over the untouched corpus reads it,
and a run against a cache directory at mode `555` printed zero bytes to
stderr. Gate rerun unedited: `26.4` s all-green at host load `3.17`. An
earlier attempt at load `6.1` timed out the root and `internal/audio`
packages at the `30` s bound — neither is touched by this phase and both pass
at lower load, which is the flake **Race-gate care** already records.
`gofumpt`, `staticcheck`, `go vet` and `go fix` produce no output, and `dupl`
reports the same thirty-four groups as before, none naming a file this phase
wrote. Phase 2 is `Complete`.

### 2026-09-16 — Phase 3 executed (clai)

The regression and its remedy landed together, and the pairing is now a
measurement rather than an argument. With the seam pinned to one worker, exact
discovery costs `135743645` ns/op against the phase-zero line-capped
`89515806` ns/op — past the regression ceiling, exactly as D14 predicted. At
the default bound the same code costs `36641038` ns/op, so the cold path is
not merely inside the band but well under the figure it is compared against,
while `MessageCount` stopped lying: `15449` messages across the default
corpus' `61` discoverable rows, against `12159` capped.

Every new assertion was rerun against a deliberately broken build before being
believed — a serial pool, a missing `Wait`, a reinstated cap, workers
appending instead of filling slots, an ignored prefilter, and a narrowed
vendor prefilter — and each was caught by the test that claims it. That
mutation table is in the phase file, because a concurrency phase whose tests
cannot fail is the worst outcome available to it.

The prefilter is the least of the three changes and the phase file says so.
Nearly every Claude Code line carries `sessionId`, `cwd` and `timestamp`, so a
*sound* prefilter must pass nearly every line; the typed decode and the pool
are what bought the time. That is D18 working as designed — the safe direction
is the slow one — but a reader of the measured baseline should not expect the
prototype's prefilter figure from a conformant one.

Seven specification gaps are recorded as deltas in the phase file. Three are
structural and a future phase should know them: the `discoverWorkers` seam is
unexported while the tests that must replace it are external, so an
`export_test.go` was added; the README's conformance signature forces
`jsonltest` to import `internal/vendors`, contradicting the package doc phase
zero wrote — there is no cycle, and the doc now says so; and the runner takes
lines that nothing produced, so `Corpus.Lines()` was added. The others are
smaller: the typed decode needed `vendors.RawTextBlocksContent` in the generic
layer, the decode error must be ignored rather than fatal to keep the map
decode's per-field tolerance, the mid-flight cancellation row had no test name,
and the phase-zero datum of `15699` messages is the corpus total rather than
the discoverable total.

`architecture/continue-from-claudex.md` was corrected in the same change for
the two statements this phase falsifies — bounded line scanning, and a bounded
prefix with an approximate count. Phase 4 still owns its push/pull narrative
and cost budget.

Gate rerun unedited: `26` s all-green at host load `5.07`. `gofumpt`,
`staticcheck`, `go vet` and `go fix` produce no output, and `dupl` reports the
same `34` groups as phase 2, none naming a file this phase wrote. Readiness
checklist items `1` and `2` rerun over the phase files after the edit: both
pass.

### 2026-09-16 — Phase 4 executed; worklog complete (clai)

The documentation sweep found less to do than the phase expected and more than
it named. Less, because phases 1–3 each corrected `continue-from-claudex.md` in
their own change, so `reads to EOF` was already there and the
bounded-prefix/approximate-count grep already returned empty before this phase
edited anything — that row was closed by an earlier phase, which the phase file
now records rather than claiming as its own. More, because reading the whole
document turned up three further statements this worklog falsifies that the
phase's table does not list: the `Discover` doc comment's "no full-body reads",
the Rules bullet's "reads only headers/first-lines/timestamps", and
"Configuration and persistence" asserting `No new cache files` and that source
files are `read directly each time`. All three are corrected.

`architecture/config.md` was the one document outside the phase's Files table
that had to change. It states the read-only rule D24 amended — persist skipped
under `utils.NoCreateConfig` — which is still true of the native index and false
of the foreign one. Left alone it would have documented the opposite of shipped
behaviour for `chat list`, the verb this worklog exists for.

The new `Push versus pull` section carries the invariant table from this
README's Strategy into the architecture tree, plus a cost budget naming
`TestDiscoverJSONL_cacheHitOpensNothing`,
`TestForeignIndex_unchangedCorpusSurvivesSecondRun` and
`TestDiscoverJSONL_cacheAgnosticResults` as its standing proofs; all three
exist and pass. A new `Implementing a new JSONL source` section states the
`never opens files` rule and the closed-set rule, so the next vendor reads the
cost contract in the same place it reads the interface.

Every gate green unedited: `gofumpt`, `staticcheck`, `go vet`, `go fix` and
`go build` produce no output; `make qa` exits `0`; the race gate ran `24.4` s at
host load `1.13`, inside the phase-zero band of `24`–`25` s. `dupl` reports the
same `34` groups as phases 2 and 3 and — the point of the exercise — **no clone
group names either vendor's `source_reader.go`**. Two groups do name files this
worklog wrote, both inside `jsonltest` and both deliberate; one of them carried
a justification phase 3 had falsified (it claimed `jsonltest` imports no
production package), and that comment was corrected in place. It is the only Go
file this phase touched.

Coverage: the six files this worklog created cover `436` of `461` statements,
`94.6` percent, far above the seventy-percent floor; every touched package is at
or above it (`internal/vendors` `70.7`, `internal/chat` `77.6`,
`anthropic` `76.5`, `pi` `89.9`, `jsonltest` `93.3`). Repository-wide statement
coverage is `80.2` percent against the `79.630` recorded in the repository
readme. The cold-path benchmark rerun gives `30946454` ns/op against the
phase-zero `89515806` and the ceiling `107418967`, confirming phase 3's result
rather than merely citing it.

Readiness checklist rerun from this directory: items `1`, `2`, `2a`, `3`, `4`,
`5`, `6` and `7` all pass. Two residuals are recorded rather than fixed, because
an executor may not re-plan. First, the Shared interfaces block above still
gives `SourceCache` two methods; the shipped interface has three, since phase 2
added the locator and recorded it as a delta. Second, checklist item `2a`'s
grep matches only test paths under `internal/`, so phase 0's root-package
profiler test file is invisible to it, and it cannot separate a test a phase
declares from one a phase cites — both read correctly, but the mechanical
listing alone shows two false gaps.

### 2026-09-16 — Review round 1 (clai)

Two reviewers audited the completed implementation independently, against the
uncommitted working tree with phases 0 through 4 all marked `Complete`. The
verdict is **not ready**: the gates are green and the design is sound, but one
blocker breaks the property the whole worklog rests on.

**Gates, re-run by both reviewers.** `go test ./... -race -cover -count=3
-timeout=30s` exits `0` at host load below `4`. `gofumpt -l`, `go vet`,
`staticcheck` and `go fix` are all silent. `dupl -t 80 .` reports `34` groups,
none naming either vendor's `source_reader.go` — the phase-four claim reproduces.
Coverage: `internal/chat` `77.6`, `internal/vendors` `70.7`, `anthropic` `76.5`,
`pi` `89.9`, `jsonltest` `93.3`. Phase four's green-gate claims therefore stand;
green gates are not the verdict.

**Independent corroboration of the blocker.** `R1-01` was found twice, by two
reviewers, by different methods — one traced the two meanings of
`scanJSONLFileRow`'s `(SourceRow{}, false)` through `discoverJSONLFile`, the
other reproduced it end to end against the built binary by taking a session file
unreadable, listing, restoring it with its mod time intact, listing again, and
then deleting the cache to watch the row come back. The second reviewer filed it
as `R1-50`. That the same defect surfaced from static reading and from a live
binary is itself worth recording: it is not a theoretical branch, and a synthetic
`fs.FS` failing the first `Open` reproduces it as a unit test. Filed once as
`R1-01`; `R1-50` is retired as a duplicate.

**The root cause is one rule, now promoted.** `R1-01` and `R1-04` are the same
mistake in code written by two different phases: an error about *this run* was
written into a cache that outlives it. That rule is now a Strategy subsection,
*An error is not a fact about the file*, so later phases inherit it rather than
rediscovering it.

**Verified good — do not re-derive in round 2.** D15's stat-before-read holds on
every path: exactly one `fs.FileInfo` per file, and `Store` has no other call
site, so no partially scanned row can be cached. The typed-nil hazard is closed.
`ForeignIndex` is fully mutex-guarded with no self-deadlock in `Persist`. The
warm-cache invariant was verified by `strace` over a built binary — zero session
opens, one stat per file — and one cache write per listing, with `foreignChatRows`
called once. Phase one's behaviour preservation was traced field by field for
both vendors; the seam surface is closed and the reflection tests read the real
types. Phase zero's `runProfiled`/`startCPUProfile` are sound, and its
`CountingFS` implementing `fs.StatFS` is load-bearing for four later tests. The
worker pool's ordering is structural — pre-sized slots, disjoint indices,
`wg.Wait` before return, a feeder that always closes the channel — and zero,
negative and very large bounds are all safe. The prefilter guard was proved
non-vacuous by two mutations; note, though, that
`TestDiscoverJSONL_countsMatchCorpusFactsExactly` is **not** a prefilter guard
for `Cwd`, `Created` or `Model`, since it asserts `MessageCount` alone. The
exactness oracle is a genuine independent re-derivation — the `dupl` group
between `jsonl.go` and `jsonltest/corpus.go` is the evidence of that
independence, not a smell. The typed decode's ignored `json.Unmarshal` error is
safe on every constructed path. `FindJSONLSession` deliberately does not apply
the prefilter, so an unsound prefilter cannot corrupt the continue path. Phase
four's documentation matches shipped behaviour, D24 included.

**Two pre-existing flakes, not caused by this worklog.** `internal/vendors/anthropic`'s
`Test_context` hangs in `httptest.Server.Close`, and `internal/text`'s
`TestNewQuerier_costManagerErrorUsesCostWarnf` collides on stdout capture. Both
reproduce under host load and both pass below load `4`. Recorded so a later round
does not re-derive them.

> **Corrected by review 3 (`R3-55`).** The "both pass below load `4`" model is
> wrong for the second flake. A round-3 reviewer hit
> `TestNewQuerier_costManagerErrorUsesCostWarnf` with `/proc/loadavg` reading
> `1.22` and falling to `0.93`, while `go test ./internal/text/ -race -count=5`
> over that test passes and the next full gate at load about `2.5` was green.
> The collision is **stdout contention between parallel tests inside the
> package**, not host load, so a low load is no guarantee and a red run below
> load `4` is not a regression of this work. The `anthropic` hang is untouched
> by this correction.

**Routing.** A single addendum, phase 5, rather than three reopened phases: the
fixes are many, small, cross-cutting, and share the promoted rule. Phases 2, 3
and 4 keep their `Complete` status and their board rows point at the addendum,
so the executing agent's reading contract stays this README plus one phase file.

### 2026-09-16 — Phase 5 executed; review one closed (clai)

Every finding of review one is closed, and each of the five defects was made to
fail before it was made to pass. The blocker reproduced on the first run of
`TestDiscoverJSONL_cacheAgnosticWithUnreadableFile`: over a corpus holding one
unreadable file, the cached discovery returned two rows where the cache-less
one returned three, and the missing conversation stayed missing after the file
became readable again with its size and mod time untouched. That is the derived
promise broken in one line of test output, which is what the two reviewers
found by static reading and by `strace`.

**The promoted rule paid for itself.** `R1-01` and `R1-04` were fixed as one
idea rather than two patches: `scanJSONLFileRow` now returns a third result so
an open failure is distinguishable from a completed empty scan, and `liveRows`
prunes only on `fs.ErrNotExist`. Both read as the same sentence — an error is a
fact about this run — and both fixes are three lines. The negative-caching
branch is untouched, and `TestDiscoverJSONL_negativeCachingSurvivesTheSplit`
exists to keep it that way.

**Three specification defects are recorded as deltas in the phase file rather
than fixed silently.** One matters to a reviewer: the invariant row for the
typed-nil hazard names `setChatQuerier`'s non-nil guard as its mechanism, and
that guard cannot do the job — an interface holding a nil `*ForeignIndex` is
not nil, so it passes straight through. Removing the guard failed no test,
which is the mutation table's only blank row. The obligation belongs to the
composition root, so the assertion moved there, where a mutation that ignores
the constructor error does fail. The others are smaller: D25 invalidates
`TestForeignIndex_readOnlyDegradesSilently`, which the Files table does not
list, and the two new observability criteria had no observable at all until
`setChatQuerier` was split so the handler could be read back and `main.go`
gained a `newForeignIndex` seam of the kind it already uses for
`newSummarizer`.

**`R1-03` and the note half of `R1-07` are closed by the same pair of tests.**
They drive `chat list` through `Command`, `Setup` and `Run` against an isolated
`CLAI_CONFIG_DIR` and a generated corpus, so the flag state the warning is
judged against is the one the verb actually sets. That is what made the
finding: with the gate on `utils.NoCreateConfig` the interactive listing
printed zero warnings, and with it on `utils.ReadonlyConfig` the raw listing
prints none while the interactive one prints exactly one.

Gates all green unedited: `go test ./... -race -cover -count=3 -timeout=30s`
ran `24.4` s at host load `5.71`, `make qa` exits `0`, and `gofumpt`,
`staticcheck`, `go vet` and `go fix` are silent. `dupl` reports the same `34`
groups as phases 2, 3 and 4, none naming a file this phase touched. Coverage
rose where the phase worked — `internal/chat` `77.6` to `77.9`; the
`internal/vendors` figure was recorded here as rising to `70.9`, and review two
measured it unchanged at `70.7` (`R2-58`) — and the four functions this phase rewrote
are at `100` percent. The cold benchmark rerun gives `34098838` ns/op at load
`1.12` against the ceiling `107418967`. Neither pre-existing flake reproduced,
at loads up to `7.15`.

### 2026-09-16 — Review round 2 (clai)

Two reviewers audited the phase-5 fixes independently against the uncommitted
working tree, one probing the cache and the wiring, one probing discovery and
the documents. The verdict is **not ready**. The design is sound and the gates
are green, but the rule review one promoted is still broken on the branch phase
five did not touch, so the property the whole worklog rests on still does not
hold.

**Gates, reproduced by both reviewers.** `go test ./... -race -cover -count=3
-timeout=30s` exits `0` at host loads `1.35`, `1.46`, `2.41` and `3.67`, about
`24.4` s; neither recorded flake reproduced. `gofumpt -l`, `go vet`,
`staticcheck` and `go fix` are silent. `dupl -t 80 .` reports `34` groups, none
naming a worklog file. Coverage: `internal/chat` `77.9`, `internal/vendors`
`70.7`, `anthropic` `76.5`, `pi` `89.9`, `jsonltest` `93.3`, root `95.6`. The
shipped benchmark runs `31032276`–`34098838` ns/op against the historical
ceiling `107418967`. Every phase-5 figure reproduces except the
`internal/vendors` coverage, which both reviewers measured at `70.7` rather than
the recorded `70.9` (`R2-58`, corrected in place).

**Independent corroboration of the blocker, again.** `R2-01` was found twice by
different probes: one reviewer enumerated every path to `cache.Store` and found
exactly one call site whose error argument is discarded upstream; the other
built an `fs.FS` whose `Open` succeeds and whose `Read` fails and watched a
conversation disappear permanently across a warm run. Two reviewers reproducing
the same failure by different methods is evidence worth keeping, exactly as
`R1-01` and `R1-50` were in round one. Filed once as `R2-01`; `R2-50` is retired
as a duplicate. `R2-04` and the first item of `R2-56` were likewise the same
defect and are filed once as `R2-04`.

**The blocker is the round-one blocker's class, not its instance.** Phase 5 split
the *open* failure out of the zero row and stopped there. `scanJSONLRawLines`
returns `s.Err()`, `scanJSONLFileRow` discards it with `_ =`, and its in-code
comment — "the scan reads to EOF, so every counted role is counted" — is the
false assumption. Two scenarios were reproduced against the pristine tree: a
conversation that disappears permanently once the filesystem heals, the `R1-01`
failure mode verbatim; and a session read-failing mid-file cached at
`MessageCount` `2` against a true `40`, which reintroduces through the failure
branch the exact defect D1 exists to remove, and unlike the oversized-line case
is not deterministic in the file's content. Phase 5's own specification cited "a
network filesystem hiccup" as motivation — a case that errors from `Read`, not
`Open`, and precisely the branch it left unfixed. A read failure is also the
*more likely* of the two in practice: the open is usually served from the dentry
cache while the read goes to the wire.

**The fix must discriminate, not blanket.** Recorded as **D28**. A blanket "any
scan error skips `Store`" would be wrong in the other direction, because
`bufio.ErrTooLong` is a property of the bytes: the same file truncates
identically every run, a cache-less rescan produces the same row, and
`(size, mtime)` invalidates it the moment the content changes. That is `R2-51`'s
verdict on the truncation path, reached independently, and it is correct — under
the promoted rule's own words, "something the filesystem actually reported about
the file's *content*". So the oversized-line row stays cacheable and every other
scanner error skips `Store`, which needs `errors.Is(err, bufio.ErrTooLong)`
rather than a bare nil-check. `R2-51` leaves one residual obligation, now on the
parameters table: `ReadMaxToken` is a compile-time constant and is not part of
the cache key, so raising it would leave every previously truncated row cached
at its old undercount indefinitely — changing it requires a
`foreign-index-version` bump.

**One finding was upgraded and one contract changed.** `R2-03` was filed as
information, because phase 5's invariant literally promises that a chat verb
invokes the factory exactly once and its test asserts `1` for those verbs — so
the behaviour is in contract. The contract is what is wrong. `readOnlyChatSetup`
calls `dir` and `dirv2` shell-prompt hot paths, so a precmd hook running
`clai -r c dirv2` now decodes a corpus-sized index on every prompt render: the
cost `R1-02` was raised to remove, relocated from every verb to most chat verbs
rather than removed. Upgraded to **major** and recorded as **D27**: the
construction becomes lazy behind a once-guard, and phase 5's invariant row and
`TestCommands_buildConstructsNoForeignIndex` are amended to assert zero for
`help`, `dir`, `dirv2` and `delete`. `R2-57` was upgraded from note to minor for
the same reason — four stale Go doc comments in the files a vendor author opens
first are the "cost rule lives in prose" failure D7 says this worklog exists to
remove, and one of them, `SourceRow`'s "`MessageCount` may be approximate", is
now not merely stale but inverted.

**The most reusable output of two review cycles is still trapped in the
worklog.** `R2-56`: the promoted Strategy rule never reached
`architecture/continue-from-claudex.md`, although the round-one routing note said
it was promoted so later phases inherit it and phase 4's job was carrying README
Strategy invariants into the architecture tree. The worklog is deleted by
convention once the effort ships, and `R2-01` proves the shipped code still
violates the rule. Phase 6 carries it across with D28's distinction.

**Verified good — do not re-derive in round three.** The phase-5 signature change
is clean through the concurrency path: `scanJSONLFileRow`'s new return is
consumed entirely inside `discoverJSONLFile`, `scanJSONLFileSlots` is
behaviourally byte-identical, a per-file error cannot abort a run or corrupt a
slot, and parallel-matches-serial holds green under `-race`. Cancellation is now
fully specified and guarded, with stored rows proved field-identical to a full
cache-less scan. `R1-01`'s original branch is correctly and minimally fixed with
the negative-caching branch pinned. There is exactly one `Store` call site in the
package, and `cachedSessionPath` and `FindJSONLSession` never write; the
stat-failure path stores nothing and has no nil dereference. The typed-nil
obligation sits at the composition root with a real assertion that a mutation
fails, and phase 5's honest note that `newChatQuerier`'s guard is not the
mechanism is accurate. One index construction, one cache read and at most one
write per invocation, confirmed through the real dispatcher. D25's gate is
verified on every listing path with a real built binary — one warning
interactive, silence under `-r` — and `warn` fires at most once with no deadlock
in `liveRows` into `Persist`. Both prefilters are sound over their corpora within
D26's assumption. `StatAbs`/`OpenAbs` symmetry is real and `CountingFS`
implements `fs.StatFS`. `architecture/config.md` and the push/pull sections match
shipped behaviour. Five mutations of the phase-5 fixes were all caught, so no
phase-5 test in scope is vacuous.

**Routing.** A single addendum again, phase 6, on phase 5's rationale: the fixes
are many, small and cross-cutting, and two of them share D28. Phases 2 through 5
keep their `Complete` status and their board rows point at phase 6, so the
executing agent's reading contract stays this README plus one phase file.

### 2026-09-16 — Phase 6 executed; review two closed (clai)

Every finding review two routed to the addendum is closed, each production fix
proved by a test shown to fail against the shipped tree before the fix landed.
The tree stays uncommitted; nothing was stashed.

**The blocker was reproduced in both of its scenarios before it was fixed.** An
`fs.FS` whose `Open` succeeds and whose `Read` fails delivers no bytes at all in
one fixture and exactly two lines in the other. The first cached a zero row
under the pre-read `(size, mtime)` and the conversation never came back after
the filesystem healed; the second persisted `MessageCount` `2` against a true
`40`. `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile` showed the
derived-cache promise broken the same way `R1-01` did. The fix is
`errors.Is(err, bufio.ErrTooLong)` in `discoverJSONLFile`, and the mutation that
turns it into a blanket skip is caught by two tests — this phase's
`TestDiscoverJSONL_oversizedLineRowStaysCacheable` and phase three's
`TestDiscoverJSONL_oversizedLineTruncatesScan`, which the specification named as
the tripwire and which fires exactly as predicted.

**`Locate`'s disagreement needed a cross-directory fixture to exist at all.**
Phase five's test writes both duplicates into one directory, where walk order
and full-path order coincide; the new fixture uses the sibling projects `proj`
and `proj-bak`, where the hyphen sorts below the separator. Proved through
`anthropic.SourceReader.Read`: the warm index opened the backup's transcript and
the cache-less walk opened the project's. `compareWalkOrder` is extracted rather
than inlined, and a subtest pins it as a total order, since the tail rule cannot
be reached through a walk.

**D27 amended one more test than the specification listed.** The two named in
*Amends earlier phases* were amended as written, but
`TestChatCommand_failedFactoryLeavesCacheNil` — which the README names as one of
the three tests closing `R1-02` — also asserts the eager construction D27
removes, and the specification does not mention it. It was amended where it
lives, and phase six's own `TestChatCommand_lazyFactoryFailureLeavesCacheUnset`
narrowed to the property only it owns: a failed construction is memoised rather
than retried on every consultation.
`TestChatCommand_cacheFactoryRunsOncePerVerb` also needed more than the
narrowing the specification describes: under D27 a consulting verb constructs
nothing at `Setup` either, so the surviving assertion moved to a run of the verb
through `Command` → `Setup` → `Run`.

**Three of the documentation findings were already landed by the review's own
change.** `R2-04`, the README half of `R2-55` and `R2-51` were verified against
their acceptance commands rather than edited again. The architecture work is
real and new: the promoted rule with D28's refinement is now a section of the
source-reader contract, so it survives this worklog's deletion.

**Gates.** `make qa` exits `0` at host load `1.39`, run twice. `gofumpt`,
`staticcheck`, `go vet` and `go fix` silent; `dupl -t 80 .` reports the same
`34` groups, none naming a file this phase touched. Coverage: `internal/chat`
`78.9` — up from review two's `78` — `anthropic` `76.5`, `pi` `89.9`,
`jsonltest` `93.3`, root `95.6`. The benchmark runs `32569032` ns/op against the
ceiling `107418967`. Neither recorded flake reproduced. Readiness checklist
items `1`, `2`, `2a` and `6` pass from this directory.

**`R2-58` has a cause, and it is not an arithmetic slip.** `internal/vendors`
coverage was measured twelve times and returned `70.9` six times and `70.6` or
`70.7` six times. `scanJSONLFileSlots`'s cancellation branch — the `break feed`
reached only when the feeder loses the race to the cancel — is covered or not
per run, so the package total oscillates. Phase five's `70.9` and both
review-two reviewers' `70.7` are both real measurements of the same tree. The
figure should be quoted as a range from here on; the code behind it is phase
three's and no phase since has touched it.

**One surviving mutation, recorded rather than fixed.** Assigning the factory's
result without the handler's non-nil guard is caught by nothing, and cannot be:
a failed factory returns a genuinely nil interface, and a typed nil defeats an
interface non-nil check anyway. That is phase five's own finding reproduced
under the new wiring, and the obligation stays at the composition root, where a
mutation that lets a typed nil escape does fail
`TestCommands_buildConstructsNoForeignIndex`.

### 2026-09-16 — Review round 3 (clai)

Two reviewers audited phase 6 and the whole worklog independently against the
uncommitted working tree. **Both returned `ready`.** There is no blocker and no
major. The two halves of a sign-off were assessed separately and both hold: the
work ships clean through every gate, and the work is correct. Round 2's
substantive open question — whether the promoted rule *An error is not a fact
about the file* holds on **both** branches in code and has escaped the worklog
into the architecture tree — is closed on both counts. The twelve findings this
round files are `minor` and `note`; none of them blocks a release.

**Gates, reproduced by both reviewers.** `make qa` exits `0`.
`go test ./... -race -cover -count=3 -timeout=30s` is green at host loads `1.67`
and about `2.5`, in `24.4` to `24.9` s. `gofumpt -l`, `go vet`, `staticcheck`
and `go fix` are silent. `dupl -t 80 .` reports `34` groups, whose only
production pair is the deliberate, documented oracle-independence clone between
`jsonl.go` and `jsonltest/corpus.go`. Coverage: `internal/chat` `78.9`,
`internal/vendors` `70.9` — **oscillating**, see `R3-57` — `anthropic` `76.5`,
`pi` `89.9`, `jsonltest` `93.3`, root `95.6`. The benchmark runs `33411174`
ns/op, inside the shipped band and far under the historical ceiling.

**One gate observation worth recording.** The root package alone accounts for
`22.6` s of the `30` s bound at `count=3` — under eight seconds of headroom.
That, not the concurrency phase 3 introduced, is what makes the repository gate
load-sensitive at all, and it is why a new default-on feature added to the root
package's fixtures is expensive in a way its own runtime does not explain.

**`R2-01` is closed as a class, verified four ways.** `scanErr` is returned on
all three exits of `scanJSONLFileRow`, including the identity-less one. There is
exactly one `cache.Store` call site and the D28 gate dominates it. The gate sits
**before** the `cacheable` check, so a nil-cache run and a warm-cache run agree
by construction rather than by the cache happening to be safe. No other value
entering a row is run-scoped: `Fields`, the prefilter and the `ModTime` fallback
are all pure functions of the bytes plus the key. And `bufio.ErrTooLong` cannot
carry or be masked by a transport error, because `bufio.Scanner.setErr` is
first-write-wins and `Scan` returns immediately, so `Err()` yields the bare
sentinel. Both directions of D28 are pinned by mutation.

**Verified good — do not re-derive.** `compareWalkOrder` is a faithful
`WalkDir` oracle over sixty random trees with adversarial sibling names at every
depth, and a total order over twenty adversarial paths; its unreachable
segment-count tail rule is pinned by a direct subtest. Lazy resolution is
race-free: sixty-four goroutines build exactly one index, all observe the same
non-nil interface, a panicking factory does not deadlock later askers, and the
memoised failure does not retry. `persistForeignCache` is called from exactly
one place, outside the interactive loop, so there is no skipped or doubled
write, and the `continue` path mutating nothing means having no persist there is
correct. `Locate` filters by source before ranking and `cachedSessionPath`
re-validates the winner. `liveRows` still prunes on `fs.ErrNotExist` alone.
`ChatHandler` is never copied — `go vet` copylocks is clean. No phase-6 test is
vacuous: all ten fail under targeted mutation and the phase's own mutation table
reproduces exactly. All eight readiness items were assessed; item `2`'s repair
is non-vacuous — eighty-seven names across six phases, so an empty `uniq -d` is
a real negative — and item `2a`'s unscoping does surface the two root-package
files it used to miss. Every Definition-of-success row's cited evidence exists
and demonstrates its outcome, with the documented cold-path residual from
`R1-52`. `git diff` over the two vendor test files confirms exactly thirteen
`Discover` and nine `Read` changes and nothing else, so phase 1's "tests
unedited" claim survives phase 2's plumbing.

**The round's root cause is one rule, now promoted.** Both reviewers arrived at
it independently: *a corrected claim is not corrected until every copy of it
is*, and the copies are enumerable — README invariant row, README parameters
row, owning phase's contract table, Go doc comment, architecture section. Five
of this round's findings are one surviving copy each, and every round so far has
spent at least one finding the same way. The rule is a Strategy subsection so a
later phase inherits the enumeration rather than rediscovering it.

**The flake model was wrong, and that matters more than the flake.**
`R3-55`: the recorded "both pass below load `4`" makes a maintainer who runs the
gate once at low load, sees red, and trusts the README read it as a regression
of this change. A reviewer reproduced `internal/text`'s collision at loadavg
`1.22`, and the same test passes under `-count=5` in isolation. The cause is
stdout contention between parallel tests inside the package. Restated in place
above and in phase 4.

**Routing.** A third addendum, phase 7, on the rationale phases 5 and 6 used.
One reviewer proposed a plain documentation change instead, which is a fair
reading of a `ready` verdict — but three of the findings are code plus tests
(`R3-01`, `R3-02`, and D29's error message under `R3-03`), and an untracked
sweep would leave them with no contract, no acceptance row and no evidence. The
worklog's own lifecycle carries them. Phases 1 through 6 keep their `Complete`
status and their board rows point at phase 7.

### 2026-09-16 — Phase 7 executed; review three closed; worklog finished (clai)

Phase 7 executed as specified, tests first. Review three's verdict was `ready`,
so nothing here closed a blocker or a major: three code findings of `minor` and
`note` severity, and the surviving copies of two claims earlier rounds had
already corrected elsewhere.

**The identity scan (`R3-01`).** `jsonlFileIdentity` returns its scanner error
and `FindJSONLSession` fails the lookup instead of walking on. A refused open
stays a skip — the error-coverage table says so, and the walk has always
tolerated a file it cannot open — while a read that fails before the identity
line now surfaces. The proof is at the reader boundary, not at the generic entry
point: with the walk-first of two sibling duplicates unreadable, the pre-fix tree
returned the *backup copy's* transcript through `anthropic.SourceReader.Read`,
which is `R2-02`'s user-visible defect reached through a different door. That
test uses a real `chat.ForeignIndex` for its warm half, because the generic test
file's own cache double ranges a map and cannot rank two rows that share an
identity.

**The guarded accessor (`R3-02`).** The reviewer's probe was reproduced exactly:
`-race` reports a write inside `sync.Once.doSlow` against `persistForeignCache`'s
bare field read. `ChatHandler` gains a mutex, the resolver writes under it, and a
new non-constructing accessor reads under it. `sync.Once` alone cannot serve a
reader that must not construct, because its happens-before only reaches a caller
of `Do`. The accessor was preferred over a stated call contract for the reason
this round promoted into Strategy: a stated contract is one more copy of a claim
that can rot.

**D29's message (`R3-03`).** The wrap sits on `vendors.ScanJSONLLines` and
nowhere else — generic, because `ReadMaxToken` is a generic constant and the
project forbids vendor-specific logic in shared code. It keeps
`errors.Is(err, bufio.ErrTooLong)` true through `%w`, which is what stops it
silently reverting D28; the two oversized-line tripwires pass unedited and a
mutation that drops the `%w` fires them. Discovery's own scanner is untouched.

**The documentation findings are where the round's rule earned itself.** Walking
the five copies for both claims turned up two the findings had not named: the
`jsonltest.Corpus` doc comment stating the refuted "a directory walk produces
lexical path order" as a general fact, and phase 0's declaration of the same
field. Correcting only the three named locations would have made phase 7 the
fifth round to correct a subset. Both are fixed.

**Gates.** `gofumpt`, `staticcheck`, `go vet`, `go fix` silent; `dupl` at `34`
clone groups, the count phase 6 recorded, with none of the new code named;
`go test ./... -race -cover -count=3 -timeout=30s` green twice, at host loads
`1.24` and `1.55`, the root package taking `22.7` s of the `30` s bound. Neither
recorded flake reproduced. The cold benchmark is `29918965` ns/op against the
shipped `31032276`. `internal/vendors` coverage rises from the `70.6`–`70.9`
range to `72.2`–`72.5`; every other package's figure is unchanged. Readiness
items `1`, `2`, `2a`, `3` and `6` all pass from this directory.

**The worklog is finished.** All eight phases are `Complete`, every validation
and review finding of every round is closed, and phase 7's checkbox ticks are
recorded in the four phase files that carried them. Three specification
inaccuracies are recorded in phase 7's *What the specification got wrong* — the
most substantive being the integration contract's warm-cache row, whose "both
fail" holds at the reader boundary it names but not at `FindJSONLSession`, where
a warm index answers from `Locate` plus one `stat` and never reads. What remains
belongs to the maintainer: an optional review round four over phase 7, and the
release itself. Nothing is committed; the change is left in the working tree.

### 2026-09-17 — AGENTS.md gained a Code style section; the change brought into line (clai)

`AGENTS.md` gained a **Code style** section after this worklog was finished:
never log-and-return, always return the error; push an async routine's error to
the parent over a channel; and no bloaty redundant comments — private functions
rarely need one, public ones describe only non-intuitive behaviour. The finished
change predates all of it, so it was retro-fitted in place. Nothing was
committed; the working tree is still the done-state.

**`Persist` returns an error (rule one).** `ForeignIndex.Persist()` warned at
three failure sites and returned nothing, and `persistForeignCache` was
documented as "deliberately silent and unfailable". Both now return the error.
`ForeignIndex` loses its `errOut` writer and its `warned` flag entirely and is a
pure value again: it writes the file or says why it could not.

The announcement moved to the one top-level boundary that owns the decision.
`foreignChatRows` defers
`cq.announceForeignCacheFailure(cq.persistForeignCache())`, and the announcer is
the handler's — it holds the `sync.Once`, writes to `ChatHandler.errOut`, and
gates on `utils.ReadonlyConfig` rather than `utils.NoCreateConfig` exactly as
before (D25). Every invariant the old shape carried survives: a failed write
still cannot fail a listing, because the defer discards nothing but an already
handled error; `SkipIndex` still suppresses all index I/O, now by returning
`nil` early; and the warning still fires at most once, now per handler, which is
once per invocation.

The tests assert the same properties through the new seam rather than a weaker
one. `TestForeignIndex_undirectoryableParentWarnsOnce` became
`...ReturnsError`: both attempts now report the failure *and* the index is
asserted to print nothing itself, which is a stronger claim than the old
"exactly one warning". `TestForeignIndex_readOnlyDegradesSilently` keeps its
three subtests and its name, but the raw-run and `NoCreateConfig` cases now feed
a real `Persist` error to `announceForeignCacheFailure` twice, so the D25 gate
and the once-only property are pinned together. `runListVerb` captures the
verb's stderr instead of injecting a buffer into the index, which is a truer
test of the same thing — it is now the real writer under test. The load-silence
assertions that the old `errOut` buffer carried are kept where they still mean
something, by capturing stderr around the construction.

**Roughly 320 comment lines went (rule three).** The added comment volume across
the twelve non-test Go files falls from `558` to `235`, a `58` % cut. What went
was worklog rationale that the worklog already holds: the 34-line `SourceRow`
history in `source.go`, the three-step contract essay on `DiscoverJSONL`, the
D26 paragraph repeated in both `MayContribute`s and again in `conformance.go`,
and the doc comments on ten private functions — `walkJSONLPaths`,
`scanJSONLFileSlots`, `applyLineFields`, `buildSession` and the rest — that
restated what their five lines of body already said. Enum members whose comment
repeated their identifier (`KindEmptyFile is zero bytes`) and accessor comments
that repeated their signature (`Opens reports how many times name was opened`)
went the same way.

Four notes were kept deliberately, each because losing it reopens a fixed
defect: the `%w` over `bufio.ErrTooLong` in `ScanJSONLLines` is load-bearing
because discovery's cacheability gate is `errors.Is` on that sentinel (D28); the
`fs.FileInfo` in `discoverJSONLFile` is taken *before* the read and is the only
one taken (D15); `compareWalkOrder` is `filepath.WalkDir`'s per-directory order
and **not** whole-path lexical order (R2-02); and `resolvedForeignCache` takes
the mutex because `sync.Once`'s happens-before only reaches a goroutine that
calls `Do` (R3-02).

**One extra fix.** The `Code style` section also forbids panicking, and
`jsonltest.buildSession` panicked on an unreachable `json.Marshal` failure. It
returns an error now, threaded through `WriteCorpusErr`, which already returns
one. The branch is genuinely unreachable, so it costs two uncovered statements:
`internal/vendors/jsonltest` reads `93.0` % against the `93.3` % this worklog
recorded. That is the whole of the delta, and the package stays above the `90` %
preferred bar.

**Gates.** `gofumpt`, `staticcheck`, `go vet` and `go fix` silent; `dupl` at
`34` clone groups, unchanged, with none of the touched code named;
`go test ./... -race -cover -count=3 -timeout=30s` green at host load `3.7`, the
root package taking `22.8` s of the `30` s bound. `internal/chat` holds
`78.9` %, `internal/vendors` `72.5` %, `anthropic` `76.8` %, `pi` `91.1` % and
the root `95.6` %. The `internal/text` stdout-contention flake
(`TestNewQuerier_costManagerErrorUsesCostWarnf`) fired once on an earlier run at
the same load and passed on the rerun, as phase 4 records it doing; the
`httptest.Server.Close` hang in `internal/vendors/anthropic` did not reproduce.
