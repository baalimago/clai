# Phase 4 — Docs and quality-gate sweep

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Put the push/pull contract and its cost budget into the architecture
documentation so the next vendor cannot reintroduce the rescan, and prove every
repository gate green unedited.

## Specification

### The architecture document

`architecture/continue-from-claudex.md` describes discovery as it was before
this worklog: a bounded prefix scan with an explicitly approximate message
count, a stale comment listing pi as a future reader, and a source-table row
that predates the pi JSONL reader. Each required change carries a phrase that a
grep can find, so "the document was updated" is a checkable claim rather than a
judgement:

| Section to change                    | Required content                                                                                 | Checkable phrase        |
| ------------------------------------ | -------------------------------------------------------------------------------------------------- | ----------------------- |
| Discovery description                | A cache miss reads to EOF; the bounded-prefix rule and its line count are gone                     | `reads to EOF`          |
| Message count                        | Exact from discovery onward; the "approximate until `Read`" caveat is removed                      | `count is exact`        |
| New section on how the index learns  | Native conversations are pushed to the index at save; foreign conversations are pulled and validated by size and mod time; the derived cache is deletable at any time | `Push versus pull` |
| Cost budget                          | A listing over an unchanged corpus opens no session file, naming the test that proves it            | `opens no session file` |
| Implementing a new JSONL source      | Implement `JSONLSchema`; optionally `LinePrefilter`; run the conformance suite; never open files in vendor code | `never opens files` |
| The closed-set rule                  | A `LineFields` field enters only when the chat list gains a capability that consumes it eagerly      | `closed set`            |
| The future-readers comment           | pi is implemented; the comment must not list it as future work                                      | —                       |
| The Pi row in the source table       | pi is a local JSONL session reader, not a web API or browser export                                 | `local JSONL`           |

Any other document asserting the old rule is corrected in the same phase.

### Quality gates

Every gate in the repository's QA table runs unedited, and the duplication
report is read rather than merely run: the extraction in phase one should have
removed clones between the two vendors, so a surviving clone between them is a
finding to explain in Implementation notes, not a number to accept.

Coverage is checked for the packages this worklog touched, against the floor
named in the README's definition of success.

### Files

| File                                        | Change                                                                 |
| --------------------------------------------- | ------------------------------------------------------------------------ |
| `architecture/continue-from-claudex.md`      | Every row of the table above                                            |
| Other files under `architecture/`            | Only if a grep shows they assert the deleted rule; none expected        |

This phase writes no test file: its evidence is the phrase greps and the
repository's own gates.

## Integration contract

`unit-test-only` for the documentation change; the gates below are the
integration evidence for the worklog as a whole.

## Acceptance criteria

