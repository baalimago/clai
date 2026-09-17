# Phase 7 — Review three fixes

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Close the findings review three routed to an addendum: stop the identity scan
from answering an unreadable file the way it answers a file with no identity,
pin `persistForeignCache`'s access to the resolved cache instead of asserting it
in prose, make the token-bound dead end explain itself (D29), and carry two
already-corrected claims into the copies that still contradict them.

## Specification

This phase is a third addendum on the rationale phases five and six used: it
re-opens no phase file and introduces no design the README does not already
carry. Its reading contract is the README and this file. The findings it closes
are listed in the README feedback index under **Review three**; each subsection
below names the ones it closes.

**It closes no blocker and no major, because review three returned `ready`.**
Both reviewers state the work can ship. This phase exists because three of its
findings are code plus tests, and a finding that is code plus tests wants a
contract, an acceptance row and evidence — which is what a phase is. One
reviewer proposed a plain documentation change instead; that reading is fair for
the two documentation findings and wrong for the three code ones.

Two of the five findings — `R3-52` and `R3-56` — are instances of the Strategy
rule *A corrected claim is not corrected until every copy of it is*. This phase
is therefore also the first to work under that rule, and it walks the five copies
for every claim it touches before it writes its verification table.

### The identity scan must not answer an unreadable file (`R3-01`, minor)

`jsonlFileIdentity` in `internal/vendors/jsonl_discover.go` calls
`scanJSONLRawLines` with `_ =` and returns `""` on any failure. A read that
fails before the file's first identity line is therefore indistinguishable from
a file that names no session, and `FindJSONLSession`'s walk simply moves on.

The consequence is the one the README invariant *lookup and discovery never
disagree about which file holds a session, cache or no cache* forbids, reached
through the discarded error instead of through the ordering `R2-02` fixed. Two
files carry session `S`: `proj/s.jsonl`, which the walk reaches first, and
`proj-bak/s.jsonl`, the sibling-directory fixture `R2-02` introduced. A read
failure on the walk-first file makes the walk answer `proj-bak/s.jsonl`, while a
warm cache answers `proj/s.jsonl` through `Locate`. `clai chat continue` then
opens a different transcript depending on cache warmth — the same user-visible
defect `R2-02` was raised for. With no duplicate present the user is told the
session was not found instead of being told about the transport error.

This is **not a regression** — `HEAD` discards the same error in the same place
— and **not a caching violation**, because nothing is stored. It is the last
instance in this file of the pattern `R2-01` was about: the Strategy rule
applied to an *answer* rather than to durable state. And the file is
inconsistent with itself, because both vendors' `Read` already treats the same
error as fatal.

**The fix propagates.** `jsonlFileIdentity` returns the scanner's error
alongside the identity, and `FindJSONLSession` distinguishes "this file names no
session" from "this file could not be read": the first is a skip, as today, and
the second fails the lookup with the transport error rather than walking on to a
file the walk would not have chosen. Nothing about a healthy walk changes, and
the cached path in `cachedSessionPath` is untouched.

The fixture is a duplicate identity in sibling directories plus a read failure
on the walk-first file, built with the existing `failReadFS` recipe.

### `persistForeignCache`'s call contract becomes a mechanism (`R3-02`, note)

`persistForeignCache` in `internal/chat/handler_list_chat.go` reads
`cq.foreignCache` directly, and its doc comment advertises "it reads the
resolved field rather than the resolver" as a deliberate safety property. It is
safe only because the single call site is a `defer` on the goroutine that just
resolved the cache. A reviewer proved the hazard with a probe: one goroutine
calling `foreignCacheOrNil()` while another calls `persistForeignCache()` is
reported by `-race` as a write/read on `ChatHandler.foreignCache`, the write
being inside `sync.Once.doSlow`.

Nothing pins either side of this. Mutating `persistForeignCache` to go through
`foreignCacheOrNil()` breaks no test, so neither the current property nor its
replacement is guarded, and a future `defer cq.persistForeignCache()` at a
higher level — or the same call from a worker — would introduce a real race no
existing test would catch.

