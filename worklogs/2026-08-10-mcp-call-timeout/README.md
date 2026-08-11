# MCP per-call timeout (`timeout_seconds`)

Analysis date: 2026-08-10. Implementation date: 2026-08-11 (clai repo, uncommitted).

## Status board

| #   | Phase                     | Status | Summary                                                                                  |
| --- | ------------------------- | ------ | ---------------------------------------------------------------------------------------- |
| 1   | Root-cause confirmation   | Done   | The worklog's A4.1 premise is wrong for v1.10.21; the real gap is the missing per-call bound |
| 2   | Design (scope A vs B)     | Done (annotated R1-01) | Per-call timeout chosen; teardown-on-timeout deferred. Rationale corrected in Review 1   |
| 3   | Tests-first implementation| Done   | 6 new tests + hang testserver tool, all red before implementation                        |
| 4   | Quality gates             | Done   | gofumpt, vet, fix, staticcheck, dupl, `go test ./... -race -count=3` all pass            |

## The full problem

### Incident (from sakfraga worklog 26-08-10-prod-harvest-skip-tweak.md)

The bulk-harvest round of 2026-08-10 hung 139/140 harvests. All five crawler
workers parked in `clai/internal/tools/mcp.(*mcpTool).call` for 8.5 hours in
`[select, 770 minutes]`, zero LLM calls after restart, zero outcomes. The
SIGQUIT goroutine dump showed every worker in the same stack: `selectgo` at
`tool.go:75`, waiting on the MCP `tools/call` response. The Playwright MCP
server never answered because chromium never finished a navigation.

### Root cause, confirmed against the deployed code

The worklog's addendum claimed the receive loop had no `case <-ctx.Done()`
(A4.1 patch target). That is factually wrong for the deployed binary:

- sakfraga pins `github.com/baalimago/clai v1.10.21` (go.mod:10, no vendor, no replace).
- `v1.10.21` resolves to `a7d5afc4` (2026-08-10 05:11:15Z), the current HEAD.
- The receive-loop ctx case was added 2026-06-17 (commit `bdfbcfbf`,
  "fix: Tool calls no longer freeze on mcp calls"), an ancestor of the tag.

The `[select, 770 minutes]` dump is exactly what a two-case select with
neither case firing looks like. The workers were parked because:

1. The Playwright server never wrote a response (chromium stuck on a
   navigation; the SYN_SENT sockets to 194.14.103.77 were chromium's own,
   a symptom).
2. The caller's context never fired: sakfraga's `runHarvestAgent`
   (`harvest.go:444`) passes the process-lifetime context — no deadline,
   nobody cancels it. The worklog's own quote of `harvest.go:438-441` states
   this explicitly.

Root cause in one sentence: **the MCP call wait had two exit conditions —
a server response or ctx cancellation — and in production neither ever
occurred, because nothing in the mcp package imposed its own time bound and
the caller passed a deadline-free context.**

## Design decisions

### Scope: per-call timeout (A), not server teardown (B)

Verified against the current architecture:

- Tool calls are sequential per executor (`tool_executor.go` runs a batch
  in a `for` loop): within one agent, no response stealing is possible.
  Across agents, calls are NOT serialized; see R1-01 for the shared-channel
  hazard and its follow-up.
- The receive loop is orphan-safe: `resp.ID != id → continue` discards late
  responses from timed-out calls.
- The receive loop already honors ctx (Fact above); the timeout only needs to
  arm it via `context.WithTimeout`.

Consequences of A: a hung call fails after N minutes, the worker is freed,
the server process survives, later calls get fresh bounds. B (kill the server
on timeout via `ev.Cancel`) was deferred: it kills the connection on any slow
call, and clai has no server auto-restart, so teardown removes capability
without restoring it.

### Config surface: one field, three surfaces

`McpServer` is the shared struct for all three definition surfaces:

1. server JSON file `<configDir>/mcpServers/<name>.json`
2. profile `mcp_servers` map
3. pkg API `Configurations.McpServers` / `agent.WithMcpServers`

Adding the field to the struct makes it configurable everywhere with zero
plumbing. Field shape: `TimeoutSeconds int`, `json:"timeout_seconds,omitempty"`,
`0` = unbounded. Matches the existing `TimeoutMS int` idiom
(`internal/text/shell_context.go:79`). Flags rejected as overkill.

### Rejected alternatives

- Read deadline on the client stdout pipe (worklog A4.2): kills the whole
  connection on one slow call; fragile with `bufio.Scanner`; the per-call
  timeout already bounds the caller and process death already surfaces as
  "connection closed".
- `notifications/cancelled` on timeout: correct nicety, but the wedged server
  in the incident was not reading stdin; deferred.

## Changes

