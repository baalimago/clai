# Tooling System Architecture Addendum: Asynchronous Tool Async Commands

This document is an **addendum to `architecture/tooling.md`**. It specifies how clai should support **asynchronous tool execution** for long-running work where success is producing a managed child process that keeps running after the tool call returns.

This v1 design is intended for **POSIX systems only**. Process-group creation and signal-driven cancellation are part of the contract.

Typical cases are:

- port-forward sessions
- local servers started for later use
- long-running shell commands
- sub-agent or nested `clai` runs

The goal is simple: let an agent start a subprocess, get back a handle immediately, inspect it later, wait for it when needed, and cancel it if the plan changes.

Non-goals:

- persisting async commands across independent clai invocations
- exposing arbitrary daemon management outside the owning session
- introducing a distributed scheduler, queue, or workflow engine

## V1 scope

This document is intentionally declarative about **v1 only**.

V1 includes:

- a session-bound in-memory async command registry
- subprocess-backed async commands only
- `async_cmd_run`
- `async_cmd_status`
- `async_cmd_logs`
- `async_cmd_await` for one or more explicit `async_cmd_id` values
- `async_cmd_cancel`
- bounded previews plus on-disk logs
- cleanup of all session-owned async commands on session end

Explicitly out of scope for v1:

- filter-based `async_cmd_await`
- cross-session persistence
- non-process async command kinds
- compatibility wrappers around `clai_run`
- any MCP lifecycle unification work
- user-configurable retention or eviction policies

## Why this is needed

The current tooling model is mostly synchronous: a tool is called, it runs, and a result string is returned.

That works for file reads, searches, and short commands. It does not model workflows where success means **the process is still alive**.

Examples:

- `kubectl port-forward ...` must remain running while later steps use the forwarded port.
- a temporary HTTP server may need to stay alive for follow-up calls.
- a child `clai` run may continue working while the parent agent does something else.

## Existing foundation in clai

clai already contains the beginnings of this model.

### Subprocess tracking in existing tools

The current subprocess-oriented tools already demonstrate the essential behavior:

- non-blocking subprocess spawn
- stable ID generation
- stdout/stderr capture
- background wait goroutine
- later inspection by ID
- waiting for active async commands with timeout

These tools should be treated as a **reference for implementation**, not as the public shape of the new design.

### Existing lifecycle reference

`pkg/tools/clai_tool_run.go` and `pkg/tools/clai_tool_wait_for_workers.go` already demonstrate the essential subprocess lifecycle that v1 needs.

That code should be treated as an implementation reference for process tracking behavior, even though the public API proposed here is different and `clai_run` itself is not part of the target design.

## Design goal

Introduce a **generic async command runtime** for subprocess-backed tools.

This runtime should be:

- simple
- inspectable
- awaitable
- cancelable
- reusable
- session-bound

The purpose is not to invent a scheduler or orchestration system. It is only to make child processes manageable from the tool runtime.

## Hypothesis and validation

The design hypothesis is:

> clai already has enough subprocess lifecycle machinery that a small shared async command substrate can unify existing worker-style tools and future long-running commands without changing the synchronous tool-call contract.

This hypothesis is supported by the current codebase:

- `pkg/tools/clai_tool_run.go` already does non-blocking spawn, run-ID allocation, output capture, and background wait.
- `pkg/tools/clai_tool_wait_for_workers.go` already does later coordination over active subprocesses.

So the architectural move is justified. The main risk is not feasibility, but allowing the public API to drift away from the actual lifecycle guarantees the runtime can enforce.

## Core abstraction: Async command registry

Add a process-tracking subsystem conceptually named an **async command registry**.

It is the asynchronous analogue of the tool registry:

- the **tool registry** stores callable capabilities
- the **async command registry** stores active and completed subprocess async commands

Each async subprocess creates an **async command record**.

## Async command model

Each async command should have a structured record with fields roughly like these.

```json
{
  "async_cmd_id": "async_cmd_...",
  "kind": "process",
  "tool_name": "async_cmd_run",
  "owner": "session",
  "status": "starting|running|succeeded|failed|cancelled",
  "started_at": "RFC3339 timestamp",
  "finished_at": "RFC3339 timestamp or null",
  "pid": 12345,
  "argv": ["kubectl", "port-forward", "svc/api", "8080:80"],
  "cwd": "/repo",
  "stdout_log_path": "/tmp/...",
  "stderr_log_path": "/tmp/...",
  "stdout_preview": "...",
  "stderr_preview": "...",
  "exit_code": 0,
  "error": "..."
}
```

