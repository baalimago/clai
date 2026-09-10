# Phase 7 — Docs and quality-gate sweep

**Status:** Complete

[← README](./README.md)

## Goal

Document the feature where the repository keeps its architecture notes and
prove every gate passes unedited.

## Specification

### Documentation

| Document                            | Change                                                                                                                                                                  |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `architecture/summaries.md` (new)   | The feature note: data model, the `submit_summary` tool and its validation, the in-flight lifecycle (isolated context, display → join → persist), the batch command, the model ladder, display precedence, the `StopEvent` root-cancel caveat, debug switch |
| `architecture/README.md`            | Index entry under "Core concepts"                                                                                                                                       |
| `architecture/chat.md`              | `Chat` struct block gains the three fields; `chat summarize` in the subcommand list; the mirror copy and `-re` adoption rule                                             |
| `architecture/dirscope.md`          | Lookback descriptor example shows the `title: summary` form and the fallback                                                                                            |
| `architecture/config.md`            | Text mode config keys `summarize-conversations`, `summary-model`; `DEBUG_SUMMARY` in the debug table; `-summarize`/`-sm` in the flag notes                              |
| `architecture/cmd-dispatch.md`      | Subcommander tree gains `summarize|s`; flag-group table gains the summarize group and the two query flags                                                                |
| `architecture/query.md`             | Post-processing step lists the summary join                                                                                                                             |
| `architecture/tooling.md`           | `SkipAmbientMcpServers` under MCP configuration                                                                                                                         |
| `main.go` usage                     | Example line for `chat summarize` (added in phase 5; verified here)                                                                                                     |

### Gates

Every command from the README validation policy, run from the repository
root, unedited. `dupl` output triaged: a clone inside new code is fixed;
pre-existing clones are listed in the implementation notes.

Coverage: `internal/summary` and every file added by this worklog at or
above the repository floor stated in `AGENTS.md`.

## Integration contract

`unit-test-only` — this phase adds no behavior; the e2e suites of phases 3
to 6 are the evidence and must be green in the sweep.

## Acceptance criteria

| Outcome                                                          | Evidence                                                   |
| ---------------------------------------------------------------- | ---------------------------------------------------------- |
| Every document row above landed                                  | `git diff --stat architecture/ main.go`                    |
| `make qa` passes                                                 | Command output recorded in the implementation notes        |
| `go run github.com/mibk/dupl@latest -t 80 .` triaged             | Clone list with a verdict per group in the notes           |
| Coverage floor met for new packages                              | `go test ./internal/summary/ -cover` output in the notes   |
| All phases marked Complete in the README status board            | README                                                     |

Files: `architecture/summaries.md`, `architecture/README.md`,
`architecture/chat.md`, `architecture/dirscope.md`, `architecture/config.md`,
`architecture/cmd-dispatch.md`, `architecture/query.md`,
`architecture/tooling.md`.

## Error coverage

| Failure                                   | Expected outcome                                                     | Evidence                       |
| ----------------------------------------- | -------------------------------------------------------------------- | ------------------------------ |
| A gate fails                              | The phase stays open; the failing output and the fix are in the notes | Implementation notes           |
| A new clone group inside worklog code     | Refactored before sign-off                                            | `dupl` rerun in the notes      |

## Implementation notes

Session `session_01KXDFdab6MmuuKetC1EBowJ` (phase-7 worker, claude-fable-5-1),
2026-09-09, started after the phase-6 sign-off; host load 4–8 on 22 cores.

Deltas from the specification:

- **Consolidation carried in from the phase-5 sign-off.** The four-line
  label stamping (`Title`, `Summary`, `SummaryAt` with the `time.Now()`
  fallback, append `Queries`) existed in `applySummary`
  (`internal/text/summary_launch.go`) and inline in `summarizeOne`
  (`internal/chat/handler_summarize.go`). It is now
  `func (s Summary) ApplyTo(c *pub_models.Chat)` in
  `internal/models/summary.go`; `applySummary` is deleted and both call
  sites call `res.summary.ApplyTo(chat)` / `s.ApplyTo(&c)`. Test written
  first: `TestSummaryApplyTo` in `internal/models/summary_test.go` (stamps
  fields and appends queries in order; zero `GeneratedAt` falls back to
  the call time and appends nothing). The existing `TestFinalize_join` and
  `TestHandleSummarize_*` suites pass unchanged; `internal/models` is at
  100% coverage.
- `architecture/tooling.md`: phase 1 had already placed the
  `SkipAmbientMcpServers` sentence in the MCP lifecycle list; this phase
  adds the paragraph under "Configuration layout" the phase table names
  and leaves the lifecycle sentence in place. The command-ban section was
  checked against `pkg/tools/cmd_ban.go` (`WithCmdBanContext` copies its
  slice), `pkg/tools/bash_tool_freetext_command.go` (`Call` permissive,
  `CallWithContext` validates) and `internal/text/tool_executor.go`
  (single `WithCmdBanContext` attach site): it matches the code.
- `architecture/cmd-dispatch.md` also gained `-sm` next to `-cm` in the
  completion list and `summarize` in the config-prep command list, both
  facts the phase table did not name but the code has.
- `architecture/chat.md`: the `dirv2` JSON example gained the two optional
  keys and a `chat summarize` subsection was added ahead of the
  `chat list` section, since the "subcommand list" the phase names is the
  "No model interaction" section plus the per-verb sections.