| File | Change | Why |
| --- | --- | --- |
| `pkg/text/models/tools.go` | `McpServer.TimeoutSeconds int` field | Config knob, shared by all three surfaces |
| `internal/tools/mcp/tool.go` | `mcpTool.timeout`; `call()` wraps ctx with `context.WithTimeout` when > 0 | The bound; receive-loop ctx case is the exit |
| `internal/tools/mcp/manager.go` | `handleServer` wires `ev.Server.TimeoutSeconds` | One-line plumbing through the existing `ControlEvent.Server` |
| `internal/tools/mcp/testserver/main.go` | new `hang` tool that never answers | Wedged-server simulator for tests |
| `internal/tools/mcp/tool_test.go` | 5 new tests | Pin the timeout, the receive loop, the wiring |
| `internal/text/querier_setup_tools_test.go` | config-parse test | Pin the server-JSON surface |
| `architecture/tooling.md` | documents `timeout_seconds` | Config surface documentation |

The receive loop, the client, and the manager's lifecycle were not otherwise
touched: the server process is never killed (scope A).

## Tests

Written first; the package did not compile until the implementation landed
(red), then green.

- `TestMcpTool_CallWithContext_PerCallTimeout` — hang tool, 100ms tool
  timeout, 5s caller ctx: fails at ~100ms with `DeadlineExceeded`. Red
  before the fix (waited the full 5s caller deadline).
- `TestMcpTool_CallWithContext_ZeroTimeoutHonorsCallerDeadline` — `0` leaves
  the bound to the caller's ctx.