**The fix prefers the accessor over the sentence.** A non-constructing guarded
accessor reads the resolved field under the same guard the resolver writes it
under, and returns nil when nothing has been resolved. `persistForeignCache`
goes through it. The doc comment then states a property the code holds rather
than a contract a caller must remember, which is the point: a stated contract is
one more copy of a claim that can rot, and the Strategy rule this round promoted
is about exactly that. If the accessor cannot be had for free, the fallback is
to state the contract explicitly on `persistForeignCache` — but the accessor is
the preferred outcome and the specification is written for it.

Two properties must hold after the change and both are tested. The accessor
**never constructs**: a handler that has resolved nothing persists nothing and
asks the factory for nothing, which is what the doc comment already promises and
what D27 exists to protect. And concurrent resolution and persistence are clean
under `-race`.

### The token-bound dead end explains itself (`R3-03`, note; D29)

A session file holding a line above `vendors.ReadMaxToken` discovers a truncated
row, which D28 correctly calls a fact about the file and caches. Selecting that
row runs `Read`, which routes the same `bufio.ErrTooLong` through its
`scan jsonl` wrap and fails the verb with a raw scanner message. Because the row
is now cached, the dead end is permanent until the bytes change; before D28 the
line cap phase three deleted meant discovery never formed an opinion about such
a file at all.

**D29 is the governing decision and the executor must not shortcut it.** `Read`
must **not** tolerate the truncation: silently feeding a shortened conversation
to a model is worse than failing, and it would contradict D1's "fix the field,
do not drop it" stance. The listing does **not** gain a marker column: that is
scope this worklog does not own. What is owed is an **intelligible error** —
one naming the cause and the bound, so the dead end is explained rather than
cryptic — and a **recorded residual**, which the README now carries as a known
limitation beside the rule that produces it.

**The message is generic, not vendor-specific.** `ScanJSONLLines` in
`internal/vendors/jsonl.go` is the one function both vendors' `Read` scans
through, and `ReadMaxToken` is a generic constant, so the wrap belongs there and
not in either vendor package. The wrap must keep `errors.Is(err, bufio.ErrTooLong)`
true, because discovery's D28 gate is that exact call: an opaque replacement
would silently revert the oversized-line row to uncacheable with no test
failing. Discovery reads through `scanJSONLRawLines` and is untouched either
way, and phase three's and phase six's oversized-line tests are the tripwires
that say so.

Each vendor's `Read` keeps its existing `scan jsonl` wrap unchanged and inherits
the message through it.

### The conformance overstatement's three surviving copies (`R3-52`, minor)

`R2-55` qualified the conformance claim in exactly two documents. Three others
still carry it unqualified, and the earliest of the three is the one a vendor
author reads first:

| Location                                       | What it says now                                                                     | Why the qualification is owed                                                                 |
| ------------------------------------------------ | -------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| `internal/vendors/schema.go`, the `LinePrefilter` doc comment | The suite "fails on exactly that mistake"                              | This is the interface doc a vendor author reads **before** writing a `MayContribute`, earlier than the architecture document that was corrected |
| `internal/vendors/jsonltest/conformance.go`    | "a wrong prefilter is a test failure rather than a quiet loss"                        | D26's own wording required the assumption to live in the suite's documentation; the file documents the canonical-spelling half and not the resolution half |
| `phase-3-parallel-exact.md`, the prefilter invariant row | Unscoped                                                                   | The README's copy of the same row is scoped; the phase's is not                                |

Unmet clauses: `R2-55` itself — five of the six anthropic markers can be deleted
**individually** without failing the suite, because every generated Claude line
carries the session-identifier key — plus D7's stated purpose, that the cost rule
lives in code instead of prose, and `R2-57`'s upgrade rationale, which is that a
stale comment in the file a vendor author opens first is the failure D7 exists to
remove.

The consequence is concrete. A future vendor author writes a marker set narrower
than its `Fields`, the corpus happens to carry another marker on every line, the
suite passes, and discovery silently undercounts: D1's defect reached through
the documentation route D7 exists to close.

**All three are qualified, not rewritten**, in the words the two corrected
documents already use: the suite fails on a narrowing the fixture corpus can
witness, and a marker every fixture line carries can be dropped without it
noticing. The suite is worth keeping and the claim is true of the mistake it was
built for.