Not every field must be user-visible in the first implementation, but the internal model should support them.

One field should be clarified up front:

- `owner` should mean the in-process session identifier, not a durable user identity.

And one field should probably be deferred from the externally visible contract:

- `pid` is operationally useful, but should be treated as best-effort metadata rather than a primary handle. `async_cmd_id` must remain the only stable identifier.

## State machine

The lifecycle should stay minimal and predictable.

```text
starting -> running -> succeeded
starting -> running -> failed
starting -> failed
running -> cancelled
```

The important part is that status is typed and monotonic.

Cancellation semantics should also be explicit:

- if cancellation begins while an async command is non-terminal, the final status becomes `cancelled`
- `exit_code` and underlying process error remain recorded as metadata
- if the process reaches a terminal state before cancellation wins the race, preserve that original terminal state

The state model is stronger if terminality is explicit:

- non-terminal: `starting`, `running`
- terminal: `succeeded`, `failed`, `cancelled`

That distinction matters for `await`, `list`, and cleanup semantics.

## Ownership and cleanup

The session owns every async command.

When the parent clai process context is cancelled, interrupted, or completed, all async commands started in that session must begin cleanup. Async commands must not outlive the session that created them.

Completed async commands remain inspectable until the owning session ends. In v1:

- terminal async command metadata remains in memory until session teardown
- associated log files remain available until session teardown
- `async_cmd_id` values are unique within a session and are never reused

Tool output is also retained in the session conversation transcript as normal tool result messages. The on-disk stdout/stderr logs are therefore an operational companion for inspection, not the only place the agent-visible result exists.

This is the right default, but the shutdown contract should be made explicit:

1. request graceful termination first
2. wait a bounded grace period
3. force kill if still alive

Without this, AC6 is underspecified and may become flaky across platforms.

Concretely, the substrate should expose one cancellation policy used by both explicit `async_cmd_cancel` and session teardown:

```text
cancel request
  -> send graceful signal/interrupt
  -> wait up to configured grace period
  -> if process still alive, force kill
  -> record final terminal status
```

## Separation of concerns

The runtime should be split into two layers.

### 1. Async execution substrate

This is internal Go code responsible for:

- creating async command IDs
- spawning processes with context
- capturing stdout/stderr
- maintaining status transitions
- exposing thread-safe inspection, await, and cancel APIs

This layer should not know about LLM vendors.

### 2. Tool adapters

These are the tools exposed to the model, for example:

- `async_cmd_run`
- `async_cmd_status`
- `async_cmd_await`
- `async_cmd_cancel`
- `async_cmd_list`
- `async_cmd_logs`

These tools translate JSON input/output into substrate operations.

The public surface should stay intentionally small. That is a strength of the proposal.

## Recommended refactor path

The existing subprocess tools should be treated as the prototype to generalize.

### Step 1: extract common runtime types

Extract the shared concepts currently embedded in the subprocess tools:

- process record
- registry/map with mutex
- stdout/stderr log plumbing
- background wait goroutine
- exit status recording

Move these to an internal package, conceptually something like:

`internal/tools/async_cmds`

Suggested primitives:

- `Manager`
- `asyncCmd` (internal runtime type)
- `SpawnSpec`
- `Status`

### Step 2: build generic async tools

Add a small, general tool surface for async lifecycle management.

Recommended minimum set:

#### `async_cmd_run`

Starts a subprocess and returns a structured async command handle.

Inputs should be minimal:

```json
{
  "command": "kubectl",
  "args": ["port-forward", "svc/api", "8080:80"],
  "cwd": "/path/optional",
  "env": {"KUBECONFIG": "..."}
}
```

The command shape should follow the repo's existing safety posture. One tangible improvement is to state that this tool executes a program directly, not via implicit shell parsing. In other words:

- `command` is the executable path/name
- `args` is already tokenized
- shell features like pipes, redirects, `&&`, and glob expansion are unavailable unless the caller explicitly invokes a shell

That removes ambiguity and materially improves safety.

Recommended contract:

```json
{
  "command": "bash",
  "args": ["-lc", "kubectl get pods | head -n 5"]
}
```

