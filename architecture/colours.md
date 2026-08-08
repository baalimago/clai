# COLOURS

This document describes how terminal colours work in clai.

## Overview

clai supports ANSI coloured output for a number of CLI printing paths (e.g. pretty printing chat messages and printing obfuscated chat summaries).

There are two key concepts:

1. **A global theme** loaded from `<clai-config-dir>/theme.json`.
2. **A global colour disable switch** using the standard `NO_COLOR` environment variable.

If `NO_COLOR` is set to a truthy value, clai emits **no ANSI escape sequences**.

## Theme file: `<clai-config-dir>/theme.json`

On startup, clai ensures a theme file exists and loads it.

- Path: `<clai-config-dir>/theme.json`
- Loader: `internal/utils.LoadTheme(configDir)`
- Startup hook: `internal.Setup(...)` calls `utils.LoadTheme(claiConfDir)` early.

The file is automatically created with defaults if missing.

### Theme fields

All colour fields are raw ANSI escape sequences represented as JSON strings.

| Field | Purpose |
|------:|---------|
| `primary` | Primary UI color (headers, structural prefixes). |
| `secondary` | Secondary UI color (interactive prompts, truncation markers). |
| `breadtext` | Default readable text color for table rows and general menu text. |
| `roleSystem` | Colour for `system` role labels. |
| `roleUser` | Colour for `user` role labels. |
| `roleTool` | Colour for `tool` role labels. |
| `roleReasoning` | Colour for `reasoning` role labels (thinking/chain-of-thought). |
| `roleOther` | Fallback colour for any other/unknown role. |
| `notificationBell` | Whether clai should emit terminal BEL (`\a`) after successful task completion. |
| `toolOutputRows` | Maximum terminal rows for each non-raw tool result. Default: `6`. |
| `rollingOutput` | Nested configuration for the shared rolling activity viewport. |
| `rollingOutput.enabled` | Use the shared rolling activity viewport. Default: `true`. |
| `rollingOutput.windowCellHeight` | Maximum terminal cells (rows) for the shared non-raw reasoning and tool activity viewport. Default: `30`. |

Defaults are chosen to match the existing `AttemptPrettyPrint` role palette (system=blue, user=cyan, tool=magenta, reasoning=warm-gray).

Example:

```json
{
  "primary": "\u001b[38;2;110;130;150m",
  "secondary": "\u001b[38;2;140;165;190m",
  "breadtext": "\u001b[38;2;200;210;220m",
  "roleSystem": "\u001b[34m",
  "roleUser": "\u001b[36m",
  "roleTool": "\u001b[35m",
  "roleReasoning": "\u001b[38;2;180;170;150m",
  "roleOther": "\u001b[34m",
  "notificationBell": true,
  "toolOutputRows": 6,
  "rollingOutput": {
    "enabled": true,
    "windowCellHeight": 30
  }
}
```

Existing theme files get `toolOutputRows: 6` when clai loads them. Set a
positive value to change the display limit.

Existing theme files get the `rollingOutput` block with defaults when clai
loads them. If `rollingOutput.enabled` is `false`, reasoning uses the plain
non-rolling thinking format. Tool activity keeps the new header and result
format, but clai appends each block without cursor redraws. Set
`rollingOutput.windowCellHeight` to a positive value to change the rolling
viewport size. The obsolete `reasoningOutputRows`, `activityOutputRows`, and
flat `rollingOutput` keys are ignored. The earlier kebab-case keys
`rolling-output` and `window-cell-height` are migrated to the camelCase
spelling on load, keeping their values.

`notificationBell` is intended for terminal/tmux attention behavior. Depending on terminal and tmux configuration, BEL may produce an audible bell, visual flash, or other attention marker.

Missing keys in an existing `theme.json` are filled from the defaults when the file is loaded, and the added keys are announced on the command line (e.g. `added new field(s) to theme.json: roleReasoning, tableItems`). A key that is present in the file is never overwritten, so a user's explicit `"notificationBell": false` stays disabled.

## Disabling colour: `NO_COLOR`

clai follows the common `NO_COLOR` convention (see also `main.go` usage text).

- Implementation: `internal/utils.NoColor()` (truthy check of `NO_COLOR`).
- All theme colour application should go through `internal/utils.Colorize(color, s)`.
  - When `NO_COLOR` is truthy, `Colorize(...)` returns `s` unchanged.

## Where colours are applied

### 1) Pretty printing chat messages

Function: `internal/utils.AttemptPrettyPrint(w, msg, username, raw)`

Behaviour:

- If `raw` is set: prints `msg.Content` directly.
- Else if `NO_COLOR` is set: prints `role: content` as plain text (no ANSI, no glow).
- Else:
  - If the destination is not a terminal (piped/redirected/captured output): prints a coloured `role:` prefix using `ancli.ColoredMessage`.
  - Else if `glow` is not installed: prints a coloured `role:` prefix using `ancli.ColoredMessage`.
  - Else: prints a coloured `role:` prefix and then runs `glow` to format markdown.

### 2) Obfuscated chat printing

Function: `internal/chat.printChatObfuscated(w, chat, raw, width)`