### "Lexical order" in the file a contributor edits (`R3-56`, note)

`discoverSlot`'s and `walkJSONLPaths`'s doc comments in
`internal/vendors/jsonl_discover.go`, and `phase-3-parallel-exact.md`'s
discovery-steps table, all still say the walk's "lexical order". `R2-02`
established that the ordering is per-directory entry order and explicitly **not**
whole-path lexical order; the README invariant and
`internal/chat/foreign_index.go` were corrected, these three were not.

This is the exact phrasing that produced `R1-05`'s wrong mechanism and then
`R2-02`'s wrong implementation, left standing in the file a contributor edits.
A reviewer also found and corrected the README's parallel-discovery invariant
row carrying the same phrase while filing round two, which makes this the same
claim's fourth copy and the reason the Strategy rule was promoted.

The replacement says what `compareWalkOrder`'s own doc comment already says:
`filepath.WalkDir` sorts the entries of each directory by name and descends, so
the order is per-directory entry order, segment by segment, and not whole-path
byte order.

### Amends earlier phases

This subsection names tests and rows other phases own. It is the one place in
this file that does, and readiness-checklist item `2` skips it for that reason.
Edit them where they live; do not copy them here.

| Owner                                 | Test or row                                        | Amendment                                                                                     |
| --------------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `phase-3-parallel-exact.md`           | `TestDiscoverJSONL_oversizedLineTruncatesScan`      | Must still pass **unedited**: D29 changes only the `Read` path's message, so a discovery behaviour change here means the wrap leaked into `scanJSONLRawLines` |
| `phase-6-review-2-fixes.md`           | `TestDiscoverJSONL_oversizedLineRowStaysCacheable`  | Must still pass **unedited**: it is the D28 tripwire, and it fails if the new message stops satisfying `errors.Is(err, bufio.ErrTooLong)` |
| `phase-2-foreign-index.md`            | `TestForeignChatRows_singleCacheWrite`              | Must still pass **unedited**: the guarded accessor must not change how many times the cache is written |
| `phase-1-generic-jsonl-discovery.md`  | `TestFindJSONLSession_notFound`                     | Must still pass **unedited**: a readable file with no identity is still a skip, and a walk that finds nothing is still "not found" |
| `phase-3-parallel-exact.md`           | Its prefilter invariant row, and its discovery-steps table row | Scoped to the fixture corpus (`R3-52`); the steps row states per-directory entry order (`R3-56`) |

### Invariants

| Invariant                                                                                   | Mechanism                                                                                                      | Test                                                            |
| --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------ |
| A file the filesystem refused to **read** is never reported as a file that names no session  | `jsonlFileIdentity` returns the scanner's error; `FindJSONLSession` fails the lookup instead of walking on         | `TestFindJSONLSession_readFailureIsNotAMissingSession`           |
| Lookup never answers with a duplicate the walk would not have chosen because of a read failure | The propagated error stops the walk before it reaches the sibling candidate                                     | `TestFindJSONLSession_readFailureNeverAnswersWithASibling`       |
| Persisting the foreign cache never constructs one                                            | `persistForeignCache` reads the resolved value through a non-constructing guarded accessor, never the resolver     | `TestChatHandler_persistForeignCacheDoesNotConstruct`            |
| Resolving and persisting the foreign cache concurrently is race-free                         | The accessor reads the field under the guard the resolver writes it under                                         | `TestChatHandler_persistForeignCacheRacesResolution` under `-race` |
| A scan ended by the token bound fails with an error naming the cause and the bound            | `ScanJSONLLines` wraps `bufio.ErrTooLong` once, generically, and both vendors' `Read` inherit it through their existing wrap | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`  |
| The intelligible error stays the same sentinel to `errors.Is`                                 | The wrap uses `%w`, so D28's discovery gate is unaffected                                                         | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`, plus the two tripwires in *Amends earlier phases* |
| Continuing a token-bound session tells the user why                                           | The vendor's `Read` surfaces the generic message through its own wrap                                             | `TestSourceReaderRead_tokenBoundErrorIsIntelligible`, `TestPiSourceReaderRead_tokenBoundErrorIsIntelligible` |

