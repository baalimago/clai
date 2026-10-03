# pkg/tools

`pkg/tools` holds the tools the clai agent can call. Every tool is a `models.LLMTool` value: a `Specification` that the model reads, and a `Call` method that runs when the model asks for it.

Pass tools to an agent with [`agent.WithTools`](../agent/README.md) or select them by name with `agent.WithToolGlobs`. The CLI uses the same catalog, so a tool behaves identically in both.

## Catalog

The tool name is what the model sees. The Go identifier is what you pass to `WithTools`.

| Tool name | Go value | Purpose |
|---|---|---|
| `apply_patch` | `ApplyPatch` | Add, update, delete or move files with a patch. |
| `async_cmd` | `AsyncCmdRun` | Start a session-owned subprocess in the background. Legacy alias `async_cmd_run`. |
| `async_cmd_status` | `AsyncCmdStatus` | Return the structured status of an async command. |
| `async_cmd_logs` | `AsyncCmdLogs` | Return bounded stdout and stderr previews plus log file paths. |
| `async_cmd_await` | `AsyncCmdAwait` | Wait for named async commands. Session end cancels the rest. |
| `async_cmd_cancel` | `AsyncCmdCancel` | Request cancellation of a running async command. |
| `audio_transcribe` | `AudioTranscribe` | Transcribe a local audio file to text, optionally with timestamps and speakers. |
| `cat` | `Cat` | Display the contents of a file. |
| `clai_check` | `ClaiCheck` | Check whether a `clai_run` run-id is running, completed or failed. |
| `clai_help` | `ClaiHelp` | Show the clai usage text. |
| `clai_result` | `ClaiResult` | Return the stdout, stderr and status code of a `clai_run` run-id. |
| `clai_run` | `ClaiRun` | Spawn clai as a non-blocking subprocess with a run-id. |
| `clai_wait_for_workers` | `ClaiWaitForWorkers` | Wait for the current clai workers, or cancel them on timeout. |
| `cmd` | `Cmd` | Run any freetext command. Returns an error on a non-zero exit code. Legacy alias `freetext_command`. |
| `cp` | `Cp` | Copy a file or directory recursively. |
| `date` | `Date` | Get or format the current date and time. |
| `ffprobe` | `FFProbe` | Extract metadata from media files. |
| `file_tree` | `FileTree` | List the filetree of a directory. |
| `file_type` | `FileType` | Determine the file type of a path. |
| `find` | `Find` | Search for files in a directory hierarchy. |
| `git` | `Git` | Run read-only git commands: log, diff, show, blame, status. |
| `go` | `Go` | Run `go` commands such as test, run and build. |
| `head` | `Head` | Display the first part of a file. |
| `jq` | `JQ` | Process a JSON file with the real `jq` executable. |
| `line_count` | `LineCount` | Count the lines in a file. |
| `load_skill` | `LoadSkill` | Load a trusted skill by name when a task matches it. |
| `ls` | `LS` | List the files in a directory. |
| `mkdir` | `Mkdir` | Create directories, including missing parents. |
| `mktemp` | `Mktemp` | Create a temporary directory removed when the run ends. |
| `pwd` | `Pwd` | Print the current working directory. |
| `rg` | `RipGrep` | Search for a pattern in files using ripgrep. |
| `rows_between` | `RowsBetween` | Fetch the lines between two line numbers, inclusive. |
| `rsync` | `Rsync` | Sync files or directories, with archive and partial-transfer modes. |
| `sed` | `Sed` | Regex substitution on a file, optionally within a line range. |
| `tail` | `Tail` | Display the last part of a file. |
| `website_text` | `WebsiteText` | Fetch a URL as text; markup is stripped from HTML. |
| `write_file` | `WriteFile` | Write content to a file, creating or overwriting it. |

`pkg/text/models` also declares a `ToolName` constant per tool name, plus three more names: `search_conversations`, `inspect_conversation` and `read_message`. Those three come from the CLI's directory-scope lookback, not from this package, so they are not in the table above.

## Choosing tools

With no `WithTools` and no `WithToolGlobs`, an agent gets the whole catalog. Both filters are narrowing, never widening:

```go
// Only these two tools.
agent.New(agent.WithTools([]models.LLMTool{tools.Cat, tools.RipGrep}))

// Everything from one MCP server, plus nothing else.
agent.New(agent.WithToolGlobs("mcp_playwright_*"))
```

## Async commands

The five `async_cmd` tools form one lifecycle: run, status, logs, await, cancel. A subprocess started with `async_cmd` is owned by the session. When the session ends, every command still running is cancelled, first gracefully and then forced. Await a command before you end a run unless you intend the cancellation.

Async commands run without a shell. Give the executable path and already-tokenized arguments instead of a command string.

## Refusing commands

`WithCmdBanList` applies to `cmd` and `async_cmd`. A refused command never reaches a subprocess, and the refusal names the entry that matched so the model can see why the call was refused. Entries are whitespace-split tokens matched against the command string, not shell glob patterns.

## Building your own

A tool is any value with `Call(models.Input) (string, error)` and `Specification() models.Specification`. Wrap a `Specification` literal to inherit the model-facing metadata:

```go
type PingTool models.Specification

var Ping = PingTool{
	Name:        "ping",
	Description: "Reply with pong.",
}

func (p PingTool) Specification() models.Specification { return models.Specification(p) }
func (p PingTool) Call(models.Input) (string, error)     { return "pong", nil }
```

The CLI inspects any tool in this shape. Run `clai tools` to list the registered set and `clai tools <name>` to print one JSON schema.

Design notes for the registry, the allow-list selection and the tool-call loop live in [`architecture/tooling.md`](../../architecture/tooling.md) and [`architecture/tooling-async.md`](../../architecture/tooling-async.md).