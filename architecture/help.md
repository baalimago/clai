# Help System Architecture

Help has three surfaces, all derived from the command map — nothing is
hand-maintained:

1. **`clai help`, `clai -h`, `clai --help`** print the usage to stdout,
   with nothing on stderr, and exit 0: prerequisites, the generated sorted
   command table (`help` and `completion` included), a per-command help
   pointer, config/cache dirs and examples. This is the surface agents are
   told to run.
2. **Bare `clai` and unknown commands** print the same usage through the
   dispatcher, after an error for an unknown command. Exit code 1 (no
   command ran).
3. **`clai <command> -h`**, or **`clai help <command>`** (any level:
   `clai q -h`, `clai help chat list`) prints that command's `Help()` text
   — description plus an Examples block — followed by its flagset-derived
   flag list and, for a `Subcommander`, its subcommand table.

## Entry flow

```text
clai help / -h / --help / -help
  → cli.helpAsCommand rewrites the flag to the help command
    → withHelp's command prints usage %v ← commandTable(cmds + completion)

clai help <command> ...
  → cli.helpAsCommand rewrites it to `<command> ... -h` (below)

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
| `internal/cli` | `usageTemplate` + dir interpolation in `Run()` |
| `internal/cli/help.go` | `helpAsCommand`, `withHelp`, `commandTable`: the help command and its flag spellings |
| `internal/<domain>/cmd.go` | per-command help strings with Examples; `internal.Command.Help()` appends the flag list |
| `go_away_boilerplate/pkg/cmd` | `-h` routing, sub-table composition, sorted command table |

## Profile documentation

The old `clai help profile` doc (`internal.ProfileHelp`) lives in
`clai profiles -h`.
