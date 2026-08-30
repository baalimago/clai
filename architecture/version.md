# Version Command Architecture

Command: `clai version`

The **version** command prints build/version information and exits. It lives in `internal/version/cmd.go`.

## Entry Flow

```text
main.go:run()
  → cmd.Run(...)                     # go_away_boilerplate/pkg/cmd dispatch
    → version command Run (internal/version/cmd.go)
      → printVersion()
  → exit 0
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/version/cmd.go` | Implements the command and `version.Print()` |

## Output

`internal/version.Print()` prints:

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

Returns nil on success, so the top-level runner exits with code 0.
