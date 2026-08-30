# Help System Architecture

There is no `help` command. Help has two surfaces, both derived from the
command map — nothing is hand-maintained:

1. **Bare `clai` (and unknown commands)** print the dispatcher usage:
   prerequisites, the generated sorted command table, a per-command `-h`
   pointer, config/cache dirs and examples. Exit code 1 (no command ran).
2. **`clai <command> -h`** (any level: `clai q -h`, `clai chat -h`,
   `clai chat list -h`) prints that command's `Help()` text — description
   plus an Examples block — followed by its flagset-derived flag list and,
   for a `Subcommander`, its subcommand table.

## Entry flow

```text
bare clai / unknown command
  → cmd.Run → ErrNoArgs / ArgNotFoundError
    → printUsage: usage template %v ← generated command table
      (config/cache dirs interpolated in main.run before cmd.Run)

clai <command> -h
  → the command's flagset returns flag.ErrHelp
    → cmd printHelp → helpText(command)
        = command.Help()            # description + Examples (internal/<domain>/cmd.go)
        + "Flags:" PrintDefaults    # claiCommand.Help(), flagset-derived
        + subcommand table          # upstream, for Subcommanders
```

The flagset's own output is silenced upstream (`parseFlagset` →
`io.Discard`), so stdlib's "Usage of x:" dump never duplicates the help.

## Key files

| File | Purpose |
|------|---------|
| `main.go` | `usageTemplate` + dir interpolation in `run()` |
| `internal/<domain>/cmd.go` | per-command help strings with Examples; `internal.Command.Help()` appends the flag list |
| `go_away_boilerplate/pkg/cmd` | `-h` routing, sub-table composition, sorted command table |

## Profile documentation

The old `clai help profile` doc (`internal.ProfileHelp`) lives in
`clai profiles -h`.
