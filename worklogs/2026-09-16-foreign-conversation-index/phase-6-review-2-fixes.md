# Phase 6 — Review two fixes

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Close every finding of the second review round: make the shipped code obey the
README's promoted rule on the branch phase five did not split, make `Locate`
rank by the walk's own order, build the foreign index only for the verbs that
read it, and carry the rule and its D28 refinement out of this worklog and into
the architecture tree before the worklog is deleted.

## Specification

This phase is a second addendum on phase five's rationale: it re-opens no phase
file and introduces no design the README does not already carry. Its reading
contract is the README and this file. The findings it closes are listed in the
README feedback index under **Review two**; each subsection below names the ones
it closes. Two of them share a single maintainer decision, D28.

### A read failure is not a fact about the file either (`R2-01`, blocker; D28)

`scanJSONLFileRow` in `internal/vendors/jsonl_discover.go` calls
`scanJSONLRawLines`, which returns the scanner's `Err()`, and discards it. A
read that fails partway through a file therefore returns
`(SourceRow{...}, ..., nil)` — indistinguishable from a scan that reached EOF —
and `discoverJSONLFile` reaches `cache.Store` with a row built from a partial
read, keyed by the `(size, mtime)` pair taken **before** the read. The failure
changed neither of those, so the row can never be invalidated. The in-code
comment "the scan reads to EOF, so every counted role is counted" is the false
assumption the fix removes.

This is the class of `R1-01`, not a regression of it: phase five split out the
*open* failure and stopped there. Phase five's own specification cited a network
filesystem hiccup as motivation — a case that errors from `Read`, not from
`Open`, and precisely the branch it left unfixed. A read failure is also the
more likely of the two, because the open is usually served from the dentry cache
while the read goes to the wire.

Two user-visible failures follow. A conversation **disappears permanently**: a
warm run lists fewer rows than a cache-less run, and keeps doing so after the
filesystem heals, because nothing about the file changed. And a **wrong count is
persisted**: a session that read-fails partway through is cached at the number of
messages the scan managed to see, against a true total many times larger — which
reintroduces, through the failure branch, the exact defect D1 exists to remove.
Unlike the oversized-line case, it is not deterministic in the file's content:
two runs can cache two different counts for the same bytes.

Unmet clauses: the README Strategy rule *An error is not a fact about the file*;
the README invariant *the foreign index is derived: removing it changes results
in no way except timing*; the Definition of success row *deleting
`foreign_index.cache` changes only timing, never rows*; phase five's acceptance
criterion *deleting the cache changes no row, over a corpus containing an
unreadable file*; and phase five's integration-contract row *no row written for
it on the first run*.

**The fix discriminates; it does not blanket-skip.** D28 is the governing
distinction and the executor must not shortcut it:

- `bufio.ErrTooLong` **stays cacheable**. An oversized line is a property of the
  bytes. The same file truncates in the same place every run, a cache-less
  rescan produces the same row, and `(size, mtime)` invalidates the row the
  moment the content changes. Cache-agnosticism holds, so the row is a fact
  about the file and persisting it is legitimate. This is `R2-51`'s verdict.
- Every **other** scanner error must skip `Store`. It is a fact about this run,
  invisible to `(size, mtime)`, so a row written from it can never be
  invalidated.

So `scanJSONLFileRow` propagates the scanner's error alongside its existing
open-failure error, and `discoverJSONLFile` reaches `Store` when the error is
nil or `errors.Is(err, bufio.ErrTooLong)`, and skips it otherwise. A bare
nil-check is **wrong**: it would silently revert the oversized-line behaviour
phase three specified, phase four documented and phase three's oversized-line
test pins, and it would remove the cached counterpart of that behaviour with no
test failing.

Nothing about the hit path, the stat order, the negative-caching branch or the
row contents changes. A file skipped for a read failure is simply rescanned next
run, exactly as an unopenable one now is.

### `Locate` ranks by walk order, not by whole-path order (`R2-02`, minor)

`ForeignIndex.Locate` in `internal/chat/foreign_index.go` keeps the lexically
smallest matching **full path**, and its doc comment asserts that this is the
walk fallback's own tie-break. It is not. `WalkJSONLFiles` uses
`filepath.WalkDir`, which orders **entries within each directory**, depth first.
The hyphen sorts below the path separator, so for sibling project directories
`proj` and `proj-bak` the walk visits `root/proj/s.jsonl` first while
`root/proj-bak/s.jsonl` is the lexically smaller full path. `Locate` answers
with the file the walk would not have opened.

This breaks the README invariant *lookup and discovery never disagree about
which file holds a session, cache or no cache*, whose stated mechanism is
exactly the false claim. It is not exotic: Claude Code's layout is
`~/.claude/projects/<cwd-slug>/<uuid>.jsonl`, where the slug replaces separators
with hyphens, so suffix-related sibling directories are the norm — a worktree,
a backup, a renamed checkout. `R1-05`'s own motivating case produces it. Proved
end to end through `anthropic.SourceReader.Read`: the cache-less walk returned
one transcript and the warm index returned the other, so `clai chat continue`
opens a different transcript depending on whether the cache is warm. The
disagreement the invariant forbids was made deterministic rather than removed.

