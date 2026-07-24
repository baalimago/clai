# Phase 2 — Delete and edit in macro mode

**Status:** ✅ Complete

[← README](./README.md)

## Goal

Message pickers (`deleteMessageInChat`, `editMessageInChat`) already use
`.WithInput(cq.input)` and work in macro mode — each extra arg becomes one
line of table input. The only gap: `table.ErrUserInitiatedExit` was not
handled, causing trailing `"q"` terminators to surface as errors.

## Specification

Macro mode faithfully simulates interactive input. No special-casing:

- `clai chat list 3 d` → selects chat 3, enters message picker, trailing
  `"q"` exits the picker without deleting (same as pressing `q` interactively).
- `clai chat list 3 d 0:5` → selects chat 3, deletes messages 0–5, trailing
  `"q"` exits picker.
- `clai chat list 3 e 5` → selects chat 3, opens message 5 in `$EDITOR`.
  Still works in macro mode; `$EDITOR` opens normally. If no editor is
  available (CI), this fails as it would interactively.
- `clai chat list 3 e` → selects chat 3, edit picker gets `"q"` → exits
  without editing.

### Changes

**`deleteMessageInChat`**: add `errors.Is(err, table.ErrUserInitiatedExit)`
alongside existing `table.ErrBack` check, so trailing `"q"` exits the
picker loop cleanly.

**`editMessageInChat`**: same addition.

## Integration contract

| Scenario | Input stream | Observable result |
|----------|-------------|-------------------|
| Select + delete (no indices) | "3", "d", "q"×10 | Picker opens, "q" exits, no deletion, list exits |
| Select + delete + indices | "3", "d", "0:5", "q"×10 | Messages 0-5 deleted from chat 3, list exits |
| Select + edit (no index) | "3", "e", "q"×10 | Picker opens, "q" exits, no edit, list exits |
| Select + edit + index | "3", "e", "5", "q"×10 | Message 5 opens in $EDITOR, save, list exits |
| Select + continue (enter) | "3", "q"×10 | actOnChat reads "" → prints chat, dirscope bind |

## Acceptance criteria

- [x] `deleteMessageInChat` handles `ErrUserInitiatedExit` gracefully
- [x] `editMessageInChat` handles `ErrUserInitiatedExit` gracefully
- [x] `go test ./internal/chat/ -race -timeout=30s` passes
- [x] `go test ./... -race -cover -timeout=10s` all pass

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Deletion picker gets "q" (exhausted macro) | Clean exit, no deletion |
| Edit picker gets "q" | Clean exit, no edit |
| $EDITOR fails in macro edit flow | Error propagated (same as interactive) |
| Message index out of range | "out of range" notice, picker reopens, next input consumed |

## Review findings (R1, 2026-07-23)

**Verified-good:**
- `deleteMessageInChat` (line 944) already catches `table.ErrBack` → `return nil`. Adding `errors.Is(err, table.ErrUserInitiatedExit)` alongside it is a one-line change per function. Correct.
- `editMessageInChat` (line 979) same pattern. Correct.
- Both pickers use `cq.input` via `selectMessagesAt` → `WithInput(cq.input)`. The macro reader is already set by Phase 1, so these functions inherit it without changes.
- When `deleteMessageInChat` returns nil (clean exit from macro "q"), `handleDeleteMessages` returns nil, `actOnChat` returns nil, and `listChats` continues the loop. The next macro "q" hits the list table → `ErrUserInitiatedExit` → caught by Phase 1's fix → list exits. Flow is correct.

**No blocking issues found.** The plan for this phase is sound.

## Implementation notes

*Implemented by worker session 2 (imago, 2026-07-23).*

### Files modified

- `internal/chat/handler_list_chat.go`:
  - `deleteMessageInChat`: changed `if errors.Is(err, table.ErrBack)` → `if errors.Is(err, table.ErrBack) || errors.Is(err, table.ErrUserInitiatedExit)`.
  - `editMessageInChat`: same change.

### Rationale

- Both pickers use a `for` loop pattern identical to `listChats`: on error, check for back/quit, else propagate.
- Combined `ErrBack || ErrUserInitiatedExit` in a single `if` using `||` — leaner than two separate `if` blocks, matching the idiomatic pattern in the codebase (e.g., `setup_actions.go` line patterns).
- No new unit tests added: the change is a one-line error-guard addition in both functions. UAT-level macro mode tests are deferred to Phase 4.
