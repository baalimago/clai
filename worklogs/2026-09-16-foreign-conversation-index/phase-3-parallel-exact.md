# Phase 3 — Parallel and exact discovery

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Make a cache miss read the whole file so `MessageCount` is exact, and pay for it
with a bounded worker pool and a byte prefilter, so that a cold listing is not
slower than the line-capped scan it replaces.

## Specification

### Why the two land together

Exact counting is slower than the capped scan when done serially; parallel
discovery is what makes it cheaper than today. Shipping the cap deletion without
the pool would leave a user-visible regression standing for a phase (D14).
Neither half may be merged alone.

### Structure

`DiscoverJSONL` becomes three ordered steps. The order is what makes the result
deterministic, so it is part of the contract, not an implementation detail:

| Step  | Work                                                                  | Concurrency                   |
| ----- | ----------------------------------------------------------------------- | ------------------------------- |
| One   | Walk both roots, collecting absolute paths in the walk's own order — per-directory entry order, not whole-path byte order (`R2-02`, `R3-56`) | Serial, as today               |
| Two   | Per path: stat, cache lookup, scan on a miss, cache store               | Bounded by `discover-workers`  |
| Three | Compact the indexed result slots, dropping files with no identity       | Serial                         |

Workers write into a pre-sized slice at their own index and never `append`, so
no ordering is lost and no lock is needed for the results. The cache is shared
and already safe for concurrent use. The output is therefore identical to the
serial output, file for file and field for field — that equality is the test,
rather than a weaker "contains the same rows" assertion.

The bound is reached through the `discoverWorkers` seam rather than computed
inline, so a test can set it (D20). A seam is what makes both the bound and its
degenerate values reachable; a value derived from `runtime.NumCPU()` inside the
function would make every assertion about it host-dependent.

| Limit                 | Injectable                   | README parameter    | How the test reaches it                                          |
| --------------------- | ---------------------------- | ------------------- | ------------------------------------------------------------------ |
| Peak concurrent scans | `discoverWorkers` seam       | `discover-workers`  | The seam is replaced with a fixed value; a counting schema records the peak |
| Degenerate bound      | `discoverWorkers` seam       | `discover-workers`  | The seam returns zero or a negative value; discovery still completes |

Goroutine lifetime is structural, not observed: `DiscoverJSONL` owns its workers
under a `sync.WaitGroup` and returns only after `Wait`, so no goroutine can
outlive the call (D22). The repository forbids new dependencies, so no leak
detector is used and none is needed.

### Exactness

`vendors.DiscoverMaxLines` is deleted. The scan reads to EOF, so every counted
role is counted. The scanner token bound `vendors.ReadMaxToken` stays: a single
line larger than it remains a truncated scan, which is a pre-existing and tested
behaviour.

`ScanJSONLLines` keeps only its token bound. Its line-bound parameter has no
non-zero caller once the cap is gone, and a parameter that is always zero is how
the next reader learns the wrong rule. Each vendor's one full-read call site
drops the argument; what it reads does not change.

### The prefilter

Reading every line does not mean decoding every line. `vendors.LinePrefilter` is
an optional upgrade of `JSONLSchema`: when a schema implements it, the scan asks
`MayContribute(line)` first and only decodes lines that pass. A schema that does
not implement it is decoded line by line, which is correct and slower — the safe
direction (D18).

This is the failure mode that sank the earlier `Provides()` design, so it is
closed by a conformance suite rather than by prose. The suite has an
error-returning core so its own failure is observable (D21):

| Invariant                                                   | Mechanism                                                                                              | Test                                                          |
| ------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| A prefilter never hides a line the schema would have used, **over the fixture corpus the suite is run with** (`R2-55`, `R3-52`) | `CheckSchemaConformance` returns an error for any fixture line whose `Fields` is non-zero but which `MayContribute` rejects. It detects a narrowing only when the corpus holds a line the narrowed filter misses; it does not police the marker set | `TestSchemaConformance_anthropic`, `TestSchemaConformance_pi` |
| A prefilter never turns a quoted role marker into a message  | The corpus contains a body quoting a marker; counts are compared to corpus facts                         | `TestDiscoverJSONL_countsMatchCorpusFactsExactly`              |
| Every vendor is conformance-tested                           | Each vendor package calls the shared runner with its own corpus shape                                    | Both tests above                                                |