**The fix ranks candidates in walk order:** compare the two paths segment by
segment, which is what `filepath.WalkDir` orders files by, and keep the
candidate the walk would reach first. The invariant's wording is right and the
implementation is wrong, so the wording is not narrowed. The doc comment is
corrected in the same change, and the README invariant row now states walk order
as its mechanism.

The existing test cannot observe this: it writes both duplicates into **one**
directory, where full-path order and walk order coincide. The new fixture places
the duplicates in sibling directories whose names differ by a suffix.

### The index is built only by a verb that reads it (`R2-03`, major; D27)

`setChatQuerier` in `internal/chat/cmd.go` invokes the factory for every chat
verb: `continue`, `delete`, `list`, `dir`, `dirv2` and `help`. Only `list`,
through `handleListCmd` into `foreignChatRows`, and `continue`, through
`readForeignChat`, ever touch `foreignCache`. The rest pay a whole
`os.ReadFile` plus `json.Unmarshal` of a corpus-sized index for nothing.

Phase five's invariant literally promises that a chat verb invokes the factory
exactly once, and its test asserts one invocation for those verbs, so the
shipped behaviour is in contract. **The contract is what is wrong.**
`readOnlyChatSetup`'s own comment calls `dir` and `dirv2` shell-prompt hot
paths, so `clai -r c dirv2` in a precmd hook decodes the index on every prompt
render. That is the cost `R1-02` was raised to remove, relocated from every verb
to most chat verbs rather than removed.

**D27: the construction becomes lazy.** Only a verb that actually consults the
cache constructs it. Three properties of `R1-02`'s fix must survive:

- **At most one construction per invocation**, however many times the consuming
  code asks — a `sync.Once`-style guard or equivalent, not a construction per
  call site.
- **The typed-nil protection stays at the composition root**, where phase five
  demonstrated it belongs: a failed construction yields a nil interface, never
  an interface holding a nil pointer, and the assertion that proves it stays on
  the root-package test rather than moving to `newChatQuerier`'s guard, which
  phase five showed is not the mechanism.
- **The factory keeps its error handling.** A construction that fails leaves the
  field unset and the listing scans, with no failure surfaced to the user.

### Amends earlier phases

This subsection names tests other phases own. It is the one place in this file
that does, and readiness-checklist item `2` skips it for that reason.

| Owner                                 | Test                                          | Amendment                                                                                          |
| --------------------------------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `phase-5-review-1-fixes.md`           | `TestCommands_buildConstructsNoForeignIndex`  | Asserts no construction for `help`, `dir`, `dirv2` and `delete`, and one each for `list` and `continue` |
| `phase-5-review-1-fixes.md`           | `TestChatCommand_cacheFactoryRunsOncePerVerb` | Restricted to the consulting verbs; the non-consulting verbs move to this phase's own test        |
| `phase-5-review-1-fixes.md`           | Its invariant row *a chat verb invokes the factory exactly once* | Restated as the two D27 rows the README now carries                              |
| `phase-3-parallel-exact.md`           | `TestDiscoverJSONL_oversizedLineTruncatesScan` | Must still pass unedited: D28 keeps the token-bound row cacheable, so a blanket skip would break it |

Amending a phase-five test is expected here and is authorised by D27. Do not
copy the tests into this phase; edit them where they live and leave the
declaration with phase five.

### The `SourceCache` block gains `Locate` (`R2-04`, minor)

The README's *Shared interfaces* section shows a two-method `SourceCache`;
`internal/vendors/source.go` has three. It matters because `R1-05` and `R2-02`
are entirely about `Locate`'s contract, and that block is also where the
typed-nil rule is stated, so it is the one place an executor reads both. Phase
four recorded it as a residual an executor may not fix; two review rounds later
it is still wrong in the one document every executor reads. Fixed in the README
by this phase, with `Locate`'s walk-order tie-break named in the block.

### The stale Go doc comments (`R2-57`, minor)

Four doc comments in the four files a future vendor author opens first still
describe the design this worklog replaced. These are the "cost rule lives in
prose" failures D7 says the worklog exists to remove, left in exactly the place
D7 says they do the most damage.

| Location                                             | What it says now                                                                                   | Why it is false                                                                                     |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------ |
| `internal/vendors/source.go`, the `SourceRow` contract | `MessageCount` **may be approximate** during discovery; exact counts are available after `Read`     | Both sentences are false and inverted: discovery is exact, and `Read` offers no later refinement. This is the contract comment on the struct every vendor implements |
| `internal/vendors/source.go`, the `SourceReader` contract | `Discover` MUST be read-only and fast — **no full body parsing**                                 | A cache miss now reads to EOF and decodes every candidate line. Phase four's journal claims it fixed this; that correction landed in the architecture document, not here |
| `internal/vendors/anthropic/source_reader.go`        | discovery is **bounded**                                                                            | It refers to the `DiscoverMaxLines` phase three deleted from this same file                          |
| `internal/vendors/pi/source_reader.go`               | discovery is **bounded**                                                                            | The same                                                                                             |

The replacements state the shipped rule: discovery is exact and reads to EOF on
a miss, costs one `stat` on a hit, and is bounded by the cache rather than by a
line cap.

### The promoted rule reaches the architecture tree (`R2-56`, note)

`architecture/continue-from-claudex.md` does not carry the Strategy rule *An
error is not a fact about the file*, although review one's routing note said it
was promoted so later phases inherit it, and phase four's job was carrying
README Strategy invariants into the architecture tree. It is the most reusable
output of two review cycles, `R2-01` proves the shipped code still violates it,
and the worklog is deleted by convention once the effort ships.

