# Phase 3 — clai width unification

**Status:** Done  
[Back to README](./README.md)

## Goal

Replace all independent clai terminal-width paths with one dimensions snapshot supplied by the shared package.

## Specification

Update querier setup, tool execution, printing, glow sizing, photo animation, chat listings, tool listings, MCP descriptions, and related helpers. Remove `Querier.termWidth` as an independently refreshed width; use a dimensions snapshot and explicit width-aware table helpers. Keep noninteractive fallbacks deterministic. Do not change vendor code.

## Integration contract

| Trigger | Collaborator | Observable result | Required side effect | Prohibited side effect |
|---|---|---|---|---|
| Interactive query starts | dimensions viewer | One initial snapshot is stored | All render paths can consume it | No duplicate width source |
| Glow renders | shared snapshot | Glow receives current width | Width is passed explicitly | No hidden `table.TermWidth` query |
| Tool/chat listing renders | shared snapshot | Consistent truncation | One operation-level snapshot | No environment lookup |
| Non-terminal output runs | output-mode detection | Safe noninteractive behavior | No signal watcher | No ANSI-only width corruption |

## Acceptance criteria

- No clai production source directly calls `table.TermWidth`.
- No clai production source uses hidden width-discovery truncation APIs.
- All width-aware output uses the shared dimensions snapshot or an explicitly supplied snapshot.
- The dimensions snapshot is taken from the session output writer's fd (R2-02): the querier binds to `userConf.Out` and the chat handler binds to `out`, so `clai 2>/dev/null` observes stdout, not stderr.
- Existing output behavior and tests remain valid.
- A repository search documents every intentional compatibility wrapper use.
- Important new integration behavior, including initialization, non-terminal fallback, refresh failure, and output-helper errors, has boundary tests. Do not require tests that only mirror trivial implementation branches.

## Error coverage

| Failure | Expected behavior | Test |
|---|---|---|
| Initial dimensions unavailable | Safe fallback without query failure where current behavior allows | querier setup test |
| Output is not a terminal | Raw/noninteractive path is selected | output mode tests |
| Width is narrow | Truncation and wrapping remain bounded | utility and command tests |
| width-aware collaborator returns an error | Caller follows the documented fallback and does not emit malformed output | collaborator error tests |

## Implementation notes

### Snapshot lifecycle (R1-03)

`utils.SessionDimensions(w)` (internal/utils/dimensions.go) is clai's single
width-discovery point. It binds to the writer's fd, so the observed size
matches the file clai actually writes to (R2-02). A non-terminal writer, a
failed read, or a reported zero size deterministically yields
`dimensions.Fallback` (80x24); no error propagates, matching the legacy
silent fallback.

- Session owner: `Querier` owns the snapshot for text-query sessions
  (`NewQuerier` resolves `utils.SessionDimensions(output)` once);
  `ChatHandler` owns it for chat-command sessions (`New` resolves
  `utils.SessionDimensions(out)` once). The stored `dims` value is the one
  snapshot for the whole session; every listing/tool operation receives it
  (`cq.dims.Width`, `q.dims.Width`) instead of taking a fresh one, so one
  complete operation renders with one resolved dimension set.
- Standalone render operations (glow sizing in `AttemptPrettyPrint`, photo
  animation, `clai tools` listing, MCP debug output) resolve one snapshot at
  the start of the operation from the writer they actually write to
  (`os.Stdout` or the passed writer).
- No live refresh exists in phase 3: the snapshot is static for the session.
  SIGWINCH refresh (a fresh snapshot replacing the stored one) is phase 5;
  `dimensions.Viewer.Snapshot` already returns the last valid snapshot or
  `Fallback` plus a wrapped `ErrUnavailable` on failure, so the
  preserve-last-valid-value rule is inherited unchanged.

### Width-source inventory (R1-05)

