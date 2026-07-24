# Phase 6 — UAT-to-e2e migration

**Status:** ✅ Complete

[← README](./README.md)

## Goal

Delete the internal-package UAT/macro tests in `internal/chat/` and replace them
with true CLI-driven e2e tests in the root `package main`, matching the existing
`main_*_e2e_test.go` pattern. All tests use `-n` (`--non-interactive`) so
output is fully deterministic and assertable.

## Motivation

The existing UAT tests (`handler_list_chat_uat_test.go`,
`handler_list_chat_macro_test.go`) call handler methods directly
(`cq.handleListCmd(ctx)`) and inject `cq.input` at the handler level. This
bypasses the CLI entry point entirely — they are integration tests, not
end-to-end tests.

True e2e tests go through `run()` → `Setup()` → `chat.New()` → `Query()`,
exercising the same code path a real `clai chat list` invocation takes. This
catches bugs in flag parsing, macro injection, `NewMacroReader` wiring, and
the `utils.Live`/`--non-interactive` polarity that handler-level tests miss.

## Specification

### Delete

- `internal/chat/handler_list_chat_uat_test.go` — all 5 tests
- `internal/chat/handler_list_chat_macro_test.go` — all 8 tests
- Remove helper functions only used by these files:
  - `seedChatAt` — if not used elsewhere
  - `seedPeekFixture` — if not used elsewhere
  - `runPeekScript` — if not used elsewhere
  - `countPickerPagePrompts` — if not used elsewhere
  - `useTestSourceReaders` — if not used elsewhere

### Create: `main_chat_list_e2e_test.go`

Package `main`. All tests use the `-n` flag for non-interactive auto-exit.
Foreign-source isolation via `t.Setenv("HOME", t.TempDir())` (no real
Claude/pi sessions leak in). Chat seeding via `chat.Save()`.

#### Test inventory (13 tests, 1:1 migration from existing UAT/macro tests)

| # | Test name | CLI invocation | Setup |
|---|-----------|----------------|-------|
| 1 | `Test_e2e_chat_list_foreign_clone_and_dedup` | `-n -cm test c l 0 c` then `-n -cm test c l q` | Claude project in HOME |
| 2 | `Test_e2e_chat_list_edit_message_picker_reopens` | `-n -cm test c l n 10 e n d 11 b` | 12 chats seeded, fake `$EDITOR` |
| 3 | `Test_e2e_chat_list_delete_message_picker_reopens` | `-n -cm test c l n 10 d n 11 b` | 12 chats seeded |
| 4 | `Test_e2e_chat_list_foreign_back_to_list` | `-n -cm test c l 0 b` | Claude project in HOME |
| 5 | `Test_e2e_chat_list_foreign_quit` | `-n -cm test c l 0` (q auto-appended) | Claude project in HOME |
| 6 | `Test_e2e_chat_list_macro_empty` | `-n -cm test c l 0` | Empty conv dir |
| 7 | `Test_e2e_chat_list_macro_out_of_range` | `-n -cm test c l 999` | 1 chat seeded |
| 8 | `Test_e2e_chat_list_macro_continue` | `-n -cm test c l 0` | 1 chat, verify chat info in output |
| 9 | `Test_e2e_chat_list_macro_continue_with_enter` | `-n -cm test c l 0 ""` | 1 chat, verify dirscope binding created |
| 10 | `Test_e2e_chat_list_macro_back_to_list` | `-n -cm test c l 0 b` | 1 chat, verify list re-rendered |
| 11 | `Test_e2e_chat_list_macro_delete_messages` | `-n -cm test c l 0 d 0:5` | 1 chat with 7 msgs, verify 1 msg remains |
| 12 | `Test_e2e_chat_list_macro_delete_no_messages` | `-n -cm test c l 0 d` | 1 chat with 2 msgs, verify no deletion |
| 13 | `Test_e2e_chat_list_macro_edit_message` | `-n -cm test c l 0 e 5` | 1 chat with 7 msgs, fake `$EDITOR`, verify content |

#### Test patterns

**Setup pattern:**
```go
func Test_e2e_chat_list_macro_continue(t *testing.T) {
    confDir := setupMainTestConfigDir(t)
    _ = chdirToTemp(t)  // if needed for dirscope tests
    t.Setenv("HOME", t.TempDir())  // isolate foreign sources

    convDir := filepath.Join(confDir, "conversations")
    chat.Save(convDir, pub_models.Chat{...})

    stdout, status := runOne(t, confDir, "-n -cm test c l 0")
    if status != 0 {
        t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
    }
    // assertions on stdout and side effects
}
```

