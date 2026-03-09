# Replay Command Architecture

Commands:

- `clai replay` (aliases: `re`) – replay the most recent message from the global previous query (`globalScope.json`).
- `clai dre` – replay the most recent message from the *directory-scoped* conversation bound to the current working directory.

These are *display* commands; they don’t call any LLM vendor.

## Entry Flow

### `clai replay`

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → REPLAY
    → chat.Replay(postFlagConf.PrintRaw, false)
```

### `clai dre`

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → DIRSCOPED_REPLAY
    → setupDRE() → returns dreQuerier
  → querier.Query(ctx)
    → chat.Replay(raw, true)
```

`dre` is implemented as a small `models.Querier` wrapper so it fits the common `Setup() → Querier.Query()` execution pattern.

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | Dispatches REPLAY and DIRSCOPED_REPLAY modes |
| `internal/dre.go` | Implements `dreQuerier` and `setupDRE` |
| `internal/chat/replay.go` | Implements `chat.Replay(raw, dirScoped)` |
| `internal/chat/dirscope.go` | Directory binding resolution needed for dir-scoped replay |
| `internal/chat/reply.go` | Stores/loads `globalScope.json` |
| `internal/utils/pretty_print.go` (or similar) | `AttemptPrettyPrint` (glow formatting, raw mode) |

## What gets replayed

### Global replay (`clai replay`)

`chat.Replay(raw=false, dirScoped=false)`:

1. Loads `<clai-config>/conversations/globalScope.json` via `LoadPrevQuery("")`.
2. Selects the last message in the transcript.
3. Pretty prints it via `utils.AttemptPrettyPrint(..., raw)`.

If `globalScope.json` is missing, `LoadPrevQuery` prints a warning (`no previous query found`) and returns an empty chat.

### Directory-scoped replay (`clai dre`)

`chat.Replay(raw, dirScoped=true)` calls `replayDirScoped`:

1. Resolves config dir.
2. Loads the directory binding (from `conversations/dirs/` metadata) via `ChatHandler.LoadDirScope("")`.
3. If no binding exists: returns error `no directory-scoped conversation bound to current directory`.
4. Loads the bound conversation JSON `<clai-config>/conversations/<chatID>.json`.
5. Pretty prints the last message.

## Raw vs pretty output

Both `replay` and `dre` honor `-r/-raw`:

- raw: print message without glow/format post-processing
- non-raw: attempt markdown formatting via glow

## Relationship to query reply flags

- `-re` (reply mode) *uses* `globalScope.json` as context for the next query.
- `-dre` (dir-reply mode) is implemented by copying the directory-scoped conversation into `globalScope.json` (see `SaveDirScopedAsPrevQuery`) and then using the normal `-re` plumbing.

So `replay`/`dre` are for inspection; `-re`/`-dre` are for context selection.