For older messages (all but the last 6 messages), it prints one-line summaries.
The `width` argument is the session's resolved terminal width; every truncation
in this render uses it, so one operation renders with one dimension set.

- The bracket prefix such as:

  `[#0   r: system    l: 00200]: ...`

  is styled as:

  - `[#... r: ` and ` l: ...]: ` uses `theme.primary`
  - the role value (e.g. `system`) uses the role colour from the theme

All colouring is applied via `utils.Colorize(...)`, so it automatically respects `NO_COLOR`.

### 3) Tables / menus (e.g. `clai chat list`)

- Table header + divider: `theme.primary`
- Table rows: `theme.breadtext`
- Interactive prompt line: `theme.secondary`

### 4) Completion notification

After a successful task/query completes, clai may emit terminal BEL depending on `theme.notificationBell`.

Implementation:
- `main.go:triggerCompletionNotification()`
- `internal/utils.NotificationBellEnabled()`

Raw `chat dir` and `chat dirv2` output does not include BEL. Redirected model
output and model output with `-rf` or `-response-format` also exclude BEL.

Structured output blocks all intermediate display output. These rules keep the JSON valid for tools such as `jq`.

### 5) Tool activity

Non-raw tool activity does not use glow. Each execution is one visually
separate block. The block starts with a primary-colour header such as
`▸ filesystem.list_directory  path=/work`, then has an indented result preview,
then one blank line. Built-in names stay unchanged. MCP names drop `mcp_` and
display as `server.tool`. Input keys are sorted, so headers are deterministic.

clai wraps the result preview for the terminal width and shows no more than
`toolOutputRows` terminal rows. At the default limit, clai shows the first
three rows, an omitted-row marker, and the last two rows. Error results have a
magenta `✗` marker. The full bounded result stays in the chat history and model
context. Assistant and tool role labels are not printed for these blocks.

Async lifecycle results are display summaries only. Start, status, await, and
cancel results show concise status, ID, and exit data. Log results show status
and decoded stdout/stderr preview text instead of a JSON envelope. Raw,
structured, and debug output keep their existing behaviour.

MCP server stderr output follows the session display mode. In a rolling-output
session, server stderr lines appear in the shared viewport as bounded blocks
with a secondary-colour header such as `▸ mcp.filesystem log`, one block per
server per drain, limited to the same body budget as tool results. Lines that
match the error keyword set (`error`, `fatal`, `panic`, `fail`, `exception`,
`denied`, `refused`, `unable`, `cannot`, `could not`, `warn`, `timeout`,
`unreachable`, case-insensitive) get a magenta `✗` marker. Error lines and the
10-line stderr tail of an unexpectedly terminated server are elevated outside
the window: the window region clears, the styled error block prints into the
scrollback, and the window redraws below it, so the error is never evicted.
Server logs never enter the chat history or model context. In plain non-rolling
and debug sessions the lines print directly as before; in raw, structured, and
redirected-output sessions normal lines are suppressed and only error lines
print, on stderr, so stdout stays clean.

### 6) Streamed reasoning and assistant prose

Normal streaming shows reasoning, assistant prose, and tool activity in one
rolling viewport. Reasoning starts with the warm-hue `∴ thinking` header.
Assistant prose that streams while the window is open (for example a short
preamble before a tool call) is added as its own block with an `assistant`
header; it is never re-printed outside the window. Its indented body is
neutral, like a reasoning or tool result body. The viewport uses no more
than `rollingOutput.windowCellHeight` terminal cells in total. Each tool
preview remains limited by `toolOutputRows`. Long text wraps by terminal
display-cell width, including wide Unicode characters. When the viewport is
full, each new row removes the oldest visible row and redraws the same
region.

The viewport stores logical activity blocks, not wrapped rows. Each block
keeps its complete sanitized content; colours and indentation are derived
again at each rewrap. When the terminal dimensions change, clai rewraps
every retained block at the new width and redraws the window in one frame.
The effective window height is `min(windowCellHeight, terminal height)`.
A terminal-height grow can bring retained content back into view, bounded
by retention, not by the row cap.

A terminal resize (`SIGWINCH`, for example a tmux pane resize) re-queries
the terminal size through the shared dimensions viewer, rewraps every
retained block at the new width, and redraws the window in one frame. The
resize is consumed in the serialized session loop, never in the signal
callback, so a redraw can never race a streamed token or tool write. Resize
bursts coalesce; a failed re-query keeps the last valid size. A resize that
arrives before the first reasoning or tool event updates the session
snapshot, so the first render already uses the new dimensions. Raw,
structured, debug, and redirected output never start the resize watcher.

The complete reasoning stays in the session, model context, and saved chat.
clai removes terminal control sequences only from the display copy. Raw,
structured, debug, and redirected stdout do not use this viewport. Redirected
stdout is identical to raw output. When activity ends, the final viewport
stays visible and the final answer starts below it.

## Customization

To customize colours, edit:

- `<clai-config-dir>/theme.json`

You can change values to any valid ANSI escape sequence (e.g. 24-bit colours via `\u001b[38;2;R;G;Bm`).
