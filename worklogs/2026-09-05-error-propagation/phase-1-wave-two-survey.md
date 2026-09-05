# Phase 1 — wave-two survey

**Status:** Complete
[Worklog README](./README.md)

## Goal

Enumerate the work the first-wave survey only scoped: every chain-breaking
error site, every swallowed error, the per-vendor terminal states, the
channel contract, the CLI mapping, and the retry-loop blast radius — so
phases 2–9 can be executed without re-deriving any of it.

## Specification

Survey only. No production code changes. Measured against this working copy
at `v1.10.23-18-g59999b9`, non-test Go files only (216 of them).

Counting method for `%v`: an AST pass over every non-test file
(`go/parser`, no type checker) that extracts each `fmt.Errorf` /
`errors.New` / `fmt.Sprintf` / `ancli.*f` call, matches format verbs to
arguments positionally, and prints the argument source text. The script is
throwaway (scratchpad); the tables below are its audited output. The
per-site classification — "is this argument an error, and does the format
string also carry `%w`" — was made by reading each site.

## Finding 1 — the `%v` count in the first-wave survey measured the wrong thing

The README's wave-one table says **"Sites formatting an error with `%v` into
a returned error: 55"**. That number counts `%v` *verbs* in `fmt.Errorf`,
not error-valued ones.

Recounted:

| Measure | Count |
| --- | --- |
| `%v` verbs inside `fmt.Errorf` (non-test) | 114 |
| …whose argument is an error value | 33 |
| …**and** whose format string has no `%w` → **chain broken** | **26** |
| Chain breaks outside `fmt.Errorf` (`errors.New(<string>)`, `%s` of a message field) | 5 |
| **Total chain-breaking sites** | **31** |

The other 81 `%v` verbs format non-error data — file paths, model names,
`res.Status`, command output, glob patterns. They are not defects and are
not in scope for phase 8.

### The 31 chain-breaking sites

Ordered by terminality, which is the sequencing the README asked for.

**Tier A — terminal, on the public surface (phase 8 starts here).**

| # | Site | Shape |
| --- | --- | --- |
| A1 | `pkg/agent/agent.go:298` | `fmt.Errorf("publicQuerier.Setup failed to CreateTextQuerier: %v", err)` |
| A2 | `pkg/text/full.go:77` | same message, same break |
| A3 | `pkg/text/full.go:92` | `fmt.Errorf("pq.Query failed to Setup clone: %v", err)` |

A1–A3 are the last hop before a library consumer. Every typed error produced
by phases 4–6 dies here on the setup path.

**Tier B — terminal, inside the query path.**

| # | Site | Shape |
| --- | --- | --- |
| B1 | `internal/text/setup_querier.go:231` | `failed to create text querier: %v` |
| B2 | `internal/text/setup_querier.go:222` | `failed to setup prompt: %v` |
| B3 | `internal/text/setup_querier.go:147` | `profile override failure: %v` |
| B4 | `internal/text/querier_setup_tools.go:81` | `errs = append(errs, fmt.Errorf("…: %v", file, unmarshalErr))` |
| B5 | `internal/vendors/openai/dalle.go:67` | `failed to get config dir: %v` |
| B6 | `internal/vendors/openai/sora.go:54` | `failed to get config dir: %v` |
| B7 | `internal/vendors/openai/sora.go:200` | `video generation %s: %v`, arg `job.Error` |

**Tier C — terminal, in config/IO plumbing that every run traverses.**

| # | Site | Shape |
| --- | --- | --- |
| C1–C6 | `internal/utils/config.go:258, 263, 268, 302, 308, 317` | `failed to {read,unmarshal,parse} config '%v', error: %v` — six sites, two three-site clones |
| C7 | `internal/utils/prompt.go:42` | `failed to read stdin: %v` |
| C8 | `internal/theme.go:19` | `failed to find config dir: %v` |

C1–C6 all discard `err`, so `errors.Is(err, os.ErrNotExist)` and
`*json.SyntaxError` are unreachable from any caller of config loading.