This phase carries the rule into the document's source-reader contract, with
D28's distinction stated alongside it: a content-determined bound is a fact
about the file and may be cached; a transport error is a fact about this run and
may not. Both halves travel together, because the rule without the refinement
reads as "never cache after an error", which is the wrong fix.

### The two overstated documents and D26's open loop (`R2-55`, note)

The conformance suite was verified non-vacuous by nine mutations — narrowing the
markers to the session-identifier key alone is caught, an empty marker set is
caught, and its three anti-vacuity guards are intact — but five of the six
anthropic markers can be deleted **individually** without failing it, because
every generated Claude line carries the session-identifier key. The suite
detects a narrowing only when the fixture corpus happens to contain a line the
narrowed filter misses; it does not police the marker set.

Two statements are qualified rather than rewritten, because the suite is worth
keeping and the claim is true of the mistake it was built for:

- `architecture/continue-from-claudex.md`'s "which fails on exactly that
  mistake" gains its clause: it fails on a narrowing the fixture corpus can
  witness, and a marker every fixture line carries can be dropped without it
  noticing.
- The README invariant *a prefilter never hides a line the schema would have
  used* is scoped to the fixture corpus the suite runs with — done in the same
  change as this phase's README edits.

Separately, D26's stated mechanism is that a vendor format change becomes a
review finding rather than a silent undercount, but phase five's acceptance grep
was scoped to `internal/vendors/`, so the canonical-spelling assumption landed
in both readers and the suite and **not** in
`architecture/continue-from-claudex.md`'s *Implementing a new JSONL source*
section — the section a new vendor author reads before writing a
`MayContribute`. One sentence there closes D26's own loop.

### The `ReadMaxToken` coupling (`R2-51`, note)

`vendors.ReadMaxToken` is a compile-time constant and is **not** part of the
cache key, while `foreign-index-version` is the documented lever for an
incompatible change. Raising the token bound would therefore leave every
previously truncated row cached at its old undercount indefinitely, with no
`(size, mtime)` change to invalidate it. Changing `ReadMaxToken` requires a
`foreign-index-version` bump; the sentence sits beside both parameter rows in
the README, added in the same change as this phase's other README edits.

### The worklog's own hygiene (`R2-52`, `R2-53`, `R2-54`, `R2-58`)

`R2-53`, `R2-54` and `R2-58` were closed in the review's own change: all thirteen
round-one checkboxes across phases two, three and four are ticked, phase five's
six item-`1` violations and its coverage row are corrected, and
readiness-checklist items `2` and `2a` are repaired — item `2` now qualifies
"declared" as naming in a phase's contract half, stopping at
`## Implementation notes` and skipping an `### Amends earlier phases`
subsection, and item `2a` no longer scopes its file grep to `internal/`, so it
can see the root-package test files whose invisibility was recorded twice
already.

`R2-52` remains for this phase: `phase-3-parallel-exact.md`'s
integration-contract row for a mid-flight cancellation still reads *Required
side effects: None*, which is what `R1-51` reported, while phase five states the
real rule in its own contract row. The worklog carries two contract tables
contradicting each other for the same trigger, and the one a reader reaches from
the phase that owns parallel discovery is the wrong one. Restate the row or
annotate it to point at phase five's.

This phase also ticks its own round-two checkboxes in phases two, three, four
and five as it lands each finding.

### Invariants

| Invariant                                                                              | Mechanism                                                                                                            | Test                                                                 |
| ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------ |
| A scan that failed to read its file is never written to the cache                       | `scanJSONLFileRow` propagates the scanner's error; `discoverJSONLFile` skips `Store` unless it is the token bound      | `TestDiscoverJSONL_readFailureIsNotCached`                            |
| Discovery stays cache-agnostic over a corpus containing a file that opens and cannot be read | Nil-cache, cold-cache and warm-cache runs return identical rows, before and after the filesystem heals              | `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile`                  |
| A partial read never becomes a persisted count                                          | The skipped `Store` leaves no row, so the next run rescans and reports the true count                                 | `TestDiscoverJSONL_partialReadNeverPersistsACount`                    |
| A scan truncated by the content-determined token bound stays cacheable                  | `errors.Is(err, bufio.ErrTooLong)` is the one error that still reaches `Store`, because the same bytes truncate identically every run (D28) | `TestDiscoverJSONL_oversizedLineRowStaysCacheable`  |
| `Locate` returns the file the walk would reach first                                    | Candidates are compared segment by segment, which is `filepath.WalkDir`'s ordering for files                          | `TestForeignIndex_locateFollowsWalkOrder`                             |
| Lookup and discovery agree across sibling directories, not only within one               | The same ranking, exercised through `anthropic.SourceReader.Read` over duplicates in suffix-related sibling directories | `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs`            |
| A chat verb that never consults the cache constructs no index                           | The factory is invoked at the point of consultation, not in `setChatQuerier` (D27)                                    | `TestChatCommand_onlyConsultingVerbsConstructTheIndex`                |
| At most one construction per invocation, however many times the verb asks                | A once-guard around the lazy factory                                                                                  | `TestChatCommand_lazyFactoryRunsAtMostOnce`                           |
| A lazy construction that fails still leaves a nil interface, never a typed nil           | The factory yields a nil interface on failure; the assertion stays at the composition root                            | `TestChatCommand_lazyFactoryFailureLeavesCacheUnset`                  |

