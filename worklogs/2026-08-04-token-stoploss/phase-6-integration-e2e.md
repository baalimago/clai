# Phase 6 — Integration & e2e

**Status:** Complete
**Back to:** [README](./README.md)

## Goal

Prove the stoploss end-to-end through the CLI with the mock vendor, prove
legacy config compatibility, and update the architecture docs.

## Specification

### Mock vendor behavior (already sufficient, no changes expected)

`internal/vendors/mock.go` reports per-step usage from the LAST user message
(`mockUsageForPrompt`: prompt = `len(strings.Fields(content))`, completion =
2×prompt) and drives agent loops via `tool_X` tokens in the last user message.
Two e2e levers:

- Crossing: set `stoploss.max-tokens` below the step-1 usage. Example: prompt
  `run tool_ls` (2 fields) → usage 6; `max-tokens: 5` crosses at step 1.
- Post-handover refusal: configure the handover message to contain real tool
  tokens, e.g. `tool_ls tool_cat tool_rg tool_git` (registered built-in
  tools). Each post-handover step calls one tool; each is refused by the
  ladder; persistence past the final warning yields `io.EOF` (clean end).
  NOTE: the io.EOF tail is already covered by Phase 3 integration tests; the
  e2e asserts the refusal is visible in the transcript, not the exact stop
  point.

### E2E cases (in-process harness, per `main_cmd_ban_e2e_test.go` pattern)

1. **Handover injection + summary.** `textConfig.json` = `{ model: "test",
   use-tools: true, stoploss: { "max-tokens": 5, "max-tokens-handover-instructions":
   "wrap up now" } }`; query `run tool_ls`. Assert: run exits 0; the saved
   conversation file under `<confDir>/conversations/` contains a user message
   whose content is `wrap up now` positioned AFTER the `tool_ls` assistant
   call and its tool result; the final assistant message is the summary; the
   stdout contains the `stoploss` notice (or stderr — pin whichever the
   harness captures).
2. **Post-handover refusal visible.** Same config but the instructions message
   = `tool_ls` (a tool token). Assert: the saved conversation contains a tool
   result starting with `ERROR: No more tool calls allowed`, and the run ends
   with exit 0 and a final assistant message. The test must also prove that the
   tool was not invoked after handover; refusal occurs before side effects.
3. **`max-tool-calls` still enforced.** `textConfig.json` = `{ model: "test",
   use-tools: true, "max-tool-calls": 1 }`; query `run tool_ls tool_cat`.
   Assert: exactly one tool executes; the second call is refused before its
   implementation runs and its result carries `No more tool calls allowed`.
   This is the intended pre-invocation safety behavior (a regression pin, not
   a byte-identical implementation requirement).
4. **`max-tool-calls: 0` = unlimited.** Same as case 3 but `max-tool-calls: 0`
   and a multi-tool prompt. Assert: all tools execute, outputs visible, no
   refusal text anywhere.
5. **Legacy config compat.** `textConfig.json` containing
   `"token-warn-limit": 333333` (and nothing else new) loads and runs without
   error and without any interactive prompt (non-interactive harness: set
   `-n` or feed EOF; the run must not block on stdin).
6. **Flag override (needs Phase 4).** File sets `stoploss.max-tokens: 100000`;
   run with `-max-tokens=5`; assert the small limit takes effect (handover
   injected). File sets `max-tool-calls: 5`; run with `-max-tool-calls=1`;
   assert the flag value is used.

Phase 3 must also include a controller-level test whose usage values are set
directly by the fake model and are independent of prompt text. The mock-vendor
cases prove CLI transcript and ordering; they do not prove the controller's
numeric metric.

### Docs

- `architecture/query.md:89`: replace the "Token warning" step with the
  stoploss description (per-step context check + handover injection + refusal
  ladder).
- `architecture/query.md:120`: keep the `max-tool-calls` sentence, add "0
  means unlimited".
- `architecture/config.md`: add `stoploss` to the mode-config contents list
  (text), note the sunset of `token-warn-limit`, note the flags.
- `architecture/tooling.md`: add the 0=unlimited note where tool budgets are
  discussed (if the doc gains a budget section; otherwise add one line under
  the max-tool-calls mention).
- `main.go` usage flags: done in Phase 4.
- `architecture/setup.md`: only if Phase 5 changed wizard behavior.

## Integration contract

