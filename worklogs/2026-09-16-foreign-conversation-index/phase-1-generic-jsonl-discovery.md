# Phase 1 — Generic JSONL discovery

**Status:** Complete
**Worklog:** [README](./README.md)

## Goal

Move the walk, scan and aggregate loop out of both vendors into
`internal/vendors`, leaving each vendor with a line-to-fields function, with no
change to what `chat list` shows.

## Specification

### What moves

Each vendor currently owns four near-identical pieces: `Discover` (walk),
`discoverOne` (open, scan, aggregate, fall back), `findSessionFile` (walk) and
`fileHasSessionID` (open, scan, match). All four become two generic functions.
The vendor keeps its schema knowledge and nothing else.

| Deleted from each vendor                | Replaced by                                       |
| ----------------------------------------- | --------------------------------------------------- |
| `discoverOne`                            | `vendors.DiscoverJSONL`                            |
| `findSessionFile`, `fileHasSessionID`    | `vendors.FindJSONLSession`                         |
| The body of `Discover`                   | A call to `vendors.DiscoverJSONL` with the schema  |
| Preview truncation and preview fallback  | The generic aggregator                              |

The vendor implements `vendors.JSONLSchema` as declared in the README. `Fields`
receives one raw line and returns what that line contributes. It never sees a
path, a handle, a reader or a line number, so it cannot decide how much of a
file is read — that is the whole point of the extraction.

### Aggregation rules

`DiscoverJSONL` builds one `SourceRow` per file from the `LineFields` stream.
The rules are fixed here so no vendor can restate them:

| `SourceRow` field        | Rule                                                                                      | Fallback                                          |
| -------------------------- | ------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| `Source`                 | `schema.SourceName()`                                                                       | None; empty is a programming error                 |
| `RawPath`                | The walked absolute path                                                                    | None                                                |
| `SourceID`               | First non-empty `LineFields.SessionID`                                                      | Row is dropped                                      |
| `Cwd`                    | First non-empty `LineFields.Cwd`                                                            | Empty                                               |
| `Created`                | First non-zero `LineFields.Timestamp`                                                       | The file's mod time, from this phase's single stat  |
| `Model`                  | First non-empty `LineFields.Model`                                                          | Empty                                               |
| `FullFirstUserMessage`   | `LineFields.UserText` of the first `LineRoleUser` line **whose `UserText` is non-empty**     | Empty                                               |
| `FirstUserMessage`       | `FullFirstUserMessage` truncated to `source-preview-runes`, newlines to spaces               | `source-missing-preview`                            |
| `MessageCount`           | Count of lines whose role is `LineRoleUser`, `LineRoleAssistant` or `LineRoleTool`           | Zero                                                |

The non-empty qualifier on the preview is not a refinement, it is the existing
behaviour: both vendors guard preview extraction with a "still empty" check, and
Claude Code emits every tool result as a user-typed line whose text extraction
yields nothing. Taking the first user line unconditionally would preview nothing
for any transcript that opens with a tool result, and — because the full text
feeds `ComputeGroupKeyFromText` — would silently move that conversation's group.

**The count rule and the preview rule diverge on purpose.** A user line with no
extractable text still counts as a message; it just cannot be the preview.

`LineRoleSkip` discards the entire line: it contributes no field and no count.
`LineRoleNone` contributes fields but no count, which is how a pi `session` line
supplies identity without being a message.

Counting differences between vendors are expressed only by which role a schema
returns. Claude never returns `LineRoleTool`; pi returns it for `toolResult`.
No vendor-specific branch exists in the generic layer.

| Invariant                                                             | Mechanism                                                                     | Test                                            |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------- |
| `LineFields` stays a closed set                                        | The struct's field set is asserted, so adding a field fails until the README parameters table is changed | `TestLineFields_closedSet`   |
| A vendor cannot widen its own read                                     | `JSONLSchema` exposes no path, handle, reader or line index                     | `TestJSONLSchema_surfaceIsLineOnly`              |
| Discovery never writes to a foreign root                               | No code path opens a source file for writing                                    | `TestDiscoverJSONL_neverWritesToSourceRoot`      |