### Parameters this phase owns

Values live in the README parameters table and are not restated here.

| Parameter                                                                      | Why this phase owns it                                                        |
| -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `chat.CommandDeps.ForeignCache` invoked lazily, behind a once-guard              | D27 moves the invocation from `setChatQuerier` to the consulting verb           |
| The cacheable-scan-error rule: `errors.Is(err, bufio.ErrTooLong)` alone may `Store` | D28 is the distinction `R2-01`'s fix turns on                                |
| The `vendors.ReadMaxToken` and `foreign-index-version` coupling                  | `R2-51`'s residual obligation; both parameter rows carry the same sentence      |

### Files

| File                                                | Change                                                                                                                      |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `internal/vendors/jsonl_discover.go`                 | `scanJSONLFileRow` propagates the scanner error; `discoverJSONLFile` stores only on a nil error or the token bound; the false EOF comment is replaced |
| `internal/vendors/jsonl_discover_test.go`            | `TestDiscoverJSONL_readFailureIsNotCached`, `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile`, `TestDiscoverJSONL_partialReadNeverPersistsACount`, `TestDiscoverJSONL_oversizedLineRowStaysCacheable` |
| `internal/chat/foreign_index.go`                     | `Locate` ranks candidates in walk order; its doc comment states the real tie-break                                            |
| `internal/chat/foreign_index_test.go`                | `TestForeignIndex_locateFollowsWalkOrder`, `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs`                           |
| `internal/chat/cmd.go`                               | The factory is no longer invoked in `setChatQuerier`; the handler receives it and resolves it lazily behind a once-guard      |
| `internal/chat/handler.go`                           | The `foreignCache` field is resolved at the point of consultation                                                             |
| `internal/chat/handler_list_chat.go`                 | The listing and continue paths resolve the cache through the guard                                                            |
| `internal/chat/cmd_test.go`                          | `TestChatCommand_onlyConsultingVerbsConstructTheIndex`, `TestChatCommand_lazyFactoryRunsAtMostOnce`, `TestChatCommand_lazyFactoryFailureLeavesCacheUnset` |
| `main_foreign_cache_test.go`                         | The zero-construction assertion is extended to the non-consulting chat verbs; see *Amends earlier phases*                     |
| `internal/vendors/source.go`                         | The `SourceRow` and `SourceReader` doc comments state the shipped rule (`R2-57`)                                              |
| `internal/vendors/anthropic/source_reader.go`        | The "bounded" sentence replaced (`R2-57`)                                                                                    |
| `internal/vendors/pi/source_reader.go`               | The same                                                                                                                     |
| `architecture/continue-from-claudex.md`              | The promoted rule with D28's distinction (`R2-56`); the conformance clause qualified and the canonical-spelling sentence added to *Implementing a new JSONL source* (`R2-55`) |
| `README.md`                                          | The `SourceCache` block (`R2-04`), the prefilter invariant's qualification (`R2-55`), the `ReadMaxToken` coupling (`R2-51`)   |
| `phase-3-parallel-exact.md`                          | The cancellation contract row restated or annotated (`R2-52`)                                                                |

## Integration contract

| Trigger                                                                                                    | Collaborators                                              | Observable result                                                    | Required side effects                                                | Prohibited side effects                                                  |
| -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------ | ------------------------------------------------------------------------ | ------------------------------------------------------------------------ | ---------------------------------------------------------------------------- |
| A session file opens and then fails to read during a cold listing, then reads normally again with its size and mod time unchanged | generated corpus, an `fs.FS` whose `Read` fails, live `ForeignIndex` | The second listing shows the row with its true count            | The file is scanned again on the second run                            | No row written for it on the first run; no listing failure; no count persisted from the partial read |
| A session of forty messages read-fails after two                                                            | the same                                                    | A cache-less rescan reports `40`                                     | Nothing stored for that file                                           | No cached `MessageCount` of `2`                                             |
| A file whose first oversized line ends its scan, listed twice                                               | generated corpus, live `ForeignIndex`                       | Both listings show the same truncated count                          | The truncated row is stored and is a hit on the second run             | No rescan on the second run; no divergence from a cache-less run            |
| Two files carry the same source and session identifier in **sibling** directories whose names differ by a suffix | warm `ForeignIndex`, `anthropic.SourceReader.Read`      | Continuing opens the file the walk reaches first                     | One stat to confirm the cached path                                    | No disagreement with a cache-less run; no dependence on cache warmth        |
| `clai c dirv2` runs with a populated cache directory                                                        | the real command map and dispatcher, a populated cache dir  | The verb's own output                                                | None                                                                   | No `ForeignIndex` constructed; no read of the cache file                    |
| `clai c help` and `clai c delete` run                                                                       | the same                                                    | The verb's own output                                                | None                                                                   | The same                                                                     |
| `clai c list` runs                                                                                          | the same, plus a generated corpus                           | Every row listed                                                     | Exactly one construction, one cache read and one cache write           | No second construction                                                       |
| The lazy construction fails when a consulting verb runs                                                     | a failing constructor at the composition root               | Every row listed, from a full scan                                   | None                                                                   | No typed nil reaching `DiscoverJSONL`; no panic; nothing on stderr for a raw run |

## Acceptance criteria

