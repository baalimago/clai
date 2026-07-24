# Phase 1 — Core plumbing

**Status:** ✅ Complete

[← README](./README.md)

## Goal

Parse extra positional args as macro inputs and route them to the appropriate
table input streams so that `clai s 3 1` and `clai chat list 3 d` work
end-to-end up to the action choice.

## Specification

### New components

- **`internal/utils/macro.go`** — `NewMacroReader(inputs []string) io.Reader`
  joins inputs with newlines plus 10 trailing `"q\n"` terminators. Returns nil
  for empty/nil input.

- **`internal/utils/macro_test.go`** — verifies empty → nil, content ordering,
  and presence of `"q\n"` terminators after real inputs.

### Modified components

- **`internal/setup.go`** — after theme load, before mode dispatch:
  `extractMacroInputs(mode, postFlagArgs)` returns extra args for
  SETUP/TOOLS/PROFILES/CHAT(list). Sets `setup.Input` to the reader.

- **`internal/chat/handler.go`** — `New()` detects `subCmd ∈ {list, l}` with
  extra args; sets `cq.input = utils.NewMacroReader(argsArr[1:])`.

- **`internal/chat/handler_list_chat.go`** — `listChats` error handler:
  `table.ErrUserInitiatedExit` → `return nil` (clean macro termination).

### What it does NOT do

- Does NOT handle the action-dispatch loop (`actOnChat`) differently in macro
  mode. Delete/edit still enter the interactive message picker.
- Does NOT handle foreign-chat actions.
- Does NOT add any CLI flags. Macro mode is purely positional: extra args
  after the command/subcommand.

## Integration contract

| Scenario | Input | Observable result |
|----------|-------|-------------------|
| `clai s 3 1` | `setup.Input` = reader("3\n", "1\n", "q\n"×10) | Setup wizard reads category 3, sub-option 1 |
| `clai chat list 3` | `cq.input` = reader("3\n", "q\n"×10) | List table selects chat 3, actOnChat reads "q" → exit |
| `clai chat list 3 d` | `cq.input` = reader("3\n", "d\n", "q\n"×10) | Select chat 3, actOnChat reads "d" → enters message picker, picker reads "q" → exits |
| `clai chat continue myid` | No macro detected (subCmd≠list) | Normal interactive continue |
| Plain `clai s` | No macro (no extra args) | Normal interactive setup |
| `clai tools ls` | `setup.Input` = reader("ls\n", "q\n"×10) | Tools subcommand reads "ls" |

## Acceptance criteria

- [x] `go test ./internal/utils/ -run TestNewMacroReader` passes
- [x] `go test ./... -race -timeout=30s` — all existing tests pass
- [x] `go build ./...` compiles cleanly
- [x] `setup.Input` is set when extra args follow SETUP/TOOLS/PROFILES
- [x] `cq.input` is set when extra args follow `chat list`
- [x] Trailing `"q"` terminates list loop without error

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Macro input exhausted mid-table | Trailing "q" → `ErrUserInitiatedExit` → clean exit |
| Empty macro args (e.g. `clai s`) | `NewMacroReader` returns nil → no change to Input |
| Non-list chat subcommand with extra args | Args treated as chat ID/prompt (existing behavior) |
| `chat list` with invalid selection index | Table prints "out of range", reads next input |
| Reader returns io error | Wrapped in `read macro input: ...` and propagated |

## Review findings (R1, 2026-07-23)

**Verified-good:**
- `setup.Input` (package-level `var Input io.Reader` in `internal/setup/setup.go:13`) already respected by all setup tables via `WithInput(Input)`. Injecting a reader here is the correct strategy.
- `cq.input` field exists on `ChatHandler` (`handler.go:67`) and is already plumbed to all chat tables (`WithInput(cq.input)`) and `ReadUserInputFrom(cq.input)`. It is never set by `New()` — currently nil → TTY fallback. Setting it to a macro reader is a minimal, correct change.
- `table.ReadUserInputFrom` (`go_away_boilerplate/pkg/table/input.go:27`) handles non-nil readers byte-by-byte without buffering ahead, so a shared `strings.Reader` works correctly across multiple table instances and direct `ReadUserInputFrom` calls. Returns `ErrUserInitiatedExit` when input is "q" or "quit".
- `chat.New()` receives the full args string (subcommand + rest) via `conf.PostProccessedPrompt`. Self-detection of `subCmd ∈ {list, l}` with extra args avoids plumbing through `text.Configurations`. Clean design.
- All existing tests pass (`go test ./... -race -cover -timeout=10s`), `go vet` clean.

