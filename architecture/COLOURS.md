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

All fields are raw ANSI escape sequences represented as JSON strings.

| Field | Purpose |
|------:|---------|
| `primary` | Primary UI color (headers, structural prefixes). |
| `secondary` | Secondary UI color (interactive prompts, truncation markers). |
| `breadtext` | Default readable text color for table rows and general menu text. |
| `roleSystem` | Colour for `system` role labels. |
| `roleUser` | Colour for `user` role labels. |
| `roleTool` | Colour for `tool` role labels. |
| `roleOther` | Fallback colour for any other/unknown role. |

Defaults are chosen to match the existing `AttemptPrettyPrint` role palette (system=blue, user=cyan, tool=magenta).

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
  - If `glow` is not installed: prints a coloured `role:` prefix using `ancli.ColoredMessage`.
  - If `glow` is installed: prints a coloured `role:` prefix and then runs `glow` to format markdown.

### 2) Obfuscated chat printing

Function: `internal/chat.printChatObfuscated(w, chat, raw)`

For older messages (all but the last 6 messages), it prints one-line summaries.

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

## Customization

To customize colours, edit:

- `<clai-config-dir>/theme.json`

You can change values to any valid ANSI escape sequence (e.g. 24-bit colours via `\u001b[38;2;R;G;Bm`).
