# Phase 6 — Surfaces

**Status:** Complete

[← README](./README.md)

## Goal

Render the title everywhere a conversation is labelled, and the summary
where the README specifies it, with unlabelled chats unchanged.

## Specification

One helper, `labelFor(title, firstUserMessage string) string` in
`internal/chat/label.go`, returns the title when non-empty, else the first
user message. Every surface below calls it.

| Surface                          | File                                   | Change                                                                                                                                                    |
| -------------------------------- | -------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `chat list` row (narrow and wide) | `internal/chat/handler_list_chat.go`  | `chatListRow` gains `Title`, `Summary`; native rows copy them from the index row; the prompt column renders `labelFor` through the existing width truncation |
| Group row                        | same                                   | Label is the newest member's `labelFor`; aggregates unchanged                                                                                             |
| Chat info (`printChatInfoCommon`) | same                                  | Labelled chat: `title: <title>` then `summary: <summary>` lines; unlabelled chat: today's `summary: "<first message…>"` line verbatim                     |
| Foreign chat info                | same                                   | Unchanged (no title source)                                                                                                                               |
| `chat dir` (v1)                  | `internal/chat/handler_dir.go`         | JSON gains `title`, `summary` (`omitempty`); pretty output adds `title:` and `summary:` lines after `prompt:` only when present                            |
| `chat dirv2`                     | same                                   | JSON gains the same two keys; pretty output as v1; `version` stays                                                                                        |
| Lookback block                   | `internal/chat/dirscope_lookback.go`   | Element text is `title: summary` when the row has a title (`title` alone when the summary is empty), else `previewOf(first, lookback-preview-runes)`       |
| `search_conversations` rows      | —                                      | Unchanged (out of scope)                                                                                                                                  |
| Obfuscated preview (`chat continue`) | —                                  | Unchanged                                                                                                                                                 |

Substring filtering (`/`) in `chat list` is implemented by the `table`
package of `go_away_boilerplate` over the rendered rows, so a title becomes
searchable with no clai-side change; `TestListChats_filterMatchesTitle`
drives it through the macro reader with a `/` input.

## Integration contract

| Trigger                                                                                                   | Collaborators / fakes                        | Observable result                                                                                                | Required side effects | Prohibited side effects            |
| --------------------------------------------------------------------------------------------------------- | -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | --------------------- | ---------------------------------- |
| Seed one labelled (`title: "Fix auth"`) and one unlabelled conversation; `clai -n -r c l q`               | e2e temp config dir                          | Row for the labelled chat shows `Fix auth`; the other shows its first message                                    | Index rebuilt         | None                               |
| Seed a labelled chat, bind CWD to it; `clai -r c dirv2`                                                   | e2e temp config dir                          | JSON has `"title":"Fix auth"` and `"summary":…`; `version` unchanged                                             | None                  | No config dir creation             |
| Same; `clai -r c dir`                                                                                     | e2e temp config dir                          | v1 JSON has the two keys                                                                                         | None                  | None                               |
| Same binding with history; `clai -cm test -lb -t ls q "hello"`                                            | Mock vendor                                  | Persisted system message contains `<conversation … >Fix auth: <summary></conversation>`                          | As today              | None                               |
| Same with the chat unlabelled                                                                             | Mock vendor                                  | Element text is the first-message head as today                                                                  | As today              | None                               |
| `clai -n -r c l 0 q` on the labelled chat                                                                 | e2e temp config dir                          | Info view shows `title:` and `summary:` lines                                                                    | None                  | None                               |

## Acceptance criteria

| Outcome                                                          | Test                                                                                           |
| ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `labelFor` precedence                                            | `TestLabelFor` (`internal/chat/label_test.go`)                                                 |
| List row and group row render the label                          | `TestListChats_rowShowsTitle`, `TestListChats_groupRowShowsNewestTitle` (`internal/chat/handler_list_chat_test.go`) |
| Substring filter matches the title                               | `TestListChats_filterMatchesTitle`                                                             |
| Chat info: labelled and unlabelled forms                         | `TestChatInfo_titleSummaryLines`, `TestChatInfo_unlabelledUnchanged`                           |
| `chat dir`/`dirv2` JSON and pretty                               | `TestDirInfo_titleSummary`, `TestDirInfoV2_titleSummary` (`internal/chat/handler_dir_test.go`) |
| Lookback block forms                                             | `TestBuildLookbackDescriptor_titleSummary`, `TestBuildLookbackDescriptor_fallback` (`internal/chat/dirscope_lookback_test.go`) |
| Integration rows                                                 | `Test_e2e_chat_list_shows_title`, `Test_e2e_chat_dirv2_title_summary`, `Test_e2e_chat_dir_title_summary`, `Test_e2e_lookback_block_title_summary`, `Test_e2e_chat_info_title_summary` (`main_summary_e2e_test.go`) |
| Existing list, dir, dirv2 and lookback e2e stay green            | Existing suites                                                                                |

Files: `internal/chat/label.go`, `internal/chat/label_test.go`,
`internal/chat/handler_list_chat.go`, `internal/chat/handler_list_chat_test.go`,
`internal/chat/handler_dir.go`, `internal/chat/handler_dir_test.go`,
`internal/chat/dirscope_lookback.go`, `internal/chat/dirscope_lookback_test.go`,
`main_summary_e2e_test.go`.

## Error coverage

