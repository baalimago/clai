# Phase 4 — completion engine in `pkg/cmd`

**Status:** Complete
[Worklog README](./README.md)

## Goal

Ship shell completion as a `pkg/cmd` feature: auto-registered
`completion <bash|zsh>` and hidden `__complete` commands whose suggestions are
derived from the registered command map, flagsets, and `Subcommander` trees,
with two optional interfaces for app-specific values.

## Specification

Repo: go_away_boilerplate, branch `main` (README D9). New files under `pkg/cmd/`
(e.g. `completion.go`, `completion_scripts.go`, tests). Depends on phases
1–3. Generic design is ported from clai `internal/completion.go` (the proven
protocol and shell scripts); clai-specific value sources stay out (phase 9).

1. **Protocol** (identical to clai's today): shell invokes
   `<binary> __complete <words...>` where `words[0]` is the binary name; the
   engine prints one `value\tkind` line per suggestion; kinds:
   `plain`, `file`, `dir`. Bash/zsh scripts map `file`→`compgen -f`,
   `dir`→`compgen -d` / `_files`. Scripts are the clai templates with the
   binary name parameterized — derived from `filepath.Base(os.Args[0])`.
2. **Auto-registration.** `cmd.Run` injects `completion` and `__complete`
   into the command map unless those keys are already taken (app override
   wins). `__complete` never appears in the usage/command listing;
   `completion` does. Injection must not mutate the caller's map (clone).
3. **Exported hook API:**

   ```go
   type CompletionKind string // "plain" | "file" | "dir"
   type CompletionItem struct { Value string; Kind CompletionKind }

   // FlagValueCompleter is optionally implemented by a Command to complete
   // values for its own flags. flagName is without dashes.
   type FlagValueCompleter interface {
       CompleteFlagValue(flagName, partial string) []CompletionItem
   }
   // ArgCompleter is optionally implemented by a Command to complete its
   // positional arguments. Returning an empty non-nil slice means
   // "complete nothing" (suppresses defaults).
   type ArgCompleter interface {
       CompleteArgs(args []string, partial string) []CompletionItem
   }
   ```

4. **Engine rules**, evaluated against the word list (current = last word,
   prev = one before), after resolving as much of the command/sub path as the
   words allow:
   - prev is a value-taking flag (arity via the phase-2 `IsBoolFlag` union —
     scoped to the resolved command's flagset once a command is known):
     ask the owner command's `FlagValueCompleter` if implemented, else no
     suggestions.
   - current starts with `-`: suggest the resolved command's flag names
     (dash-prefixed, sorted); before any command is resolved, suggest the
     union of all top-level commands' flag names.
   - no command resolved yet: suggest top-level command names (keys split on
     `|`, both forms) + flags as above.
   - command resolved and it is a `Subcommander`: suggest its subcommand
     names filtered by prefix.
   - command resolved, not a Subcommander: delegate to `ArgCompleter` if
     implemented; else suggest nothing (free-text positionals).
   - All name lists sorted; prefix-filtered by `current`.
5. **Purity constraint** (README invariant): the engine touches only
   `Flagset()`, `Subcommands()`, and the two hook interfaces. It never calls
   `Setup` or `Run` of any command. `__complete` and `completion` exit via
   normal return (exit 0), errors exit 1 with a single stderr line — a broken
   completion must never corrupt the user's shell session (stdout stays
   suggestion-lines-only).
6. **Accepted limitation** (documented): value completion for a flag typed
   *before* any command is resolved is not offered.

## Integration contract

Mock tree from phase 3 (`chat|c`→`list|l`/`del`, `q|query` with
`String("cm")`), `q` implements `FlagValueCompleter` (`cm` → `m1`,`m2`) and
`ArgCompleter` (returns empty non-nil).

| # | Scenario | `__complete` words (after binary) | stdout lines | Prohibited |
|---|----------|-----------------------------------|--------------|------------|
| 1 | empty position 0 | `""` | all command names (both alias forms) + flags, sorted, kind `plain` | `__complete` listed |
| 2 | command prefix | `ch` | `chat` | `c` (fails prefix) |
| 3 | flag names for resolved command | `q -` | `-cm` | other commands' flags |
| 4 | flag value via hook | `q -cm ""` | `m1`, `m2` | — |
| 5 | flag value, prefix filtered | `q -cm m1` | `m1` | `m2` |
| 6 | subcommand names | `chat ""` | `del`, `l`, `list` | top-level commands |
| 7 | ArgCompleter suppression | `q hello ""` | no lines | command/flag fallback |
| 8 | file kind passthrough | hook returns `{Value:"x",Kind:"file"}` | line `x\tfile` | — |
| 9 | `completion bash` / `completion zsh` | — | script containing `__complete` call wired to the binary's basename; `complete -F`/`compdef` registration | hardcoded app name |
| 10 | unsupported shell | `completion fish` | exit 1, one stderr line | stdout pollution |

## Acceptance criteria

- [x] All ten contract rows pass end-to-end through `cmd.Run` (captured
      stdout/stderr, checked exit codes).
      Evidence: `Test_complete_suggestions` (rows 1–8 + fallback/limitation
      variants), `Test_completion_scripts` (rows 9, 10) in
      `pkg/cmd/completion_test.go`.
- [x] Auto-registration: an app map without `completion`/`__complete` gets
      both; an app-defined `completion` key is not overridden; the caller's
      map is not mutated.
      Evidence: `Test_completion_autoRegistration` (3 subtests, incl.
      app-defined `__complete` winning over interception).
- [x] Emitted bash script sources cleanly under `bash -n` and zsh script
      under `zsh -n` (syntax check in tests, skipped when the shell binary is
      absent — mark clearly, never fake).
      Evidence: `Test_completion_scripts` bash/zsh subtests (`exec.LookPath`
      guard, `t.Skipf` when absent; both shells present locally and checked).
- [x] Engine purity proven: mock commands panic in `Setup`/`Run`; the full
      completion suite passes without tripping them.
      Evidence: `completionTree()` wires panicking `Setup`/`Run` into every
      mock; the whole suite (incl. `completion bash`/`fish` dispatch) is
      green.
- [x] Godoc for `CompletionItem`, both interfaces, the protocol, and the
      documented limitation (spec item 6).
      Evidence: `completion.go` doc comments on `CompletionKind`,
      `CompletionItem` (protocol), `FlagValueCompleter` (limitation),
      `ArgCompleter` (suppression semantics).

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Hook panics | recovered; empty suggestions; exit 0 (shell must survive) | unit with panicking hook |
| Words empty (`__complete` with no args) | no output, exit 0 | unit |
| Unknown command prefix in words | falls back to top-level suggestions for last word | unit |
| `completion` with no shell arg | exit 1, usage hint on stderr | contract row 10 variant |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–3).**

