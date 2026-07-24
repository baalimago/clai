# Phase 3 — Foreign-chat actions in macro mode

**Status:** ✅ Complete

[← README](./README.md)

## Goal

Verify that `actOnForeignChat` works correctly in macro mode. No code changes
needed — the action prompt already respects `cq.input` and the list loop
already handles `table.ErrUserInitiatedExit`.

## Specification

`actOnForeignChat` reads `choice` via `table.ReadUserInputFrom(cq.input)`:

- `"c"` / `""` → clone foreign session, continue, `errExitList`
- `"b"` → back to list (continue loop)
- `"q"` → `table.ErrUserInitiatedExit` → caught by list loop → clean exit

All three paths work as-is with macro input. `ClearTermTo` calls in the
foreign path write escape sequences to `cq.out`; harmless in non-TTY mode.

## Integration contract

| Scenario | Input | Result |
|----------|-------|--------|
| `clai chat list 0 c` (foreign) | "0", "c", "q"×10 | Clone foreign chat, continue, exit list |
| `clai chat list 0 b` (foreign) | "0", "b", "q"×10 | Show info, back to list, next "q" exits |
| `clai chat list 0 q` (foreign) | "0", "q", "q"×9 | Select chat, actOnForeignChat gets "q" → ErrUserInitiatedExit → list exits |

## Acceptance criteria

- [x] Foreign-chat continue works with macro input (code-path verified)
- [x] Foreign-chat back works with macro input
- [x] Foreign-chat quit works with macro input
- [x] `go test ./internal/chat/ -race -timeout=30s` passes

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Foreign reader fails during clone | Error propagated, macro stops |
| Clone target disk full | Error propagated |

## Review findings (R1, 2026-07-23)

**Verified-good:**
- `actOnForeignChat` (line 734) reads `choice` via `table.ReadUserInputFrom(cq.input)` — same reader as everything else. Macro input flows naturally.
- All three paths (`"c"`/`""` → clone+exit, `"b"` → back, `"q"` → `ErrUserInitiatedExit`) work without changes.
- `ClearTermTo` escape sequences are harmless in non-TTY mode.

**R1-02 (Medium): Dependency on Phase 1 not documented**

`actOnForeignChat` returns `table.ErrUserInitiatedExit` on "q" (line 758). This propagates to `listChats` line 721. Without Phase 1 adding the `ErrUserInitiatedExit` catch on the foreign-chat error path (see R1-01), macro quit from foreign-chat actions would fail. The plan says "No code changes needed" — this is true for THIS phase, but the AC "Foreign-chat quit works with macro input" depends on Phase 1's fix covering all three paths. State this dependency explicitly so an implementer doesn't skip path 3 in Phase 1.

## Implementation notes

*Implemented by worker session 3 (imago, 2026-07-23).*

### Verification summary

All three foreign-chat macro paths were verified via code-path analysis and
confirmed with new UAT tests:

- **Continue** (already covered by `TestUAT_ListSelectContinue_ForeignClaudeChat_ClonesAndThenDedups`):
  row select → "c" → `cloneForeignChat` → `errExitList` → `listChats` returns nil.
- **Back** (new `TestUAT_ListSelectBack_ForeignChat_ReturnsToList`):
  row select → "b" → `printChatInfoForeign` + `ClearTermTo` → return nil →
  loop continues → next "q" → `tb.Run()` returns `ErrUserInitiatedExit` →
  caught at line 681 → clean exit.
- **Quit** (new `TestUAT_ListSelectQuit_ForeignChat_ExitsCleanly`):
  row select → "q" → `actOnForeignChat` returns `ErrUserInitiatedExit` →
  caught at line 725 → clean exit. Verified no clone occurs.

### Code-path trace (no changes needed)

```
listChats (line 530)
  ├─ tb.Run() → select row [0] OK
  ├─ sel.Kind == chatRowForeign → enters foreign path (line 713)
  ├─ reader.Read(ctx, sel.SourceID) → foreign chat
  └─ actOnForeignChat(foreign, reader, groupKey) (line 721)
       ├─ printChatInfoForeign (line 735)
       ├─ table.ReadUserInputFrom(cq.input) (line 738)
       └─ switch:
            ├─ "c"/"C"/"" → cloneForeignChat → errExitList
            ├─ "b"/"B" → ClearTermTo → return nil (loop continues)
            └─ "q"/"Q" → return table.ErrUserInitiatedExit
  
  Back in listChats (line 721-729):
    ├─ errExitList → return nil (line 722-723)
    ├─ ErrUserInitiatedExit → return nil (line 725-726)
    └─ other error → return err (line 728)
```

### Files modified

- `internal/chat/handler_list_chat_uat_test.go` — added two UAT tests:
  `TestUAT_ListSelectBack_ForeignChat_ReturnsToList` and
  `TestUAT_ListSelectQuit_ForeignChat_ExitsCleanly`.

### R1 findings resolved

- **R1-02 (Medium):** Confirmed dependency on Phase 1 is satisfied — all three
  `ErrUserInitiatedExit` catch sites in `listChats` were implemented in Phase 1.
  Foreign-chat quit works end-to-end.