| Outcome                                                                                              | Test or command                                                                                                |
| -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| A read failure is never written to the cache                                                          | `TestDiscoverJSONL_readFailureIsNotCached`                                                                      |
| Deleting the cache changes no row, over a corpus containing a file that opens and cannot be read       | `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile`                                                            |
| No count is ever persisted from a partial read                                                        | `TestDiscoverJSONL_partialReadNeverPersistsACount`                                                              |
| An oversized line still truncates, and its row is still cached and still a hit                        | `TestDiscoverJSONL_oversizedLineRowStaysCacheable`                                                              |
| `Locate` answers with the file the walk reaches first, across sibling directories                     | `TestForeignIndex_locateFollowsWalkOrder`, `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs`              |
| A chat verb that never consults the cache constructs no index                                         | `TestChatCommand_onlyConsultingVerbsConstructTheIndex`                                                           |
| A consulting verb constructs the index at most once                                                   | `TestChatCommand_lazyFactoryRunsAtMostOnce`                                                                      |
| A failed lazy construction leaves a nil interface and the listing still works                         | `TestChatCommand_lazyFactoryFailureLeavesCacheUnset`                                                             |
| The amended phase-five assertions hold under the new contract                                         | The two tests named in *Amends earlier phases*, run unedited after amendment                                     |
| The README `SourceCache` block matches the shipped interface                                          | Read the block against `internal/vendors/source.go`; the three method names agree                                |
| No stale doc comment survives in the rewritten files                                                  | `grep -n 'may be approximate\|no full body parsing\|discovery is bounded' internal/vendors/source.go internal/vendors/anthropic/source_reader.go internal/vendors/pi/source_reader.go` returns nothing |
| The promoted rule and D28's distinction are in the architecture tree                                  | `grep -n 'not a fact about the file' architecture/continue-from-claudex.md` returns the rule and its refinement  |
| A new vendor author reads the canonical-spelling assumption where they write a `MayContribute`         | `grep -n 'canonically spelled' architecture/continue-from-claudex.md` returns a line in *Implementing a new JSONL source* |
| The conformance claim carries its qualification                                                       | `grep -n 'fails on exactly that mistake' architecture/continue-from-claudex.md` returns the qualified sentence   |
| The token-bound coupling is stated beside both parameter rows                                         | `grep -n 'foreign-index-version' README.md` names both the `vendors.ReadMaxToken` row and the version row        |
| The cancellation contract rows no longer contradict each other                                        | `grep -n 'cancel' phase-3-parallel-exact.md phase-5-review-1-fixes.md` shows one rule, stated or cross-referenced |
| Readiness checklist items `1`, `2`, `2a` and `6` pass from the worklog directory                      | The four commands in the README checklist                                                                        |
| Every gate passes unedited                                                                            | `make qa`; `go test ./... -race -cover -count=3 -timeout=30s`                                                    |
| Coverage of the touched packages stays at or above the floor                                          | `go test ./internal/vendors/... ./internal/chat/ . -cover`                                                        |
| The cold benchmark stays under the ceiling                                                            | The phase-zero benchmark command at `benchmark-invocations`, compared against `benchmark-regression-band`         |

## Error coverage

| Failure                                                                    | Expected outcome                                                                                  | Test                                                        |
| ------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| The scanner returns a read error partway through a walked file               | The row is skipped for this run and nothing is cached; the next run retries the file                 | `TestDiscoverJSONL_readFailureIsNotCached`                   |
| The scanner returns `bufio.ErrTooLong`                                       | The truncated row is cached exactly as before, and is a hit next run                                 | `TestDiscoverJSONL_oversizedLineRowStaysCacheable`           |
| `OpenAbs` fails for a walked file                                            | Unchanged from phase five: skipped, nothing cached                                                   | `TestDiscoverJSONL_readFailureIsNotCached` subtest            |
| The scan completes and finds no session identity                             | Unchanged: the zero row is cached                                                                    | `TestDiscoverJSONL_partialReadNeverPersistsACount` subtest    |
| Two cached rows share an identity across sibling directories                 | The walk-order winner is returned, and it is the file a cache-less run opens                         | `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs`      |
| The lazy construction fails on a consulting verb                             | The field stays a nil interface, the listing scans, and nothing panics                               | `TestChatCommand_lazyFactoryFailureLeavesCacheUnset`          |
| A consulting verb resolves the cache more than once                          | The second resolution returns the first result; the constructor runs once                            | `TestChatCommand_lazyFactoryRunsAtMostOnce`                   |
| A non-consulting verb runs with a populated cache directory                  | Nothing is constructed and the cache file is never opened                                            | `TestChatCommand_onlyConsultingVerbsConstructTheIndex`        |
| A vendor spells a JSON key non-canonically                                   | Out of contract by D26, now documented where a vendor author writes the prefilter                    | The architecture grep in the acceptance table                 |

## Implementation notes

`clai`, `2026-09-16`. Deltas only.

**The blocker's fix is three lines and one of them is the whole decision.**
`scanJSONLRawLines` already returned the scanner's error; `scanJSONLFileRow`
now names it `scanErr` and returns it on both of its success paths, including
the identity-less one, and `discoverJSONLFile` gates `Store` on
`err == nil || errors.Is(err, bufio.ErrTooLong)`. The negative-caching branch,
the hit path, the stat order and the row contents are untouched: a file skipped
for a read failure is simply rescanned next run, exactly as an unopenable one
is. The false in-code comment — "the scan reads to EOF, so every counted role
is counted" — is replaced by the rule it violated.

