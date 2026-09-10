# Phase 3 — Model and index fields

**Status:** Complete

[← README](./README.md)

## Goal

Give `Chat` and the chat index the title, summary and timestamp fields, and
make every existing persist path carry them through unchanged.

## Specification

### `pub_models.Chat`

`pkg/text/models/chat.go` gains, after `GroupKey`:

```go
Title     string    `json:"title,omitempty"`
Summary   string    `json:"summary,omitempty"`
SummaryAt time.Time `json:"summary_at,omitzero"`
```

`SummaryAt` is stamped by the caller from `Summary.GeneratedAt` (D16). No
method computes any of the three.

### Index row

`internal/chat/index.go`:

- `chatIndexRow` gains `Title`, `Summary` (`omitempty`) and
  `Updated time.Time` (`json:"updated,omitzero"`).
- `chatIndexRowFromChat` mirrors `Title` and `Summary`.
- `upsertChatIndex` stamps `row.Updated = time.Now().UTC()` on every
  upsert.
- `rebuildChatIndex` stamps `Updated` from the conversation file's
  modification time (`DirEntry.Info().ModTime()`), zero when unavailable.
- Readers treat a zero `Updated` as `Created` (helper
  `chatIndexRow.effectiveUpdated()`); no `chatIndexVersion` bump (D14).
  Legacy caches decode with zero `Updated`.

### Persist paths

| Path                                            | Today                                                      | Change                                                                                                     | Test                                             |
| ----------------------------------------------- | ---------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| `chat.Save` (source conversation)               | Marshals the whole struct                                   | None; verified                                                                                             | `TestSave_roundTripsTitleSummary`                |
| `SaveAsPreviousQuery` → `globalScope.json`      | Explicit field copy                                         | Copy `Title`, `Summary`, `SummaryAt`                                                                       | `TestSaveAsPreviousQuery_mirrorCarriesFields`    |
| `SaveAsPreviousQuery` → promotion copy          | Explicit field copy (dead on the query path, kept)          | Copy the three fields                                                                                      | `TestSaveAsPreviousQuery_promotionCarriesFields` |
| `SetupInitialChat` reply branch (`-re`)         | Adopts `Created`, `Messages`, `Queries` from the mirror; the mirror's id is `globalScope`, so a fresh id is generated and the run forks | Also adopt `Title`, `Summary`, `SummaryAt` when the mirror has them, so the fork inherits its parent's label (D28) | `TestSetupInitialChat_replyAdoptsFields`         |
| `-dre` (`LoadDirScopedContext`)                 | Loads the full file                                         | None; verified                                                                                             | `TestLoadDirScopedContext_keepsFields`           |
| `chat continue` with a prompt                   | Appends in memory, binds, does not save                     | None; verified                                                                                             | existing                                         |
| `cloneForeignChat`                              | `Save`                                                       | None; clone has no title                                                                                   | existing                                         |
| Index upsert / rebuild                          | Mirrors selected fields                                     | Mirror the two fields; stamp `Updated`                                                                     | `TestChatIndex_mirrorsTitleSummary`, `TestUpsertChatIndex_stampsUpdated`, `TestRebuildChatIndex_updatedFromModTime` |

`EnsureOriginDir` is untouched; it already reads the existing file only
when `OriginDir` is empty.

## Integration contract