### Session lookup

`FindJSONLSession` walks the same roots and asks each file for its identity. It
stops reading a file at the first line whose `Fields` yields a non-empty
`SessionID`, because a session file has exactly one identity — measured, not
assumed (D23), and guarded by a fixture that carries two. This removes the
lookup path's dependence on the line cap rather than re-bounding it (D17).

| Invariant                                                                     | Mechanism                                                                 | Test                                            |
| ------------------------------------------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------- |
| A session file has exactly one identity; the first line that yields one decides | The scan returns as soon as `SessionID` is non-empty, match or not          | `TestFindJSONLSession_stopsAtFirstIdentityLine`  |
| Lookup and discovery never disagree about which file holds a session           | Both take `SourceID` from the first non-empty `SessionID`, including on the two-identity fixture | `TestFindJSONLSession_agreesWithDiscover` |

### Behaviour preservation

This phase changes no observable behaviour. The line cap stays exactly where it
is, applied by the generic scan loop instead of by each vendor. The two vendor
test files must pass **unedited**; that is this phase's strongest acceptance
signal, and it is scoped to this phase — phase 2 changes both interface methods
and edits their call sites.

### Supporting helper

`StatAbs(fsys fs.FS, absPath string) (fs.FileInfo, error)` joins `OpenAbs` in
`jsonl.go`, so the single stat per file goes through the injectable filesystem.
It calls `fs.Stat`, which falls back to opening the file when the filesystem
does not implement `fs.StatFS` — which is why every test filesystem in this
worklog is `jsonltest.CountingFS`, and why a hand-rolled fake would make later
never-opened assertions fail against correct code.

### Files

| File                                                                                        | Change                                                          |
| --------------------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| `internal/vendors/schema.go`                                                                 | New: `LineRole`, `LineFields`, `JSONLSchema`                     |
| `internal/vendors/jsonl_discover.go`                                                         | New: `DiscoverJSONL`, `FindJSONLSession`, the aggregator          |
| `internal/vendors/jsonl.go`                                                                  | Add `StatAbs`; `ScanJSONLLines` and the cap constant unchanged    |
| `internal/vendors/anthropic/source_reader.go`                                                | `Fields` and the schema methods; four members deleted             |
| `internal/vendors/pi/source_reader.go`                                                       | Same                                                              |
| `internal/vendors/jsonl_discover_test.go`                                                    | New: aggregation, lookup and preview tests                        |
| `internal/vendors/schema_test.go`                                                            | New: `TestLineFields_closedSet`, `TestJSONLSchema_surfaceIsLineOnly` |
| `internal/vendors/anthropic/source_reader_test.go`, `internal/vendors/pi/source_reader_test.go` | **Unedited**                                                  |

## Integration contract

| Trigger                                                                    | Collaborators                          | Observable result                                                  | Required side effects | Prohibited side effects                              |
| ---------------------------------------------------------------------------- | ---------------------------------------- | -------------------------------------------------------------------- | ----------------------- | ------------------------------------------------------ |
| `chat list` with both vendor roots populated                               | generated corpus, real readers          | The same rows, order, previews and counts as before the refactor    | None                  | No write anywhere; no read outside the vendor roots   |
| A transcript whose first user line carries only tool-result content        | generated corpus                        | The preview is the first line with real text; the count includes both | None                  | No `(no preview)` placeholder; no group-key change    |
| `chat continue` for a session whose identity is on the first line          | generated corpus, `jsonltest.CountingFS` | The owning file is returned                                          | None                  | No read past that line of that file                   |
| A file carrying two distinct identities                                    | generated corpus                        | Discovery and lookup both use the first one                          | None                  | No disagreement between the two paths                 |
| A vendor root that does not exist                                          | empty temp dir                          | No rows, no error                                                     | None                  | No error surfaced to the list                         |

## Acceptance criteria

