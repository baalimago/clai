# Help Command Architecture

Command: `clai help` (aliases: `h`)

The **help** command prints the usage string (defined in `main.go`) rendered with a few runtime defaults, plus some special-case help for `profile`.

This is intentionally separate from `-h` flag behavior; `clai -h` is discouraged and replaced by a dummy flag message.

## Entry Flow

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → HELP
    → printHelp(usage, allArgs)
  → exit (utils.ErrUserInitiatedExit)
```

## Key Files

| File | Purpose |
|------|---------|
| `main.go` | Defines the `usage` template string |
| `internal/setup.go` | HELP dispatch and `printHelp()` implementation |
| `internal/utils/config.go` | `GetClaiConfigDir`, `GetClaiCacheDir` used to fill in template |

## Behavior

### `clai help profile` (special case)

`printHelp()` checks:

- if `len(args) > 1 && (args[1] == "profile" || args[1] == "p")`

Then it prints `internal.ProfileHelp` and returns.

This is the deep-ish help for profile concepts and usage.

### General help (`clai help`)

`printHelp()`:

1. Resolves config and cache directories (best effort).
2. Calls `fmt.Printf(usage, ...)` to fill in defaults like:
   - default `-re`, `-r`, `-t`, `-g`, `-p` values
   - config dir + cache dir paths
3. Prints to stdout.

## Exit behavior

Returns `utils.ErrUserInitiatedExit`, so the process exits with code 0.
