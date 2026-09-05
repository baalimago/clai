# Phase 8 — chain-break repairs and swallowed terminal states

**Status:** Complete
[Worklog README](./README.md)

## Goal

Repair every chain-breaking site from the survey's tiers A–E, execute the
swallowed-state outcomes S2–S11 per the README repair table (D13, D16), and
convert clai's own substring matcher to `errors.Is`.

## Specification

Depends on phases 1 and 2; independent of the decoding phases. Site lists
were measured at the README's baseline ref — re-anchor by content (message
string, symbol), not line number. S1 is not here: it dissolved into the
frame decode point (README repair table).

### Chain-break repairs — tiers A–E

Repair rule: where a cause error exists, it is wrapped with `%w` (replacing
`%v` or `errors.New`-of-a-message); where the payload carries structured
facts (JSON-RPC code, status), they are preserved in the message. No
behavior changes beyond the error value itself, except where a row says so.

**Tier A — public surface (`pkg/agent`, `pkg/text`).**

| Site | Repair |
| --- | --- |
| `pkg/agent/agent.go` — `publicQuerier.Setup failed to CreateTextQuerier: %v` | `%w` |
| `pkg/text/full.go` — same message | `%w` |
| `pkg/text/full.go` — `pq.Query failed to Setup clone: %v` | `%w` |

**Tier B — query path.**

| Site | Repair |
| --- | --- |
| `internal/text/setup_querier.go` — `failed to create text querier: %v` | `%w` |
| `internal/text/setup_querier.go` — `failed to setup prompt: %v` | `%w` |
| `internal/text/setup_querier.go` — `profile override failure: %v` | `%w` |
| `internal/text/querier_setup_tools.go` — the unmarshal append | `%w` on the unmarshal error |
| `internal/vendors/openai/dalle.go` — `failed to get config dir: %v` | `%w` |
| `internal/vendors/openai/sora.go` — `failed to get config dir: %v` | `%w` |
| `internal/vendors/openai/sora.go` — `video generation %s: %v` with `job.Error` | preserve the job error content; `%w` where the value is an error |

**Tier C — config/IO plumbing.**

| Site | Repair |
| --- | --- |
| `internal/utils/config.go` — the six read/unmarshal/parse sites | `%w`, so `errors.Is(err, os.ErrNotExist)` and `errors.As` for `*json.SyntaxError` reach callers |
| `internal/utils/prompt.go` — `failed to read stdin: %v` | `%w` |
| `internal/theme.go` — `failed to find config dir: %v` | `%w` |

**Tier D — per-command, outside the text path.**

| Site | Repair |
| --- | --- |
| `internal/photo/cmd.go` — prompt setup and querier creation | `%w` |
| `internal/video/cmd.go` — prompt setup and querier creation | `%w` |
| `internal/chat/replay.go` — `failed to load previous reply: %v` | `%w` |
| `internal/audio/split.go` — ffprobe duration | `%w`, **and** split the `err != nil \|\| duration <= 0` branch so a well-formed non-positive duration gets its own message instead of `error: <nil>` |

**Tier E — non-`fmt.Errorf` breaks.**

| Site | Repair |
| --- | --- |
| `internal/tools/mcp/tool.go` — `errors.New(resp.Error.Message)` | include the JSON-RPC error code alongside the message |
| `internal/tools/mcp/tool.go` — `errors.New(buf.String())` | wrap with context naming the tool |
| `internal/tools/mcp/manager.go` — the initialize and tools/list `resp.Error` sites | include the JSON-RPC error code |
| `internal/text/tool_executor.go` — `errors.New(out)` | wrap with context naming the tool call |

**Not repaired** (deliberate, from the survey): the tool-result rendering
in `tool_executor.go` (`ERROR: ` prefix — rendering for the model is the
point), the usage-string `%v` in `internal/chat/handler.go`, and every
`pkg/tools` site (zero chain breaks there — README survey baseline).

### The explicit/ambient MCP split (D13, S2–S4)

In the MCP startup path (`internal/text/querier_setup_tools.go`,
`internal/tools/mcp/manager.go`):