| Input / trigger                                            | Collaborators / fakes        | Externally observable result                                                        | Required side effects           | Prohibited side effects                    |
| ---------------------------------------------------------- | ---------------------------- | ----------------------------------------------------------------------------------- | ------------------------------- | ------------------------------------------ |
| Case 1 config + `q run tool_ls`                            | mock vendor, in-process CLI  | exit 0; saved conversation shows handover user msg after tool result; final summary | notice printed                  | no injection; run error                    |
| Case 2 config + `q run tool_ls`                            | same                         | exit 0; `No more tool calls allowed` tool result present; final summary present     | none                            | tool executed with visible output          |
| Case 3 config + multi-tool prompt                          | same                         | second tool refused, exit 0                                                         | none                            | regression vs today                        |
| Case 4 config + multi-tool prompt                          | same                         | all tools run with visible outputs, no refusal text                                 | none                            | refusal on 0                               |
| Case 5 legacy config                                       | same                         | runs to completion, exit 0, no stdin block                                          | none                            | prompt or hang                            |
| Case 6 flags                                               | same                         | flag value wins over file for both limits                                           | none                            | file value wins                           |

## Acceptance criteria

1. E2E cases 1–6 pass in the in-process harness (each names the exact
   conversation file / output it asserts).
2. `rg -n "token-warn|tokenWarn|TokenWarn|tokenLengthWarning" architecture/` returns no hits.
3. `architecture/query.md` and `architecture/config.md` describe the stoploss
   and no longer describe the pre-query token warning.
4. `go test ./... -timeout=60s` green (full suite, not just e2e).

## Error coverage

| Failure condition                                | Expected error / recovery / external outcome                    | Test        |
| ------------------------------------------------ | -------------------------------------------------------------- | ----------- |
| Handover message contains no tool tokens         | mock produces the summary directly (case 1 path)               | case 1      |
| Handover message contains tool tokens            | refusal ladder engages (case 2 path)                           | case 2      |
| Run blocked on stdin in case 5                   | harness fails the test (must not hang); use `-n` / closed stdin | case 5      |
| Conversation dir missing                         | existing finalizer behavior; test reads only what was written  | cases 1–2   |

## Implementation notes

Executed 2026-08-04 (imago + clai, worker session 6).

Added `main_stoploss_e2e_test.go` (root package, `main_*_e2e_test.go` pattern):

- `runStoplossE2E` in-process harness (captures stdout+stderr via the existing
  `captureStdoutStderr`), `loadSavedStoplossChat` reads the promoted
  conversation file (`findSavedConversationFile`), `indexOfMessage` and
  `assertValidToolExchangesE2E` transcript helpers, `writeStoplossTextConfig`
  pins `model: test` + `use-tools: true` for every case.
- Case 1 (`Test_e2e_stoploss_handover_injection_and_summary`): config
  `max-tokens: 5` + `max-tokens-handover-instructions: "wrap up now"`,
  query `run tool_ls` (usage 6 crosses at step 1). Asserts exit 0, the
  `stoploss: context usage` notice, the handover user message positioned
  AFTER the ls tool result, and the final assistant summary
  (`done after tool for: wrap up now`).
- Case 2 (`Test_e2e_stoploss_post_handover_refusal_visible`): same config but
  the handover message is `tool_ls tool_cat tool_rg tool_git` — the
  mock-vendor lever example verbatim from the spec. Probe evidence for the
  resolution: a lone `tool_ls` token is skipped by the mock's executed-count
  logic (ls already ran at the crossing step), producing 0 refusals and the
  summary directly; the multi-token message yields exactly three refusals
  (cat, rg, git). Asserts every post-handover tool result carries only the
  ladder text (no tool output => no side effect), at least one refusal, a
  final assistant summary, and exit 0.
- Case 3 (`Test_e2e_stoploss_max_tool_calls_still_enforced`):
  `max-tool-calls: 1`, query `run tool_ls tool_cat`. Asserts the ls result
  carries `[ Tool calls remaining: 1 ] ` and the cat result carries
  `ERROR: No more tool calls allowed` (pre-invocation refusal; the result is
  the ladder text, not a cat invocation error).
- Case 4 (`Test_e2e_stoploss_max_tool_calls_zero_is_unlimited`):
  `max-tool-calls: 0`, query `run tool_ls tool_pwd` (pwd is used instead of
  cat because the mock fabricates empty inputs and pwd executes cleanly,
  giving a visible output anchored to the CWD). Asserts both tools execute,
  the pwd result contains the CWD, and no refusal text appears anywhere.
- Case 5 (`Test_e2e_stoploss_legacy_config_compat`): config carries
  `token-warn-limit: 333333`; run with `-n -r` and query args (`hello`), so
  stdin is never consulted. Asserts exit 0, the query runs to completion,
  and the legacy key produces no warning.
- Case 6 (`Test_e2e_stoploss_flag_overrides_file`): file
  `stoploss.max-tokens: 100000` + `-max-tokens=5` injects the handover (flag
  wins); file `max-tool-calls: 5` + `-max-tool-calls=1` refuses the second
  call (flag wins).

Docs (acceptance criteria 2–3):