**R1-01 (High): `listChats` error handler under-specified — three distinct paths carry `ErrUserInitiatedExit`**

`handler_list_chat.go:listChats` has three error paths that can surface `table.ErrUserInitiatedExit`:
1. After `tb.Run()` (line 667) — table-level "q"
2. After `actOnChat()` (line 698) — native-chat action prompt reads "q" (line 250)
3. After `actOnForeignChat()` (line 715) — foreign-chat action prompt reads "q" (line 749)

The spec says "`listChats` error handler: `table.ErrUserInitiatedExit` → `return nil`" but does not enumerate the paths. If only path 1 is fixed, `clai chat list 3` (select row → "q" at action prompt) still fails: `actOnChat` returns `ErrUserInitiatedExit` at line 250, propagates through line 698 unchecked → `fmt.Errorf("failed to select chat: %w", ...)` → command exits with error.

Add `errors.Is(err, table.ErrUserInitiatedExit)` checks to all three paths. This is also a dependency for Phase 3 path 3 (foreign-chat quit).

**R1-03 (Medium): `extractMacroInputs` API is under-specified**

The function receives `(mode, args)` but the spec does not define:
- Whether `args` includes the command as `args[0]` (it does — `postFlagArgs` from `parseFlags`)
- Return type (presumably `[]string`)
- Per-mode routing: SETUP/TOOLS/PROFILES return `args[1:]`; CHAT returns nil (self-detection happens in `chat.New()`)

Without this, an implementer might strip the wrong arg or pass chat args to both `setup.Input` AND `chat.New()`.

**R1-07 (Low): Error coverage lists "Reader returns io error" as a new concern**

`ReadUserInputFrom` already wraps io errors with `"read macro input: %w"` (input.go:45). No additional code is needed for this. Harmless documentation noise.

## Implementation notes

*Implemented by worker session 1 (imago, 2026-07-23).*

### Files created

- `internal/utils/macro.go` — `NewMacroReader(inputs []string) io.Reader`: joins inputs with newlines + 10 trailing `"q\n"`. Returns nil for empty/nil.
- `internal/utils/macro_test.go` — verifies nil-for-empty, content ordering, single-input, 10 trailing quits.

### Files modified

- `internal/setup.go`:
  - Added `extractMacroInputs(mode Mode, postFlagArgs []string) []string` — returns `postFlagArgs[1:]` for SETUP/TOOLS/PROFILES, nil for everything else (including CHAT).
  - After theme load, before mode dispatch: `setup.Input = utils.NewMacroReader(macroInputs)` when extras present.
- `internal/setup_test.go` — added `TestExtractMacroInputs` with 7 sub-tests.
- `internal/chat/handler.go` — `New()`: after struct initialization, if `(subCmd == "list" || subCmd == "l") && len(argsArr) > 1`, sets `ch.input = utils.NewMacroReader(argsArr[1:])`.
- `internal/chat/handler_list_chat.go` — `listChats`:
  - Path 1 (after `tb.Run()`): added `errors.Is(err, table.ErrUserInitiatedExit)` → `return nil`.
  - Path 2 (after `actOnChat()`): added `errors.Is(err, table.ErrUserInitiatedExit)` → `return nil`.
  - Path 3 (after `actOnForeignChat()`): added `errors.Is(err, table.ErrUserInitiatedExit)` → `return nil`.

### R1 findings resolved

- **R1-01 (High):** All 3 error paths in `listChats` now catch `ErrUserInitiatedExit`.
- **R1-03 (Medium):** `extractMacroInputs` implemented with per-mode routing as specified.
- **R1-07 (Low):** Acknowledged — no new code needed; `ReadUserInputFrom` already handles io errors.

## Review findings (R2, 2026-07-24)

**R2-02 (Low): `utils.Live` global lacks test-isolation reset**

`internal/utils/macro.go:15` declares `var Live bool` as a package-level toggle. `Setup()` sets it at line 499: `utils.Live = !postFlagConf.NonInteractive`. All e2e tests currently use `-n` so `Live` is always false, but a future test that runs without `-n` would inherit the last test's value. Consider adding a `t.Cleanup` reset in tests that modify `Live`, or resetting it in `run()`'s deferred path.

**Verified-good (R2):**
- All 3 error paths in `listChats` confirmed guarded (lines 681, 705, 725).
- `NewMacroReader` nil-safety, content ordering, 10 trailing quits, and live-mode transition all correct.
- `extractMacroInputs` per-mode routing: SETUP/TOOLS/PROFILES → `args[1:]`, CHAT/default → nil.
- Injection points: `setup.go:499-502`, `handler.go:306-308` correct.
