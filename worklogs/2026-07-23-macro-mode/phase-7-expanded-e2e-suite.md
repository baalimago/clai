# Phase 7 — Expanded e2e macro regression suite

**Status:** ✅ Complete

[← README](./README.md)

## Goal

Add e2e tests for macro-mode paths not covered by the Phase 6 1:1 migration:
group drill-down, dirscoped filter toggle, and setup wizard macro paths.

## Motivation

Phase 6 migrates the existing 13 tests. Those cover the flat chat-list → actOnChat
→ picker flow and foreign-chat flow, but leave gaps:

- **Group drill-down:** Selecting a group row enters a sub-view listing member
  conversations. This path goes through `listChats` with `groupKey != ""`, a
  distinct code path with its own pagination, back label, and error handling.
- **Dir filter toggle:** Pressing `"d"` toggles `dirFilterOn`, re-derives rows
  through `prepareListRows` with `inDir` predicate, and re-renders the table.
  This exercises the `errToggleDirFilter` sentinel path.
- **Setup wizard:** `clai setup` accepts macro inputs for category selection
  and config-item selection. This validates the `setup.Input` injection path
  through the full CLI.

## Specification

### New tests: `main_chat_list_e2e_test.go`

| # | Test name | CLI invocation | Setup | What it verifies |
|---|-----------|----------------|-------|------------------|
| 14 | `Test_e2e_chat_list_macro_group_drill_and_back` | `-n -cm test c l 0 0 b` | 2 chats with same GroupKey | Group row → drill → select member → back → list, verify group label and [b]ack to list |

**Note on group drill-down:** The macro sequence `c l 0 0 b` means:
1. Select row 0 (group row) → drills into group, renders member list
2. Select row 0 (member chat) → enters actOnChat, which gets trailing `q` → exit

A second variant `c l 0 b` verifies backing out of group view without selecting a member.

| 15 | `Test_e2e_chat_list_macro_group_back_without_select` | `-n -cm test c l 0 b` | 2 chats with same GroupKey | Group row → "b" (back) → back to list |

**Dir filter:**

| 16 | `Test_e2e_chat_list_macro_dir_filter_toggle` | `-n -cm test c l d d` | 2 chats: 1 bound to CWD, 1 not | Toggle on → verify filtered view → toggle off → verify full view |

| 17 | `Test_e2e_chat_list_macro_dir_filter_empty` | `-n -cm test c l d` | Empty conv dir, CWD with no bindings | Toggle on → verify empty dirscoped view |

### New tests: `main_setup_macro_e2e_test.go`

| # | Test name | CLI invocation | Setup | What it verifies |
|---|-----------|----------------|-------|------------------|
| 18 | `Test_e2e_setup_macro_select_category_quit` | `-n s 0` | Config dir with default files | Category 0 selected → config list shown → auto-quit |
| 19 | `Test_e2e_setup_macro_select_config_and_back` | `-n s 0 0 b` | Config dir with default files | Category 0 → config 0 → preview → back → back to config list → quit |
| 20 | `Test_e2e_setup_macro_select_config_and_quit` | `-n s 0 0 q` | Config dir with default files | Category 0 → config 0 → preview → quit → clean exit |

**Note on setup macro scope:** Deeper setup paths (reconfigure, copy, delete, editor-based
actions) are not included in this phase. They require complex fixture setup (specific config
content to reconfigure, `$EDITOR` for editor actions, confirmation prompts) and the
regression value per test is lower than chat-list paths. These can be added in a follow-up
phase if needed.

## Acceptance criteria

- [x] Tests 14–17 implemented and passing (chat list expansion)
- [x] Tests 18–20 implemented and passing (setup macro)
- [x] `go test ./... -race -cover -timeout=10s` all pass
- [x] `staticcheck` clean
- [x] `gofumpt` clean
- [x] `go build` clean

## Design decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D10 | Separate `main_setup_macro_e2e_test.go` | Setup macros are conceptually different from chat-list macros; separate file keeps tests organized |
| D11 | No deep setup reconfigure tests yet | Complex fixture setup (specific JSON to edit, editor subprocess) outweighs regression value for this phase |
| D12 | Group tests need at least 2 chats with same GroupKey | Single-member groups are suppressed; need 2+ for a group row to appear |