**Tier D — terminal per command, outside the text path.**

| # | Site | Shape |
| --- | --- | --- |
| D1 | `internal/photo/cmd.go:74` | `failed to setup prompt: %v` |
| D2 | `internal/photo/cmd.go:81` | `failed to create photo querier: %v` |
| D3 | `internal/video/cmd.go:63` | `failed to setup prompt: %v` |
| D4 | `internal/video/cmd.go:67` | `failed to create video querier: %v` |
| D5 | `internal/chat/replay.go:22` | `failed to load previous reply: %v` |
| D6 | `internal/audio/split.go:172` | `failed to parse ffprobe duration %q for %v: %v` |

D6 has a second defect: the branch is `if err != nil || duration <= 0`, so
on a well-formed but non-positive duration `err` is nil and the message
renders `error: <nil>`. The repair must split the two conditions.

**Tier E — non-`fmt.Errorf` chain breaks.**

| # | Site | Shape |
| --- | --- | --- |
| E1 | `internal/tools/mcp/tool.go:122` | `errors.New(resp.Error.Message)` — the JSON-RPC error code is dropped |
| E2 | `internal/tools/mcp/tool.go:147` | `errors.New(buf.String())` |
| E3 | `internal/tools/mcp/manager.go:85` | `fmt.Errorf("initialize responded with err: %s", resp.Error.Message)` |
| E4 | `internal/tools/mcp/manager.go:106` | `fmt.Errorf("tools/list resp.Error: %s", resp.Error.Message)` |
| E5 | `internal/text/tool_executor.go:177` | `errors.New(out)` |

### Deliberate `%v` — not repaired

Recorded so phase 8 does not "fix" them:

| Site | Why it stays |
| --- | --- |
| `internal/text/tool_executor.go:142` (`out = "ERROR: " + err.Error()`) | The error is being rendered into a **tool-result message for the model**, not returned. Rendering is the point. |
| `internal/chat/handler.go:116` | `%v` formats `chatUsage`, a usage string. |
| Every `pkg/tools/bash_tool_*.go` `output: %v` site | The `%v` is `string(output)`; the same format string carries `%w` for the error. Chain intact. |

### `pkg/tools` needs no repair at all

The README's phase-8 table lists `pkg/tools` as 14 sites and tells the next
agent to "start with `pkg/*`". Every one of those 14 is either `%w`-paired
or formats non-error data. **Zero chain breaks in `pkg/tools`.** The
public-surface work is A1–A3 (`pkg/agent`, `pkg/text`) only.

One judgment call in `pkg/tools`, severity low, listed for the record:
`bash_tool_freetext_command.go:174` extracts `exitErr.ExitCode()` and drops
`*exec.ExitError` itself, so `errors.As` for it fails downstream. Deliberate
(the exit code is the interesting fact) but worth a `%w` on the next touch.

### clai is its own bad consumer

`internal/chat/handler.go:219` does
`strings.Contains(err.Error(), "failed to list chats")`. This is the exact
anti-pattern the worklog exists to delete, inside clai, one module boundary
short of sakfråga's. It is the natural first `errors.Is` conversion and a
free proof that the vocabulary works.

## Finding 2 — swallowed errors: 51 sites, not 29, and one of them is the bug

Recount of `ancli.Warnf` / `Errf` / `PrintWarn` / `PrintErr` sites that
consume an error and let execution continue: **51** non-test sites. The
wave-one count of 29 appears to have covered `ancli.Warnf` alone.

Triage into three buckets.

### Bucket 1 — correctly non-fatal (31 sites, no action)