**`bufio.ErrTooLong` is not wrapped, but `errors.Is` is still the right check.**
`bufio.Scanner.Err` returns the sentinel itself, so `==` would work today. The
identity comparison would silently stop working if a future scan wrapped it,
and the specification states the check as `errors.Is`.

**`Locate`'s comparator is extracted rather than inlined.** `compareWalkOrder`
splits both paths on the separator and compares segment by segment, falling
back to the segment count. The tail rule is unreachable through a walk — a
path cannot be both a file and a directory — so it is pinned by a direct
subtest instead, because a comparator that is not a total order is unsound
wherever it is used next.

**A third phase-five test had to be amended, and the specification does not
name it.** `TestChatCommand_cacheFactoryRunsOncePerVerb` and
`TestCommands_buildConstructsNoForeignIndex` are the two listed in *Amends
earlier phases*, and both were amended as written. But
`TestChatCommand_failedFactoryLeavesCacheNil`, which the README names as one of
the three tests closing `R1-02`, asserts *the factory ran once* immediately
after `newChatQuerier` — the exact behaviour D27 removes — so it fails on the
first line of the lazy wiring. It was amended in place, where it lives, to the
same shape: nothing is built at construction, the first consultation builds
once, and a failure leaves a nil interface. This phase's
`TestChatCommand_lazyFactoryFailureLeavesCacheUnset` was then narrowed to what
only it owns — a failed construction is **memoised, not retried**, so three
consultations run the failing factory once and the listing scans every time —
rather than restating the phase-five assertions and giving `dupl` a clone.
Reported, not silently improved: see *What the specification got wrong*.

**`TestChatCommand_cacheFactoryRunsOncePerVerb` needed a second half, not just
a narrowing.** Under D27 a consulting verb constructs nothing during `Setup`,
so "asks for exactly one" is no longer observable there at all. The verb loop
now asserts zero at `Setup` for `continue` and `list`, and a sibling subtest
drives each of those verbs to completion through `Command` → `Setup` → `Run`
and asserts exactly one. Driving them is what makes the assertion real: both
reach the consultation, `continue` through its fall-back to the listing.

**The lazy resolution lives on the handler, not on the command adapter.**
`newChatQuerier` now only carries the factory across. `ChatHandler` gained
`newForeignCache` and a `sync.Once`, and `foreignCacheOrNil` is called by
`readForeignChat` and once at the top of `foreignChatRows`.
`persistForeignCache` deliberately reads the resolved field rather than the
resolver, so a verb that built no index cannot be made to build one by the
write half. Tests that inject a cache by assigning the field still work
untouched, because a nil factory leaves the `Once` a no-op.

**The handler's non-nil guard is still not the mechanism, and the mutation
table says so.** Assigning the factory's result unconditionally is not caught
by any test, and cannot be: a factory that fails returns a genuinely nil
interface, so the guard is a no-op, and a factory that returns a typed nil
defeats the guard anyway because such an interface is not nil. That obligation
sits at the composition root, where a mutation that ignores the constructor
error does fail `TestCommands_buildConstructsNoForeignIndex`. This reproduces
phase five's finding under the new wiring rather than contradicting it.

**Four of the documentation findings were already landed by the review's own
change** and needed verification, not editing: the README `SourceCache` block
already carries `Locate` with its walk-order tie-break (`R2-04`), the prefilter
invariant already carries its fixture-corpus qualification and both parameter
rows already carry the `ReadMaxToken` coupling sentence (`R2-55`, `R2-51`).
Each is recorded against its acceptance command below.

### Every fix was proved by a failing test first

| Finding | The test, run against the shipped code | Outcome |
| --------- | ---------------------------------------- | --------- |
| `R2-01` | `TestDiscoverJSONL_readFailureIsNotCached` | failed: `the read-failing file was cached as {... MessageCount:0 ...}` — the vanishing-row scenario, cached under the pre-read stat |
| `R2-01` | `TestDiscoverJSONL_partialReadNeverPersistsACount` | failed: the partial row was returned and stored at `MessageCount` `2` against a true `40` |
| `R2-01` | `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile` | failed: after the filesystem healed, the cached run returned two rows where the cache-less run returned three |
| `R2-01` | `TestDiscoverJSONL_oversizedLineRowStaysCacheable` | **passed** before the fix, by design: it is the tripwire against a blanket skip, not a reproduction |
| `R2-02` | `TestForeignIndex_locateFollowsWalkOrder` | failed on the first round: `Locate` answered with the file under `proj-bak` while the walk reaches `proj` first |
| `R2-02` | `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs` | failed through `anthropic.SourceReader.Read`: the warm index opened the backup's transcript and the cache-less walk opened the project's |
| `R2-03` | `TestChatCommand_onlyConsultingVerbsConstructTheIndex` | failed for `help`, `dir`, `dirv2` and `delete`: each constructed one index |
| `R2-03` | `TestChatCommand_lazyFactoryRunsAtMostOnce` | failed: "building the handler constructed `1` indexes" |
| `R2-03` | `TestChatCommand_lazyFactoryFailureLeavesCacheUnset` | failed: "building the handler asked for `1` indexes" |
| `R2-03` | `TestChatCommand_failedFactoryLeavesCacheNil`, amended | failed against the eager wiring on its own amended first assertion, before the once-guard existed |