| Production width source (before) | Replacement | Where |
|---|---|---|
| `Querier.termWidth` cached from `table.TermWidth()` | `Querier.dims` from `utils.SessionDimensions(output)` | querier_setup.go, querier.go, tool_executor.go, session_runner.go |
| `toolDisplayWidth()` fallback chain (cached width, then `table.TermWidth`, then 80) | removed; callers use `q.dims.Width` | tool_executor.go |
| `table.TermWidth()` wide-table gate in `listChats` | `cq.dims.Width > 120` | handler_list_chat.go |
| `table.WidthAppropriateStringTrunc` for chat summaries | `WidthAppropriateStringTruncWithWidth(..., cq.dims.Width)` | handler_list_chat.go |
| `table.WidthAppropriateStringTrunc` in `messagePickerRow` | `WidthAppropriateStringTruncWithWidth(..., width)` threaded from `cq.dims.Width` | handler_list_chat.go |
| `table.WidthAppropriateStringTrunc` in `initialPrompt` | `WidthAppropriateStringTruncWithWidth(..., width)` threaded from `cq.dims.Width` | handler_dir.go |
| `table.WidthAppropriateStringTruncColored` in the obfuscated bridge | `WidthAppropriateStringTruncColoredWithWidth(..., width)` threaded from `cq.dims.Width` | obfuscated_print.go, handler.go |
| `table.TermWidth()` in photo animation | `utils.SessionDimensions(os.Stdout).Width` | funimation_0.go |
| `table.TermWidth()` in glow sizing | `utils.SessionDimensions(w).Width` | print.go (`AttemptPrettyPrint`) |
| `table.WidthAppropriateStringTrunc` in the `clai tools` listing | `WidthAppropriateStringTruncWithWidth(..., utils.SessionDimensions(os.Stdout).Width)` | tools/cmd.go |
| `table.WidthAppropriateStringTrunc` in MCP debug output | `WidthAppropriateStringTruncWithWidth(..., utils.SessionDimensions(os.Stdout).Width)` | tools/mcp/tool.go |

Intentionally excluded from the inventory: the go_away_boilerplate
compatibility wrappers `table.TermWidth`,
`table.WidthAppropriateStringTrunc`, and
`table.WidthAppropriateStringTruncColored` remain for external consumers but
have no clai production caller; a repository search confirms the only
remaining matches are in worklog/architecture documents. Non-interactive
commands (`clai photo`, `clai tools`, MCP debug output) resolve one
standalone snapshot from stdout and never start a signal watcher.

### Error coverage mapping

| Failure | Behavior | Test |
|---|---|---|
| Initial dimensions unavailable | `NewQuerier` succeeds and stores `dimensions.Fallback` | `Test_Querier_NewQuerier_dimsBoundToOutputWriter` (querier_setup_test.go) |
| Output is not a terminal | Raw/noninteractive path is selected | existing `Test_Querier_SuppressCompletionNotification` and `Test_Querier_Query_DebugRedirectedOutputPrintsFinalAssistantAnswer` |
| Width is narrow | Truncation and wrapping stay bounded | existing `UpdateMessageTerminalMetadata`/`glowRenderArgs`/`PrintToolActivity` tests; `TestListChats_NarrowWidthShowsCostAndPrompt` now drives width through `cq.dims` |
| width-aware collaborator returns an error | Caller surfaces the error instead of malformed output | new `TestAttemptPrettyPrint_GlowFailureSurfacesError` and `TestPrintToolActivity_WriterErrorSurfaces` (utils) |

### Commands and results

Baseline (start of session, in-flight phase-3 work from the previous session):

```bash
cd /home/imago/Projects/public/clai && go build ./...
```

Failed: `internal/photo/funimation_0.go:25:5: undefined: fmt` (import
removed while `fmt.Printf` remained), and `go vet ./...` additionally
reported the `messagePickerRow` arity change and the removed `termWidth`
field against tests. These were the known in-flight breakages.

After the change:

```bash
go test ./... -race -cover -count=3 -timeout=30s
```

All packages ok, exit 0 (root package 54.9% coverage; internal/text 71.8%;
internal/chat 70.7%; internal/utils 76.5%; internal/tools 82.3%;
internal/photo 13.6%).

```bash
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
```

All clean; dupl reports only pre-existing clones, none in phase-3 files.

## Review findings (review 1, 2026-08-07)

**R1-03 — Major — resolved in implementation notes.** [x] The snapshot
lifecycle is defined: `Querier`/`ChatHandler` own one snapshot per session
resolved from the session writer's fd; standalone operations resolve one
snapshot from the writer they use; listing/tool operations receive the
session snapshot; phase 5 owns live refresh and inherits the
preserve-last-valid-value rule from `dimensions.Viewer.Snapshot`.

**R1-05 — Normal — resolved in implementation notes.** [x] The width-source
inventory table lists every production width source and its replacement and
names the intentionally excluded compatibility wrappers.

Verified good: the phase preserves raw/structured safety and prohibits vendor
changes, matching the existing streaming architecture.

## Review findings (review 2, 2026-08-07)

Cross-reference R2-02 (phase 1): the snapshot fd must equal the session writer fd. Today `table.TermWidth` queries stderr while clai writes to `querier.out`, so `clai 2>/dev/null` on a wide terminal renders at the 80-column fallback. Add the acceptance criterion "the dimensions snapshot is taken from the session output writer's fd".