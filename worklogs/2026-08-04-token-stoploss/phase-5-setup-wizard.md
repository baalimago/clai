# Phase 5 — Setup wizard

**Status:** Not Started
**Back to:** [README](./README.md)

## Goal

Verify that `clai setup` can interactively edit the `stoploss` object, pin it
with tests, and add small polish only if the tests reveal a gap.

## Specification

### Background (discovery)

`internal/setup/setup_actions.go` already routes values by type in
`handleValue`:

- `map[string]any` → `editMap` (interactive loop: `[a]dd [u]pdate [r]emove [d]one`, recursive `handleValue` on the selected key, `castPrimitive` casts input to bool/int/float/string).

Selecting `stoploss` in `interractiveReconfigure` (the field-by-field flow)
therefore already opens an object editor: `update max-tokens` goes through
`getNewValue` → `castPrimitive` (int), and `update
max-tokens-handover-instructions` goes through the same path (string). No new
editing machinery is expected.

### Verification

- `textConfig.json` (or any config with a `stoploss` object) through the
  interactive reconfigure flow: select `stoploss` → `editMap` presents the
  two keys → update `max-tokens` to a new int → update the instructions
  string → `[d]one` → the file on disk carries the new values with correct
  types (int stays int, string stays string).
- The flow must also handle: adding a key, removing a key, and an empty
  `stoploss` object.

### Macro-mode verification (trial, 2026-08-04)

The full flow is drivable from the CLI through macro mode: extra positional
args become `setup.Input` lines, and `-n` appends 10 trailing `q` lines for
graceful exit. The trial ran a built binary of this branch against a sandbox
config dir (`CLAI_CONFIG_DIR=/tmp/clai-phase5/cfg`, `textConfig.json` with a
`stoploss` object). All runs exited 0:

```bash
clai -n s 0 0 c 4 u max-tokens 5000 u max-tokens-handover-instructions "Wrap up now. Summarize your work for handover." d d q
clai -n s 0 0 c 4 d d q      # round-trip, no key touched: file byte-identical (md5 equal)
clai -n s 0 0 c 4 r max-tokens-handover-instructions d d q
clai -n s 0 0 c 4 a max-tokens 200000 d d q   # stoploss was {}
clai -n s 0 0 c 4 u max-tokens abc d d q      # pins invalid-int behavior: writes "abc" as a string
```

Results: `max-tokens` updates stay JSON numbers, the instructions string is
written JSON-escaped, `[r]emove` deletes only the key, `[a]dd` on an empty
object creates `max-tokens` as an int, and an invalid int stays a string.
The indices are fixture-dependent: category 0 (general config) is stable, but
the config row index and the `stoploss` field index depend on the file set
and the sorted top-level keys (see R7-02).

### Polish (only if a gap is proven by the tests)

Candidate gaps and their fixes, in priority order:

1. Multiline instructions message cannot be entered via the single-line
   `getNewValue` input. If the tests deem this a real usability gap, route
   string values longer than a line (or the specific key
   `max-tokens-handover-instructions`) through the existing
   `$EDITOR`-based `actionReconfigureStringFieldWithEditor` path
   (`unescapeEditWithEditor`). NOTE: this is a pre-existing limitation for
   every long string field (e.g. `system-prompt`), so it is NOT required by
   this phase — do not fix it globally here.
2. The key list does not show types. Optional: annotate `stoploss` keys with
   their JSON types in `editMap`'s prompt (`keys: max-tokens(int),