### Parameters this phase owns

Values live in the README parameters table and are not restated here.

| Parameter                                                                          | Why this phase owns it                                                    |
| ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------- |
| `jsonlFileIdentity`'s propagated scanner error                                       | `R3-01`: it is what lets `FindJSONLSession` tell the two facts apart        |
| `ChatHandler.persistForeignCache`'s guarded, non-constructing accessor                | `R3-02`: the mechanism replaces the stated call contract                    |
| The token-bound read failure's user-facing message (D29)                              | `R3-03`: D29 chooses an intelligible error over tolerance and over a marker |

### Files

| File                                                | Change                                                                                                                  |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `internal/vendors/jsonl_discover.go`                 | `jsonlFileIdentity` returns its scanner error; `FindJSONLSession` distinguishes the two facts (`R3-01`); `discoverSlot` and `walkJSONLPaths` say per-directory entry order (`R3-56`) |
| `internal/vendors/jsonl_discover_test.go`            | `TestFindJSONLSession_readFailureIsNotAMissingSession`, `TestFindJSONLSession_readFailureNeverAnswersWithASibling`         |
| `internal/vendors/jsonl.go`                          | `ScanJSONLLines` wraps the token-bound error with the cause and the bound, keeping the sentinel (D29)                     |
| `internal/vendors/jsonl_test.go`                     | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`                                                                    |
| `internal/vendors/schema.go`                         | The `LinePrefilter` doc comment carries the conformance qualification (`R3-52`)                                            |
| `internal/vendors/jsonltest/conformance.go`          | The header carries the resolution half of the qualification (`R3-52`)                                                     |
| `internal/vendors/anthropic/source_reader_test.go`   | `TestSourceReaderRead_tokenBoundErrorIsIntelligible`                                                                       |
| `internal/vendors/pi/source_reader_test.go`          | `TestPiSourceReaderRead_tokenBoundErrorIsIntelligible`                                                                     |
| `internal/chat/handler_list_chat.go`                 | `persistForeignCache` reads through a non-constructing guarded accessor; its doc comment states the mechanism (`R3-02`)    |
| `internal/chat/handler.go`                           | The guarded accessor beside `foreignCacheOrNil` (`R3-02`)                                                                 |
| `internal/chat/handler_list_chat_test.go`            | `TestChatHandler_persistForeignCacheDoesNotConstruct`, `TestChatHandler_persistForeignCacheRacesResolution`                |
| `phase-3-parallel-exact.md`                          | The prefilter invariant row scoped (`R3-52`); the discovery-steps row restated (`R3-56`); see *Amends earlier phases*      |

## Integration contract

| Trigger                                                                                     | Collaborators                                                        | Observable result                                                          | Required side effects                            | Prohibited side effects                                                     |
| ----------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------------ | ---------------------------------------------------- | ------------------------------------------------------------------------------- |
| Two files carry the same source and session identifier in sibling directories, and the walk-first one fails to read | generated corpus, `failReadFS`, nil cache, `anthropic.SourceReader.Read` | The lookup fails with the transport error                                 | None                                               | No answer naming the sibling; no silent fall-through to the second file        |
| The same corpus with the same read failure, but with a **warm** cache                        | the same, plus a live `ForeignIndex`                                     | The warm and cache-less runs agree — both fail, rather than one opening each file | None                                             | No disagreement with a cache-less run; no dependence on cache warmth            |
| A single session file fails to read before its first identity line                           | generated corpus, `failReadFS`, nil cache                                | The lookup reports the transport error, not "session not found"            | None                                               | No cache write; no row stored                                                   |
| A session file with no identity line at all, readable                                         | generated corpus, nil cache                                              | Unchanged: the walk skips it and reports the session not found             | None                                               | No error about reading                                                          |
| A listing runs and the handler never consulted the foreign cache                              | the real command map and dispatcher, a populated cache dir               | The verb's own output                                                      | None                                               | No index constructed by the persist; no cache file opened                       |
| A goroutine resolves the cache while another persists it                                      | a live `ForeignIndex`, two goroutines, `-race`                           | Both complete                                                              | At most one construction; at most one write        | No data race reported on `ChatHandler.foreignCache`                             |
| `clai chat continue` selects a listed row whose file holds a line above the token bound       | generated corpus with an oversized line, warm `ForeignIndex`             | The verb fails with a message naming the oversized line and the bound      | None                                               | No truncated conversation handed to a model; no raw scanner sentinel in the message; no change to the cached row |
| The same corpus listed twice                                                                  | generated corpus with an oversized line, live `ForeignIndex`             | Unchanged from phase six: both listings show the same truncated count      | The truncated row is stored and is a hit next run  | No rescan; no divergence from a cache-less run                                  |

## Acceptance criteria

| Outcome                                                                                              | Test or command                                                                                                |
| -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| A read failure is never reported as a file that names no session                                      | `TestFindJSONLSession_readFailureIsNotAMissingSession`                                                          |
| A read failure never makes the lookup answer with a sibling duplicate                                  | `TestFindJSONLSession_readFailureNeverAnswersWithASibling`                                                      |
| A readable file with no identity is still skipped, and the lookup still reports the session not found  | The phase-one lookup test named in *Amends earlier phases*, run unedited                                         |
| Persisting the cache constructs nothing                                                               | `TestChatHandler_persistForeignCacheDoesNotConstruct`                                                            |
| Resolution and persistence are race-free                                                              | `TestChatHandler_persistForeignCacheRacesResolution` under `-race`                                               |
| The write count per invocation is unchanged                                                           | The phase-two single-write test named in *Amends earlier phases*, run unedited                                   |
| The token-bound error names the cause and the bound, and is still the sentinel                        | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`                                                           |
| Continuing a token-bound session tells the user why, in both vendors                                  | `TestSourceReaderRead_tokenBoundErrorIsIntelligible`, `TestPiSourceReaderRead_tokenBoundErrorIsIntelligible`      |
| D28's cacheability of the token-bound row is unchanged                                                | The two oversized-line tripwires named in *Amends earlier phases*, both run unedited                             |
| The conformance claim carries its qualification wherever it is made                                   | `grep -n 'fails on exactly that mistake' internal/vendors/schema.go` and `grep -n 'quiet loss' internal/vendors/jsonltest/conformance.go` each return a sentence carrying the fixture-corpus scope |
| No copy of the conformance claim is left unqualified                                                  | The five copies walked and listed in Implementation notes: README invariant row, README parameters row, `phase-3-parallel-exact.md`'s row, the two Go doc comments, and the architecture section |
| "Lexical order" no longer describes the walk anywhere                                                 | `grep -rn 'lexical order' internal/vendors/ internal/chat/ worklogs/2026-09-16-foreign-conversation-index/` returns nothing that describes `filepath.WalkDir` |
| The known limitation is recorded with its cause                                                       | `grep -n 'listable' README.md` returns the D29 residual in *Strategy*                                            |
| Readiness checklist items `1`, `2`, `2a`, `3` and `6` pass from the worklog directory                 | The five commands in the README checklist                                                                        |
| Every gate passes unedited                                                                            | `make qa`; `go test ./... -race -cover -count=3 -timeout=30s`                                                     |
| Coverage of the touched packages stays at or above the floor                                          | `go test ./internal/vendors/... ./internal/chat/ . -cover`, with `internal/vendors` quoted as a range (`R3-57`)   |
| The cold benchmark stays under the ceiling                                                            | The phase-zero benchmark command at `benchmark-invocations`, compared against `benchmark-regression-band`         |

