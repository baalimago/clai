# DRE (Directory Replay) Command Architecture

Command: `clai [flags] dre`

The **dre** command prints the most recent message from the **directory-scoped conversation** bound to the current working directory (CWD).

This is the directory-scoped analog of `clai replay` / `clai re`.

> Related: `clai -dre query ...` uses the bound chat as context. The directory binding record, its `version: 2` storage, and the always-on history recording this command reads from are defined in `architecture/dirscope.md`. See also `CHAT.md` (dir-scoped bindings) and `QUERY.md`.

## Entry Flow

```text
main.go:run()
  → cmd.Run(...)                  # go_away_boilerplate/pkg/cmd dispatch
    → dir-replay command Setup → dreQuerier (internal/chat/cmd.go)
    → adapter Run → dreQuerier.Query(ctx)
    → chat.Replay(raw, true)
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/chat/cmd.go` | dir-replay command definition (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/chat/cmd.go` | Implements the `dre` command and its querier (`dreQuerier`) |
| `internal/chat/replay.go` | `Replay(raw, dirScoped)` + `replayDirScoped` |
| `internal/chat/dirscope.go` | Directory binding storage + lookup (`LoadDirScope`) |
| `architecture/dirscope.md` | Authoritative spec for the binding record, history recording, and lookback |
| `architecture/chat.md` | Background: how conversations and dir bindings work |

## How it finds the conversation

Directory scope is loaded via `ChatHandler.LoadDirScope("")`; empty string means “use current working directory”.

If no binding exists (`ds.ChatID == ""`), `dre` errors with:

- `no directory-scoped conversation bound to current directory`

Bindings are created/updated primarily by:

- `clai query ...` (non-reply queries update the binding to the newly used chat)
- `clai chat continue <id|index>` (binds the selected chat to CWD)

## What it prints

Once `chatID` is resolved:

1. Load `<configDir>/conversations/<chatID>.json`.
2. Select the last message in the transcript.
3. If raw mode is active, remove `ReasoningContent` from the display copy.
4. Print via `utils.AttemptPrettyPrint(..., raw)`.

`clai -r dre` prints only the final message content. It does not print reasoning or the completion notification bell.

Stored reasoning remains in the conversation file.

## Error handling / exit codes

- On success, `dre` prints and returns nil; normal exit code is 0.
- Missing binding or missing conversation file returns an error and results in non-zero exit.