A degraded but meaningful run follows. Reasoning-sidecar load/save/remove
(`internal/chat/chat.go:38,68,266`), price-scheme caching and user-role
lookup (`internal/cost/manager.go:158,208`), the four config upgrades and
two profile-directory walks (`internal/setup/config_lifecycle.go:60–98`),
theme load (`internal/theme.go:22`), config migration
(`internal/utils/config.go:173`), the write-then-fall-back-to-`/tmp` pairs
(`internal/utils/file.go:42`, `internal/vendors/openai/sora.go:241` — both
return a real error if the fallback also fails), per-model config defaults
(`internal/vendors/openai/dalle.go:96`, `sora.go:67`), shell-context
appending (`internal/text/conf.go:224`), glob file reads
(`internal/glob/glob.go:88`), the profiler in `main.go:127,134`, and the
display/diagnostic sites (`internal/chat/handler.go:211,225`,
`internal/setup/setup_actions.go:826,851`, `internal/skills/logger.go:67`,
`internal/text/querier.go:159,171,186`, `internal/tools/mcp/client.go:121,148,189`,
`internal/text/session_runner.go:304`).

### Bucket 2 — hides a terminal or capability-losing state (11 sites, action required)

| # | Site | What it hides |
| --- | --- | --- |
| S1 | `internal/text/generic/stream_completer.go:182` | **The headline defect — see Finding 3.** |
| S2 | `internal/text/querier_setup_tools.go:151` | An MCP client that fails to start is a warning; the run proceeds without the tools the caller registered. |
| S3 | `internal/text/querier_setup_tools.go:214` | `setupMcpManager` failure → `return` with no tools, no error to the caller. |
| S4 | `internal/tools/mcp/manager.go:38` | Per-server `handleServer` failure warns and drops the server. |
| S5 | `internal/tools/mcp/manager.go:155` | A malformed `json.RawMessage` is logged and dropped — the request that is waiting for that response never gets one. |
| S6 | `internal/tools/mcp/manager.go:160` | Same, for the `Response` unmarshal. |
| S7 | `internal/tools/mcp/tool.go:112` | Tool-response unmarshal failure logged, empty result returned to the model. |
| S8 | `internal/text/finalizer.go:112` | `SaveAsPreviousQuery` failed → the reply is not persisted; a later `-re`/`-dre` silently reads stale state. |
| S9 | `internal/text/querier_setup.go:278` | OpenRouter catalog init failure → cost accounting silently absent for the run. |
| S10 | `internal/text/session_runner.go:98` | A consumer-supplied `CallUsageRecorder` failing is invisible to that consumer. |
| S11 | `internal/text/tool_executor.go:168` | Same for `ToolCallRecorder`. |

S2–S4 share one decision: **is "the agent started without the tools you
asked for" terminal?** For a CLI user, arguably not. For `pkg/agent`, where
the caller passed `WithMcpServers` and the task depends on those tools, it
is. Recommendation: keep the warning, and add an accessible record of
degraded startup rather than making it fatal — see the README open decision
**D6**.

S10/S11 are the recorder seams from the metrics worklog; the consumer owns
the recorder and cannot see it failing. Low cost to propagate.

### Bucket 3 — dies with the retry loop (2 sites)

`internal/text/session_runner.go:178` and the two `ancli.Warnf` calls inside
`waitForRateLimitReset` (`:159`, `:164`) are removed by phase 3.

Plus one to delete on sight: `internal/vendors/mistral/mistral.go:66`,
whose message is literally
`"failed to delete range. No error management here... Not great. Why error here? Stop please...: %v"`.

## Finding 3 — a provider error at HTTP 200 is dropped silently

This is the most important thing the survey found, and it is not in the
first-wave README.

`generic.StreamCompleter` decodes each SSE frame into `chatCompletionChunk`
(`internal/text/generic/stream_completer_models.go:64`). That struct has
fields for `id`, `object`, `created`, `model`, `system_fingerprint`,
`choices` and `usage`. **It has no `error` field.**

OpenAI-compatible providers — OpenRouter and DeepSeek among them — can
answer `200 OK`, open the stream, and then emit an error object as a frame
(`{"error": {...}}`) instead of a choice. What happens today:

1. `json.Unmarshal` succeeds. Unknown keys are ignored, so `chunk` is the
   zero value.
2. `len(chunk.Choices) == 0` → `handleStreamChunk` returns
   `models.NoopEvent{}` (`stream_completer.go:191`).
