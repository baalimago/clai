# Phase 1 — Matching engine

**Status:** Complete

[← README](./README.md)

## Goal

Provide a pure, unit-tested token/phrase matcher in `pkg/tools` that decides
whether a command string is banned by a list of phrase entries.

## Specification

### New components

- **`pkg/tools/cmd_ban.go`**:
  - `tokenizeCommand(raw string) []string` — normalize a raw command string
    into a flat token list (rules below).
  - `matchCmdBan(command string, entries []string) (matched string, banned bool)`
    — return the first entry whose tokens appear as a contiguous, in-order run
    in the command's token list. Empty `entries` → not banned. Empty command →
    not banned.
- **`pkg/tools/cmd_ban_test.go`** — table-driven unit tests, no integration
  behavior (unit-test-only phase).

### Tokenization rules (D2, Q7: A)

1. Split the raw command on whitespace into raw tokens.
2. Strip quotes from each raw token, single-sided, one layer: remove one
   leading quote character (`'` or `"`) if present AND one trailing quote
   character (`'` or `"`) if present, independently; do not recurse. So
   `'git` -> `git`, `commit'` -> `commit`, `"it's"` -> `it's`,
   `"x"'` -> `x"`. (Review 1 R1-01: "surrounding" was ambiguous — the
   rule-3 example requires this single-sided reading.)
3. Flatten (emergent from rules 1–2, NOT a separate pass — Review 2
   R2-04): a quoted multi-word argument lands as inner tokens, so
   `sh -c 'git commit'` yields the words `sh`, `-c`, `git`, `commit`.
4. Split each word on shell metacharacters `; | & ( ) < >` and backtick, so
   `git;echo` yields `git`, `echo`; `$(git log)` yields `$`, `git`, `log`
   (the `$` survives: `$` is not a metachar; the `(` and `)` split away).
   A backtick-quoted `git log` yields `git`, `log`. (Review 1 R1-02: the
   example previously claimed `git`, `log`.)
5. Drop empty tokens.

### Matching semantics (D2)

Entries are tokenized by whitespace split ONLY (empty tokens dropped); rules
1–5 above apply to the command string only, never to entries — a quoted or
metachar-containing entry therefore matches nothing by design (Review 2
R2-02).

1. Tokens are exact and case-sensitive (`rm` does not match `RM` or `rmdir`).
2. An entry of N tokens matches if those N tokens appear contiguously and in
   order anywhere in the command's token list.
3. Entry `rm` bans `rm -rf /`, `echo x | rm -rf`, `sh -c "rm -rf /"`,
   `docker exec c rm -f f`. It does not ban `rmdir empty`.
4. Entry `git commit` bans `git commit -m "x"`, `git stash && git commit`,
   `sh -c 'git commit'`. It does not ban `git log`, `commit git`, or
   `x=git; $x commit` — the matcher sees literal text only; it does not
   expand variable assignments or command substitutions (Review 1 R1-03,
   elevated into README Strategy).
5. The first matching entry in list order is reported.

### What it does NOT do

- Does NOT enforce anything, hold state, or touch configuration.
- Does NOT handle regex, globs, or case-insensitive matching.
- Does NOT trace execution (`sh -x`) or inspect process trees (rejected, see
  README "Rejected alternatives").

## Integration contract

`unit-test-only` — pure functions with no I/O or side effects.

## Acceptance criteria

- [x] `tokenizeCommand` strips one quote layer, flattens quoted words, splits on metachars, drops empties — `TestCmdBanTokenizeCommand` (16 cases)
- [x] `tokenizeCommand` empty input → empty slice — `TestCmdBanTokenizeCommand/empty_input`
- [x] `matchCmdBan` with empty entries → not banned — `TestCmdBanMatch/nil_entries`, `TestCmdBanMatch/empty_entries`
- [x] `matchCmdBan` bans single-token entry (`rm`) in direct, piped, quoted-shell, and nested forms; does not ban `rmdir` or `RM` — `TestCmdBanMatch` (5 banned + 2 not-banned cases)
- [x] `matchCmdBan` bans multi-token entry (`git commit`) contiguously and in order, including inside `sh -c 'git commit'`; does not ban `git log` or reversed order — `TestCmdBanMatch` (3 banned + 2 not-banned cases)
- [x] `matchCmdBan` bans metachar-joined forms (`git;echo`, `git&&echo`, `$(git log)`, backticks) — `TestCmdBanMatch` (4 cases)
- [x] `tokenizeCommand` keeps a lone `$` from `$(...)` (R1-02); `sh -c 'git commit'` yields `sh`, `-c`, `git`, `commit` (single-sided strip, R1-01) — `TestCmdBanTokenizeCommand/command_substitution_keeps_dollar`, `.../quoted_multi-word_flattens`
- [x] `matchCmdBan` returns the first matching entry in list order — `TestCmdBanMatch/first_match_in_list_order`, `TestCmdBanMatch/later_entry_matches`
- [x] `go test ./pkg/tools/ -run 'TestCmdBan' -timeout=30s` passes — ok 0.005s

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Nil entries slice | Treated as empty → not banned |
| Empty command string | Not banned |
| Entry longer than command token count | Not banned (no run possible) |
| Tokens present but not contiguous (e.g. `git x commit`) | Not banned |
| Quoted multi-word argument (`sh -c 'git commit'`) | Flattened to `git`, `commit` → banned by entry `git commit` |
| Metachar-joined tokens (`git;echo`) | Split → banned by entry `git` |
| Case mismatch (`RM -rf /` vs entry `rm`) | Not banned |