Each vendor also decodes into a typed struct instead of `map[string]any` inside
`Fields`. The generic layer never sees either: it receives `LineFields`.

### Files

| File                                          | Change                                                                       |
| ----------------------------------------------- | ------------------------------------------------------------------------------ |
| `internal/vendors/jsonl_discover.go`           | The three-step structure, the worker pool and the `discoverWorkers` seam      |
| `internal/vendors/schema.go`                   | `LinePrefilter`                                                               |
| `internal/vendors/jsonl.go`                    | `DiscoverMaxLines` deleted; `ScanJSONLLines` loses its line bound              |
| `internal/vendors/anthropic/source_reader.go`  | Typed decode in `Fields`; `MayContribute`; its one full-read call site drops an argument |
| `internal/vendors/pi/source_reader.go`         | Same                                                                          |
| `internal/vendors/jsonl_discover_test.go`       | Exactness, parallel-equality, pool-bound and cancellation tests                |
| `internal/vendors/jsonltest/conformance.go`    | New: `RunSchemaConformance` over `CheckSchemaConformance`                      |
| `internal/vendors/jsonltest/conformance_test.go` | New: the runner's own failure and success cases                              |
| `internal/vendors/anthropic/schema_conformance_test.go`, `internal/vendors/pi/schema_conformance_test.go` | New                        |

## Integration contract

| Trigger                                                                  | Collaborators                   | Observable result                                                     | Required side effects | Prohibited side effects                              |
| ---------------------------------------------------------------------------- | --------------------------------- | ------------------------------------------------------------------------- | ----------------------- | ------------------------------------------------------ |
| Cold `chat list` over a corpus whose sessions exceed the fixture line count | generated corpus, nil cache      | Every `MessageCount` equals the corpus fact                              | None                  | No count capped; no row dropped or reordered           |
| The same corpus discovered serially and in parallel                        | generated corpus, `discoverWorkers` seam | Byte-identical row slices                                        | None                  | No ordering difference; no data race                   |
| A corpus containing a message body that quotes a role marker               | generated corpus                 | The quoted marker is not counted as a message                            | None                  | No over-count                                          |
| Cold `chat list` with the cache enabled                                    | generated corpus, empty cache    | Rows written once, within `benchmark-regression-band` of the phase-zero figure | Cache written once | No regression beyond the band                          |
| Context cancelled while workers are running                                | cancellable context, live cache  | The context error is returned and no rows                                | The rows a worker finished before the cancel stay stored and are valid hits next run — each came from a complete scan keyed by its own pre-read stat. Stated in full, and exercised, by the review-one addendum `phase-5-review-1-fixes.md` (`R1-51`, `R2-52`) | No goroutine outlives the call; no row stored from a partial scan |

## Acceptance criteria

| Outcome                                                                 | Test or command                                                                     |
| --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Counts are exact for sessions longer than the deleted cap                | `TestDiscoverJSONL_countsMatchCorpusFactsExactly`                                     |
| Parallel output equals serial output exactly                             | `TestDiscoverJSONL_parallelMatchesSerial` under `-race`                               |
| Peak concurrency never exceeds the injected bound                        | `TestDiscoverJSONL_workerPoolIsBounded`                                               |
| Discovery returns only after every worker has finished                   | `TestDiscoverJSONL_returnsAfterWorkersFinish`                                         |
| A prefilter cannot hide a contributing line, for either vendor           | `TestSchemaConformance_anthropic`, `TestSchemaConformance_pi`                         |
| A schema without a prefilter still discovers correctly                   | `TestDiscoverJSONL_schemaWithoutPrefilter`                                            |
| The deleted symbol is gone repository-wide                               | `grep -rn 'DiscoverMaxLines' --include='*.go' .` returns nothing                       |
| A cold, uncached discovery is within `benchmark-regression-band` of the phase-zero figure | The phase-zero benchmark command at `benchmark-invocations`, both figures recorded below |
| The repository gate passes unedited, within its recorded band            | `go test ./... -race -cover -count=3 -timeout=30s`, timing recorded below              |

## Error coverage