- A server requested via `pkg/agent`'s `WithMcpServers` is **explicit**:
  any startup failure (spawn, initialize, tools/list) is collected and,
  after the existing in-`Setup` wait completes, joined into the `Setup`
  return as `claierr.NewMcpServerStartup(name, stage, cause)` per failed
  server. `Setup` returning nil means every explicitly requested server is
  running.
- A server discovered from the config directory is **ambient**: startup
  failure keeps today's warn-and-degrade.
- The CLI path registers no explicit servers, so its behavior is unchanged
  (D6).

### The remaining swallowed states (D16)

| Row | Repair |
| --- | --- |
| S5–S6 (`internal/tools/mcp/manager.go` response routing) | a `json.RawMessage` or `Response` that fails to unmarshal is delivered as an error to the request waiting on that id, not logged and dropped; the requester's call returns that error |
| S7 (`internal/tools/mcp/tool.go`) | a tool-response unmarshal failure returns an error result to the model instead of an empty success |
| S8 (`internal/text/finalizer.go`) | a failed reply persist joins the run's returned error (`errors.Join` with any run error) so a later replay cannot silently read stale state |
| S9 (`internal/text/querier_setup.go` catalog init) | stays a warning; a code comment states the README rationale (ambient capability) |
| S10–S11 (recorder seams) | stay as documented; a code comment at each site states the never-abort contract and that the consumer owns the recorder |
| the mistral delete-range warn (`internal/vendors/mistral/mistral.go`) | reworded to a plain warning; behavior unchanged (README repair table) |

### clai as its own consumer

`internal/chat/handler.go`'s `strings.Contains(err.Error(), "failed to
list chats")`: the producing site in the chat package wraps a new
package-internal sentinel (e.g. `errListChats`) with `%w`, and the handler
matches with `errors.Is`. The sentinel is internal — it is a chat-listing
state, not a provider meaning, so it does not join `pkg/claierr`.

### Files

- the sites above, plus their `_test.go` files
- `pkg/agent` / `internal/text` tests for the `Setup` contract

## Integration contract

| Trigger | Collaborators / fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| `Setup` with `WithMcpServers` naming a server whose process fails to start | agent with a fake/nonexistent server command | `Setup` returns an error: `errors.Is(err, claierr.ErrMcpServerStartup)`, `errors.As` yields the server name | run does not start | nil from `Setup`; the failure demoted to a warning |
| `Setup` with several explicit servers, more than one failing | fakes as above | one joined error; each failed server's name reachable | — | only the first failure reported |
| `Setup` with a failing ambient (config-dir) server and no explicit ones | config dir with one broken server json | `Setup` returns nil; warning printed; run proceeds without the server | degraded startup warned | `Setup` failing |
| MCP server answers a request with malformed JSON | fake MCP transport | the waiting request's call returns the unmarshal error | — | the error only logged; the requester hanging or receiving an empty success |
| Tool response unmarshal fails | fake MCP transport | the model receives an error result for that call | — | an empty success result |
| Reply persist fails at run end | querier with a failing persister | the run's returned error contains the persist failure (`errors.Is`-reachable) | — | a warn-only outcome with a nil run error |
| Chat listing fails under the handler | chat handler with failing lister | the handler's fallback path triggers via `errors.Is` | — | message-substring matching |
| Config file unreadable / malformed | config load with missing and with corrupt file | `errors.Is(err, os.ErrNotExist)` and `errors.As(err, *json.SyntaxError)` respectively hold at the caller | — | the cause formatted away |
| ffprobe reports a well-formed non-positive duration | audio split with stub ffprobe output | a distinct error naming the non-positive duration, not `error: <nil>` | — | the parse-failure message reused |

## Acceptance criteria