## Error coverage

| Failure                                                                    | Expected outcome                                                                                     | Test                                                            |
| ------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| `OpenAbs` fails for a file the identity walk reaches                         | Unchanged: skipped, and the walk continues. An open failure is already distinguishable and already not cached | `TestFindJSONLSession_readFailureIsNotAMissingSession` subtest |
| The scanner returns a read error before the first identity line               | The lookup fails with that error rather than reporting the session missing                                | `TestFindJSONLSession_readFailureIsNotAMissingSession`           |
| The scanner returns a read error after the identity line was already found    | The identity stands; a file's identity is established by its first identity line (D17)                     | `TestFindJSONLSession_readFailureIsNotAMissingSession` subtest |
| A read failure hides the walk-first of two duplicates                         | The lookup fails rather than answering with the sibling the walk would not have chosen                     | `TestFindJSONLSession_readFailureNeverAnswersWithASibling`       |
| `persistForeignCache` runs on a handler that resolved nothing                  | Nothing is written and nothing is constructed                                                              | `TestChatHandler_persistForeignCacheDoesNotConstruct`            |
| `persistForeignCache` runs concurrently with a first resolution                | Both complete with no race and at most one construction                                                    | `TestChatHandler_persistForeignCacheRacesResolution`             |
| `ScanJSONLLines` ends on the token bound                                       | The error names the oversized line and the bound and still satisfies `errors.Is(err, bufio.ErrTooLong)`    | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`           |
| `ScanJSONLLines` ends on any other scanner error                               | Unchanged: the error is returned as it is                                                                  | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound` subtest   |
| A vendor `Read` hits the token bound                                           | The verb fails with the intelligible message; no truncated conversation is returned                        | `TestSourceReaderRead_tokenBoundErrorIsIntelligible`, `TestPiSourceReaderRead_tokenBoundErrorIsIntelligible` |
| Discovery hits the token bound                                                 | Unchanged by D29: the truncated row is still stored and still a hit next run                               | The two tests in *Amends earlier phases*                        |