| Failure                                                          | Expected outcome                                                           | Test                                                    |
| ------------------------------------------------------------------ | ---------------------------------------------------------------------------- | --------------------------------------------------------- |
| One worker's file is unreadable                                  | Its slot is empty and dropped; every other row is unaffected                | `TestDiscoverJSONL_parallelUnreadableFileIsolated`       |
| One worker's file has an oversized line                          | That file's scan truncates; other workers are unaffected                    | `TestDiscoverJSONL_parallelOversizedLineIsolated`        |
| A vendor's `MayContribute` wrongly rejects a contributing line    | `CheckSchemaConformance` returns an error naming the offending line          | `TestCheckSchemaConformance_detectsUnderInclusivePrefilter` |
| A vendor's `MayContribute` accepts everything                    | `CheckSchemaConformance` returns nil; discovery stays correct                | `TestCheckSchemaConformance_acceptsOverInclusivePrefilter`  |
| The context is cancelled before any worker starts                | The context error is returned; no file is opened                            | `TestDiscoverJSONL_cancelledBeforeStart`                 |
| The injected worker bound is zero or negative                    | The pool falls back to a single worker rather than deadlocking              | `TestDiscoverJSONL_degenerateWorkerCount`                |

## Implementation notes

Session: `Claude Opus 5 (1M context)` via Claude Code, `2026-09-16T18:00Z`.

Deltas only; the specification is not restated.

### Seams the phase asked for but did not provide

`discoverWorkers` is unexported and the generic discovery tests live in
`package vendors_test`, so "the seam is replaced with a fixed value" was not
reachable as written. `internal/vendors/export_test.go` now exposes
`SetDiscoverWorkers(n int) func()`, which compiles only under the test build.
It is not in the Files table.

The conformance runner has the signature the README fixes, which forces
`jsonltest` to import `internal/vendors` — and that contradicts the package
doc phase zero wrote, which claims no production imports. There is no cycle:
nothing under `internal/vendors` imports `jsonltest` outside a test binary.
The package doc now says so instead of claiming the opposite.

The runner takes `lines [][]byte` and nothing in `jsonltest` produced them, so
`Corpus.Lines()` was added: every non-empty line of every corpus file, in walk
order. A conformance suite over hand-written lines would prove nothing about
the corpus the counting tests use.

### The typed decode needed a generic helper, and needed to stay tolerant

With `Fields` decoding into a struct, a message body arrives as
`json.RawMessage`, and `vendors.TextBlocksContent` only accepts `any`. Both
vendors would otherwise have cloned a raw-bytes variant, which `dupl` forbids,
so `vendors.RawTextBlocksContent` lives in `jsonl.go` — a generic-layer
addition the Files table does not list. It decodes block by block, so one
unreadable block drops that block rather than the whole body, which is what
the decoded form did.

Returning the zero `LineFields` on a decode error would have been a
behavioural change rather than a speed-up: the map decode tolerated a wrongly
typed field per field, while a struct decode reports one type error for the
whole line. Both vendors therefore ignore the decode error. A syntax error
still leaves every field zero, and a type error still fills the remaining
fields — in both cases exactly what the map decode contributed.

The unexported `scanJSONLRawLines` lost its line bound as well as the exported
`ScanJSONLLines`. The Files table names only the exported one, but leaving the
always-zero parameter one level down is the same defect the phase names.

### Cancellation

The contract row says the context error is returned and does not say what
happens to the rows already scanned. They are dropped: `DiscoverJSONL` returns
`nil` rows, which is what the pre-existing cancelled-walk test already
asserted, and half a listing is worse than none because nothing marks it as
half. The acceptance and error tables name only the cancelled-before-start
case although the integration contract has a mid-flight row, so
`TestDiscoverJSONL_cancelledWhileWorkersRun` was added for it.

### Where the speed actually came from

The prefilter is the smallest of the three changes. Nearly every Claude Code
line carries `sessionId`, `cwd` and `timestamp`, so a sound prefilter must
pass nearly every line; allocations per discovery fell from `598961` to
`428053`, and that is mostly the typed decode. The pool is what pays for
exactness: measured serially, exact discovery costs `135743645` ns/op against
the phase-zero line-capped `89515806` ns/op — above the regression ceiling,
which is D14 reproduced as a measurement rather than an argument. The same
code at the default bound costs `36641038` ns/op.

### Corpus totals

The README's phase-zero datum reads as though exact discovery should report
`15699` messages. It reports `15449` across the same `61` rows, because `250`
of those messages live in the identity-less fixture that discovery drops by
design. The `15699` figure is the corpus total, not the discoverable total;
`12159` is the capped discoverable total and is the number that moves.

