# Phase 5 — Chat list: previous-query line and group totals

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

`clai chat list` names the conversation a plain `-re` continues on a line above the table, and its group rows count each spend once by totalling every member's own spend, so a family of forks no longer multiplies the cost of the history they share.

## Interface

CLI: `clai chat list` (text output), implemented in `internal/chat/handler_list_chat.go` (render loop, `buildGroupRow`, `chatListRow`), `internal/chat/list_continues.go` (new: the line). The index fields behind the totals are implementation (Local facts).

### CLI

```text
clai chat list           # the line, then the header, then the rows
clai -r chat list        # same, plain text
clai -json ... chat list # unchanged: no line

-re continues: 0 · fork verb plan
Index | Source | Created | Model | Cost | About
...
```

- `list.1` The line is written to the list's output (`cq.out`) before the table header on every render of the table: the first draw, every page change, every toggle (dir filter, foreign), and the group view. It is one line ending in a newline; with no record there is no line and no blank line (P11).
- `list.2` Wording (P11, `<label>` is the row's `Title`, else its first user message, through `labelFor`):
  - the record's chat is a row of the shown list: `-re continues: <index> · <label>`;
  - it is a member of a collapsed group row of the shown list: `-re continues: <index> [group] · <label>`;
  - it exists but is not in the shown rows (a dir filter, a hidden foreign view, another group's view): `-re continues: <chatID> · <label> (not in this view)`;
  - no record (`ErrNoPreviousQuery`), or the record's conversation is gone (`ErrPreviousQueryGone`): no line;
  - the record cannot be read (a decode error, an unsupported version, an unreadable conversation file): `-re continues: unreadable record (<reason>)`, `<reason>` the error's one-line text through `utils.SanitizeTerminalRow`; the list still draws and exits 0.
  - `<index>` is the index the table prints for that row, so `clai chat continue <index>` continues the chat the line names.
- `list.3` `chat list` writes nothing for the line (I9): it reads the record through `previous-query-pointer:LoadPreviousQuery (import)` only, never calls `MigratePreviousQuery`, never clears or rewrites the record, and never creates `conversations/state/`. A legacy mirror (`globalScope.json`, `prevQuery.json`) with no record yields no line and stays on disk.
- `list.4` The `-json` mode, `chat dir`, `chat dirv2`, `-re`, `-dre` and `chat continue` output are unchanged.
- `list.5` A group row's Cost and Tokens are the sum of its native members' own spend (I10); a foreign member contributes zero, as before. An ungrouped row, a group's member row (the group view), `chat dir` and `chat dirv2` show a chat's whole history as before. A group of a chat and its forks totals the family's spend once: the parent's queries are counted in the parent's own spend, and the fork's inherited copies of them are not.

- `list.6` (I10) The chat index records each chat's own spend (`chatOwnSpend`), so a group total is an index read. A cache written before this phase is rebuilt on its first read (in memory when read-only), and the rebuilt rows carry own spend.

### Read-only line

`internal/chat/list_continues.go` (new). These are the members the clauses above are built on.

```go
// previousQueryKind is what a read-only look at the record found.
type previousQueryKind int

const (
	prevNone       previousQueryKind = iota + 1 // no record, or its conversation is gone
	prevNamed                                   // the record names a readable conversation
	prevUnreadable                              // the record or its conversation cannot be read
)

// previousQueryState is what that look found.
type previousQueryState struct {
	Kind   previousQueryKind
	Chat   pub_models.Chat // prevNamed
	Reason string          // prevUnreadable
}

// readPreviousQueryState maps LoadPreviousQuery onto a state: ErrNoPreviousQuery and ErrPreviousQueryGone
// are prevNone, every other error is prevUnreadable. It is read once per list invocation, before the
// render loop.
func readPreviousQueryState(ctx context.Context, confDir string) previousQueryState

// previousQueryLine is the text list.2 asks for, "" when there is none (without the newline).
func previousQueryLine(state previousQueryState, shown []chatListRow, inGroupView bool) string
```

Consumes:

- `previous-query-pointer:LoadPreviousQuery`, `previous-query-pointer:PreviousQuery`, `previous-query-pointer:ErrNoPreviousQuery`, `previous-query-pointer:ErrPreviousQueryGone` (import).
- `chatOwnSpend`, `OwnSpend` (phase 2): the own-spend rule (`ownSpend.1` to `ownSpend.4`); this phase stores and totals it.
- `labelFor`, `prepareListRows`, `collapseGroupRows`, `buildGroupRow`, `chatIndexRowFromChat`, `readChatIndex`, `rebuildChatIndex`, `table.New` (repository code).

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1  | With a record naming the chat shown at index 0, the output starts with `-re continues: 0 · <label>` and the header follows on the next line | list.1, list.2, P11 | `TestChatList_continuesLine_ownRow` |
| 2  | A record naming a member of a collapsed group gives `<index> [group] · <label>` with the group row's index | list.2 | `TestChatList_continuesLine_inGroup` |
| 3  | A filter or hidden foreign view that hides the chat gives `<chatID> · <label> (not in this view)` | list.2 | `TestChatList_continuesLine_notInView` |
| 4  | No record, or a gone conversation: no line, byte-identical output to before this phase | list.2, P11 | `TestChatList_noLine` |
| 5  | A corrupt record, a record of another version: the unreadable line, the table still drawn, exit 0 | list.2 | `TestChatList_unreadableRecord` |
| 6  | The line precedes the header on every page and toggle render of a multi-page list | list.1 | `TestChatList_lineOnEveryRender` |
| 7  | `chat list` leaves the whole config dir byte-identical, with a read-only config dir and `NoCreateConfig` | list.3, I9 | `TestChatList_writesNothing` |
| 8  | A legacy mirror and no record gives no line and the mirror stays | list.3, I9 | `TestChatList_legacyMirrorNotMigrated` |
| 9  | `-json` output has no line | list.4 | `Test_e2e_chat_list_json_unchanged` |
| 10 | A parent and two forks of it form one group whose Cost and Tokens equal the parent's plus each fork's own queries, counted once | list.5, I10 | `TestBuildGroupRow_ownSpend` |
| 11 | The fork's own (group-view) row still shows its whole history | I10 | `TestChatList_groupViewShowsWholeHistory` |
| 12 | A cache written by the previous index version is rebuilt on first read; the rebuilt rows carry own spend | list.6, I10 | `TestReadChatIndex_olderVersionRebuilt` |
| 13 | Existing `chat list` expectations that gain the line, and existing group-total expectations, are updated and pass | list.1, list.5 | `make qa` |

## Test cases

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `TestChatList_continuesLine_ownRow` | command | list.1, list.2 | three chats, a record naming the second row | stdout begins `-re continues: 1 · <title>\n` then the header | print the line below the table |
| `TestChatList_continuesLine_inGroup` | command | list.2 | two chats sharing a `GroupKey` collapse to one row; the record names the older member | `-re continues: 0 [group] · <label>` | print the member's own index |
| `TestChatList_continuesLine_notInView` | command (table: dir filter on with the chat elsewhere; foreign hidden) | list.2 | a record naming a chat with another `OriginDir` | `-re continues: <chatID> · <label> (not in this view)` | omit the line |
| `TestChatList_noLine` | command (table: no record; record of a deleted chat) | list.2, list.4 | each case | the table output equals the pre-phase output byte for byte; no blank line | print a placeholder |
| `TestChatList_unreadableRecord` | command (table: invalid JSON; `version: 99`; a conversation file that is not JSON) | list.2 | each | `-re continues: unreadable record (<one line>)`, rows drawn, exit 0 | fail the list, or rewrite the record |
| `TestChatList_lineOnEveryRender` | command | list.1 | page size 2, 5 chats, macro input `n` then a toggle then `q` | the line appears once before each header | print it only on the first draw |
| `TestChatList_writesNothing` | command | list.3, I9 | a populated confDir made read-only (`chmod`), `NoCreateConfig`, a record | the tree (paths, bytes, mtimes) is identical after | create `conversations/state/` |
| `TestChatList_indexUnreadable` | command | list.1 | `chat_index.cache` replaced by a directory | the existing `failed to read chat index` error; no `-re continues` line before it | draw a partial table |
| `TestChatList_legacyMirrorNotMigrated` | command | list.3 | `globalScope.json` and no record | no line; the mirror and the absent record are as before | call `MigratePreviousQuery` |
| `TestPreviousQueryLine` | unit (table over `previousQueryState` x rows x group view) | list.2 | none, named own/group/hidden, unreadable with a multi-line reason | exact strings; the reason is one sanitised line | emit control characters |
| `TestReadPreviousQueryState` | unit | list.2 | a fake of `LoadPreviousQuery`'s error set: nil, `ErrNoPreviousQuery`, a wrapped `ErrPreviousQueryGone`, a decode error | `prevNamed`, `prevNone`, `prevNone`, `prevUnreadable` | treat the sentinel errors as unreadable |
| `TestChatIndexRowFromChat_ownSpend` | unit | list.6, I10 | the same chats | `TotalCostUSD`/`TotalTokens` are the whole history; `OwnTotal*` are `chatOwnSpend`'s; JSON uses `own_total_tokens`, `own_total_cost_usd`, omitted when zero | change `TotalTokens` |
| `TestBuildGroupRow_ownSpend` | unit | list.5 | group members built from a parent and two forks via `chatIndexRowFromChat` through `collapseGroupRows` | the group Cost/Tokens equal parent + fork A own + fork B own; a foreign member adds zero; `GroupMemberIDs` lists the natives | sum `TotalCostUSD` |
| `TestChatList_groupViewShowsWholeHistory` | command | list.5, I10 | enter the group view of that family | each member row's Cost is its whole history | show own spend in a member row |
| `TestReadChatIndex_olderVersionRebuilt` | unit | list.6 | a cache file with `version` one lower and rows without own fields | rebuilt from the files, version current, own fields present; in read-only mode in memory only | keep stale rows |
| `Test_e2e_chat_list_json_unchanged` | command (e2e) | list.4 | `-json chat list` with a record | no `-re continues` text in stdout; records as before | change JSON records |
| `make qa` | command (gate) | criterion 13 | the updated expectations | all gates pass | loosen an assertion |
| `TestExistingChatListExpectations` | command | list.1 | the existing `chat list` unit and e2e expectations that run after a query (`e2e/chat_list_e2e_test.go`, `internal/chat/handler_list_chat_test.go`, `list_continue_macro_test.go`) | updated by hand to the new first line; unrelated rows unchanged | loosen an assertion to `Contains` |

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| The record cannot be decoded, has another version, or names an invalid ID | the line `-re continues: unreadable record (<reason>)`; the list draws; exit 0 | `TestChatList_unreadableRecord` |
| The record's conversation file cannot be read for a reason other than absence | the same unreadable line | `TestChatList_unreadableRecord` |
| The record names a conversation that is gone, or there is no record | no line; exit 0 | `TestChatList_noLine` |
| The config dir is read-only | the list works; nothing is written (I9) | `TestChatList_writesNothing` |
| The chat index cannot be read | as today (`failed to read chat index`); no line is drawn before the error | `TestChatList_indexUnreadable` |
| A chat with queries but a zero `Created` | every query with a time counts as own (`Before(zero)` is false); no panic | `TestChatIndexRowFromChat_ownSpend` |

## Golden diff

| Golden | Change |
| ------ | ------ |
| `internal/chat/testdata/persister/save.golden` | index `version` 3 → 4; the row gains `own_total_tokens` and `own_total_cost_usd` |
| `internal/chat/testdata/persister/promote-new.golden` | index `version` 3 → 4 |
| `internal/chat/testdata/persister/promote-existing.golden` | index `version` 3 → 4 |

No existing `chat list` expectation gains the line: none runs with a previous-query record. The root's Golden diff records the net result.

## Removes

None.

## Local facts

- `ownSpend.2` (signed) leaves a local-only query with a zero `CreatedAt` uncounted on a fork, including a fork a `cc:` pull keeps; only legacy data without timestamps is affected (R1-63).
- Own spend is stored in the index row, not computed at render. The row gains `OwnTotalTokens int` (JSON `own_total_tokens`, omitted when zero) and `OwnTotalCostUSD float64` (JSON `own_total_cost_usd`, omitted when zero), written by `chatIndexRowFromChat` from `chatOwnSpend`. `chatListRow` gains `OwnTotalTokens`, `OwnTotalCostUSD` and `GroupMemberIDs []string`; `buildGroupRow` totals the own fields of its native members (foreign members add zero) and lists their IDs.
- `chatIndexVersion` goes from 3 (the tree's value, `internal/chat/index.go`, raised by `previous-query-pointer`) to 4, with the history line `4 — own_total_tokens and own_total_cost_usd added (I10): a cache of an older version is rebuilt`. If the constant already holds 4 when the phase starts, it is raised by one more. Tests name the constant, not a literal.
- The index is rebuilt once after the version bump, as every earlier bump did; `chat list` (read only) rebuilds in memory without writing it (`NoCreateConfig`), so the first read after upgrading is slower and the cache is written by the next writing verb.
- Own spend is stored in the row (not computed at render) so `list` stays an index read: it opens no conversation file but the previous-query chat's.
- A group row's Messages column still sums member message counts, so a fork's copied messages count again in the wide layout. I10 binds spend, not messages; left as is.
- A `LocalForked` copy is created with `Created` at its first query the cloud copy lacks (phase 3, `forkCopy`), so `chatOwnSpend` counts exactly its local-only queries.
- Group totals of rows built by hand in existing tests (`TotalTokens` and `TotalCostUSD` set, own fields absent) now total zero; those tests set the own fields to the same values. No fallback from own to total is added: it would double count a fresh fork whose own spend is legitimately zero.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                                                          | Result                                  |
| ----- | ----------- | ---------------------------------------------------------------- | --------------------------------------- |
| start | 615303b+wip | `make qa`; `go vet ./...`; dupl -t 80 (without `.claude`)        | qa exit 0 (104 packages ok), vet ok, 49 |
| end   | 615303b+wip | `make qa`; `go vet ./...`; dupl -t 80 (without `.claude`)        | qa exit 0 (104 packages ok), vet ok, 49 |
| end   | 615303b+wip | `make qa`                                                        | exit 0 (104 packages ok)                |
| fix-end | 615303b+wip | `make qa`; `go vet ./...`; dupl -t 80 (without `.claude`) | R1-60 to R1-63: qa exit 0, vet 0, 49 (tests `TestClearPreviousQueryLine`, `TestChatList_writesNothing` read-only, `TestReadPreviousQueryState_goneAndInvalid`, `TestGroupTotal_cloudLocalForkCountsEachSpendOnce`) |
| recovery | — | rebuilt from the review-1 transcript | this file was emptied at 13:51 by review 1's edit script (`write(s+'\n' if … else '')`); restored from that reviewer's full read plus its intended Review 1 section, then R1-61's Golden diff and R1-63's note added |

Andon: none.

Files: `internal/chat/list_continues.go` (new; the line, `previousQueryStateFor` returns no state under `-json`), `internal/chat/list_continues_test.go` (new), `internal/chat/index.go` (version 4, `OwnTotal*` row fields), `internal/chat/handler_list_chat.go` (`chatListRow` own fields and `GroupMemberIDs`, `buildGroupRow` totals own spend, line written before each `tb.Run()`), `architecture/chat.md`, `e2e/chat_list_e2e_test.go` (two new rows).

Surprises:

- No existing `chat list` expectation gained the line: none runs with a previous-query record. Updated for own fields or the version instead: `TestPrepareListRows_DirScopedGroups` and `TestCollapseGroupRows_BasicGrouping` (own fields set), `TestReadChatIndex_legacyRowsZeroUpdated` (version literal 3 to 4), `e2e/conversation/summary_e2e_test.go` `setupSummaryE2E` (seeded cache version 3 to 4), and the goldens `internal/chat/testdata/persister/{save,promote-new,promote-existing}.golden` (version 4, own fields).
- Under `-json` the table is a JSON event stream, so the line would appear as an event; `previousQueryStateFor` suppresses it there (list.4).
- The table prints its header once per `Run`, with no redraw on page change, so the line is printed once per `Run` (first draw, each toggle, group view); in interactive mode it is cleared after `Run` by `utils.TerminalRows` so toggles do not stack lines.

## Review findings

### Review 1, 2026-11-23

Ready: no blocker or major. Gates: `make qa QA_PROCS=8` twice exit 0 (no differing test), `go vet ./...` 0, dupl -t 80 (without `.claude`) 49 groups, unchanged.

- [ ] R1-60 minor, `internal/chat/list_continues.go:86-92`: `clearPreviousQueryLine` (interactive path, `cq.input == nil`) has no test. Every interactive toggle depends on it not stacking the line. Add a unit test that calls it with `input` nil and a buffer and asserts the `ClearTermTo(TerminalRows(line, width))` bytes, a wrapped line (Width small) and the no-line and macro (`input != nil`) cases write nothing. Read-through finds the arithmetic right: after `Run` the table's own clears leave the cursor on its first header row, and `ClearTermTo(r)` clears that row plus the `r` rows of the line, no row above (simulated against `table.go` and `term.go` of go_away_boilerplate v1.33.15).
- [ ] R1-61 minor, phase file `## Golden diff` and `internal/chat/index_test.go:584`: Golden diff says "None" but `testdata/persister/{save,promote-new,promote-existing}.golden` changed. The change follows legitimately: they embed `chat_index.cache`, so `version` 3 to 4 and the two `own_total_*` members are the only diff (verified for `save.golden`; nothing else differs). Amend the Golden diff row to name them (and `e2e/conversation/summary_e2e_test.go`'s seeded version), and `TestReadChatIndex_legacyRowsZeroUpdated` writes the literal `4` where the Local facts say tests name the constant: use `chatIndexVersion`.
- [ ] R1-62 minor, `internal/chat/list_continues_test.go:195`: `TestChatList_writesNothing` does not make the config dir read-only (`chmod`) as its row requires; it sets `NoCreateConfig`, compares bytes and mtimes, and removes `state/tmp` first. A write attempt into a writable dir is caught only if it changes a file, not if it is silently denied. `TestReadPreviousQueryState` also never passes a wrapped `ErrPreviousQueryGone`, and no row covers a record naming an invalid chat ID (Error management row 1).
- [ ] R1-63 minor, `internal/chat/list_continues_test.go:363`: I10 for a fork made by a `cc:` pull is proved only with hand-built rows. No test feeds a `LocalForked` copy (`forkCopy`, `Created` = first local-only query, `source`/`source_id` stamped, `group_key` kept) through `chatIndexRowFromChat` and `collapseGroupRows` beside the cloud copy. Add one so the group total of X and L equals X's queries plus L's local-only queries once. Traced by hand: `chatOwnSpend` gives X every query since X.Created and L only queries at or after its `Created`, so the total is right; a local-only query with a zero `CreatedAt` is skipped for a fork (`own_spend.go:26`) and is counted nowhere.

Verified good (note):

- P11 wording, every case, in code and `TestPreviousQueryLine`/e2e: own row (`<i> · <label>`), `[group]` (only outside the group view), `(not in this view)` (also inside another group's view), `prevNone` for no record and a wrapped `ErrPreviousQueryGone`, unreadable with a one-line `SanitizeTerminalRow` reason; the list still draws and exits 0.
- `-r` prints the same plain line (`Test_e2e_chat_list_continues_line`); `-json` reads no record and prints no line (`previousQueryStateFor`), `Test_e2e_chat_list_json_unchanged`.
- I9: `readPreviousQueryState` calls `LoadPreviousQuery` only (read and `Load`, no `MigratePreviousQuery`, no write); `TestChatList_legacyMirrorNotMigrated`, `TestChatList_noStateDirCreated`.
- I10: group row totals `OwnTotal*` of native members only (foreign add zero); a fork's inherited queries precede its `Created` and are not counted; the group view and own rows keep `TotalCostUSD`.
- Version 4 rebuild: older cache is rebuilt, in memory only under `NoCreateConfig` (`TestReadChatIndex_olderVersionRebuilt`).

### Review 3 (root), 2026-11-23

- [ ] R3-06 (minor, owner fix) `internal/chat/list_continues.go:83,91`: `writePreviousQueryLine` drops the error of `fmt.Fprintln` and `clearPreviousQueryLine` discards the error of `table.ClearTermTo`, while `listChats` returns every other `ClearTermTo` error (`handler_list_chat.go:490,561,1088`); CLAUDE.md: return an error on every failure. Item: return both errors to `listChats` and add a row with a failing writer.