If a caller wants shell semantics, they must opt in explicitly by launching a shell.

Output should include at least:

```json
{
  "async_cmd_id": "async_cmd_123",
  "status": "running",
  "pid": 12345,
  "stdout_log_path": "...",
  "stderr_log_path": "..."
}
```

#### `async_cmd_status`

Returns structured current state for one async command.

Recommended response shape:

```json
{
  "async_cmd_id": "async_cmd_123",
  "status": "running",
  "started_at": "2026-06-15T12:00:00Z",
  "finished_at": null,
  "pid": 12345,
  "exit_code": null,
  "error": null
}
```

#### `async_cmd_logs`

Returns either current output, a truncated view, or file paths if the output is too large.

This should also define whether logs are split by stream. A better default is:

- separate `stdout` and `stderr` fields
- optional previews plus file paths
- explicit truncation markers/flags

Otherwise agents will have to parse presentation text instead of structured output.

Recommended response shape:

```json
{
  "async_cmd_id": "async_cmd_123",
  "status": "running",
  "stdout": {
    "preview": "line 1\nline 2\n",
    "truncated": false,
    "log_path": "/tmp/clai-async-cmd-async_cmd_123-stdout.log"
  },
  "stderr": {
    "preview": "",
    "truncated": false,
    "log_path": "/tmp/clai-async-cmd-async_cmd_123-stderr.log"
  }
}
```

#### `async_cmd_await`

Waits for one or more explicitly named async commands to reach terminal state, with timeout.

In v1, this tool accepts explicit `async_cmd_id` values only. Filter-based await semantics are intentionally out of scope.

This tool should only return per-async-command terminal snapshots plus a deterministic aggregate result such as `completed`, `timed_out`, or `cancelled_by_session`.

Recommended response shape:

```json
{
  "result": "completed",
  "async_cmds": [
    {
      "async_cmd_id": "async_cmd_123",
      "status": "succeeded",
      "exit_code": 0,
      "error": null
    }
  ]
}
```

#### `async_cmd_cancel`

Requests that a running async command be stopped.

#### `async_cmd_list`

Deferred from v1.

## Spawn specification

Async execution should be described by a small spawn spec rather than scattered ad hoc code.

Conceptually:

```go
type SpawnSpec struct {
    Command []string
    CWD string
    Env map[string]string
    Labels map[string]string
}
```

This spec should stay intentionally small.

One concrete improvement: encode the executable separately from arguments in the internal API as well, for parity with the proposed external tool shape. For example, conceptually:

```go
type SpawnSpec struct {
    Command string
    Args []string
    CWD string
    Env map[string]string
    Labels map[string]string
}
```

This avoids repeated `[]string{binary, ...}` splitting/validation and aligns better with `exec.Command`.

If a caller needs to start a subprocess, the normal expectation should be:

- supply command
- supply args
- optionally set cwd
- optionally set env
- get back an async command handle

Anything beyond that should be justified by a demonstrated need, not designed up front.

## Interaction with the current synchronous tool interface

The tool interface can remain synchronous if async tools follow one simple rule:

- spawn tools return a handle immediately
- inspection, await, and cancel happen in later tool calls

So the call boundary is synchronous, while the subprocess lifecycle is asynchronous.

No future/promise abstraction is required.

The immediate output of each async lifecycle tool call is still captured like any other tool result through the normal tool invocation path and appended to the session transcript. This means:

- `async_cmd_run` returns the initial handle payload into the transcript
- `async_cmd_status`, `async_cmd_logs`, `async_cmd_await`, and `async_cmd_cancel` each append their structured result payloads into the transcript
- the referenced stdout/stderr log files complement those transcript entries with stream-oriented process output suitable for deeper investigation by both agent and user

## Suggested output shape

For agentic reliability, async tools should return stable, machine-friendly fields.

Recommendation:

- preserve human readability
- ensure stable field names
- include the `async_cmd_id` prominently

Example:

```json
{
  "async_cmd_id": "async_cmd_pf_abc123",
  "status": "running",
  "pid": 43122,
  "stdout_log_path": "/tmp/clai-async-cmd-async_cmd_pf_abc123-stdout.log",
  "stderr_log_path": "/tmp/clai-async-cmd-async_cmd_pf_abc123-stderr.log"
}
```