### Maintenance contract honoured in the same change

`architecture/continue-from-claudex.md` stated the deleted rule in two places:
`ScanJSONLLines` described as bounded line scanning, and the discovery section
promising a bounded prefix of two hundred lines with an approximate
`MessageCount`. Both now state what the code does. Phase 4 still owns that
document's push/pull narrative and cost budget.

### Acceptance, row by row

| Outcome                                                        | Evidence                                                                              |
| ---------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| Counts are exact past the deleted cap                          | `TestDiscoverJSONL_countsMatchCorpusFactsExactly` passes; with a cap reinstated it reports `200` against the fact `500` |
| Parallel output equals serial output                           | `TestDiscoverJSONL_parallelMatchesSerial` under `-race`, at one, two, eight and sixty-four workers |
| Peak concurrency never exceeds the injected bound              | `TestDiscoverJSONL_workerPoolIsBounded` asserts equality with the bound, not a ceiling  |
| Discovery returns only after every worker has finished         | `TestDiscoverJSONL_returnsAfterWorkersFinish`; the last file is provably still scanning when the first finishes |
| A prefilter cannot hide a contributing line, for either vendor | `TestSchemaConformance_anthropic`, `TestSchemaConformance_pi`, both over generated corpora |
| A schema without a prefilter still discovers correctly         | `TestDiscoverJSONL_schemaWithoutPrefilter`, which holds both directions                |
| The deleted symbol is gone repository-wide                     | `grep -rn 'DiscoverMaxLines' --include='*.go' .` returns nothing                        |
| Cold discovery within the regression band                      | `36641038` ns/op against the phase-zero `89515806` ns/op — under the figure, not merely under the ceiling |
| The repository gate passes unedited                            | `26` s, all green, host load `5.07`                                                     |

Every error-coverage row has a passing test of the name the table gives, plus
`TestDiscoverJSONL_cancelledWhileWorkersRun` for the contract row the tables
left unnamed.

### Verification

| Command                                                                                      | Result                                                        |
| ---------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| `go test ./... -race -cover -count=3 -timeout=30s`                                             | all green, `26` s wall at host load `5.07`, reconfirmed green at load `3.67` |
| `go test ./internal/vendors/ -race -run 'TestDiscoverJSONL_...'`                               | every phase test passes under the race detector                |
| `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x` | `36641038` ns/op, `24343291` B/op, `428053` allocs/op    |
| the same command on the unmodified tree, same session                                          | `90983674` ns/op, `26263196` B/op, `598961` allocs/op          |
| the same command with the seam pinned to one worker                                            | `135743645` ns/op — the regression the pool pays for           |
| the same command, rerun by phase 5 against the shipped tree at load `1.12`                      | `34098838` ns/op, `24343347` B/op, `428053` allocs/op — this phase's figure reproduces, unlike the pre-phase-three rows it is compared against (`R1-52`) |
| `grep -rn 'DiscoverMaxLines' --include='*.go' .`                                               | no match                                                       |
| `gofumpt`, `staticcheck`, `go vet`, `go fix`                                                   | no output                                                      |
| `dupl -t 80 .`                                                                                 | `34` clone groups, the same set as before; none names a file this phase wrote |

One gate run at host load `8.08` failed
`internal/text`'s `TestNewQuerier_costManagerErrorUsesCostWarnf`, which asserts
its captured stdout is empty and instead caught warnings naming models no test
in this worklog uses — `ollama:phi`, `mercury-pro`. It passes in isolation and
in both runs below load `6`; it is a pre-existing stdout-capture collision in a
package this phase does not touch, and it is recorded rather than worked
around.

Statement coverage after the change: `internal/vendors` `70.5`,
`internal/vendors/anthropic` `76.5`, `internal/vendors/pi` `89.9`,
`internal/vendors/jsonltest` `93.3`.

### Every new assertion was checked against a broken implementation

A passing test that cannot fail is the failure mode this phase is most exposed
to, so each was rerun against a deliberately wrong build and the wrong build
was reverted:

