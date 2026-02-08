# Version Command Architecture

Command: `clai version`

The **version** command prints build/version information and exits. It is implemented as a special-case in `internal.Setup()`.

## Entry Flow

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → VERSION
    → printVersion()
  → exit (utils.ErrUserInitiatedExit)
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | Dispatches VERSION mode |
| `internal/version.go` | Implements `printVersion()` |

## Output

`internal/version.go:printVersion()` prints:

1. If linker-injected build variables are present:
   - `version: <BuildVersion>`
2. Otherwise it reads module build info via `runtime/debug.ReadBuildInfo()` and prints:
   - `version: <bi.Main.Version>`
3. It then prints each module dependency:

```text
<dep.Path> <dep.Version>
```

## Build variables

`BuildVersion` and `BuildChecksum` are package-level variables intended to be set via build flags in a pipeline. If not set, `go install` builds will rely on `debug.ReadBuildInfo()`.

## Exit behavior

Returns `utils.ErrUserInitiatedExit` so the top-level runner exits with code 0.