| Trigger                                                                                           | Collaborators / fakes                                   | Observable result                                                                            | Required side effects                                        | Prohibited side effects                              |
| ------------------------------------------------------------------------------------------------- | ------------------------------------------------------- | -------------------------------------------------------------------------------------------- | ------------------------------------------------------------ | ---------------------------------------------------- |
| Seed `conversations/<id>.json` with `title: "T"`, `summary: "S"`, `summary_at`, and a `globalScope.json` mirror of it (`id: globalScope`); run `clai -cm test -re q "more"` | Mock vendor; e2e temp config dir | A second conversation file exists whose id differs from the seed, holding `T`, `S`, the seed's `summary_at`, the seed's messages and the new turn; the seed file is byte-identical to before | Mirror rewritten with the fields | The seed file is not modified; the dirscope binding is not written (today's `-re` rule); no summarizer run (phase 4 confirms through the launch table) |
| Seed the same conversation, bind the temp CWD to it; run `clai -cm test -dre q "more"`            | Mock vendor; e2e temp config dir                        | File keeps `T`, `S`, `summary_at`                                                             | Binding history bumped as today                              | None beyond today's                                  |
| Delete `chat_index.cache`; run `clai -n -r c l q` (read-only: rebuilds in memory, writes no cache), then `clai -r -cm test q "hello"` whose `chat.Save` performs the persisted rebuild (D30) | e2e temp config dir with two seeded files, one labelled; mock vendor | The list run writes no cache; the cache written by the save holds `title`/`summary` for the labelled row only, an `updated` equal to the file's mtime for both seeds, and an upsert-time `updated` for the new row | Cache written by the save | The list run writes nothing |

## Acceptance criteria

| Outcome                                                                          | Test                                                                                                   |
| -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| Fields round-trip through JSON; zero `SummaryAt` is omitted                      | `TestChat_titleSummaryRoundTrip`, `TestChat_summaryAtOmittedWhenZero` (`pkg/text/models/chat_test.go`) |
| Index mirrors the fields; legacy cache decodes with zero `Updated`               | `TestChatIndex_mirrorsTitleSummary`, `TestReadChatIndex_legacyRowsZeroUpdated`                          |
| Upsert stamps `Updated`; rebuild uses mtime; zero falls back to created          | `TestUpsertChatIndex_stampsUpdated`, `TestRebuildChatIndex_updatedFromModTime`, `TestChatIndexRow_effectiveUpdated` |
| Every persist-path row above holds                                               | The tests named in the persist-path table                                                              |
| Integration rows one and two                                                     | `Test_e2e_reply_preserves_title_summary`, `Test_e2e_dirreply_preserves_title_summary` (`main_summary_e2e_test.go`) |
| Integration row three                                                            | `Test_e2e_chat_list_rebuild_mirrors_title` (`main_summary_e2e_test.go`)                                |

Files: `pkg/text/models/chat.go`, `pkg/text/models/chat_test.go`,
`internal/chat/index.go`, `internal/chat/index_test.go`,
`internal/chat/reply.go`, `internal/chat/reply_test.go`,
`internal/chat/dirscope_test.go`, `internal/text/conf.go`,
`internal/text/conf_reply_test.go`, `main_summary_e2e_test.go`.

## Error coverage

| Failure                                                          | Expected outcome                                                   | Test                                          |
| ---------------------------------------------------------------- | ------------------------------------------------------------------ | --------------------------------------------- |
| Mirror lacks the fields (pre-feature `globalScope.json`)         | `-re` adopts nothing; the fork starts unlabelled                    | `TestSetupInitialChat_replyAdoptsFields`      |
| Mirror carries a non-`globalScope` id (hand-edited file)         | `LoadGlobalScope` normalizes the id, so the reply still forks under a fresh id; the fields are adopted the same way (D30) | `TestSetupInitialChat_replyAdoptsFields`      |
| Rebuild cannot stat a file                                       | Row indexed with zero `Updated`; rebuild continues                 | `TestRebuildChatIndex_updatedFromModTime`     |
| Cache row has `updated` but the key is malformed                 | Existing corrupted-cache path: full rebuild                        | existing `readChatIndex` tests                |

## Implementation notes

### 2026-09-09 — phase-3 worker (claude-fable-5-1, session_01KXDFdab6MmuuKetC1EBowJ), 18:16–18:40 UTC

Tests written first (all red at compile time), then the four production
edits. Deltas from the specification:

- `TestSave_roundTripsTitleSummary` lives in `internal/chat/reply_test.go`
  (the persist-path file) because `internal/chat/chat_test.go` is not in
  the phase file list; the shared fixtures `labelledChat`/`assertLabelled`
  there are reused by `dirscope_test.go`.
- Error row two ("Mirror carries a non-`globalScope` id … Today's guard
  adopts the id") is not what the code does: `chat.LoadGlobalScope`
  normalizes any id in `globalScope.json` to `globalScope` before
  `SetupInitialChat` sees it, so the id guard never fires and the reply
  always forks under a fresh id (consistent with the README evidence
  bullet, not with the row). `TestSetupInitialChat_replyAdoptsFields`
  asserts a fresh fork id in every case and field adoption in both
  labelled cases; the row's observable that matters — the fields are
  adopted the same way — holds.
- Adoption is guarded on `InitialChat.Summary == "" && iP.Summary != ""`
  so a pre-feature mirror adopts nothing and a caller-populated label
  is never overwritten.
- Integration row three names `clai -n -r c l q` as the trigger and
  "Cache written" as a required side effect. `chat list` is structurally
  read-only (`readOnlyChatSetup` sets `utils.NoCreateConfig`,
  `internal/chat/cmd.go`; `rebuildChatIndex` then skips
  `writeChatIndex`, pinned by the existing
  `TestReadChatIndex_ReadonlySilentAndNoPersist`), so that trigger can
  never persist a rebuilt cache. `Test_e2e_chat_list_rebuild_mirrors_title`
  runs the spec's trigger first and asserts no cache is written, then
  runs `clai -r -cm test q rebuild trigger`, whose `chat.Save` performs
  the persisted rebuild (`upsertChatIndex` → `readChatIndex` → missing
  cache → `rebuildChatIndex` → `writeChatIndex`), and asserts the row's
  observables: `title`/`summary` on the labelled row only, `updated`
  equal to each seeded file's mtime, an upsert-time `updated` on the new
  row. Reported as a specification gap in the README session journal;
  the row's trigger text needs amending or the deviation accepting.
- The unstat-able-file error row is proven through a small seam,
  `entryModTime(fs.DirEntry) time.Time`, called with a `DirEntry` whose
  `Info()` fails (a real `ReadDir` entry cannot be made to fail
  deterministically inside the loop). Mtimes are stored as UTC, matching
  the upsert stamp.
- `upsertChatIndex` stamps `Updated` on the row after
  `chatIndexRowFromChat`, so the batch writer phase 5 adds can reuse the
  same builder and stamp its own time.

Verification (all from the repository root):

```bash
go test ./pkg/text/models/ ./internal/chat/ ./internal/text/ -race -count=1 -timeout=30s \
  -run 'TestChat_|TestChatIndex|TestReadChatIndex_legacy|TestUpsertChatIndex_stamps|TestRebuildChatIndex_updated|TestChatIndexRow_|TestSave_roundTrips|TestSaveAsPreviousQuery_|TestLoadDirScopedContext_|TestSetupInitialChat_reply'
go test . -race -count=1 -timeout=30s -run 'Test_e2e_reply_preserves_title_summary|Test_e2e_dirreply_preserves_title_summary|Test_e2e_chat_list_rebuild_mirrors_title' -v
```

Both `ok`; every named test passes. Coverage of the touched functions
(`go tool cover -func`): `effectiveUpdated` 100%, `chatIndexRowFromChat`
100%, `entryModTime` 100%, `rebuildChatIndex` 94.0%, `upsertChatIndex`
88.9%, `SaveAsPreviousQuery` 78.6%, `SetupInitialChat` 75.0%.

```bash
go run mvdan.cc/gofumpt@latest -w -l .            # no output, exit 0
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # no output, exit 0
go vet ./...                                       # exit 0
go fix ./...                                       # exit 0
go run github.com/mibk/dupl@latest -t 80 .         # "Found total 31 clone groups", none in phase files
go test ./... -race -cover -count=3 -timeout=30s
```

First full gate run at load average 20–24: 41 packages `ok`; the root
package and `internal/audio` hit the 30 s alarm, and
`internal/tools/mcp` failed `TestMcpTool_CallWithContext_CancelBeforeSend`
(timing) — the same three packages the phase-1 journal recorded, none
touched by this phase. `go test . ./internal/audio ./internal/tools/mcp
-race -cover -count=3 -timeout=30s` alone: all three `ok` (root 27.8 s).
Second attempt (background, gated on load below 12) was killed by the
orchestrator before it produced output. Foreground reruns on the
orchestrator's instruction:

```bash
uptime   # load 24.4 / 16.1 / 12.2 at start, 29.8 at end
go test ./... -race -cover -count=3 -timeout=30s   # exit 1: 42 ok; FAIL root (30.216s, alarm in Test_e2e_async_long_running_logs), FAIL internal/audio (30.152s, alarm in TestAssemblerCleansUpOnSuccessAndError); internal/tools/mcp ok
make lint                                          # exit 0, no output
go vet ./...                                       # exit 0
go run github.com/mibk/dupl@latest -t 80 .         # Found total 31 clone groups (pre-existing; none in phase files)
uptime   # load 12.6 / 15.7 / 12.6 at start, 18.6 at end
go test ./... -race -cover -count=3 -timeout=30s   # exit 1: 42 ok; FAIL root (30.311s) and internal/audio (30.295s), both 30 s alarms; internal/tools/mcp ok (18.5s)
```

The two failing packages are untouched by this phase; both pass alone at
`-count=3` (root 27.8 s of the 30 s budget, `internal/audio` 23.5 s), so
the failures are the package wall-clock exceeding the alarm under host
load, the same signature the phase-1 journal recorded. The phase is left
In Progress until the full gate is rerun green on a quiet machine (no
code change expected) and the row-three trigger deviation is accepted or
the row amended.

### 2026-09-09 18:45Z — orchestrator sign-off (same session)

- Spec corrections accepted as D30: integration row three and error row
  two rewritten to the observed behavior; the tests already prove the
  rewritten rows.
- Gate evidence: `go test ./... -race -cover -count=3 -timeout=30s -p 1`
  → exit 0, every package `ok` (root 25.3 s, `internal/audio` 13.7 s).
  The same command without `-p 1` timed out in root and `internal/audio`
  on four consecutive runs at host load 9–16 with 4.5 GB of swap in use
  (external to this session; the sandbox shows no local CPU consumer),
  while the identical command passed after phase 1 at 18:13Z. Every
  other gate (`make lint`, `go vet`, `dupl`) clean. Phase set to
  Complete on the `-p 1` evidence; the unedited parallel gate is rerun at
  every later phase boundary and must be green in the phase 7 sweep.

## Review findings

### Review 2 — 2026-09-10 (`worklog-review`, post-implementation)

No findings.

Verified good:

- `Chat.Title`/`Summary`/`SummaryAt` (`omitzero`) and `QueryCost.Purpose`
  are additive; the mirror copy and the promotion copy in
  `internal/chat/reply.go` carry all three; the `-re` branch of
  `SetupInitialChat` adopts them only when the fork has none and the
  mirror has a summary (`internal/text/conf.go`).
- The index row carries `title`, `summary`, `updated` without a version
  bump; `effectiveUpdated` falls back to `created`; rebuild stamps from
  the file mtime through `entryModTime`; upsert stamps now; the model
  resolver skips `Purpose` rows (`internal/chat/index.go`).
- D30 is reflected in the e2e: `chat list` never persists a rebuilt
  cache; the next `chat.Save` does.
