# DRE (Directory Replay) Command Architecture

Command: `clai [flags] dre`

The **dre** command prints the most recent message from the **directory-scoped conversation** bound to the current working directory (CWD).

This is the directory-scoped analog of `clai replay` / `clai re`.

> Related: `clai -dre query ...` uses the bound chat as context. See `CHAT.md` (dir-scoped bindings) and `QUERY.md`.

## Entry Flow

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → DIRSCOPED_REPLAY
    → setupDRE(...) → dreQuerier
  → dreQuerier.Query(ctx)
    → chat.Replay(raw, true)
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | Dispatches DIRSCOPED_REPLAY mode |
| `internal/dre.go` | Implements the `dre` command querier (`dreQuerier`) |
| `internal/chat/replay.go` | `Replay(raw, dirScoped)` + `replayDirScoped` |
| `internal/chat/dirscope.go` | Directory binding storage + lookup (`LoadDirScope`) |
| `architecture/CHAT.md` | Background: how conversations and dir bindings work |

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
3. Print via `utils.AttemptPrettyPrint(..., raw)`.

## Error handling / exit codes

- On success, `dre` prints and returns nil; `internal.Setup` does not force exit (it returns a querier), so normal exit code is 0.
- Missing binding or missing conversation file returns an error and results in non-zero exit.