Deltas from spec:

- `__complete` is implemented as an **interception in `cmd.Run`** (exact
  `args[1] == "__complete"` match, skipped when the app's map matches that
  key) rather than an injected `Command`. Rationale: a Command's flagset
  would choke on arbitrary flag-shaped words (`__complete q -cm` →
  "flag provided but not defined"), and interception keeps `__complete` out
  of usage listings and suggestion lists by construction — no hidden-key
  convention needed. Observable behavior matches the contract exactly.
- `completion` is injected via `withCompletion(binaryName(args), commands)`
  (map clone; app key wins via `matchCommand` so alias forms also count as
  taken). Binary name comes from `filepath.Base(args[0])`, threaded at
  injection — nothing reads `os.Args`.
- Engine word walk lives in `resolveCompletionPath`: descends
  command/Subcommander levels, skips flags (consuming values per arity —
  resolved command's flagset once known, phase-2 union before that),
  collects positionals for `ArgCompleter`, and reports a trailing
  value-taking flag as `pendingValueFlag` (cursor holds its value).
  Unknown words before any command resolves are ignored → top-level
  fallback for the last word.
- Hook calls go through `safeHook` (recover → nil suggestions), covering
  the panic error-coverage row for both hook interfaces.
- Scripts are clai's proven bash/zsh templates with `%[1]s` binary-name
  substitution (zsh `%%` escaped for Sprintf). Both `bash -n` and `zsh -n`
  ran locally (both shells installed).
- `isBoolFlag` extracted to `setup.go` and reused by `valueFlagUnion`.

Verification:

- `go test ./pkg/cmd/... -race -count=3 -cover -timeout=30s` → ok, 96.8%
  coverage on `pkg/cmd` (dip from 100% is uncovered defensive branches:
  `binaryName` empty-args fallback, union-error fallback).
- `make qa` → exit 0 (gofumpt rewrote the map clone to `maps.Copy`).
- Working tree still uncommitted on `main` per D10.

## Review findings

_(appended by reviewers)_