| Outcome | Evidence |
| --- | --- |
| `Setup` nil ⇒ every explicit server running; failures joined and typed (Definition of success, `Setup` item) | `Test_AgentSetup_ExplicitMcpFailure_JoinedTyped` |
| Ambient degradation unchanged | `Test_AgentSetup_AmbientMcpFailure_Degrades` |
| Malformed MCP responses reach their requester | `Test_McpManager_MalformedResponse_ErrorToRequester` |
| Tool unmarshal failures reach the model as errors | `Test_McpTool_UnmarshalFailure_ErrorResult` |
| Persist failures reach the caller | `Test_Finalizer_PersistFailure_JoinsRunError` |
| The substring matcher is gone | `Test_ChatHandler_ListChats_TypedMatch`; `grep -n "failed to list chats" internal/chat/handler.go` shows no `strings.Contains` use |
| Tier-A causes visible through the public surface | `Test_PkgAgent_SetupWrapsCause` |
| Tier-C causes visible to config callers | `Test_ConfigLoad_CausePreserved` |
| The audio-split condition is split | `Test_AudioSplit_NonPositiveDuration_DistinctFromParseError` |
| Zero chain-breaking sites remain in tiers A–E | the survey's AST recount re-run over the module reports zero (method: phase 1's Specification; the final phase re-verifies) |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Explicit MCP server fails at any startup stage | typed joined `Setup` error naming server and stage | `Test_AgentSetup_ExplicitMcpFailure_JoinedTyped` |
| Ambient MCP server fails | warn-and-degrade, `Setup` nil | `Test_AgentSetup_AmbientMcpFailure_Degrades` |
| Malformed MCP response payload | error delivered to the waiting requester | `Test_McpManager_MalformedResponse_ErrorToRequester` |
| Tool-response unmarshal failure | error result to the model | `Test_McpTool_UnmarshalFailure_ErrorResult` |
| Reply persist failure | joins the run's returned error | `Test_Finalizer_PersistFailure_JoinsRunError` |
| Chat listing failure | handler fallback via `errors.Is` | `Test_ChatHandler_ListChats_TypedMatch` |
| Missing / corrupt config file | `os.ErrNotExist` / `*json.SyntaxError` reachable | `Test_ConfigLoad_CausePreserved` |
| Non-positive ffprobe duration | distinct error, no `<nil>` rendering | `Test_AudioSplit_NonPositiveDuration_DistinctFromParseError` |

## Implementation notes

- 2026-09-05 — phase-8 worker subagent (session ended mid-phase; **In
  Progress**). Completed: the `mcp` package repairs (S4–S7 + Tier E):
  `Manager` gained an optional `startupFailures chan<- StartupFailure` report
  channel (typed `claierr.McpServerStartupError` per handshake stage, emitted
  by `handleServer`); `sendRequest` and `mcpTool.call` now deliver malformed
  frames to the waiting requester as errors instead of log-and-drop; JSON-RPC
  error codes are preserved in tool/initialize/tools-list errors. Completed:
  the explicit/ambient MCP split (D13): `AgentSettings.StrictMcpStartup`
  (set by `pkg/agent` when `WithMcpServers` is non-empty) makes
  `setupMcpManager` join typed per-server failures into the `Setup` return
  (spawn stage in `setupMcpManager`, initialize/tools-list via the Manager
  report channel); ambient config-dir servers keep warn-and-degrade;
  `setupTooling` returns the typed error, `NewQuerier` propagates it. Tree
  builds and `go vet` is clean on `internal/tools/mcp`, `internal/text`,
  `pkg/agent`; `internal/tools/mcp` tests pass. Remaining: tier A–E `%v`→`%w`
  repairs still outstanding (config.go six sites, prompt.go, theme.go,
  photo/video cmd.go, chat/replay.go, audio/split.go split-branch, mistral
  rewording, sora.go gofmt fix of the staged job.Error branch, tool_executor
  `errors.New(out)` context); S8 finalizer persist-joins-run-error; S9–S11
  comments; chat/handler `errListChats` sentinel + `errors.Is`; all
  acceptance tests; doc/status updates.