3. The runner's `case models.NoopEvent:` is empty
   (`session_runner.go:251`).
4. The stream closes. `executeModelStep` takes the `!ok` branch, sets
   `result.EndedNormally = true` and returns `AssistantText == ""`
   (`session_runner.go:212–220`).
5. `sessionRunner.Run` returns `nil`.

**A quota exhaustion mid-stream is reported to the caller as a successful
run with an empty answer.** No status code to match on, no substring to
match on — nothing crosses the boundary at all. sakfråga's `strings.Contains`
breaker cannot catch this today even by accident, because there is no error.

Two sub-defects at the same site:

- **Mis-nested return.** `stream_completer.go:179–185`:

  ```go
  err := json.Unmarshal(token, &chunk)
  if err != nil {
      if debugflags.Enabled("CHAT") {
          ancli.PrintWarn(...)
          return models.NoopEvent{}   // <- only returns under DEBUG_CHAT
      }
  }
  ```

  With `DEBUG_CHAT` unset, a genuinely malformed frame falls through and the
  zero-valued `chunk` is used. Same outcome by accident, wrong by
  construction.

- The comment "Expect some failing unmarshalls, which seems to be fine" is
  true for keep-alive frames and false for error frames. The distinction the
  code needs is exactly the `DecodeError` seam phase 4 introduces.

**This changes the phase plan.** The architecture in the README decodes
errors from the **non-OK HTTP response**. That covers the 402/429 return
path and misses this one entirely. Phase 4 must cover *two* decode points:

1. the non-OK status response (as designed), and
2. each **streamed frame**, so a `{"error": …}` frame at HTTP 200 becomes a
   vocabulary error on the channel instead of a `NoopEvent`.

`openai/responses_stream.go` is not exposed the same way — the Responses API
emits typed SSE events and the reader dispatches on event type — but it needs
the same audit in phase 5 for a `response.failed` / `error` event.

## Finding 4 — the channel contract needs a rule, not a type change

`CompletionEvent` is `any` and 17 vendors send into it. Changing that type
is a breaking change across every vendor for no gain: the runner's
`case error:` at `session_runner.go:242` already wraps with `%w`
(`fmt.Errorf("completion stream error: %w", cast)`), so a typed error placed
on the channel already survives to the caller intact.

The gap is production, not transport. Proposed contract, to be pinned in
phase 6:

- `CompletionEvent` stays `any`. No signature change.
- An `error` value on the channel is **terminal** — the runner ends the step
  and returns it — **unless** it satisfies `errors.Is(err, io.EOF)` or
  `errors.Is(err, context.Canceled)`, which the runner already treats as a
  normal end (`session_runner.go:243`). That existing behaviour becomes the
  documented rule rather than an implementation detail.
- A vendor that detects a provider error frame mid-stream **must** send a
  vocabulary error rather than a `NoopEvent` (Finding 3).
- The runner must not flatten. `%w` at `:250` is correct and is now a pinned
  invariant with a test.

Only one production site currently sends an error into a channel:
`stream_completer.go:132`, `fmt.Errorf("failed to read line: %w", err)` — a
transport read failure. It should carry the transient/connection meaning
once the vocabulary exists.

## Finding 5 — the CLI cannot distinguish exit codes without an upstream change

`main.go:139` is `os.Exit(run(os.Args[1:]))`; `run` returns
`cmd.Run(ctx, …)` (`main.go:118`). `cmd.Run` is upstream, in
`go_away_boilerplate@v1.33.12/pkg/cmd/setup.go`, and its whole error surface
is:

```go
err = command.Run(ctx)
if err != nil {
    if isUserInitiatedExit(err) { return 0 }
    ancli.Errf("failed to run: %v", err.Error())
    return 1
}
return 0
```

Every failure is exit 1. `Command.Run` returns `error`, and `cmd.Run`
collapses it to an int before clai sees it. So clai has **no in-repo way**
to map an error class to an exit code. Three options:

| Option | Cost |
| --- | --- |
| Leave exit codes at 0/1; surface the vocabulary in the stderr line | Zero. `err.Error()` is already printed, so a typed error's `Error()` string is the only change needed, and it is a formatting choice not a contract. |
| Add an optional `ExitCoder` interface to upstream `pkg/cmd` | An upstream release. There is precedent — the flag-system worklog shipped one (`2026-08-28`, phase 5). |
| Bypass `cmd.Run` in clai's `run()` | Duplicates dispatch, help, and completion. Rejected. |

**Recommended: option 1 for this worklog**, with the upstream `ExitCoder`
tracked separately. Rationale: sakfråga is a *library* consumer — it calls
`pkg/agent`, never the binary — so the whole motivating requirement is met
by phases 2–6 with no exit-code work at all. Making exit codes part of this
worklog couples a library fix to an upstream release. This is an open
decision for the user, recorded as **D5** in the README.

## Finding 6 — retry-loop blast radius, exact

Production symbols to delete:

| Symbol | Location |
| --- | --- |
| `runStepWithRetry` | `internal/text/session_runner.go:129–148` |
| `waitForRateLimitReset` | `internal/text/session_runner.go:150–183` |
| `sleepContext` | `internal/text/session_runner.go:394` — **its only two callers are inside `waitForRateLimitReset`** (`:171`, `:179`), so it dies too |
| `currentRetries` field | `internal/text/session_runner.go:33` |
| `RateLimitRetries` | `internal/text/querier.go:22` |
| `FallbackWaitDuration` | `internal/text/querier.go:23` |
| `rateLimitLastAmTokens` field | `internal/text/querier.go:68` |

One call site to rewrite: `internal/text/session_runner.go:78`,
`r.runStepWithRetry(ctx, session)` → `r.executeModelStep(ctx, session)`.

`models.ErrRateLimit` (`internal/models/models.go:60–76`) is **not** deleted
— phase 5 moves and renames it (README finding 4).

Tests to adjust:

| Test | Location | Action |
| --- | --- | --- |
| `Test_sessionRunner_Run_RateLimitRetryIsIterative` | `internal/text/session_runner_test.go:882` | Delete or invert: it asserts a second `StreamCompletions` call after an `ErrRateLimit`. Post-removal the rate limit must surface on the first call. Inverting it is the better test — it pins the new contract. |
| `Test_sleepContext` | `internal/text/session_runner_test.go:1032` | Delete with `sleepContext`. |
| `Test_waitForRateLimitReset_FallbackPath` | `internal/text/session_runner_test.go:1052` | Delete. |
| `Test_waitForRateLimitReset_FallbackHonorsCancel` | `internal/text/session_runner_test.go:1062` | Delete. |
| `Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount` | `internal/text/querier_test.go:747` | **Keep, amend.** It asserts `strings.Contains(err.Error(), "count input tokens: mock token count failure")` — that message comes from `waitForRateLimitReset:155` and disappears. The behaviour under test (conversation is persisted when stream setup fails) is still valid and still worth pinning; the assertion must move to the `ErrRateLimit` itself surfacing. |
| `MockQuerierRateLimitTokenCountFail` | `internal/text/querier_test.go:1623–1645` | Keep; it is the fixture for the amended test above. |
| `internal/models/errors_test.go:12` | | Follows `ErrRateLimit` to `pkg/` in phase 5. |

**The trap holds.** `models.InputTokenCounter` / `CountInputTokens` survives:
`internal/text/stoploss.go:163` asserts it, and the anthropic vendor calls
`CountInputTokens` unconditionally in `StreamCompletions`
(`internal/vendors/anthropic/claude_stream.go:30`).

No other package references any removed symbol; `RateLimitRetries` and
`FallbackWaitDuration` are exported from `internal/text` and therefore
unreachable outside the module.

## Finding 7 — where the vocabulary lives is now decidable

The README left this open between `pkg/text/models` and a new `pkg/llmerr`.
Two facts settle the import-cycle question:

- `pkg/text/models` imports **only stdlib** (`bytes`, `encoding/json`,
  `errors`, `fmt`, `io`, `strings`, `time`). Nothing in it can cycle.
- `internal/models` already imports `pub_models "…/pkg/text/models"`
  (`internal/models/models.go:8`). The direction is `internal → pkg`, so
  moving `ErrRateLimit` into `pkg/text/models` is a move *along* the existing
  arrow, and `internal/models` can keep an alias during the transition.

Both candidates are therefore cycle-free. The decision is naming, not
structure, and it is recorded as **D4** in the README with a recommendation
of `pkg/text/models` — the package is already the public mirror, already
imported by `pkg/agent` and `pkg/text`, and adds no new import for any
consumer.

Consumer-visibility note that makes the move mandatory rather than
cosmetic: `ErrRateLimit` currently lives in `internal/models`, so
`errors.As(err, &models.ErrRateLimit{})` is **not compilable** from outside
the module. Today no external consumer can match clai's one typed error even
though it exists.

## Finding 8 — the enumerated terminal states

Item 3 of wave two: what the vocabulary must name. Split by evidence.

**Decided — the HTTP-status baseline.** Provable from the status code alone,
no body parsing, therefore safe in `generic`:

| Status | Meaning | Vocabulary |
| --- | --- | --- |
| 401, 403 | credentials rejected | `ErrAuthFailed` |
| 402 | payment required | `ErrLikelyInsufficientCredits` |
| 404 | route or model unknown | `ErrModelNotFound` |
| 429 | throttled | `ErrRateLimited` |
| 5xx | provider-side, retryable by the caller | `ErrProviderUnavailable` |

`ErrLikelyInsufficientCredits` and `ErrRateLimited` are the two sakfråga
requires; the other three are cheap at the same seam and each is a real
terminal state a consumer would branch on.

**Decided — transport, no status involved.**

| Condition | Vocabulary |
| --- | --- |
| `client.Do` failed; stream read failed mid-response | `ErrTransport` (wraps the underlying `net`/`url` error with `%w`) |

**Needs vendor evidence — the body-decoded states.** Each of these is real,
but the exact wire signal is vendor-specific and must be confirmed against
live responses or current provider docs at implementation time, not asserted
from memory:

| State | Why it needs a name | Confirm before implementing |
| --- | --- | --- |
| quota exhausted at 429 | 429 alone means "slow down"; quota exhaustion means "stop". Backing off against an empty account is the failure mode this worklog exists to prevent. | OpenAI's `insufficient_quota` code, DeepSeek's `402 Insufficient Balance` — both named in the wave-one README, neither verified in this survey. |
| context length exceeded | Caller must trim, not retry | per-vendor code |
| content filtered | Caller must not retry at all | per-vendor code |
| malformed provider response | Distinct from "the provider said no" | in-repo (Finding 3) |

**Deliberately unnamed.** Request-construction and marshalling failures stay
plain wrapped errors: they are clai bugs, not provider states, and a
consumer has no distinct action for them.

The wave-one architecture handles the split correctly — the baseline table
above is the `baselineError` function, and the rest is each vendor's
`DecodeError`. Nothing in this finding changes that design.

## Integration contract

`unit-test-only` — and in fact evidence-only. This phase produces a
document; it changes no code and adds no test. The contracts it enumerates
are executed in phases 2–9, each of which carries its own integration
contract.

## Acceptance criteria

- [x] Every chain-breaking error site is enumerated with file, line, and
      shape, ordered by terminality.
      Evidence: Finding 1, tiers A–E, 31 sites. Reproducible via the AST
      pass described under Specification; spot-checked by reading each site.
- [x] The `%v` count in the wave-one README is either confirmed or
      corrected with method.
      Evidence: Finding 1 — corrected from 55 to 31, with the counting
      method stated and the 81 non-error `%v` verbs excluded.
- [x] Every swallowed error is triaged, and the ones hiding a terminal
      state are named.
      Evidence: Finding 2 — 51 sites, bucket 2 lists the 11 (S1–S11).