### Mutation table

Each fix was reverted in place after it landed. Every mutation was reverted
before the gates were run.

| Mutation | Caught by |
| ---------- | ----------- |
| `discoverJSONLFile` skips `Store` on any scan error (the blanket fix D28 forbids) | `TestDiscoverJSONL_oversizedLineTruncatesScan` and `TestDiscoverJSONL_oversizedLineRowStaysCacheable` — the phase-three tripwire fires too, which is why it is listed as one |
| `scanJSONLFileRow` discards the scanner error again | `TestDiscoverJSONL_readFailureIsNotCached`, `TestDiscoverJSONL_partialReadNeverPersistsACount`, `TestDiscoverJSONL_cacheAgnosticWithReadFailingFile` |
| `Locate` keeps the lexically smallest full path | `TestForeignIndex_locateFollowsWalkOrder`, `TestForeignIndex_locateAgreesWithWalkAcrossSiblingDirs` |
| `foreignCacheOrNil` resolves without the once-guard | `TestChatCommand_lazyFactoryRunsAtMostOnce` and `TestChatCommand_lazyFactoryFailureLeavesCacheUnset` — the second pins the failing-construction half, which the first cannot see |
| `newChatQuerier` resolves the cache eagerly again | `TestChatCommand_cacheFactoryRunsOncePerVerb`, `TestChatCommand_onlyConsultingVerbsConstructTheIndex`, both lazy tests, and `TestCommands_buildConstructsNoForeignIndex` |
| the composition root lets a typed nil escape | the typed-nil subtest of `TestCommands_buildConstructsNoForeignIndex` |
| `foreignCacheOrNil` assigns the factory result without its non-nil guard | **nothing** — see the note above; the guard is not the mechanism and cannot be |

### Verification

| Command | Result |
| --------- | -------- |
| `make qa` | exit `0` at host load `1.39`; every package `ok` |
| `go test ./... -race -cover -count=3 -timeout=30s` | all green, run twice inside `make qa` at host loads `1.39` and `2.2`; neither recorded flake reproduced |
| `go run mvdan.cc/gofumpt@latest -w -l .` | no output |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | no output |
| `go vet ./...`, `go fix ./...` | no output |
| `go run github.com/mibk/dupl@latest -t 80 .` | `34` clone groups, the same count the review recorded; none names `jsonl_discover.go`, `foreign_index.go`, `cmd_test.go` or `main_foreign_cache_test.go` |
| `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x` | `32569032` ns/op, `24345118` B/op, `428058` allocs/op at load `3.79` — within the band of the shipped row `31032276` and far under the historical ceiling `107418967` |
| `go test ./internal/vendors/... ./internal/chat/ . -cover` | `internal/chat` `78.9`, `anthropic` `76.5`, `pi` `89.9`, `jsonltest` `93.3`, root `95.6` percent — `internal/chat` up from review two's `78`, every other figure unchanged. `internal/vendors` is **not a stable figure**: see the row below |
| `go test ./internal/vendors/ -cover -count=1`, twelve times | `70.9` six times and `70.6` or `70.7` six times. The package's own coverage oscillates because `scanJSONLFileSlots`'s cancellation branch — `break feed`, reached only when the feeder loses the race to the cancel — is covered or not per run. This is phase-three code that no phase since has touched, and it is why phase five recorded `70.9` while both review-two reviewers measured `70.7` (`R2-58`): neither was wrong, the figure is not deterministic. A future round should quote the range |
| `go tool cover -func` over the touched code | `discoverJSONLFile`, `scanJSONLFileRow`, `scanJSONLRawLines`, `foreignCacheOrNil`, `compareWalkOrder`, `readForeignChat` and `persistForeignCache` at `100` percent; `Locate` `90.9`; `newChatQuerier` `80` |
| `grep -n 'may be approximate\|no full body parsing\|discovery is bounded' internal/vendors/source.go internal/vendors/anthropic/source_reader.go internal/vendors/pi/source_reader.go` | no output (`R2-57`) |
| `grep -n 'not a fact about the file' architecture/continue-from-claudex.md` | the section heading and the D28 refinement sentence (`R2-56`) |
| `grep -n 'canonically spelled' architecture/continue-from-claudex.md` | one line inside *Implementing a new JSONL source* (`R2-55`) |
| `grep -n 'fails on exactly that mistake' architecture/continue-from-claudex.md` | the sentence, now carrying its qualification (`R2-55`) |
| `grep -n 'foreign-index-version' README.md` | the `vendors.ReadMaxToken` row and the version row, both carrying the coupling (`R2-51`) — already landed by the review's own change |
| the README `SourceCache` block against `internal/vendors/source.go` | three method names, `Lookup`, `Store` and `Locate`, in both (`R2-04`) — already landed by the review's own change |
| `grep -n 'cancel' phase-3-parallel-exact.md phase-5-review-1-fixes.md` | one rule: the phase-three row now states the real side effect and points at phase five's (`R2-52`) |
| readiness checklist items `1`, `2`, `2a` and `6` | run from the worklog directory after every edit; all four pass. The outcomes are recorded in the README session journal's entry for this phase, not below this table — the row said "recorded below" and nothing below recorded them (`R3-58`) |