| Outcome                                                                          | Test or command                                                             |
| ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------- |
| Both vendors' existing discovery tests pass without modification                  | `git diff --exit-code -- internal/vendors/anthropic/source_reader_test.go internal/vendors/pi/source_reader_test.go` |
| Rows built from the generated corpus match the corpus facts                       | `TestDiscoverJSONL_matchesCorpusFacts`                                        |
| Every aggregation rule above holds, including each fallback                       | `TestDiscoverJSONL_aggregationRules`                                          |
| A first user line with no extractable text is counted but never previewed         | `TestDiscoverJSONL_toolResultFirstUserLinePreview`                            |
| A skipped line contributes neither fields nor count                               | `TestDiscoverJSONL_skipRoleContributesNothing`                                |
| Tool lines count for pi and not for Claude, with no branch in the generic layer   | `TestDiscoverJSONL_roleCountingIsSchemaDriven`                                |
| `LineFields` has exactly the documented fields                                    | `TestLineFields_closedSet`                                                    |
| The schema interface exposes no way to reach a file                               | `TestJSONLSchema_surfaceIsLineOnly`                                           |
| Discovery opens no source file for writing                                        | `TestDiscoverJSONL_neverWritesToSourceRoot`                                   |
| Session lookup reads one line of a file whose identity is on the first line       | `TestFindJSONLSession_stopsAtFirstIdentityLine`                               |
| Lookup and discovery agree for every file in the corpus, including the two-identity fixture | `TestFindJSONLSession_agreesWithDiscover`                          |
| Discovery cost has not regressed beyond `benchmark-regression-band`               | The phase-zero benchmark command at `benchmark-invocations`, both figures recorded below |
| The repository gate passes unedited, within its recorded band                     | `go test ./... -race -cover -count=3 -timeout=30s`                             |
| No clone introduced by the extraction                                             | `go run github.com/mibk/dupl@latest -t 80 .`                                   |

## Error coverage

| Failure                                                       | Expected outcome                                                          | Test                                                  |
| --------------------------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------- |
| A file cannot be opened mid-walk                              | That file is skipped; every other file still yields a row                  | `TestDiscoverJSONL_unreadableFileSkipped`              |
| A line is not valid JSON                                      | The line is skipped; the rest of the file is still scanned                 | `TestDiscoverJSONL_malformedLineSkipped`               |
| A line exceeds the scanner token bound                        | The scan stops at that line; the partial row is still returned if usable   | `TestDiscoverJSONL_oversizedLineTruncatesScan`         |
| A file yields no session identity                             | No row for that file; no error                                             | `TestDiscoverJSONL_noIdentityYieldsNoRow`              |
| A file yields no user text at all                             | `source-missing-preview` is used                                           | `TestDiscoverJSONL_missingPreviewFallback`             |
| A file has no usable timestamp                                | `Created` falls back to the file's mod time                                | `TestDiscoverJSONL_createdFallsBackToModTime`          |
| The context is cancelled mid-walk                             | The walk stops and the context error is returned                           | `TestDiscoverJSONL_contextCancelled`                   |
| `FindJSONLSession` finds no match                             | A not-found error naming the source and the identifier                     | `TestFindJSONLSession_notFound`                        |
| `FindJSONLSession` is given an empty identifier               | An error before any walk begins                                            | `TestFindJSONLSession_emptyIDRejected`                 |

## Implementation notes

### 2026-09-16 — Phase 1 executed (clai, Opus 5)

Deltas only; the specification stands except where noted.

**Behaviour preservation was measured, not asserted.**
`TestDiscoverJSONL_matchesCorpusFacts` was written and run *before* the
extraction, against the unmodified vendor readers, and it passed: both real
readers already agree with the corpus oracle field for field. The same test,
unedited, passes after the extraction. The oracle is the acceptance criterion
the phase asked for, and it now has a before-and-after run rather than a claim.
The corpus it uses is deliberately shorter per session than the discovery line
cap, because the oracle counts every message in a file and the capped scan
cannot; at the default fixture scale the two disagree by construction until
the cap is deleted.

