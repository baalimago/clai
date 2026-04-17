# Wrap command architecture

This document describes the `clai wrap` command: how it parses CLI input, executes a wrapped subprocess, captures that subprocess result, converts the captured data into a text prompt, and then hands that prompt to the normal text query flow.

The goal of `wrap` is to remove the need for manually copying command output into `clai`. A wrapped command is executed exactly once, its result is captured, and that result is analyzed by the configured text model.

## High level

`clai wrap` is a top-level command with this shape:

```text
clai [query flags...] wrap [wrap flags...] -- <cmd> [args...]
```

At a high level the flow is:

1. Parse normal top-level flags.
2. Resolve `wrap` as the selected command mode.
3. Parse wrap-specific flags and split on `--`.
4. Execute the wrapped subprocess exactly once.
5. Capture command line, stdout, stderr, and exit code.
6. Assemble a deterministic text prompt from that data.
7. Reuse the normal text query setup and execution path.
8. Print the model response using standard query output behavior.

This makes `wrap` a thin orchestration layer on top of the existing query architecture.

## Purpose and UX contract

The user experience contract for `wrap` is:

- the user does not manually paste command output
- the exact command is visible to the model
- stdout is available to the model
- stderr is available to the model
- the exit code is available to the model
- non-zero command exit is analyzed rather than treated as a fatal `wrap` error

`wrap` is not a shell history feature. It does not retroactively capture previously printed terminal output. It only captures the command it is directly asked to run.

## Entry flow

The command enters through the same top-level path as other commands:

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs()
    → setup wrap-specific querier
  → querier.Query(ctx)
```

`wrap` must be added as a new top-level mode in `internal/setup.go`, alongside `query`, `photo`, `video`, `chat`, and the other existing command modes.

## Command syntax and parsing

The required user-facing syntax is:

```text
clai [query flags...] wrap [wrap flags...] -- <cmd> [args...]
```

Rules:

- `wrap` is a top-level command.
- `--` is mandatory.
- everything after `--` is treated as the wrapped command and its arguments
- normal query flags remain valid
- wrap-specific flags apply only to prompt construction and subprocess capture behavior

User-visible error cases:

- no `--` separator
- `--` is present but no command follows it
- mutually exclusive wrap flags are combined
- the wrapped command cannot be started

These are user errors and must be surfaced before any model output is produced. If the wrapped command cannot be started, no query is performed.

## Wrapped command execution model

The wrapped subprocess is executed exactly once.

Execution properties:

- inherits the current working directory
- inherits the current environment
- stdout is captured
- stderr is captured
- output is not streamed directly to the user during subprocess execution

This is intentionally different from a normal shell run. The command result is buffered for analysis first, then the LLM answer is shown.

The subprocess exit code is always captured. A non-zero exit code does not stop the query path. Instead, it becomes part of the generated prompt.

## Captured data model

The prompt assembly stage always has the following conceptual inputs:

- rendered command line
- numeric exit code
- captured stdout
- captured stderr
- instruction text

Any of the output streams may be empty. Empty streams are still represented in the prompt using their normal section headers.

## Prompt assembly

`wrap` converts the captured subprocess result into a plain-text prompt and then forwards that prompt into the existing query flow.

The prompt always contains these sections, in this exact order:

1. instruction block
2. `Command:`
3. `Exit code:`
4. `Stdout:`
5. `Stderr:`

Default prompt format:

```text
Analyze this command result.

Command:
<shell-escaped command line>

Exit code:
<decimal exit code>

Stdout:
<stdout content>