| Outcome                                                                  | Test or command                                                                                       |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| Every required phrase is present in the architecture document             | One `grep -c '<phrase>' architecture/continue-from-claudex.md` per row of the table above, each non-zero |
| The document no longer claims a bounded prefix or an approximate count    | `grep -nE 'DiscoverMaxLines|bounded prefix|approximate' architecture/continue-from-claudex.md` returns nothing |
| The future-readers comment no longer lists an implemented reader          | `grep -n 'future: codex' architecture/continue-from-claudex.md` does not mention pi                      |
| No other architecture document still cites the deleted symbol             | `grep -rn 'DiscoverMaxLines' architecture/` returns nothing                                              |
| Formatting is clean                                                       | `go run mvdan.cc/gofumpt@latest -w -l .`                                                                 |
| Static analysis is clean                                                  | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`                                                 |
| Vet is clean                                                              | `go vet ./...`                                                                                           |
| Fixes are applied                                                         | `go fix ./...`                                                                                           |
| Tests pass unedited                                                       | `go test ./... -race -cover -count=3 -timeout=30s`                                                        |
| Duplication is reviewed, not merely run                                   | `go run github.com/mibk/dupl@latest -t 80 .`, findings explained below                                   |
| Touched packages meet the coverage floor                                  | `go test ./internal/vendors/... ./internal/chat/ -cover`, each at or above the floor                     |
| The whole suite runs from one command                                     | `make qa`                                                                                                |

## Error coverage

| Failure                                                          | Expected outcome                                                          | Test or action                                   |
| ------------------------------------------------------------------ | --------------------------------------------------------------------------- | -------------------------------------------------- |
| A gate fails                                                     | The phase does not complete; the failure and its fix are recorded below    | Gate output in Implementation notes               |
| A required phrase is missing from the document                   | The phase does not complete; the grep is the evidence                      | The per-phrase greps above                        |
| The duplication report shows a clone between the two vendors     | Treated as an incomplete extraction and fixed, or justified in writing     | `dupl` output quoted below                        |
| A touched package is below the coverage floor                    | Tests are added in the owning phase's area, not waived                     | Coverage output in Implementation notes           |
| The race gate is slower than its recorded band                   | Investigated before sign-off; fixture scale is the first suspect           | Timing recorded in the session journal            |

## Implementation notes

Executed `2026-09-16` by an agent session (clai, `Opus 5`) on the maintainer's
machine. Deltas only.

### The document was already partly corrected

The specification's premise — that `continue-from-claudex.md` still describes
the pre-worklog discovery — was stale by the time this phase ran. Phases one
through three each honoured the same maintenance contract in their own change,
so of the eight required phrases one (`reads to EOF`) was already present, and
the acceptance grep for a bounded prefix or a caveat-laden count already
returned nothing **before** this phase edited anything. That is recorded rather
than treated as a pass: the row was satisfied by an earlier phase, not by this
one, and a reader of this file should not infer otherwise. The remaining seven
phrases were absent and were written here.

### Three stale statements the table did not name

The table names one discovery paragraph, one count sentence and one source-table
row. Reading the whole document found three further claims this worklog
falsifies, all corrected under "Any other document asserting the old rule is
corrected in the same phase":

- the `SourceReader.Discover` doc comment promised "no full-body reads", which
  is exactly what a cache miss now does;
- the Rules section said `Discover` "reads only headers/first-lines/timestamps
  from source files", the deleted rule restated in prose;
- "Configuration and persistence" said "No new cache files" and "The source
  files are read directly each time", both untrue since the foreign index.

### `architecture/config.md` was not in the Files table but needed the change

The Files table anticipates only a grep for the deleted line-scan symbol, and
`config.md` never mentioned it. It does, however, state the read-only rule that
D24 amended: that under `utils.NoCreateConfig` an index persist is skipped so a
read-only mount produces no failed write. That is still true of the *native*
index and is now false of the foreign one, so the document would have contradicted
shipped behaviour for the one verb this worklog targets. A paragraph was added
stating the flag's scope, why the foreign index ignores it, and that the
read-only promise is kept by suppressing the warning instead.

### Duplication was read, and one clone's justification had rotted

The phase's specific concern is answered cleanly: **no clone group names either
vendor's `source_reader.go`**, so the extraction is complete. The report is the
same `34` groups phases two and three recorded.

Two groups do name files this worklog wrote, both inside `jsonltest`, and both
are deliberate:

- the corpus oracle and the independent re-derivation in its own test. The test
  file already documents why: the verification loop reads the bytes on disk
  instead of trusting the bookkeeping that produced them, so a shared helper
  would destroy the only thing the test checks.
- the fixture generator's content flattener against the generic one. The
  comment justifying it said `jsonltest` imports no production package — true
  when phase zero wrote it, false since phase three gave the conformance runner
  a `vendors.JSONLSchema` parameter, and contradicted by the package doc eight
  lines above. **The comment was corrected, the clone kept**: the standing
  reason is that a fixture generator sharing code with the layer it fixtures
  makes its oracle agree with the code under test by construction. This is the
  only Go file this phase touched, and it is a comment.

### Coverage was measured two ways because the two documents disagree

This phase asks for "the packages this worklog touched"; the README's definition
of success says "new code". Both were computed, and both clear the floor, so the
disagreement is recorded rather than resolved.

### Checklist item 2a cannot distinguish declared from cited

Its grep lists only test paths under `internal/`, so phase zero's root-package
profiler test file is invisible to it, and it cannot tell a test a phase
*declares* from one a phase *cites* — phase three names a pre-existing
`internal/text` test purely as a host-load flake observation. Both read
correctly when the pairs are read together, which is what the item instructs;
the mechanical listing alone would show two false gaps.

### Verification

| Command                                                                                               | Result                                                                    |
| -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `for p in "reads to EOF" "count is exact" "Push versus pull" "opens no session file" "never opens files" "closed set" "local JSONL"; do grep -c "$p" architecture/continue-from-claudex.md; done` | `1 1 3 1 1 1 2` — every row non-zero                                        |
| `grep -nE 'DiscoverMaxLines\|bounded prefix\|approximate' architecture/continue-from-claudex.md`        | no match                                                                   |
| `grep -n 'future: codex' architecture/continue-from-claudex.md`                                        | `// future: codex.SourceReader{}, cursor.SourceReader{}, ...` — pi is gone  |
| `grep -rn 'DiscoverMaxLines' architecture/`                                                            | no match                                                                   |
| `go run mvdan.cc/gofumpt@latest -w -l .`                                                                | no output                                                                  |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`                                               | no output                                                                  |
| `go vet ./...`                                                                                          | no output                                                                  |
| `go fix ./...`                                                                                          | no output                                                                  |
| `go build ./...`                                                                                        | no output                                                                  |
| `go test ./... -race -cover -count=3 -timeout=30s`                                                       | all green, `24.4` s at host load `1.13` — inside the phase-zero band        |
| `make qa`                                                                                               | exit status `0`, no non-`ok` line                                          |
| `go run github.com/mibk/dupl@latest -t 80 .`                                                            | `34` groups, unchanged; none names a vendor source reader                   |
| `go test ./internal/vendors/... ./internal/chat/ -cover`                                                 | generic `70.7`, chat `77.6`, anthropic `76.5`, pi `89.9`, jsonltest `93.3` percent |
| per-file statements over the six files this worklog created                                             | `436` of `461`, `94.6` percent                                             |
| `go tool cover -func` over the whole repository                                                          | `80.2` percent, against the `79.630` recorded in the repository readme      |
| `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x`   | `30946454` ns/op against the phase-zero `89515806` and the ceiling `107418967` |

Readiness checklist rerun from this directory: items `1`, `2`, `2a`, `3`, `4`,
`5`, `6` and `7` pass. Item `6` matches phases three and four only, and phase
four's match is the grep that proves the symbol is gone.

## Review findings

### Review 1 (`2026-09-16`)

One note of this phase's own, plus one consequence of a decision taken against
phase 2. Both are routed to the addendum, `phase-5-review-1-fixes.md`; this
phase is not reopened.

| ID       | Severity | Where                                            | Finding                                                            |
| -------- | -------- | -------------------------------------------------- | ---------------------------------------------------------------------- |
| `R1-54`  | note     | `architecture/continue-from-claudex.md`           | *The count is exact from discovery onward* is stated unconditionally |
| `R1-03`  | major    | `architecture/config.md`, the read-only sentence  | D25 falsifies a sentence this phase wrote; amending it is mandatory  |

- [x] `R1-54` — the discovery section states that the count is exact from
  discovery onward, with no caveat. `vendors.ReadMaxToken` still truncates a
  file's entire scan at the first oversized line, in `scanJSONLRawLines` through
  `discoverJSONLFile`, and because the truncated scan's result is now cached the
  undercount is **persisted** rather than recomputed each run — a change this
  worklog introduced, even though the token bound itself is pre-existing and
  accepted. Keeping the bound is right; stating the claim without its one clause
  is not. Fix: add the caveat naming the token bound, so *exact* is not read as
  unconditional. The phase's own checkable phrase `count is exact` stays present,
  which is why the acceptance grep did not catch this.
- [x] `R1-03` (filed against phase 2, landing here) — this phase added the
  paragraph in `architecture/config.md` stating that the foreign index keeps the
  read-only promise through its warning, because under `utils.NoCreateConfig` a
  failed write is skipped in silence. D25 moves that gate to
  `utils.ReadonlyConfig`, so the sentence becomes false for an interactive
  `chat list`, which must now warn once. Amending the paragraph is part of the
  addendum and is not optional: left alone it would document the opposite of
  shipped behaviour for the verb this worklog exists for, which is precisely the
  failure this phase was written to prevent.

**Verified good in this phase.** Every documentation statement checked against
shipped behaviour matched, D24 included, and the three stale statements this
phase found beyond its own table were genuinely stale and are genuinely fixed.
The duplication reading is sound: `dupl -t 80 .` reproduces at `34` groups with
none naming either vendor's `source_reader.go`, which is the claim the phase
exists to prove, and the two `jsonltest` groups are deliberate. The coverage
figures reproduce. The gates reproduce at host load below `4`. Two pre-existing
flakes reproduce under higher load and are **not** caused by this worklog —
`internal/vendors/anthropic`'s `Test_context` hanging in `httptest.Server.Close`,
and `internal/text`'s stdout-capture collision this phase's predecessor already
recorded; both pass below load `4`, and both are noted so a later round does not
re-derive them. Green gates are reproduced; they are not the verdict.

> **Corrected by review three (`R3-55`).** "Both pass below load `4`" is the wrong
> model for the stdout-capture collision. A round-three reviewer reproduced it with
> `/proc/loadavg` at `1.22` falling to `0.93`, while the same test passes under
> `-count=5` in isolation and the next full gate at load about `2.5` was green.
> The cause is stdout contention between parallel tests **inside**
> `internal/text`, not host load, so a low load is no guarantee and a red run
> below load `4` is not a regression of this work. The `anthropic` hang is
> untouched by the correction.

### Review 2 (`2026-09-16`)

One minor and two notes, all against the documentation contract this phase owns.
All are routed to the addendum, `phase-6-review-2-fixes.md`; this phase is not
reopened. Every gate figure this phase recorded reproduced again, and the
duplication reading reproduced at `34` groups.

| ID       | Severity | Where                                                        | Finding                                                            |
| -------- | -------- | -------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `R2-57`  | minor    | `internal/vendors/source.go`, both vendors' `source_reader.go` | Stale Go doc comments in the files this worklog rewrote             |
| `R2-56`  | note     | `architecture/continue-from-claudex.md`                       | The promoted Strategy rule never reached the architecture tree      |
| `R2-55`  | note     | `architecture/continue-from-claudex.md`, the prefilter paragraph | The conformance suite is overstated, and D26's own loop is open   |
| `R2-04`  | minor    | README, *Shared interfaces* (filed against phase 2)           | A residual this phase recorded is still wrong two rounds later      |

- [x] `R2-57` (upgraded from note) — four doc comments in the four files a
  future vendor author opens first still describe the design this worklog
  replaced. These are the "cost rule lives in prose" failures D7 says the
  worklog exists to remove.
  `internal/vendors/source.go` says `MessageCount` **may be approximate** during
  discovery and that exact counts are available after `Read` parses the full
  conversation: both sentences are now false and inverted — discovery is exact,
  and `Read` offers no later refinement — and this is the contract comment on
  the struct every vendor implements. The same file says `Discover` MUST be
  read-only and fast, **no full body parsing**; a cache miss now reads to EOF
  and decodes every candidate line. This phase's journal claims it fixed that
  one, but the correction landed in the architecture document, not here. And
  both `internal/vendors/anthropic/source_reader.go` and
  `internal/vendors/pi/source_reader.go` still say discovery is **bounded**,
  referring to the `DiscoverMaxLines` phase 3 deleted from these same two files.
- [x] `R2-56` (note) — `architecture/continue-from-claudex.md` does not carry
  the Strategy rule *An error is not a fact about the file*, although review
  one's routing note said it was promoted "so later phases inherit it", and this
  phase's job was carrying README Strategy invariants into the architecture
  tree. It is the most reusable output of two review cycles, and `R2-01` proves
  the shipped code still violates it, yet it lives only in the worklog — which
  is deleted by convention once the effort ships. The addendum carries it
  across, with D28's content-bound-versus-transport-error distinction.
- [x] `R2-55` (note) — this document says every implementor runs
  `jsonltest.RunSchemaConformance` "which fails on exactly that mistake". It
  fails on *some* instances of that mistake: five of the six anthropic markers
  can be deleted individually without failing it, because every generated Claude
  line carries `"sessionId"`. One qualifying clause fixes it. Separately, D26's
  stated mechanism is that a vendor format change becomes "a review finding
  rather than a silent undercount", but phase 5's acceptance grep was scoped to
  `internal/vendors/`, so the canonical-spelling assumption landed in both
  readers and the suite and **not** in this document's
  *Implementing a new JSONL source* section — the section a new vendor author
  actually reads before writing a `MayContribute`. One sentence there closes
  D26's own loop.
- [x] `R2-04` (filed against phase 2, recorded here) — this phase recorded the
  README `SourceCache` block's missing `Locate` as a residual an executor may
  not fix. Two review rounds later it is still wrong in the one document every
  executor reads, and `R1-05` and `R2-02` are entirely about `Locate`'s
  contract. It is fixed in the addendum rather than recorded a third time.

**Verified good in this phase.** `architecture/config.md` and the push/pull
sections match shipped behaviour, D25 included. The gates reproduce at host
loads `1.35`, `1.46`, `2.41` and `3.67`; `dupl -t 80 .` reports `34` groups with
none naming a worklog file; neither recorded flake reproduced. The coverage
figures reproduce, with the single exception `R2-58` records against phase 5.

### Review 3 (`2026-09-16`)

One note of this phase's own, against a gate observation this phase recorded and
the review-one entry repeated. Closed in the review's own change; this phase is
not reopened, and the round's verdict is **ready**.

| ID       | Severity | Where                                        | Finding                                                          |
| -------- | -------- | ---------------------------------------------- | ------------------------------------------------------------------ |
| `R3-55`  | note     | The duplication-and-gates reading, above     | The recorded flake threshold is the wrong model for one of the two flakes |

- [x] `R3-55` — this phase and the review-one journal entry both say the two
  pre-existing flakes "reproduce under host load" and "both pass below load
  `4`". A round-three reviewer hit `internal/text`'s
  `TestNewQuerier_costManagerErrorUsesCostWarnf` with `/proc/loadavg` reading
  `1.22` and falling to `0.93`, while
  `go test ./internal/text/ -race -count=5 -run …` over that test passes and the
  next full gate at load about `2.5` was green. The collision is stdout
  contention between parallel tests **inside** the package, not host load.
  Nothing here is caused by this worklog, but the recorded model is actively
  misleading: a maintainer who runs the gate once below load `4`, sees red and
  trusts this document reads it as a regression of this change. Restated in
  place above and in the README's review-one entry, with the load model
  withdrawn for this flake and left intact for the `anthropic` hang.

**Verified good in this phase.** Every gate this phase claims reproduces:
`make qa` exits `0`, `gofumpt`, `go vet`, `staticcheck` and `go fix` are silent,
and `dupl -t 80 .` still reports `34` groups whose only production pair is the
documented oracle-independence clone. The push/pull contract and the cost budget
in `architecture/continue-from-claudex.md` still match shipped behaviour, now
including the promoted rule and D28's refinement phase `6` carried across.