max-tokens-handover-instructions(string)`).

No polish is required for the phase to be Complete; the tests below are the
contract.

## Integration contract

| Input / trigger                                                                                         | Collaborators / fakes                   | Externally observable result                                                         | Required side effects | Prohibited side effects              |
| ------------------------------------------------------------------------------------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------ | --------------------- | ------------------------------------ |
| `clai setup` → reconfigure `textConfig.json` → select `stoploss` → update `max-tokens` to `5000` → done | `table` input harness (`test_input.go`) | File contains `"stoploss": { "max-tokens": 5000, ... }` with `5000` as a JSON number | none                  | value written as a string (`"5000"`) |
| Same flow, update `max-tokens-handover-instructions` to a new sentence                                  | same                                    | File contains the new string, JSON-escaped correctly                                 | none                  | none                                 |
| Same flow, `[r]emove max-tokens-handover-instructions`                                                  | same                                    | Key gone from the object                                                             | none                  | whole `stoploss` object removed      |
| `stoploss` present but empty (`{}`)                                                                     | same                                    | `[a]dd` creates `max-tokens` with a cast int; `[d]one` writes the object             | none                  | none                                 |

## Acceptance criteria

1. Tests pin the `editMap` behavior for a `stoploss`-shaped object: update
   (int stays int, string stays string), add, remove, done — mirroring the
   existing `setup_actions_test.go` `editMap` tests.
2. The `stoploss` object survives a full `interractiveReconfigure` round-trip
   unchanged when no key is touched (no key loss, no type corruption).
3. No changes to `internal/setup` production code are required for the above;
   if the implementing agent had to change production code to make a test
   pass, that is a revealed gap and must be recorded in the implementation
   notes with the polish applied.
4. `go test ./internal/setup/ -timeout=60s` green.

## Error coverage

| Failure condition                    | Expected error / recovery / external outcome               | Test                           |
| ------------------------------------ | ---------------------------------------------------------- | ------------------------------ |
| Invalid int entered for `max-tokens` | `castPrimitive` keeps it a string; JSON writes `"abc"`     | pin current behavior in a test |
| Config file malformed before editing | Existing unmarshal error path in `interractiveReconfigure` | existing setup tests           |

## Implementation notes

To be written by the executing agent.

## Review findings (Review 7, 2026-08-04)

Scope: feasibility trial of this phase's flow through clai macro mode (built
binary, sandbox `CLAI_CONFIG_DIR`, `-n` flag). No implementation exists yet;
the contract is amended in place.

| ID    | Severity | Resolution                                                                                                                                                                                                                                                                                                                                                                                                                              |
| ----- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R7-01 | High     | The integration row "`stoploss` absent from file" is not achievable: `selectFieldToEdit` lists only existing top-level keys and offers only `[d]one`, so there is no top-level add. `[a]dd` exists only inside `editMap`, which requires the key to be present (empty `{}` suffices). Resolved: the row now reads "present but empty (`{}`)". `configure with [e]ditor` remains the only existing path that can create a top-level key. |
| R7-02 | Medium   | Macro/e2e row indices are config-dependent: category 0 is stable; the config row index depends on the `*Config.json` file set (real dir: textConfig.json = 1; fresh dir: 0); the `stoploss` field index is its position in the sorted top-level keys (4 in the trial fixture). Tests must compute indices from the fixture, not hardcode them.                                                                                          |
| R7-03 | Low      | Polish #1 confirmed real: `getNewValue` reads a single line. The `$EDITOR` fallback (`actionReconfigureStringFieldWithEditor`) is wired only into the profiles category's item actions, so it is not reachable for `textConfig.json` either. Pre-existing limitation; not required by this phase.                                                                                                                                       |
| R7-04 | Low      | Polish #2 confirmed absent: `editMap` prints `Map 'stoploss' keys: [...]` without JSON types.                                                                                                                                                                                                                                                                                                                                           |
| R7-05 | Low      | Live-dir trial is premature: the current `textConfig.json` has no `stoploss` (Phases 1–2 not implemented). Re-run the macro trial against the live dir after Phases 1–2 land, recomputing indices per R7-02.                                                                                                                                                                                                                            |

Verdict: Phase 5 contract amended (R7-01); the remaining findings are
non-blocking notes for the implementing agent. Phase remains **Not Started**.