### What the specification got wrong

| Where | What it says | What is actually true |
| ------- | -------------- | ----------------------- |
| *Amends earlier phases* | Names two phase-five tests to amend | A third, `TestChatCommand_failedFactoryLeavesCacheNil`, also asserts the eager construction D27 removes and fails without an amendment. It was amended where it lives, and this phase's own failure test narrowed to the memoisation property so the two do not overlap |
| *Amends earlier phases* | `TestChatCommand_cacheFactoryRunsOncePerVerb` is "restricted to the consulting verbs" | Restricting it is not enough: under D27 those verbs construct nothing at `Setup` either, so the surviving assertion has to move to a run of the verb |
| The `R2-04`, `R2-55` and `R2-51` subsections | Written as work this phase performs on the README | The review's own change had already made all three README edits. Verified against their acceptance commands rather than repeated |

## Review findings

### Review 3 (`2026-09-16`)

One note of this phase's own against the decision it introduced, and two notes
of worklog hygiene. `R3-03` is routed to the addendum,
`phase-7-review-3-fixes.md`, and carries a new maintainer decision, D29; the
other two are closed in the review's own change. This phase is not reopened. The
round's verdict is **ready**, and both reviewers record that this phase's work
is complete and correct: `R2-01` is closed as a **class**, not only as an
instance.

| ID       | Severity | Where                                                                   | Finding                                                                 |
| -------- | -------- | ------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| `R3-03`  | note     | `internal/vendors/jsonl_discover.go` against both vendors' `Read`        | A D28-cached oversized-line row is listed but can never be continued — D29 |
| `R3-54`  | note     | README, *Invariants*, the D27 row                                        | The row misdescribes which verbs `readOnlyChatSetup` serves              |
| `R3-58`  | note     | README status board, phase `3`'s row; this file's *Verification* table    | Two board and pointer mismatches                                        |

- [x] `R3-03` — a session file containing a line above `vendors.ReadMaxToken`
  discovers a truncated row, which D28 correctly calls a fact about the file and
  caches. Selecting that row runs `Read`, which routes the same
  `bufio.ErrTooLong` through `fmt.Errorf("scan jsonl %q: %w", ...)` and fails the
  verb with a raw scanner message. Because the row is now cached, the dead-end
  row is permanent until the bytes change; before D28 the deleted line cap meant
  discovery never formed an opinion about such a file. Pre-existing on the `Read`
  side at `HEAD`, and surfaced here because D28 is what makes it durable.
  **Maintainer decision, recorded as D29.** `Read` must **not** tolerate
  `bufio.ErrTooLong`: silently feeding a truncated conversation to a model is
  worse than failing, and it would contradict D1's "fix the field, do not drop
  it" stance. Nor does the listing gain a marker column — that is scope this
  worklog does not own. The proportionate fix is an **intelligible error**
  naming the cause and the bound, so the dead end is explained rather than
  cryptic, and the residual — a listable, non-continuable row — is recorded in
  the README as a known limitation with its cause, so a future effort can decide
  about a marker with the facts in hand. The error message is the addendum's
  work.
- [x] `R3-54` — the D27 invariant row said the factory is not invoked by
  "`help`, `dir`, `dirv2` or `delete`, which `readOnlyChatSetup` calls
  shell-prompt hot paths". Per `internal/chat/cmd.go`, `readOnlyChatSetup`
  serves `list`, `dir`, `dirv2` and `help`; `delete` goes through
  `fullChatSetup`, and `list` — which **is** served by `readOnlyChatSetup` — is a
  consulting verb. D27's own decision entry states both facts correctly; only
  the compressed invariant row did not. Restated in the review's own change.
- [x] `R3-58` — two pointers that do not resolve. The README status board said
  review two routed `R2-52` and `R2-55` from phase `3` to this phase, while
  phase `3`'s own review-two table carries four findings: `R2-52`, `R2-01`,
  `R2-51` and `R2-55`. Separately, this file's *Verification* table said the
  readiness-checklist outcomes are "recorded below" and nothing below recorded
  them — they are in the README session journal's entry for this phase. Both
  corrected in the review's own change.

**Verified good in this phase.** `R2-01` is closed as a class, verified four
ways: `scanErr` is returned on all three exits of `scanJSONLFileRow`, including
the identity-less one; there is exactly one `cache.Store` call site with the D28
gate dominating it; the gate sits **before** the `cacheable` check, so nil-cache
and warm-cache runs agree by construction rather than by the cache merely being
safe; no other value entering a row is run-scoped, since `Fields`, the prefilter
and the `ModTime` fallback are all pure functions of the bytes plus the key; and
`bufio.ErrTooLong` cannot carry or be masked by a transport error, because
`bufio.Scanner.setErr` is first-write-wins and `Scan` returns immediately, so
`Err()` yields the bare sentinel. Both directions of D28 are pinned by mutation.
`compareWalkOrder` is a faithful `WalkDir` oracle over sixty random trees with
adversarial sibling names at every depth and a total order over twenty
adversarial paths, with the unreachable segment-count tail rule pinned by a
direct subtest. Lazy resolution is race-free: sixty-four goroutines build exactly
one index, all observe the same non-nil interface, a panicking factory does not
deadlock later askers, and the memoised failure does not retry. No test of this
phase is vacuous — all ten fail under targeted mutation, and this file's own
mutation table reproduces exactly.