- 2026-09-05 — phase-8 continuation worker (session 2; **In Progress**).
  Completed so far (build + vet clean on every touched package):
  - Tier C: `internal/utils/config.go` six `%v`→`%w` sites;
    `internal/utils/prompt.go`; `internal/theme.go`.
  - Tier D: `internal/photo/cmd.go` + `internal/video/cmd.go` (prompt +
    querier creation); `internal/chat/replay.go`; `internal/audio/split.go`
    D6 split-branch (distinct non-positive-duration error, parse error now
    `%w`).
  - Tier E (non-mcp): `internal/text/tool_executor.go` `toolCallError` now
    takes the tool name and wraps with context (E5); test updated.
  - S8: `sessionFinalizerer.Finalize` now returns `error`;
    `sessionFinalizer.Finalize` collects a failed reply persist into
    `persistErr` (returned at every exit so the display branches keep
    running), `sessionRunner.Run`'s defer joins it into the run error;
    `countingFinalizer` test stub and `postProcess()` call site updated.
  - S9: comment added at the OpenRouter catalog-init warn (also fixed the
    "fether" typo). S10/S11: never-abort comments added at the usage-recorder
    and tool-call-recorder seams.
  - chat/handler.go: package-internal `errListChats` sentinel added;
    producing site wraps `%w: %w`; consumer matches `errors.Is` (substring
    matcher gone).
  - mistral delete-range warn reworded (`Errf` → plain `Warnf`).
  - sora.go staged job.Error branch gofmt-fixed and documented.
  - Tests added (not yet run as a set): `Test_ConfigLoad_CausePreserved`
    (utils), `TestSplitter_NonPositiveDuration_DistinctFromParseError` +
    `TestSplitter_ParseDurationFailureWrapsCause` (audio),
    `Test_ChatHandler_ListChats_TypedMatch` +
    `Test_findChatByID_ListFailureCarriesSentinel` (chat),
    `Test_McpManager_MalformedResponse_ErrorToRequester` (mcp),
    `Test_McpTool_UnmarshalFailure_ErrorResult` (mcp).
  Remaining (next session): `Test_Finalizer_PersistFailure_JoinsRunError`
  and the `pkg/agent` tests (`Test_PkgAgent_SetupWrapsCause`,
  `Test_AgentSetup_ExplicitMcpFailure_JoinedTyped`,
  `Test_AgentSetup_AmbientMcpFailure_Degrades` — full-path via
  `WithModel("mock_test")` + real `text.CreateQuerier` is proven offline by
  the cmd-ban e2e tests); D13-focused internal/text tests; run the package
  test suites and `go test ./... -race -count=3 -timeout=30s` (root-package
  e2e hang under `-count=3` is a recorded pre-existing environment flake,
  phase-6 journal); gofumpt/staticcheck/vet sweep; README status-board row 8
  → Complete; this file's status → Complete.

- 2026-09-05 — phase-8 completion worker (session 3; **Complete**).
  Finished the remaining acceptance work and ran the full gate set.
  - Tests added: `Test_Finalizer_PersistFailure_JoinsRunError`
    (internal/text, new `finalizer_test.go`); `Test_PkgAgent_SetupWrapsCause`
    (pkg/agent); `Test_AgentSetup_ExplicitMcpFailure_JoinedTyped` +
    `Test_AgentSetup_AmbientMcpFailure_Degrades` (pkg/agent, new
    `mcp_setup_test.go`, real path via `WithModel("mock_test")` — the
    cmd-ban e2e harness); D13 internal/text pins
    `Test_setupMcpManager_ExplicitSpawnFailuresJoinedTyped`,
    `Test_setupMcpManager_ExplicitHandshakeFailureTyped` and
    `Test_setupMcpManager_StrictModeKeepsAmbientDegrade` (manager report
    channel, spawn stage, strict-ambient asymmetry).
  - Recording: the acceptance table's audio name
    `Test_AudioSplit_NonPositiveDuration_DistinctFromParseError` shipped as
    `TestSplitter_NonPositiveDuration_DistinctFromParseError` in session 2;
    the implemented name pins the same row (D6 split-branch) and is the one
    kept.
  - Gates (run 2026-09-05, after all changes): gofumpt clean (one file
    reformatted: `internal/tools/mcp/manager.go`, session-1 leftover);
    `go vet ./...` ✓; staticcheck ✓; `go fix ./...` ✓;
    `go test ./... -race -cover -count=3 -timeout=30s` ✓ for all 44
    non-root packages (root package times out under `-count=3` on the
    recorded pre-existing e2e flake, `Test_e2e_setup_announcement_survives_interactive_wizard`;
    the root suite passes under `-count=1`, exit 0). dupl reads 33 clone
    groups by phase 4's exclusion method (30 at phase 5 close; +3 all from
    phase-8 acceptance-test additions across sessions 1–3, of which +1 is
    the cross-package startup-error walker pair — test-only, structurally
    forced because `errors.Join` exposes no iteration API and the two
    packages share no test package; accepted as a signal, not a verdict).
  - Coverage: pkg/agent 94.2%, internal/text 81.8% on the gate run above.

## Review findings

None.