**One entry point changed shape against the README.** The README's shared
interfaces give `DiscoverJSONL` and `FindJSONLSession` a `cache SourceCache`
parameter, but `SourceCache` belongs to phase 2, and so does every cache
invariant. Both functions therefore ship without it, and phase 2
adds it along with the interface it names. This is the one place where the
README describes the end state of an entry point this phase creates.

**`ScanJSONLLines` is delegated, not copied.** The generic scan needs raw
line bytes; the existing helper needs decoded envelopes. Writing a second
scanner would have been the clone the phase forbids, so `scanJSONLRawLines`
owns the loop and `ScanJSONLLines` is now a decoding wrapper over it. Its
signature, its skipping rules and its bound behaviour are unchanged, which the
two vendors' unedited full-read tests exercise. The Files table says
`ScanJSONLLines` is unchanged; its observable contract is.

**A free speed-up fell out of it.** The raw scanner hands out
`bufio.Scanner.Bytes` where the old loop allocated a string per line via
`.Text()`. Allocated bytes per discovery fell from `33044851` to `26237310`
and allocations from `623968` to `598768`, at unchanged wall time.

**The preview guard is on the preview, not on the text.** The aggregation
table says the preview source is the first user line "whose `UserText` is
non-empty". Both vendors in fact guard on the *truncated* preview being empty,
which differs only for user text that is non-empty but collapses to nothing
(whitespace or newlines only): the vendors keep looking, the literal reading
would stop. `applyLineFields` keeps the vendors' rule, because this phase is
behaviour-preserving; the table should say "whose text yields a preview".

**Diagnostic strings moved to the source name.** The vendor error strings were
built from prose names, the generic ones are built from `SourceName()`:
`discover claude sessions` is now `discover claude-code sessions`, `claude
session %q not found` is now `claude-code session %q not found`, and
`claude projects root not configured` / `pi sessions root not configured` are
now `claude-code root not configured` / `pi root not configured`. No test or
document asserts these strings. pi's discovery error is unchanged.

**Two lookup edge cases changed, both narrowing.** Anthropic's old
`fileHasSessionID` scanned a file until it found the *wanted* id; pi's stopped
at the first `session` line whatever its id. Both now stop at the first line
that yields any identity, which is D17 applied uniformly. The measured
one-identity property (D23) is what makes this safe, and
`TestFindJSONLSession_stopsAtFirstIdentityLine` plus the two-identity fixture
in `TestFindJSONLSession_agreesWithDiscover` pin the new rule in place.

**The mod-time fallback now goes through the injectable filesystem.** Both
vendors called `os.Stat` directly, bypassing their own `FS` field;
`discoverJSONLFile` calls `StatAbs`. It still stats only when no line carried
a timestamp — phase 2 is what moves the stat in front of the read.

**A maintenance contract outside the Files table.**
`architecture/continue-from-claudex.md` named `discoverOne` as the thing a new
source implements. That symbol no longer exists, so the shared-skeleton
section now describes `DiscoverJSONL`, `FindJSONLSession` and `JSONLSchema`.
Phase 4 still owns the push/pull contract, the cost budget and the pi row in
that document.

**The closed-set invariant has two enforcement mechanisms, not one.** Adding a
field to `LineFields` fails `TestLineFields_closedSet` (verified by mutation:
`LineFields has 7 fields, want 6`). Adding a method to `JSONLSchema` fails
earlier than any test, at compile time, because both vendor schemas stop
implementing it — a mutation adding `Path() string` broke the build of the
package under test. `TestJSONLSchema_surfaceIsLineOnly` covers what compilation
cannot: that no method but `Fields` takes an input at all.

### Verification