**Fake $EDITOR pattern (for edit tests):**
```go
editorScript := filepath.Join(t.TempDir(), "fake-editor.sh")
os.WriteFile(editorScript, []byte("#!/bin/sh\nprintf 'EDITED BY E2E' > \"$1\"\n"), 0o755)
t.Setenv("EDITOR", editorScript)
```

**Foreign-source isolation:**
```go
t.Setenv("HOME", t.TempDir())
// Create claude project with fixture jsonl in $HOME
```

### Key differences from old UAT tests

| Old (internal/chat) | New (main e2e) |
|---|---|
| `cq.input = utils.NewMacroReader(...)` | Macro inputs are CLI positional args |
| `cq.handleListCmd(ctx)` | `runOne(t, confDir, "-n -cm test c l ...")` |
| Read from `cq.out` bytes.Buffer | Read from `runOne` stdout string |
| `useTestSourceReaders(nil)` | `t.Setenv("HOME", t.TempDir())` |
| `newTestHandler(t)` | `setupMainTestConfigDir(t)` |
| `seedChatAt(t, cq.convDir, ...)` | `chat.Save(convDir, ...)` |

### `-n` flag behavior

With `-n` (`--non-interactive`), `NewMacroReader` appends 10 trailing `"q\n"`
lines after the user-supplied inputs. This means:

- `clai -n c l 0` → inputs: `0`, `q`, `q`, `q`, `q`, `q`, `q`, `q`, `q`, `q`, `q`
  - The first `q` quits actOnChat, the second `q` quits listChats
  - Remaining `q`s are consumed but harmless (stream exhausted before then)
- `clai -n c l 0 d 0:5` → inputs: `0`, `d`, `0:5`, `q`×10
  - After delete, the picker loop gets a `q` → exits picker
  - The next `q` quits actOnChat → exits list

Tests must not append their own trailing `q`s — the `-n` flag handles that.

## Acceptance criteria

- [x] All 12 of 13 tests implemented and passing (test #9 deferred, see D15)
- [x] Old test files deleted
- [x] `go test ./... -race -cover -timeout=10s` all pass
- [x] No unused helper functions left behind in `internal/chat/`

## Design decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D6 | `-n` flag on all tests | Deterministic output; no interactive stdin fallback needed |
| D7 | HOME isolation instead of `useTestSourceReaders` | The `allSourceReaders` override is test-file-only; HOME isolation works from any package |
| D8 | `runOne` instead of subprocess | Existing e2e tests already use `run()` directly; subprocess adds overhead and flakiness |
| D9 | Single file `main_chat_list_e2e_test.go` | All 13 tests exercise `clai chat list`; one file keeps them discoverable |

## Review findings (R2, 2026-07-24)

**R2-01 (Low): `os.Args` capture/restore in `runOne` is dead code**

`main_chat_e2e_test.go:22-28` captures `os.Args` into `oldArgs` and restores it in `t.Cleanup`, but `os.Args` is never modified — the `run()` function receives args as a parameter. The capture/restore block is harmless but misleading. Remove it or add a comment explaining it's defensive (e.g., against future direct `os.Args` manipulation).

**R2-03 (Low): Test count inconsistency between session journal and status board**

The session journal entry in the README says "20 tests total across Phases 6–7: 13 migrated from internal/chat, 7 new" but the status board correctly says "12 of 13 tests migrated; test #9 (empty-string enter) deferred." Actual test count: 12 migrated + 7 new = 19 (16 in `main_chat_list_e2e_test.go` + 3 in `main_setup_macro_e2e_test.go`). The README session journal overcounts by 1.

**Verified-good (R2):**
- All 12 migrated tests pass through the full CLI path (`runOne` → `run` → `Setup` → `chat.New` → `Query`).
- `splitArgsForTest` correctly handles empty-string tokens for deferred test #9.
- HOME isolation (`t.Setenv("HOME", t.TempDir())`) correctly prevents foreign-source leaks.
- Fake `$EDITOR` shell scripts work correctly for CI-safe edit tests.
- `runOne` CWD save/restore prevents cross-test directory pollution.