## Implementation notes

Executing agent: clai (worker session 2026-08-02-01).

- The two test functions are named `TestCmdBanTokenizeCommand` and
  `TestCmdBanMatch` so the AC gate `-run 'TestCmdBan'` exercises BOTH the
  tokenizer and the matcher. A `TestTokenizeCommand` name would silently
  drop the tokenizer from the gate's regex.
- Rule 2 (quote strip) runs before rule 4 (metachar split), matching the
  spec's rule order. The two commute for every boundary case (quote chars
  are never metachars and vice versa), so the order is not observable.
- `strings.Fields` implements rule 1 (whitespace split) and rule 5 (drop
  empties) for both the command and the entries in one call; no custom
  whitespace handling was needed.
- `matchCmdBan` returns the original entry string (not its tokens) so
  Phase 2's refusal message can name the entry as configured.

Verification (all run from the repo root):

```bash
go test ./pkg/tools/ -run 'TestCmdBan' -timeout=30s   # ok 0.005s
```

```bash
go test ./pkg/tools/ -timeout=60s   # ok 2.392s (full package, incl. pre-existing tests)
```

```bash
go build ./...   # clean
```

```bash
go vet ./pkg/tools/   # clean
```

```bash
go run mvdan.cc/gofumpt@latest -l pkg/tools/cmd_ban.go pkg/tools/cmd_ban_test.go   # no output
```

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./pkg/tools/   # clean
```

```bash
go run github.com/mibk/dupl@latest -t 80 .   # 29 clone groups — unchanged from baseline, no new clones
```

## Review findings (review 1, 2026-08-02)

Reviewer: imago. The phase was Not Started; this review amends the contract
in place. Severity taxonomy and the full findings index live in the README.

- **R1-01 (High) — rule 2 ambiguous, flagship AC fails under a literal
  reading.** Naive whitespace split of `sh -c 'git commit'` yields `'git`,
  `commit'`; "strip one layer of surrounding quotes" (both sides, matching
  pair) strips neither, leaving `commit'` and breaking the AC "bans
  multi-token entry ... including inside `sh -c 'git commit'`". Rule 3's own
  example only works with single-sided stripping. Amended: rule 2 now pins
  one-leading + one-trailing, no recursion. Add unit cases for `'git`,
  `commit'`, `"it's"`, `"x"'`. Phase 4 e2e test 4 depends on this same
  reading.
- **R1-02 (Low) — rule 4 example contradicts the rule.** `$(git log)` yields
  `$`, `git`, `log` (the `$` survives; it is not a metachar). Amended the
  example; add a `$(...)` unit case asserting the `$` token is present.
- **R1-03 (Medium) — matching semantics #4 overclaims.** Entry `git commit`
  is asserted to ban `x=git; $x commit`; under this phase's own tokenizer
  the tokens are `x=git`, `$x`, `commit` — no contiguous `git`, `commit`
  run exists, so it is NOT banned. Amended: the claim is removed and the
  literal-text-only limitation is elevated into README Strategy so Phase 4
  docs and future phases inherit it.

Verified good: the pure-function split (`cmd_ban.go` + `cmd_ban_test.go`),
empty-input/nil-entries handling, the error-coverage table, and the
`-run 'TestCmdBan'` gate are checkable as written; `go test ./pkg/tools/
-timeout=60s` passes on the current tree (baseline before implementation).

## Review findings (review 2, 2026-08-02)

Reviewer: clai. The phase was Not Started; this review amends the contract
in place. Full findings index in README.

- **R2-02 (Medium) — entry tokenization unspecified.** The spec defines rules
  1–5 for "the raw command" but never states how an ENTRY string becomes
  tokens. Two readings diverge: whitespace-split only (entry `'git commit'`
  → [`'git`, `commit'`], never matches) vs the full tokenizer (→ [`git`,
  `commit`], bans any command containing the phrase). For a safety policy
  the divergence produces silently ineffective or over-broad bans. Amended:
  entries are whitespace-split only, empty tokens dropped; rules 1–5 apply
  to the command string only. Elevated into README Strategy.
- **R2-04 (Low) — rule 3 is vacuous as an independent step.** After rule 1
  (whitespace split) no token contains inner whitespace, and rule 2 already
  stripped the quotes; "split each quoted word on inner whitespace" can
  never fire. Flattening is the emergent result of rules 1–2. Amended the
  wording to "emergent from rules 1–2, NOT a separate pass"; add a unit
  case that `sh -c 'git commit'` → [`sh`, `-c`, `git`, `commit`] (already
  covered by the R1-01 case).

Verified good: with the R2-02 and R2-04 amendments the tokenizer and matcher
contracts are fully specified and self-contained; all flagship examples in
the README Strategy hold under a whitespace-split-entry + full-tokenizer
reading.

## Review findings (review 8, 2026-08-02)

Independent re-audit found no new findings. `tokenizeCommand`, entry
whitespace splitting, contiguous matching, first-match ordering, and the
documented literal-text limits remain consistent with the README strategy and
the phase tests. Phase remains Complete.