## Concurrency and thread safety

The async command registry must be safe under:

- concurrent tool calls
- background process completion goroutines
- awaits and cancels racing with completion

The current subprocess tracking pattern proves this is needed. The shared implementation should additionally prevent:

- duplicate terminal transitions
- reads of partially-updated state
- accidental deletion before status or await consumers finish

## Persistence strategy

Recommended initial position:

- **in-memory registry** for the current session
- **log files on disk** for larger output and postmortem inspection
- **no cross-process durable registry**

This stays aligned with session-only ownership.

This is sound. It also implies two user-visible constraints worth stating plainly:

- a later, separate `clai` invocation cannot inspect async commands created by a previous invocation
- while the session is alive, both transcripted tool results and referenced log files are valid investigation surfaces

## Relationship to MCP

MCP lifecycle management is a separate concern.

It may inform implementation style, but this async command design does not require shared managers, shared registries, or shared public APIs with MCP. Any future convergence should be justified separately.

## Security and safety

Async commands increase risk because they persist beyond one tool call.

Minimum safeguards:

- spawned commands remain subject to existing tool allow-list policy
- cwd/path constraints must still apply
- environment inheritance should be limited or explicit where possible
- await operations must always be bounded by timeout or session cancellation
- the runtime must clean up all session-owned children on cancellation or shutdown

For v1, the contract should also be explicit about two operational limits:

- child processes inherit the parent environment by default, with explicit overrides from tool input
- log previews are byte-bounded and full logs are retained only until session end

## Acceptance criteria

These criteria should map directly to e2e tests.

### AC1: async spawn returns a stable handle

Given a tool call that starts a long-running subprocess, clai returns a stable `async_cmd_id` immediately without blocking for completion.

### AC2: status inspection is typed and monotonic

For a running async command, `async_cmd_status` returns one of the documented states. Once an async command reaches a terminal state, later inspections never move it back to a non-terminal state.

### AC3: logs are inspectable while the async command is still running

While an async command is active, the agent can inspect current stdout/stderr previews and/or referenced log file paths.

### AC4: await works for one or multiple async commands

An agent can wait for one or multiple explicit async command IDs and receives deterministic completion or timeout results.

### AC5: cancel stops the subprocess

Cancelling a running async command transitions it to a terminal state and the underlying OS process no longer remains active after a bounded shutdown window.

### AC6: session cleanup prevents orphaned processes

When the owning session ends or is cancelled, all async commands started in that session are signalled for shutdown and do not remain orphaned.

### AC7: unknown async command IDs fail deterministically

Querying, awaiting, logging, or cancelling an unknown `async_cmd_id` returns a structured not-found error and does not mutate registry state.

## Suggested e2e test mapping

### E2E-1: basic non-blocking command

Start a command that sleeps briefly and then prints output.

Assert:

- spawn returns quickly with `async_cmd_id`
- immediate status is `starting` or `running`
- `async_cmd_await` completes successfully
- final logs contain expected output

### E2E-2: live log inspection

Start a command that emits lines over time.

Assert:

- mid-flight `async_cmd_logs` shows partial output
- final output contains all lines after await

### E2E-3: cancellation

Start a long-running command.

Assert:

- `async_cmd_cancel` returns success
- later `async_cmd_status` is terminal
- process is no longer alive

This test should additionally assert idempotency: cancelling an already-terminal async command should not corrupt state.

### E2E-4: session cleanup

Launch a background process from a session and terminate the session.

Assert:

- child process is signalled and exits
- no orphan remains after a bounded grace period

## Recommended implementation order

The lowest-risk order is:

1. extract a shared async command manager
2. add `async_cmd_run`
3. add `async_cmd_status`, `async_cmd_logs`, `async_cmd_await`, and `async_cmd_cancel`
4. implement retention, not-found handling, and cancellation semantics as first-class tests
5. remove `clai_run`-style worker-specific tooling rather than preserving it as part of the target design

## Summary

The correct mental model is:

- tools are still invoked synchronously
- some tools create **managed subprocess async commands**
- those async commands become runtime objects that can be inspected, awaited, and cancelled

clai already has the necessary implementation reference in existing subprocess handling. The architectural change is to build one small, session-bound async command subsystem for async tool processes, not to grow a larger orchestration layer or entangle it with MCP lifecycle management.