## Implementation notes

`clai`, `2026-09-16`. Deltas only.

**`R3-01`'s fix stops the walk rather than skipping the file.** `jsonlFileIdentity`
returns `(string, error)`; a refused open still returns `("", nil)` because the
error-coverage table says an unopenable file stays a skip, and a scanner error is
returned only when no identity was established, so D17's "the first identity line
decides the file" needs no second guard. `FindJSONLSession` keeps the first read
error in a variable rather than failing inside the visitor, because
`WalkJSONLFiles` swallows a visitor's return value other than `stop`; the walk is
stopped with `true` and the error is surfaced after it. Nothing on a healthy walk
changes, and `cachedSessionPath` is untouched.

**The sibling test runs through the reader, not through the generic entry point.**
The integration contract's warm row says the warm and cache-less runs must agree
rather than one opening each file, and `FindJSONLSession` alone cannot show that:
a warm lookup answers from `Locate` plus one `stat` and never reads, so it cannot
fail where a cache-less walk does. The failure the row is about is visible one
level up, at `anthropic.SourceReader.Read`, where the warm run resolves the
walk-first file and then fails reading **it** instead of quietly returning the
sibling's transcript. The test therefore drives `Read`, and it uses a real
`chat.ForeignIndex` rather than the file's `memCache` double, whose `Locate`
ranges a map and is non-deterministic when two rows share an identity — which is
the very `R2-02` condition this fixture sets up. An external test package may
import `internal/chat`, so no cycle arises.

**D29's wrap sits on `ScanJSONLLines` and nowhere else.** `scanJSONLRawLines` —
discovery's scanner — is untouched, which is what keeps
`TestDiscoverJSONL_oversizedLineTruncatesScan` and
`TestDiscoverJSONL_oversizedLineRowStaysCacheable` passing unedited. The wrap is
`%w` over `bufio.ErrTooLong`, so `errors.Is` still holds and D28's gate is
unaffected; the mutation table below pins that. Every other scanner error is
returned byte-identical, which a subtest asserts by string equality, because a
blanket wrap here would be the same class of mistake D28 forbade in discovery.

**The bound in the message is `maxToken`, not `vendors.ReadMaxToken`.** The
function takes the bound as a parameter and both vendors pass the constant, so
naming the parameter keeps the message true for any caller and lets the unit test
reach the branch with a small buffer instead of allocating the real bound.

**A token-bound scan needs a line above the scanner's starting buffer, not just
above `maxToken`.** `bufio.Scanner` only raises `ErrTooLong` when it cannot find a
token in a buffer already at the bound, and `scanJSONLRawLines` starts the buffer
at `64` KiB. A first attempt at the unit test used a bound of `64` bytes and got
no error at all, because the oversized line still fitted the starting buffer. The
test now uses the starting buffer as its bound. The two vendor `Read` tests do
write a real oversized line, because that path passes the constant.