| Criterion                                             | Command                                                                                                              | Result |
| ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | -------- |
| Vendor test files unedited                            | `git diff --exit-code -- internal/vendors/anthropic/source_reader_test.go internal/vendors/pi/source_reader_test.go`   | clean, and both packages pass |
| Every test named here                                    | `go test ./internal/vendors/ -run 'TestDiscoverJSONL\|TestFindJSONLSession\|TestLineFields\|TestJSONLSchema' -count=1 -v` | all pass |
| Repository gate                                       | `go test ./... -race -cover -count=3 -timeout=30s`                                                                      | pass, `24.0` s at host load `1.7`; root package `22.5` s |
| Discovery cost                                        | `go test ./internal/vendors/anthropic/ -run '^$' -bench BenchmarkSourceReaderDiscover -benchtime 10x`                    | `89971510` ns/op against the phase-zero `89515806` and the ceiling `107418967` |
| Same-session control for that figure                  | the same command against a pristine `HEAD` checkout with the phase-zero fixtures copied in                              | `89117806` ns/op, so the extraction costs `+0.96` percent |
| No clone introduced                                   | `go run github.com/mibk/dupl@latest -t 80 .`                                                                            | none of the new files appear; the two pre-existing `anthropic`/`pi` reader clones are gone |
| Format, lint, static analysis, fix                    | `gofumpt -w -l .`, `go vet ./...`, `staticcheck ./...`, `go fix ./...`                                                  | clean |
| New-code coverage                                     | `go tool cover -func` over the three packages                                                                           | `jsonl_discover.go` `87.5`–`100` percent per function, both vendors' `Fields` `90` percent |

The integration contract's last row — a vendor root that does not exist — had
no named test; it is covered by `TestDiscoverJSONL_missingRootYieldsNoRows`,
over both an empty root string and an absent directory.

`go fix ./...` rewrote the reflection loops in `schema_test.go` to the
`reflect` iterator forms (`Type.Fields`, `Type.Methods`, `Type.Ins`); the
rewrite is kept.

## Review findings

### Review 3 (`2026-09-16`)

One minor of this phase's own, the last instance in `jsonl_discover.go` of the
pattern `R2-01` was about. Routed to the addendum,
`phase-7-review-3-fixes.md`; this phase is not reopened. The round's verdict is
**ready** and this finding does not change it.

| ID       | Severity | Where                                                        | Finding                                                                      |
| -------- | -------- | -------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `R3-01`  | minor    | `internal/vendors/jsonl_discover.go`, `jsonlFileIdentity`     | The scanner error is discarded, so a transport failure silently changes which file the walk says holds a session |

- [x] `R3-01` — `jsonlFileIdentity` calls `scanJSONLRawLines` with `_ =` and
  returns `""` on any failure, so a read that fails before the first identity
  line is indistinguishable from a file that names no session. The consequence
  is the one the README invariant *lookup and discovery never disagree about
  which file holds a session, cache or no cache* forbids, reached through the
  discarded error rather than through the ordering. Two files carry session `S`
  — `proj/s.jsonl`, which the walk reaches first, and `proj-bak/s.jsonl`, the
  fixture `R2-02` introduced. A read failure on the walk-first file makes
  `FindJSONLSession`'s walk skip it and answer `proj-bak/s.jsonl`, while a warm
  cache answers `proj/s.jsonl` through `Locate`: `clai chat continue` opens a
  different transcript depending on cache warmth, which is the same user-visible
  defect `R2-02` was raised for. With no duplicate present the user is told
  `claude-code session "S" not found` instead of being told about the transport
  error. Not a regression — `HEAD` at `2d82dd6` discards the same error in the
  same place — and not a caching violation, because nothing is stored. But the
  file is inconsistent with itself: both vendors' `Read` already treats the same
  error as fatal. The Strategy rule is being applied to durable state and not to
  an answer. **Maintainer decision: fix it in the addendum.** Propagate the
  scanner error out of `jsonlFileIdentity` and let `FindJSONLSession`
  distinguish "no identity here" from "could not read this file". The fixture is
  a duplicate identity in sibling directories plus a read failure on the
  walk-first file, using the existing `failReadFS` recipe.

**Verified good in this phase.** `git diff` over the two vendor test files
confirms exactly thirteen `Discover` and nine `Read` call-site changes and
nothing else, so this phase's "tests unedited" claim survives phase `2`'s
plumbing and phase `3`'s signature work. `FindJSONLSession` still stops at the
first identity line and still applies no prefilter, so an unsound prefilter
cannot corrupt the continue path.