| Mutation                                             | Test that caught it                                  |
| ------------------------------------------------------ | ------------------------------------------------------ |
| Pool pinned to one worker                            | `TestDiscoverJSONL_workerPoolIsBounded` (peak one)    |
| `wg.Wait()` removed                                  | `TestDiscoverJSONL_returnsAfterWorkersFinish`          |
| A line cap reinstated inside the scan                | `TestDiscoverJSONL_countsMatchCorpusFactsExactly` (`200` against `500`) |
| Workers appending under a mutex instead of filling slots | `TestDiscoverJSONL_parallelMatchesSerial`          |
| The prefilter result ignored                         | `TestDiscoverJSONL_schemaWithoutPrefilter`             |
| Claude's prefilter narrowed to the identity key      | `TestSchemaConformance_anthropic`, which named the rejected line |

## Review findings

### Review 1 (`2026-09-16`)

Three minors, all routed to the addendum, `phase-5-review-1-fixes.md`; this
phase is not reopened. None of them touches the pool, the ordering or the
exactness result, which reproduced.

| ID       | Severity | Where                                                                          | Finding                                                             |
| -------- | -------- | -------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| `R1-51`  | minor    | Integration contract, the cancelled-mid-flight row                              | The row says the side effects are none; they are not                 |
| `R1-52`  | minor    | The README figures this phase is compared against                               | Two figures are contradicted by the shipped tree                     |
| `R1-53`  | minor    | `internal/vendors/anthropic/source_reader.go`, `internal/vendors/pi/source_reader.go` | Prefilter soundness rests on spellings `encoding/json` does not require |

- [x] `R1-51` — the contract's mid-flight cancellation row records *required
  side effects: none*, which is untrue. Files a worker finished before the
  cancel were already committed through `cache.Store`, and `internal/chat`
  persists that cache, while `DiscoverJSONL` returns no rows. The behaviour was
  traced and is **benign and arguably desirable**: each stored row came from a
  complete scan keyed by its own pre-read stat, so it is a valid hit next run —
  which is exactly the rule the addendum promotes. But
  `TestDiscoverJSONL_cancelledWhileWorkersRun` passes a nil cache, so neither the
  rule nor its benignity is written down or guarded. Fix: state the real rule in
  the contract row, and exercise the case with a live cache.
- [x] `R1-52` — the README gives a cold-discovery benchmark command and a figure
  under an instruction that says to rerun the commands; the shipped tree yields a
  figure roughly a third of it. The original is not reproducible from any tree
  state any more, because the line cap it measured was deleted by this phase, so
  the regression ceiling derived from it is permanently unverifiable too. The
  same table's corpus datum and the discoverable total it implies also disagree
  with what the shipped reader reports: `64` files, `61` rows, `15449`
  discovered against a `15699` corpus fact, the difference being the
  identity-less fixture discovery drops by design. This phase's Implementation
  notes already explain the second discrepancy under *Corpus totals*; the README
  did not carry it, so a contributor following the README's own instruction was
  left with an unexplained threefold gap and a total that looked wrong. Fixed in
  the README's Budgets table by marking both figures historical and recording the
  shipped figures beside them.
- [x] `R1-53` — `MayContribute` searches literal byte spellings while `Fields`
  decodes with `encoding/json`, which matches struct tags without regard to case
  and accepts escaped key characters. A line spelling an identity key
  differently therefore yields a non-zero `LineFields` but is rejected by the
  prefilter — the silent undercount `LinePrefilter`'s own doc comment forbids.
  `jsonltest` emits only canonical spellings, so the conformance suite
  structurally cannot catch it. Judged unreachable against today's Claude Code
  and pi writers. **Maintainer decision D26**: document the assumption rather
  than re-engineer the prefilter, as D23 handled the one-identity assumption.
  Both `MayContribute` doc comments and the suite's own documentation state that
  the prefilter is sound only for canonically spelled JSON keys. A case-variant
  fixture line is deliberately **not** added: it would make the conformance suite
  fail by construction, and the only fix would be a per-line case-insensitive
  scan whose cost defeats the prefilter.