- `architecture/query.md`: the Query Execution list drops the stale
  "Token warning" step and describes the session-runner loop with the
  stoploss check after the tool batch (handover injection + refusal ladder);
  the Tool Calls section now describes the batch preflight and keeps the
  `max-tool-calls` sentence, adding "0 (or nil) means unlimited" and the
  post-handover effective-budget-0 note.
- `architecture/config.md`: mode-config list gains the `max-tool-calls`
  budget and the nested `stoploss` policy; the token-count warning prompt is
  documented as sunset with the legacy key ignored; the flags section notes
  `-mt`/`-max-tokens` and `-mtc`/`-max-tool-calls` override the run limits
  (explicit 0 disables a file limit).
- `architecture/tooling.md`: new "Tool budgets" note under the execution
  model — both budgets, the 0 = unlimited semantics, and the pre-invocation
  refusal guarantee.

Verification of acceptance criteria:

1. E2E cases 1–6 pass in the in-process harness (each names the exact
   conversation file via `findSavedConversationFile` and/or the notice text
   it asserts).
2. `rg -n "token-warn|tokenWarn|TokenWarn|tokenLengthWarning" architecture/`
   returns no hits (the sunset note in config.md deliberately avoids the
   literal legacy key name).
3. `architecture/query.md` and `architecture/config.md` describe the stoploss
   and no longer describe the pre-query token warning.
4. `go test ./... -timeout=60s` ✓ (full suite, see gates below).

Gates: `go test . -run Test_e2e_stoploss -timeout=120s` ✓ (6/6 cases before
and after the docs); `go test . -timeout=60s` ✓; `go test ./...
-timeout=60s` ✓; `go build ./...` ✓; `go vet ./...` ✓; gofumpt ✓;
staticcheck ✓; `go fix ./...` ✓; dupl baseline unchanged (29 clone groups).
The full `-race -count=3` QA gate is Phase 7.

## Review findings (review 3, 2026-08-04)

- [x] **R3-04 — High:** Resolve the budget semantics before implementing case 3.
  Phase 3 requires refusal before tool invocation, while this phase says the
  second positive-budget call is refused but also asserts “exactly one tool
  executes.” The current executor invokes a tool before applying its output
  budget (`internal/text/tool_executor.go:103-108`), so “behavior matches today”
  and pre-invocation enforcement cannot both be literal. The contract now
  chooses preflight for every over-budget call and treats absence of the second
  side effect as the intended safety behavior.

## Review findings (Review 8, 2026-08-04)

Scope: pre-implementation feasibility probe of the mock-vendor e2e harness
(built binary of this branch, sandbox `CLAI_CONFIG_DIR` containing
`mock_test_test.json` + `textConfig.json` with `model: "test"`, run as
`clai -r -cm test q ...`). Mirrors the Phase 5 macro-mode trial (Review 7):
drive the real CLI seam, pin current behavior, record evidence for the
implementing agent.

| ID    | Severity | Resolution                                                                                                                                                                                                                                                                                    |
| ----- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R8-01 | Medium   | The executor logs `Call: '<tool>', inputs: ...` BEFORE the budget check. With `max-tool-calls: 0` today the first call is refused pre-invocation (no tool output), yet the log line still appears. Do not key e2e or unit assertions on the absence of the `Call:` log; assert on the absence of tool output instead. |
| R8-02 | Low      | Refusal text pinned: `ERROR: No more tool calls allowed. ` (trailing space). The contract's "starting with `ERROR: No more tool calls allowed`" assertion is safe.                                                                                                                                |
| R8-03 | Low      | Case 5 pre-validated: `token-warn-limit: 333333` loads and runs today (exit 0, no prompt, no stdin block) — evidence for Phase 1's no-migration claim.                                                                                                                                          |

Probed evidence (all exit 0):

Case 3 — `max-tool-calls: 1`, query `run tool_ls tool_cat`:

```
Call: 'ls', inputs: [ 'directory': '.' ]
ERROR: No more tool calls allowed. 
done after tool for: run tool_ls tool_cat
```

`tool_ls` executed (directory listing printed); `tool_cat` refused before
invocation (no second `Call:` line, no output). This is the current
enforcement the case pins as a regression.

Case 5 — legacy `token-warn-limit` config, query `hello`: loads, runs, exit
0, no prompt, no stdin block.

Phase 2 baseline — `max-tool-calls: 0`, query `run tool_ls`:

```
Call: 'ls', inputs: [ 'directory': '.' ]
ERROR: No more tool calls allowed. 
done after tool for: run tool_ls
```

Refused before side effects (no `ls` output) — the current 0 = refuse-all
semantics that Phase 2 changes and case 4 inverts.

Verdict: harness proven; cases 3 and 5 are triable before implementation;
cases 1, 2, 4, and 6 remain post-implementation. No contract amendment
needed — the probes confirm the contract's claims.

## Review findings (review 12, 2026-08-05)

None. Re-ran the six stoploss e2e cases and verified the architecture sunset
search and documented stoploss behavior.