- `main.go` usage line from phase 5 verified present:
  `clai c summarize -since 7d   # label last week's conversations …` (now `clai c summarize 7d` after D33).

Verification (all from the repository root, unedited commands):

```bash
go run mvdan.cc/gofumpt@latest -w -l .                   # exit 0, no files listed
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # exit 0, no output
go vet ./...                                             # exit 0
go fix ./...                                             # exit 0, no rewrites
go run github.com/mibk/dupl@latest -t 80 .               # Found total 31 clone groups.
go test ./internal/summary/ -cover                       # ok, coverage: 95.2% of statements
cat /proc/loadavg                                        # 4.96 6.52 5.11 before the gate
go test ./... -race -cover -count=3 -timeout=30s         # exit 0, 45 packages ok, no FAIL/panic; root 21.7 s
make qa                                                  # exit 0 (staticcheck, gofumpt, go fix, then the same gate): 45 ok; load 4.3 → 8.3
git diff --stat architecture/ main.go                    # 8 files, 106 insertions, 21 deletions (+ summaries.md untracked)
```

Coverage from the gate run: `internal/summary` 95.2%, `internal/text`
84.4%, `internal/chat` 75.2%, `internal/models` 100.0%, `internal` 80.6%,
root 56.7%. The 70% floor holds for `internal/summary`, `internal/text`,
`internal/chat` and `internal/models`.

`dupl` triage — 31 groups, the same count every phase recorded. Groups
that touch a file this worklog added or edited, with verdict:

| Group | Verdict |
|---|---|
| `internal/text/querier_setup_tools_test.go:373,394` ↔ `pkg/agent/mcp_setup_test.go:18,39` | pre-existing; the worklog hunks in that file are the import line and lines 481+ (`git diff -U0`); two test packages that cannot share a helper |
| `internal/chat/handler_list_chat_test.go:162,169` ↔ `:210,217` | pre-existing; the worklog hunk starts at line 927 |
| `internal/text/querier_test.go:1256,1284` ↔ `:1341,1369` ↔ `:1426,1454` ↔ `:1508,1536` | pre-existing; the worklog hunks are the two single-line `TokenUsage` fixture edits at 837 and 1087 |
| `internal/models/models_test.go:45,61` ↔ `:63,79` | pre-existing; the worklog adds `summary.go` and `summary_test.go` beside it, the file itself is untouched |

No clone group lies inside code this worklog added (`internal/summary/*`,
`internal/models/summary*.go`, `internal/text/summary_launch*.go`,
`internal/chat/handler_summarize*.go`, `internal/chat/label*.go`,
`main_summary_e2e_test.go`). The remaining 27 groups are in files the
worklog never touched.

## Review findings

### Review 2 — 2026-09-10 (`worklog-review`, post-implementation)

- **R2-08 (Note)** — `Summary.ApplyTo` (`internal/models/summary.go:44`)
  falls back to local `time.Now()` while every other stamp in the feature
  is UTC. Only fakes reach the fallback (the summarizer always stamps
  `GeneratedAt`). Cosmetic; phase 8 may switch it to `UTC()` while
  touching the file.

Verified good (gates re-run by the reviewer on 2026-09-10 at load
average about two on twenty-two cores, all unedited from the repository
root): gofumpt no files listed, `go vet` clean, staticcheck clean,
`go test ./... -race -cover -count=3 -timeout=30s` exit zero with every
package `ok` (root, `internal/audio`, `internal/vendors/openai` and
`pkg/agent` are the slow packages and stayed well inside the timeout),
dupl at the thirty-one baseline groups with none inside worklog-added
files. Coverage: `internal/summary` 95.2%, `internal/text` 84.4%,
`internal/chat` 75.2%, `internal/models` 100%.

### Review 3 — 2026-09-10 (`worklog-review`, holistic)

- [x] **R3-14 (Low)** — Stale text after D33 and phase 9, each verified
  against the code: `architecture/README.md:12` still names
  `clai chat summarize -since`; `internal/summary/since.go:15` says
  "resolves a `-since` value"; `architecture/query.md:125` and
  `architecture/summaries.md:199` present `summary-join-timeout` as a
  config key although `SummaryJoinTimeout` is `json:"-"` with no flag;
  `architecture/board.md:56` and `summaries.md:290` say a full UUID fits
  at ninety-six columns where the arithmetic gives ninety-four;
  `board.md:39` claims a `forceLive` seam for every caller, the ffmpeg
  split path has none; `board.md:54` omits `elapsed` from the diarization
  footer. In the worklog: README intro, board row five, the
  definition-of-success row and the phase-two summary (`parseSince` for
  `ParseSince`); phase 5 specification and integration rows; phase 8
  integration rows (`c summarize -since 7d …`); phase 7 notes
  ("usage line verified present: `clai c summarize -since 7d`"); the phase
  9 specification block (`Row` without `Index`, the pre-fix column
  widths). Fix: one pass over the list; keep journal entries as history.

Verified good (review 3): every other claim in `summaries.md`, `board.md`,
`chat.md`, `config.md`, `cmd-dispatch.md`, `continue-from-claudex.md`,
`tooling.md`, `dirscope.md`, `audio.md` matches the code (flag names and
defaults, `CLAI_SUMMARIZER`, the `About` header, the toggle labels, the
ladder, the raw objects, the board log line and footer, the TTY rule);
all forty-four test names cited by the README invariants table and the
phase 7/8 tables exist in the implied files; `go.mod`/`go.sum` unchanged.