Stderr:
<stderr content>
```

There is one blank line between sections. No extra metadata is added.

If the user supplies `-q` or `--question`, the instruction block is replaced entirely by the provided text. The rest of the structure is unchanged.

## Command rendering

The `Command:` section contains the wrapped command rendered as one shell-escaped line.

The purpose of this rendering is not shell execution. The purpose is visibility and fidelity in the prompt so the model can see the exact command shape the user ran.

This rendering must preserve distinctions between arguments, spaces, and special characters.

## Exit code handling

The prompt always includes a numeric exit code.

Rules:

- normal command exit uses its normal numeric code
- abnormal termination must still produce a deterministic numeric value derived from the process result available to Go

From the user’s perspective, `Exit code:` is always present and always numeric.

## Wrap-specific flags

The v1 behavior includes these wrap-specific flags.

### `-q, --question <text>`

Overrides the default instruction block.

### `--stdout-only`

Includes captured stdout in `Stdout:` and leaves `Stderr:` empty.

### `--stderr-only`

Includes captured stderr in `Stderr:` and leaves `Stdout:` empty.

### `--no-output`

Leaves both `Stdout:` and `Stderr:` empty.

### `--max-bytes <n>`

Limits captured stdout and stderr independently before prompt assembly. If a stream exceeds the limit, only the leading bytes up to that limit are kept. No truncation marker is added.

## Flag exclusivity rules

The following flags are mutually exclusive:

- `--stdout-only`
- `--stderr-only`
- `--no-output`

If more than one of these flags is provided, `wrap` fails before running the subprocess.

## Reuse of query architecture

Once the prompt has been assembled, `wrap` behaves like a normal text query.

That means it reuses the existing text-query pipeline described in `architecture/query.md`, including:

- text model selection
- profile handling
- shell-context injection
- tool allow-list behavior
- reply mode behavior
- output rendering behavior
- persistence of previous query context

Conceptually, `wrap` is implemented by synthesizing a query prompt rather than by creating a separate model execution stack.

## Query flag compatibility

All normal text query flags remain valid for `wrap`, including examples such as:

- `-r`
- `-re`
- `-cm`
- `-t`
- `-p`
- `-asc`

Those flags affect the LLM request exactly as they do for `query`.

They do not change how the wrapped subprocess is executed, except where a wrap-specific flag explicitly changes prompt content.

## Raw mode behavior

If raw output mode is enabled via `-r` or `--raw`, only the final model answer changes rendering mode.

The wrapped subprocess output still does not stream directly to the terminal.

## Reply behavior

When reply mode is enabled, the generated wrap prompt becomes the next user message within the normal reply flow.

`wrap` does not create a special conversation type. It participates in the same conversation persistence and replay mechanisms as normal text queries.

## Tool behavior

If the selected model run has tools enabled, the wrap-generated prompt is submitted as a normal tool-eligible query.

`wrap` does not expose subprocess output as separate tools. Stdout and stderr exist only as embedded prompt text in v1.

## Exit status of `clai wrap`

The exit behavior intentionally separates the wrapped command from the wrapping `clai` process.

- if the wrapped command runs and the LLM query succeeds, `clai wrap` exits successfully even if the wrapped command itself had a non-zero exit code
- if `wrap` fails before querying, or if the LLM query flow fails, `clai wrap` exits non-zero

This makes `wrap` a command-analysis operation rather than a simple subprocess proxy.

## E2E-visible behavior

The following outcomes are part of the v1 contract and should drive end-to-end tests:

1. `wrap` is recognized as a top-level command
2. missing `--` is rejected
3. `--` without a following wrapped command is rejected
4. captured stdout appears in the prompt sent to the model
5. captured stderr appears in the prompt sent to the model
6. captured exit code appears in the prompt sent to the model
7. a non-zero wrapped exit code still results in a query
8. `-q/--question` replaces the default instruction block
9. `--stdout-only` leaves the `Stderr:` section empty
10. `--stderr-only` leaves the `Stdout:` section empty
11. `--no-output` leaves both output sections empty
12. invalid combinations of output-selection flags fail before execution
13. normal query flags such as `-r` still work with `wrap`
14. `--max-bytes` truncates stdout and stderr independently
15. the `Command:` section is rendered as one escaped command line

## Out of scope for v1

The initial `wrap` command does not include:

- shell history integration
- automatically wrapping the previous shell command
- capturing previously printed terminal output
- shell-specific widgets or aliases
- streaming subprocess output live while also capturing it
- replaying a previous wrapped command execution

These may be built later, but they are not part of the v1 architecture contract.