- `TestMcpTool_CallWithContext_CancelWhileWaiting` — pins the receive-loop
  ctx case (the June fix's untested path).
- `TestMcpTool_CallWithContext_SuccessWithTimeout` — timeout does not disturb
  a healthy round trip.
- `TestHandleServer_CarriesMcpServerTimeout` — `TimeoutSeconds: 7` reaches
  `mcpTool.timeout == 7s`.
- `Test_findConfiguredMcpServers_ParsesTimeoutSeconds` — server JSON
  `timeout_seconds` parses into the struct.

## QA validation

| Tool        | Command                                                            | Result |
| ----------- | ------------------------------------------------------------------ | ------ |
| Format      | `go run mvdan.cc/gofumpt@latest -w -l .`                           | clean  |
| Staticcheck | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`           | clean  |
| Lint        | `go vet ./...`                                                     | clean  |
| Test        | `go test ./... -race -cover -count=3 -timeout=30s`                 | pass   |
| Fix         | `go fix ./...`                                                     | clean  |
| Dupl        | `go run github.com/mibk/dupl@latest -t 80 .`                       | no new clones |

`internal/tools/mcp` coverage: 68.0% (re-measured in Review 1).

## Review 1 (2026-08-11)

Verdict: **ready to merge.** The per-call timeout is correct, complete, and
pinned by real subprocess tests. All six QA gates re-ran green. Four findings:
one major (the design rationale's "no response-stealing" claim is wrong for
multi-agent processes), three minor. R1-01 does not reopen the timeout phases:
the fix bounds the damage of the hazard it describes. The README design
section must be corrected, and per-server serialization becomes the next
design input.

### Gates re-run (this review)

| Tool        | Command                                                            | Result |
| ----------- | ------------------------------------------------------------------ | ------ |
| Format      | `go run mvdan.cc/gofumpt@latest -l .`                              | clean  |
| Staticcheck | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`           | clean  |
| Lint        | `go vet ./...`                                                     | clean  |
| Test        | `go test ./... -race -cover -count=3 -timeout=30s`                 | pass (mcp pkg 68.0%) |
| Fix         | `go fix ./...`                                                     | clean  |
| Dupl        | `go run github.com/mibk/dupl@latest -t 80 .`                       | 30 clone groups, none in changed files |

### Verified good

- Root-cause claims confirmed against git: `v1.10.21` resolves to
  `a7d5afc4` == HEAD; `bdfbcfbf` (the receive-loop ctx case, 2026-06-17) is
  an ancestor of the tag; the ctx case is present in tool.go at HEAD.
- Wiring complete: `ControlEvent.Server` carries the full `McpServer` struct
  (`querier_setup_tools.go:143-149`); `manager.go:120` sets `mcpTool.timeout`;
  all three config surfaces flow the field by struct copy (server-JSON parse,
  `conf_profile.go:93-101`, `agent.WithMcpServers`).
- Sequential-caller orphan handling correct: a late response after a timeout
  is held by the client reader, consumed by the next call, and discarded at
  `tool.go:115` on ID mismatch. It is never lost and never misdelivered in
  the sequential case.
- Timeout composes with the caller context: `WithTimeout` keeps the earlier
  of the two deadlines. Pinned by `ZeroTimeoutHonorsCallerDeadline`.
- The five new tool tests are real end-to-end tests against a subprocess
  server, not mocks. The author reports the package did not compile until the
  field and wiring landed (red before implementation); that is consistent
  with the diff (the field did not exist before).
- The `hang` tool swallows the request and continues its read loop — a
  faithful wedged-server simulator.

### Feedback index

| ID     | Severity | Phase | Summary |
| ------ | -------- | ----- | ------- |
| R1-01  | major    | 2     | "No response-stealing" rationale wrong for multi-agent processes; per-server serialization needed |
| R1-02  | normal   | 3     | Only one of three config surfaces pinned by a test |
| R1-03  | minor    | 2     | Negative `TimeoutSeconds` is an undocumented instant-fail |
| R1-04  | minor    | 4     | QA table overstates: dupl reports 30 groups; coverage is 68.0% |

### R1-01 (major) — the no-stealing rationale is wrong; calls are not serialized per server

The design section claims: "Tool calls are strictly sequential per
`outputChan` (tool_executor.go runs a batch in a `for` loop); the classic
shared-channel response-stealing hazard does not exist."

That is sequential per executor, not per server. Verified in the code:

- `tools.Registry` is process-global and `Set` is last-wins (`registry.go`).
- `setupMcpManager` runs once per agent setup
  (`querier_setup_tools.go:212`); every agent registers its tools into the
  same global registry.
- Two agents that register the same server name end with one `mcpTool` and
  one channel set. In the incident deployment (sakfraga: one agent per
  worker, every server named "playwright") all five workers route their
  calls through the last-registered `mcpTool` and wait on the same
  `outputChan`.
- `tool.go:87-115`: an unbuffered channel delivers each response to exactly
  one waiter. A waiter that receives another caller's response discards it
  at `tool.go:115`; the rightful waiter then starves until its own context
  fires.

Consequence: the hazard is structurally present in the deployment this
change fixes. It did not fire in the incident only because the server never
answered. This change bounds the damage — the starved waiter now fails
after N seconds instead of hanging forever — so the fix stands, and phases
1, 3, and 4 keep their Done status.

Action (checkboxes):
- [x] Correct the README design section (done in this review): calls are
  sequential per executor only, not per server; concurrent waiters on one
  channel can discard each other's responses.
- [ ] Add a per-server call mutex (or a per-server response dispatcher) in
  the mcp package as a follow-up phase. Scope-B and `notifications/cancelled`
  work must assume concurrent callers.

### R1-02 (normal) — one of three config surfaces is pinned by a test

The changes table claims "one field, three surfaces", but only the
server-JSON surface has a test
(`Test_findConfiguredMcpServers_ParsesTimeoutSeconds`). The profile
`mcp_servers` map and the pkg API copy the struct, so the risk is low — but
the claim is broader than the tests.

Action (checkbox):
- [ ] Add parse tests for the profile-map and pkg-API surfaces, or scope
  the claim to the server-JSON surface.

### R1-03 (minor) — negative TimeoutSeconds is an undocumented instant-fail

`call()` applies `WithTimeout` for any value > 0. A negative value is not
"unbounded": `WithTimeout` with a negative duration returns an
already-expired context, so every call fails instantly with
`DeadlineExceeded`, including healthy ones. A config typo ("-5") disables
the server loudly. The docs only define 0.

Action (checkbox):
- [ ] Validate or clamp `TimeoutSeconds` to >= 0 at the parse sites
  (`findConfiguredMcpServers`, `conf_profile`), or document that values <= 0
  mean unbounded.

### R1-04 (minor) — QA table overstates two results

- `dupl -t 80 .` reports 30 clone groups; none are in files this change
  touched (verified against the file list). The table row says "clean".
- Measured mcp package coverage in the re-run: 68.0%, not 68.3%.

Action (checkbox):
- [ ] Amend the QA table: dupl row "no new clones", coverage 68.0%.

### Design questions answered (as asked)

1. Field shape: keep `TimeoutSeconds int` with `omitempty`. It matches the
   existing `TimeoutMS` idiom, and seconds is the right unit for server
   timeouts (human-scale minutes).
2. `0` = unbounded: keep. It is the only backward-compatible default:
   existing configs and pkg callers keep today's behavior. Note the
   deployment implication: the fix is inert until sakfraga upgrades the pin
   AND sets the value. The open-items section already says this.
3. Scope-B flag now: do not add. The no-auto-restart argument is sound:
   killing the connection removes capability without restoring it.
   `notifications/cancelled` is ineffective against a server that does not
   read stdin. The higher-value follow-up is R1-01's per-server
   serialization, then sakfraga A4.3/A4.4.

### Cross-phase invariant (elevated to strategy)

The mcp package must not assume serialized callers. Nothing in clai
serializes tool calls across agents: the registry is global and last-wins,
and each agent sets up its own server. Future phases (scope B,
`notifications/cancelled`, per-server timeouts) must treat concurrent
callers on one channel set as the normal case and serialize or dispatch per
server.


## Open items / follow-ups

- sakfraga: set `TimeoutSeconds` (e.g. 300) on the Playwright MCP server
  config; the field is already consumable via `McpServer`.
- sakfraga A4.3: per-harvest `context.WithTimeout` in `runHarvestJob` — works
  against the already-deployed v1.10.21 (receive-loop ctx case present).
- sakfraga A4.4: workerpool liveness watchdog so one stuck agent cannot jam
  the daily sweep.
- Optional clai follow-ups: `kill_on_timeout` (scope B), `notifications/
  cancelled` on timeout.
- Deployment: tag + release of clai so sakfraga can upgrade its pin.
