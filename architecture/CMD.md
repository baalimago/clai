# Cmd Command Architecture

Command: `clai [flags] cmd <text>`

The **cmd** command is a specialized variant of the text querier (`query`) designed to produce shell commands. It reuses the text pipeline end-to-end, but:

- switches the system prompt to a “write only a bash command” prompt
- enables `cmdMode` on the querier, which adds an execute/quit confirmation loop after the model finishes streaming output

## Entry Flow

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → CMD
    → setupTextQuerierWithConf(..., CMD, ...)
       → Load textConfig.json (or default)
       → tConf.CmdMode = true
       → tConf.SystemPrompt = tConf.CmdModePrompt
       → applyFlagOverridesForText(...)
       → ProfileOverrides()
       → setupToolConfig(...)
       → applyProfileOverridesForText(...)
       → SetupInitialChat(args)
       → CreateTextQuerier(ctx, tConf)
  → querier.Query(ctx)
     → (stream tokens)
     → if cmdMode: handleCmdMode() → optionally execute
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | CMD mode dispatch; sets `CmdMode` and `SystemPrompt` before chat setup |
| `internal/text/conf.go` | Defines `CmdModePrompt` default and config fields |
| `internal/text/conf_profile.go` | Special handling when combining profiles with cmd-mode prompt |
| `internal/text/querier_setup.go` | Propagates `CmdMode` into the runtime querier (`querier.cmdMode`) |
| `internal/text/querier_cmd_mode.go` | Implements the cmd execution confirmation loop |

## Prompting / profiles interaction

In cmd mode, profiles are still allowed, but `internal/text/conf_profile.go` ensures the cmd-mode prompt stays authoritative. It wraps prompts in a pattern roughly like:

```text
|| <cmd-prompt> | <custom guided profile> ||
```

and explicitly warns the model not to disobey the cmd prompt.

Also note: profile tool enabling is restricted in cmd mode:

- `c.UseTools = (profile.UseTools && !c.CmdMode) || (len(profile.McpServers) > 0)`

So a profile cannot force-enable built-in tools in cmd mode, but MCP servers may still enable MCP tools.

## Runtime behavior (`handleCmdMode`)

After streaming completes, `handleCmdMode()`:

1. Prints a newline (streaming may end without `\n`).
2. Enters a loop:

   ```text
   Do you want to [e]xecute cmd, [q]uit?:
   ```

3. If user selects `e`, it executes the model output as a local process.

### Execution details (`executeLlmCmd`)

`executeLlmCmd()`:

- expands `~` to `$HOME` via `utils.ReplaceTildeWithHome`
- removes all double quotes (`"`) from the output to approximate typical shell expansion behavior
- splits by spaces into `cmd` + `args`
- runs `exec.Command(cmd, args...)` with stdout/stderr wired to the current process

Errors:

- non-zero exit code is wrapped into a formatted `code: <exitCode>, stderr: ''` message
- other exec errors are wrapped with context

## Security notes

Cmd mode can execute arbitrary commands. The safety mechanism is explicit user confirmation before execution. Tool restrictions via profiles/flags can further reduce risk, but cannot make executing a suggested command “safe” by itself.