- [x] Terminal error states are enumerated per vendor, separating what is
      provable in-repo from what needs vendor evidence.
      Evidence: Finding 8.
- [x] The channel-borne error contract is decided.
      Evidence: Finding 4 — `CompletionEvent` stays `any`; the rule is
      terminal-unless-`io.EOF`/`context.Canceled`, pinned in phase 6.
- [x] The CLI mapping is decided, or the blocking constraint is identified
      and escalated.
      Evidence: Finding 5 — `cmd.Run` collapses every error to exit 1
      upstream. Escalated as open decision D5.
- [x] The retry-loop blast radius is confirmed, including tests.
      Evidence: Finding 6 — 7 production symbols (one more than the README
      listed: `sleepContext`), 1 call site, 6 test entries, the
      `InputTokenCounter` trap re-verified at `internal/text/stoploss.go:163`.
- [x] The open question of where the vocabulary lives is decidable.
      Evidence: Finding 7 — both candidates proven cycle-free; recommendation
      recorded as D4.

## Error coverage

Not applicable in the usual sense: this phase has no failure conditions of
its own. The matrix it *produces* is Finding 8, which becomes the error
coverage of phases 4 and 5.

One risk in the survey itself is worth recording: the `%v` recount comes
from an AST pass with no type checker, so an error-valued argument whose
expression does not look error-shaped (a bare method call returning `error`,
say) could have been missed. Mitigation: the pass printed **all 114** `%v`
arguments, not a filtered subset, and every one was read. The 31 are
complete for `fmt.Errorf`; tier E was found by separate greps for
`errors.New(` with a non-literal argument and for `.Error()`.

## Implementation notes

**Session: Claude, 2026-09-05 (survey).**

Deltas from the wave-one README, all recorded above as findings:

- The 55 `%v` figure was wrong in kind, not degree (Finding 1). The real
  work is 31 sites, and `pkg/tools` — the README's recommended starting
  point at 14 sites — has zero.
- The 29 swallowed errors are 51 (Finding 2).
- Finding 3 is new and is a live defect: a provider error frame at HTTP 200
  is reported as a successful empty run. It is the exact failure shape
  sakfråga is trying to eliminate, and the wave-one architecture does not
  cover it, because that architecture decodes only non-OK *responses*.
  Phase 4's scope grows to two decode points.
- Finding 6 adds `sleepContext` to the removal list (the README missed it)
  and identifies `Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount`
  as a keep-and-amend rather than a delete, because it asserts on a message
  string produced by a function that is being deleted.

Baseline gates (no code changed; run to establish the branch baseline):

- `go build ./...` ✓
- `go vet ./...` ✓
- `go run github.com/mibk/dupl@latest -t 80 .` → **28 clone groups**
  (note: the token-stoploss worklog's last recorded baseline was 29; the
  branch has since moved. 28 is this worklog's baseline.)

Full `make qa` is not run for a documentation-only phase; phase 9 owns the
gate sweep.

Three decisions were escalated to the user rather than settled here. All
three were answered the same day; the README carries the settled wording.

- **D4 — vocabulary home: `pkg/claierr`.** This overrides Finding 7's
  recommendation of `pkg/text/models`. Finding 7's analysis stands (both
  candidates are cycle-free), but it weighed the dependency graph and the
  decision turned on the consumer's import line: clai already aliases its own
  public models package as `pub_models`, so hosting errors there would push
  that namespace collision onto every downstream user. A stdlib-only
  `pkg/claierr` keeps `claierr.ErrRateLimited` self-describing at the call
  site and stays cycle-free on the same internal→pkg arrow.
- **D5 — exit codes stay 0/1**, as Finding 5 recommended. Phase 7 shrinks to
  stderr wording; the upstream `ExitCoder` is tracked separately.
- **D6 — MCP startup stays a warning, with the degraded state made readable
  by a `pkg/agent` caller**, as the S2–S4 recommendation proposed. The work
  folds into phase 8 rather than taking its own phase.

## Review findings

_(appended by reviewers)_