**Verified good in this phase.** The worker pool's ordering guarantee is
structural rather than incidental: pre-sized slots, disjoint indices, `wg.Wait`
before return, and a feeder that always closes the channel; zero, negative and
very large bounds are all safe. The mutation table was spot-checked and the
guard is non-vacuous. One caveat for a later round:
`TestDiscoverJSONL_countsMatchCorpusFactsExactly` is **not** a prefilter guard
for `Cwd`, `Created` or `Model` — it asserts `MessageCount` alone, so only the
conformance suite covers the other fields. The exactness oracle is a genuine
independent re-derivation of the count rather than a restatement of the
production algorithm; the `dupl` group between `jsonl.go` and
`jsonltest/corpus.go` is the evidence of that independence, not a smell. The
typed decode's ignored `json.Unmarshal` error is safe on every constructed path,
and the tolerance argument in *The typed decode needed a generic helper* holds.
`FindJSONLSession` deliberately does not apply the prefilter, so even an unsound
prefilter cannot corrupt the continue path — which is what keeps `R1-53` a minor.

### Review 2 (`2026-09-16`)

One minor of this phase's own and two notes, plus the branch of the blocker
that lives in this phase's scan path. All are routed to the addendum,
`phase-6-review-2-fixes.md`; this phase is not reopened. The pool, the ordering
and the exactness result reproduced again.

| ID       | Severity | Where                                                              | Finding                                                             |
| -------- | -------- | -------------------------------------------------------------------- | --------------------------------------------------------------------- |
| `R2-52`  | minor    | Integration contract, the cancelled-mid-flight row                  | The row still states the falsehood `R1-51` reported                  |
| `R2-01`  | blocker  | `internal/vendors/jsonl_discover.go`, `scanJSONLFileRow`            | The scanner's own error is discarded, so a partial read is cached    |
| `R2-51`  | note     | `vendors.ReadMaxToken` through `scanJSONLRawLines`                  | Verdict: the truncation path is **not** the same defect class — D28  |
| `R2-55`  | note     | `jsonltest.RunSchemaConformance` and the two conformance tests      | Non-vacuous, but lower-resolution than two documents claim           |

- [x] `R2-52` — the mid-flight cancellation row still reads *Required side
  effects: None*, which `R1-51` reported and which phase 5 corrected in **its
  own** contract row. The worklog therefore now carries two contract tables
  contradicting each other for the same trigger, and the one a reader reaches
  from the phase that owns parallel discovery is the wrong one. Fix: restate the
  real rule here, or annotate the row to point at phase 5's.