**`R3-02` got the accessor, not the sentence.** `ChatHandler` gains a
`foreignCacheMu`; `foreignCacheOrNil` writes the field under it inside the once
and then reads through the new `resolvedForeignCache`, which takes the same lock
and never constructs. `sync.Once` alone could not serve, because its
happens-before only reaches a goroutine that calls `Do` — which is exactly what a
non-constructing reader must not do. `persistForeignCache`'s doc comment now
states the mechanism instead of a contract its callers have to remember.

**The five copies, walked.** The Strategy rule this round promoted is a checklist,
so both claims were walked before this table was written.

| Copy                          | The conformance claim (`R3-52`)                               | The walk-order claim (`R3-56`)                                   |
| ------------------------------- | --------------------------------------------------------------- | ------------------------------------------------------------------ |
| README invariant row          | already scoped by the review's own change (`R2-55`)             | already corrected in both rows: the lookup row and the parallel-discovery row (`R2-02`) |
| README parameters row         | none exists: the `vendors.LinePrefilter` row states the default only, and makes no resolution claim | none exists                                    |
| The owning phase's row        | `phase-3-parallel-exact.md`'s prefilter invariant row — **changed here** | `phase-3-parallel-exact.md`'s discovery-steps row — **changed here** |
| The Go doc comment            | `internal/vendors/schema.go` and `internal/vendors/jsonltest/conformance.go` — **both changed here** | `internal/vendors/jsonl_discover.go`, `discoverSlot` and `walkJSONLPaths` — **both changed here**. `internal/chat/foreign_index.go` and `internal/vendors/source.go` were already correct |
| The architecture section      | already qualified (`R2-55`)                                     | not stated there                                                  |

**The walk found a copy the finding did not name.** `jsonltest.Corpus`'s doc
comment said its files are "in the lexical path order a directory walk produces",
which is the refuted claim stated as a general fact — true only because a
generated corpus uses fixed-width names, as the sort call five lines below it
already says. It is now stated that way, and `phase-0-measurement-gate.md`'s
declaration of the same struct field is corrected to match. Neither is in this
phase's *Files* table; both are inside the acceptance grep, and leaving them
would have made this phase the fifth round to correct a subset.

### Before and after

Every production fix has a test shown to fail against the tree as it stood before
the fix. The token-bound wrap was toggled off in place to take the two vendor
readings and restored immediately; nothing was stashed.

