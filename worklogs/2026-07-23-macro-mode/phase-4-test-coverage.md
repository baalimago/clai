# Phase 4 — Test coverage and edge cases

**Status:** ✅ Complete

[← README](./README.md)

## Goal

Add tests that exercise macro-mode flows end-to-end: setup, chat list actions,
message picker interaction, and edge cases.

## Specification

### Unit tests

- `TestExtractMacroInputs` — verify arg extraction per mode
  (file: `internal/setup_macro_test.go`)

### UAT tests (chat package)

Using the existing `newTestHandler` + `cq.input = utils.NewMacroReader(...)`
pattern:

1. `TestMacro_ChatList_Continue` — `["0"]` → select chat 0, actOnChat gets
   "q" → exits list
2. `TestMacro_ChatList_ContinueWithEnter` — `["0", ""]` → select chat 0,
   enter → prints chat, binds dirscope, exits
3. `TestMacro_ChatList_DeleteMessages` — `["0", "d", "0:5"]` → delete
   messages 0-5 from chat 0
4. `TestMacro_ChatList_DeleteNoMessages` — `["0", "d"]` → open picker,
   trailing "q" exits without deletion
5. `TestMacro_ChatList_EditMessage` — `["0", "e", "5"]` → edit message 5
   (requires $EDITOR set)
6. `TestMacro_ChatList_BackToLoop` — `["0", "b"]` → select, back to list,
   trailing "q" exits
7. `TestMacro_EmptyList` — empty conversation dir, macro input `["0"]` →
   no rows, "q" exits
8. `TestMacro_OutOfRange` — `["999"]` → "out of range", "q" exits

### UAT tests (setup)

9. `TestMacro_Setup_CategorySelection` — setup with `["1", "q"]` → enters
   category 1, then quits

## Acceptance criteria

- [x] At least 6 of the 9 tests above implemented and passing
- [x] `go test ./... -race -cover -timeout=10s` all pass

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Empty conversation dir + macro select | "no matches", "q" → clean exit |
| Index out of range | "out of range" notice, next input consumed |
| Delete non-existent chat | os.Remove error propagated |
| Edit without $EDITOR | editorEditString error propagated |

## Review findings (R1, 2026-07-23)

**Verified-good:**
- Existing UAT tests in `handler_list_chat_uat_test.go` already use `cq.input = strings.NewReader(...)` pattern. Phase 4 tests follow the same template — minimal friction.
- `newTestHandler` (dirscope_v2_test.go:15) creates handlers without setting `input` (defaults to nil/TTY). Tests that inject `cq.input` work correctly.

**R1-04 (Low): No `cq.input` cleanup guidance for shared-handler subtests**

If subtests share a `ChatHandler`, the reader position persists across sub-tests. The implementer should create fresh handlers per test or reset `cq.input`. Existing UAT tests already create fresh handlers — follow that pattern.

**R1-05 (Low): Test #2 `TestMacro_ChatList_ContinueWithEnter` needs pre-created chat**

Sending `["0", ""]` triggers `actOnChat` → `case ""` → `printChat` + `UpdateDirScopeFromCWD`. The chat with ID corresponding to row 0 must exist on disk in `cq.convDir`. The test spec doesn't mention this setup. Use `Save()` to pre-create a chat, or use a different flow.

**R1-06 (Low): Test #5 `TestMacro_ChatList_EditMessage` needs `$EDITOR`**

`editorEditString` shells out to `$EDITOR`. In CI without an editor, this fails. Use `t.Setenv("EDITOR", "cat")` or `t.Skip("requires EDITOR")` when unset. AC requires only 6 of 9 tests — this can be skipped.

## Implementation notes

**2026-07-23 (worker session 4 — imago):**

8 of 9 tests implemented in `internal/chat/handler_list_chat_macro_test.go`.
All tests use `utils.NewMacroReader(...)` + `cq.input` injection + `useTestSourceReaders(nil)`
for foreign-source isolation + `seedChatAt` for deterministic chat seeding.

- Test #9 (setup category selection) skipped — test requires full setup wizard infrastructure.
- `extractMacroInputs` unit tests already present in `internal/setup_test.go` (Phase 1).

Key patterns established:
- Empty/out-of-range: table's native "invalid selection" notice vs. listChats `fmt.Fprintf`
  for empty-row "selection out of range" path (two different code paths for validation).
- `ancli.Okf` / `ancli.Noticef` write to stdout, not `cq.out` — output assertions rely on
  side-effect verification (file changes) and `cq.out` table content only.
- Fake `$EDITOR` shell script (`printf ... > "$1"`) for CI-safe edit tests.
- Each test creates a fresh handler — no reader state sharing concerns (R1-04).