- [x] `R2-01` (filed against phase 5, reaching into this phase's scan path) —
  `scanJSONLRawLines` returns `s.Err()`, and the one caller that matters,
  `scanJSONLFileRow`, discards it with `_ =`. A read failure therefore yields
  `(SourceRow{...}, ..., nil)` and `discoverJSONLFile` reaches `cache.Store`
  with a row built from a partial read, keyed by a `(size, mtime)` pair the
  failure did not change. The in-code comment "the scan reads to EOF, so every
  counted role is counted" is the false assumption. The fix belongs to the
  addendum and must discriminate rather than blanket-skip — see D28 and
  `R2-51`.
- [x] `R2-51` (note) — verdict on the `ReadMaxToken` truncation path, recorded
  because it is the justification D28 rests on. An oversized line is a property
  of the file's bytes: the scan truncates in the same place every run, a
  cache-less rescan produces the same row, and `(size, mtime)` does invalidate
  it, since any edit that removes the oversized line changes one or the other.
  Under the promoted rule's own words — "something the filesystem actually
  reported about the file's **content**" — it is a fact about the file and
  persisting it is legitimate. `R1-54`'s documentation caveat is the correct and
  sufficient response, and a blanket "any scan error skips `Store`" would
  silently revert it along with the cached counterpart of
  `TestDiscoverJSONL_oversizedLineTruncatesScan`. One residual obligation:
  `ReadMaxToken` is a compile-time constant and is **not** part of the cache
  key, while `foreign-index-version` is the documented lever for an
  incompatible change, so raising the token bound would leave every previously
  truncated row cached at its old undercount indefinitely. Changing
  `ReadMaxToken` therefore requires a `foreign-index-version` bump; the
  addendum puts that sentence beside both parameter rows.
- [x] `R2-55` (note) — the conformance suite was verified non-vacuous by nine
  mutations: narrowing the markers to `"sessionId"` alone is **caught** (this
  phase's own recorded mutation reproduces exactly), an empty marker set is
  **caught**, and its three anti-vacuity guards are intact. But five of the six
  anthropic markers can be deleted **individually** without failing it, because
  every generated Claude line carries `"sessionId"`. The suite detects a
  narrowing only when the fixture corpus happens to contain a line the narrowed
  filter misses; it does not police the marker set. Two documents overstate it
  and gain a qualifying clause in the addendum:
  `architecture/continue-from-claudex.md`'s "which fails on exactly that
  mistake" — it fails on *some* instances of it — and the README invariant *a
  prefilter never hides a line the schema would have used*, which names the two
  conformance tests as its mechanism without qualification.

**Verified good in this phase.** The phase-five signature change is clean through
the concurrency this phase owns: `scanJSONLFileRow`'s new return is consumed
entirely inside `discoverJSONLFile`, `scanJSONLFileSlots` is behaviourally
byte-identical to its pre-phase-five form, a per-file error can neither abort a
run nor corrupt a slot, and `TestDiscoverJSONL_parallelMatchesSerial` holds green
under `-race`. Cancellation is now fully specified and guarded, with the rows
stored before the cancel proved field-identical to a full cache-less scan. Both
prefilters are sound over their corpora within D26's assumption.

### Review 3 (`2026-09-16`)

One minor and one note of this phase's own, both instances of the rule review
three promoted: a claim corrected in some of its copies and left standing in the
rest. Both are routed to the addendum, `phase-7-review-3-fixes.md`, because each
has a copy in a Go file as well as in this phase file. This phase is not
reopened, and the round's verdict is **ready**.

| ID       | Severity | Where                                                                            | Finding                                                            |
| -------- | -------- | ---------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| `R3-52`  | minor    | `internal/vendors/schema.go`, `internal/vendors/jsonltest/conformance.go`, and this file's prefilter invariant row | The conformance overstatement `R2-55` qualified survives in three places |
| `R3-56`  | note     | `internal/vendors/jsonl_discover.go`, `discoverSlot` and `walkJSONLPaths`, and this file's discovery-steps table | "Lexical order" survives in the file a contributor edits            |

- [x] `R3-52` — `R2-55` qualified the conformance claim in exactly two
  documents. Three others still carry it unqualified. `LinePrefilter`'s doc
  comment in `internal/vendors/schema.go` says the suite "fails on exactly that
  mistake" — and that is the interface doc a vendor author reads **before**
  writing a `MayContribute`, earlier than the architecture document that was
  corrected. `internal/vendors/jsonltest/conformance.go`'s header says "a wrong
  prefilter is a test failure rather than a quiet loss"; D26's own wording
  required the assumption to live in the suite's documentation, and the file
  documents the canonical-spelling half but not the resolution half. And this
  phase's own invariant row is unscoped while the README's copy is scoped.
  Unmet clauses: `R2-55` itself — five of the six anthropic markers can be
  deleted individually without failing the suite — plus D7's stated purpose,
  that the cost rule lives in code instead of prose, and `R2-57`'s upgrade
  rationale. The consequence is concrete: a future vendor author writes a marker
  set narrower than its `Fields`, the corpus happens to carry another marker on
  every line, the suite passes, and discovery silently undercounts — D1's defect
  reached through the documentation route D7 exists to close. **Maintainer
  decision:** qualify all three in the addendum, in the same words the two
  corrected documents use.
- [x] `R3-56` — `discoverSlot`'s and `walkJSONLPaths`'s doc comments in
  `internal/vendors/jsonl_discover.go`, and this file's discovery-steps table,
  all still say the walk's "lexical order". `R2-02` established that the
  ordering is per-directory entry order and explicitly **not** whole-path
  lexical order; the README invariant and `internal/chat/foreign_index.go` were
  corrected, these three were not. This is the exact phrasing that produced
  `R1-05`'s wrong mechanism and then `R2-02`'s wrong implementation, left in the
  file a contributor edits. A reviewer also found and corrected the README's
  parallel-discovery invariant row carrying the same phrase while filing round
  two, which makes this the same claim's fourth copy. Fixed in the addendum.

**Verified good in this phase.** `compareWalkOrder` is a faithful `filepath.WalkDir`
oracle over sixty random trees with adversarial sibling names at every depth, and
a total order over twenty adversarial paths; its segment-count tail rule, which no
walk can reach, is pinned by a direct subtest. The `dupl` pair between `jsonl.go`
and `jsonltest/corpus.go` remains the only production clone and remains the
evidence of the exactness oracle's independence. The `break feed` cancellation
branch is the cause of this package's oscillating coverage figure (`R3-57`) and is
this phase's code; no phase since has touched it.