| Failure                                                      | Expected outcome                                          | Test                                         |
| ------------------------------------------------------------ | --------------------------------------------------------- | -------------------------------------------- |
| Title present, summary empty (partial data)                  | Row and info show the title; lookback shows the title alone | `TestBuildLookbackDescriptor_titleSummary`, `TestChatInfo_titleSummaryLines` |
| Index row lacks the fields (pre-feature cache)               | Falls back to the first message                           | `TestListChats_rowShowsTitle`                |
| Title wider than the column                                  | Existing width truncation applies                         | `TestListChats_rowShowsTitle`                |

## Implementation notes

Session `session_01KXDFdab6MmuuKetC1EBowJ` (phase-6 worker), 2026-09-09T16:43Z.

Deltas from the specification:

- Chat info, partial label: a chat with a title and an empty summary prints
  the `title:` line only; no `summary:` line and no first-message fallback
  (`TestChatInfo_titleSummaryLines`). The labelled `title:` and `summary:`
  lines go through the same width truncation as today's quoted line, so a
  two-sentence summary never wraps the fixed-height info screen.
- `chatInfoHeight(chat)` replaces the `chatInfoPrintHeight` constant at the
  three native `ClearTermTo` sites (back, edit, delete): a labelled chat with
  a summary prints one line more than an unlabelled one, and the constant
  would leave that line on screen. The foreign site keeps the constant
  (foreign chats carry no title). Covered by
  `TestChatInfoHeight_labelledSummaryAddsOneLine`.
- `lookback-preview-runes` is now the named constant `lookbackPreviewRunes`
  (80) in `dirscope_lookback.go`; `lookbackLabel(row)` holds the element-text
  precedence so `BuildLookbackDescriptor` stays one line at the call site.
- `chat dir`/`dirv2` pretty output: `labelOutput(title, summary)` follows the
  `profileOutput` pattern (a `\n`-prefixed fragment on the `prompt:` line),
  so both formats change by one `%v` and the `version` field is untouched.
- e2e width: under captured stdout `SessionDimensions` resolves to
  `dimensions.Fallback` (80 columns), where the narrow table's prefix leaves
  the prompt column empty. `Test_e2e_chat_list_shows_title` widens the
  fallback to 200 for its duration (`wideFallback`); no production change.
- e2e lookback oracle: conversation files are JSON-marshalled with HTML
  escaping (`<` and `>` become `\u003c`/`\u003e`), so the row asserts on
  the decoded system message (`newestSystemMessage`), not on the raw file.
  The unlabelled-row contract (integration row five) is proved in the same
  test: the first `-lb` run renders `>seed query</conversation>`, the second
  renders `>Fix auth: Token refresh fixed.</conversation>` next to
  `>hello</conversation>`.
- Pre-feature cache row (error coverage): `TestListChats_rowShowsTitle`
  rewrites the v2 cache with the label fields cleared and lists again; the
  row falls back to the first message with no rebuild (v2 caches are read
  verbatim).

Verification (all from the repository root):

```bash
go test ./internal/chat/ -run 'TestLabelFor|TestListChats_rowShowsTitle|TestListChats_groupRowShowsNewestTitle|TestListChats_filterMatchesTitle|TestChatInfo_titleSummaryLines|TestChatInfo_unlabelledUnchanged|TestDirInfo_titleSummary|TestDirInfoV2_titleSummary|TestBuildLookbackDescriptor_titleSummary|TestBuildLookbackDescriptor_fallback|TestChatInfoHeight_labelledSummaryAddsOneLine' -count=1 -v
go test . -run 'Test_e2e_chat_list_shows_title|Test_e2e_chat_dirv2_title_summary|Test_e2e_chat_dir_title_summary|Test_e2e_lookback_block_title_summary|Test_e2e_chat_info_title_summary' -count=1 -v
```

Both PASS. Gates:

| Gate        | Command                                                  | Result                                                                 |
| ----------- | -------------------------------------------------------- | ---------------------------------------------------------------------- |
| Format      | `go run mvdan.cc/gofumpt@latest -w -l .`                 | exit 0, no files listed                                                |
| Staticcheck | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | exit 0, no findings                                                    |
| Lint        | `go vet ./...`                                           | exit 0                                                                 |
| Fix         | `go fix ./...`                                           | exit 0, no rewrites                                                    |
| Dupl        | `go run github.com/mibk/dupl@latest -t 80 .`             | `Found total 31 clone groups.` (baseline; none in phase-6 code)         |
| Test        | `go test ./... -race -cover -count=3 -timeout=30s`       | exit 0, 45 `ok`, no FAIL; root 21.3 s, `internal/chat` 75.2% (was 74.4%) |

Function coverage of the new code (`go tool cover -func`): `labelFor`,
`lookbackLabel`, `labelOutput`, `chatInfoLabel`, `chatInfoHeight` 100%.

## Review findings

### Review 2 — 2026-09-10 (`worklog-review`, post-implementation)

- **R2-05 (Note)** — The group row copies the newest member's label
  (`internal/chat/handler_list_chat.go:buildGroupRow`), exactly as the
  surface table specifies. When a parent is labelled by a later batch run
  and an earlier `-re` fork (the newest member) predates that label, the
  group row shows the first message although a title exists in the group.
  Suggested follow-up outside this worklog: "newest labelled member".
  Contract met; no change required.

Verified good:

- `labelFor` is the single precedence rule; the list row, group row, info
  view (`title:`/`summary:` lines, `chatInfoHeight` adds one line only
  when both are present), `chat dir`/`dirv2` (`omitempty` keys, pretty
  lines after `prompt:`, `version` untouched) and the lookback element
  (`title: summary`, title alone, else the preview) each render it once.
- The lookback element text is not escaped, which matches today's
  `previewOf` behaviour for the first message; not a regression.