| Finding | Test | Before |
| --------- | ------ | -------- |
| `R3-01` | `TestFindJSONLSession_readFailureIsNotAMissingSession` | failed: `error "stub session \"s\" not found" does not carry the transport failure` |
| `R3-01` | `TestFindJSONLSession_readFailureNeverAnswersWithASibling` | failed on the cache-less run, which returned the sibling's transcript — `Content:the backup copy` — for a session whose walk-first file could not be read |
| `R3-03` | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound` | failed on every message assertion: the error was `bufio.Scanner: token too long` and named neither the line nor the bound |
| `R3-03` | `TestSourceReaderRead_tokenBoundErrorIsIntelligible` | failed: `scan jsonl "…/huge.jsonl": bufio.Scanner: token too long` names neither `line`, `exceeds` nor `10485760` |
| `R3-03` | `TestPiSourceReaderRead_tokenBoundErrorIsIntelligible` | failed identically, through pi's own unchanged wrap |
| `R3-02` | `TestChatHandler_persistForeignCacheRacesResolution` | failed under `-race`: `race detected` — write at `sync.Once.doSlow` inside `foreignCacheOrNil`, previous read at `handler_list_chat.go:321`, which is `persistForeignCache`'s bare field read. This is the reviewer's probe, reproduced |
| `R3-02` | `TestChatHandler_persistForeignCacheDoesNotConstruct` | **passed** before the fix, by design: the pre-fix field read also constructed nothing. It is the guard on the property, so it is pinned by mutation instead — see below |

### Mutation table

Each mutation was applied in place and reverted before the gates were run.

| Mutation | Caught by |
| ---------- | ----------- |
| `persistForeignCache` goes through `foreignCacheOrNil()` — the reviewer's own probe, which broke no test before this phase | `TestChatHandler_persistForeignCacheDoesNotConstruct`: `persisting constructed 1 indexes, want 0` |
| `ScanJSONLLines` returns an opaque error instead of wrapping with `%w` | `TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound`'s sentinel assertion, and both vendor `Read` tests |
| The token-bound wrap is disabled | both vendor `Read` tests and the unit test's message assertions; the two discovery tripwires stay green, which is what says the wrap never reached `scanJSONLRawLines` |

### Verification

| Command | Result |
| --------- | -------- |
| `go run mvdan.cc/gofumpt@latest -w -l .` | no output |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | no output |
| `go vet ./...`, `go fix ./...` | no output |
| `go test ./... -race -cover -count=3 -timeout=30s` | all green, run twice at host loads `1.24` and `1.55`; root package `22.701` s of the `30` s bound. Neither recorded flake reproduced |
| `go run github.com/mibk/dupl@latest -t 80 .` | `34` clone groups, the same count phases six and the review recorded; none names `jsonl.go`, `jsonl_discover.go`, `handler.go`, `jsonl_test.go` or either new vendor test |
| `go test ./internal/vendors/... ./internal/chat/ . -cover` | `internal/vendors` `72.2` to `72.5` percent — up from the `70.6` to `70.9` range (`R3-57`) because the new tests reach `ScanJSONLLines`' wrap and the identity scan's error path; `internal/chat` `78.9`, `anthropic` `76.8`, `pi` `91.1`, `jsonltest` `93.3`, root `95.6` |
| `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x` | `29918965` ns/op, `24344322` B/op, `428055` allocs/op at load `2.19` — under the shipped row `31032276` and far under the band |
| the four tripwires in *Amends earlier phases* | `TestDiscoverJSONL_oversizedLineTruncatesScan`, `TestDiscoverJSONL_oversizedLineRowStaysCacheable`, `TestForeignChatRows_singleCacheWrite` and `TestFindJSONLSession_notFound` all pass unedited under `-race` |
| `grep -n 'fails on exactly that mistake' internal/vendors/schema.go` | one line, now continuing "when the fixture corpus can witness it" (`R3-52`) |
| `grep -n 'quiet loss' internal/vendors/jsonltest/conformance.go` | one line, now continuing "as far as that corpus can witness it" (`R3-52`) |
| `grep -rn 'lexical order' internal/vendors/ internal/chat/ worklogs/2026-09-16-foreign-conversation-index/` | two hits in `jsonltest/countingfs.go`, both describing a sorted listing rather than a walk, and the findings' own quotations of the phrase in this file and in `phase-3-parallel-exact.md` (`R3-56`) |
| `grep -n 'listable' README.md` | the D29 residual in *Strategy* and D29's own row |
| readiness checklist items `1`, `2`, `2a`, `3` and `6` | all five pass from the worklog directory. Item `1`: no output. Item `2`: no output. Item `2a`: this phase's seven tests all live in its five listed files, and all five files exist. Item `3`: this phase's three parameters each have a README row, and the guarded accessor introduces no new configurable value. Item `6`: `DiscoverMaxLines` appears in phases three, four and six only |

### What the specification got wrong

| Where | What it says | What is actually true |
| ------- | -------------- | ----------------------- |
| *Integration contract*, the warm-cache row | The warm and cache-less runs "both fail" | Only at the reader boundary the row's own collaborator list names. `FindJSONLSession` with a warm index answers from `Locate` plus one `stat` and never reads, so it **succeeds** where the cache-less walk fails; the agreement the row is really about is that neither run ever names the sibling, and both verbs fail. The test is written at `Read` for that reason |
| *Files* | Lists `internal/vendors/jsonl_test.go` as a file to change | No such file existed; it is new |
| *Files* | The two documentation findings' file lists | Neither names `internal/vendors/jsonltest/corpus.go` or `phase-0-measurement-gate.md`, which carry a fourth and fifth copy of the walk-order claim that the acceptance grep does reach. Both are corrected here |
| *Error coverage* | A scanner read error "after the identity line was already found" is a distinct case | It is unreachable by construction: the scan stops at the first identity line, so no further read is issued. The subtest is kept as a regression guard on that ordering, not as a reproduction |

## Review findings

None.
