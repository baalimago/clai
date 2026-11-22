# Help System Architecture

Help has four surfaces, all derived from the command map; only the man
page's ENVIRONMENT and SEE ALSO are written by hand:

1. **`clai help`, `clai -h`, `clai --help`** print the usage to stdout,
   with nothing on stderr, and exit 0: the title, prerequisites, the generated sorted
   command table (`help`, `completion` and `man` included), a per-command help
   pointer, config/cache dirs and examples. This is the surface agents are
   told to run.
2. **Bare `clai` and unknown commands** print the same usage through the
   dispatcher, after an error for an unknown command. Exit code 1 (no
   command ran).
3. **`clai <command> -h`**, or **`clai help <command>`** (any level:
   `clai q -h`, `clai help chat list`) prints that command's `Help()` text
   — description plus an Examples block — followed by its flagset-derived
   flag list and, for a `Subcommander`, its subcommand table.
4. **`man clai`** (or `clai man`) is the usage and every command's `-h` text
   as one man page; see [manpage.md](./manpage.md).

The upstream `completion` and `man` commands print their own one-paragraph
help, with no Examples block or flag list.

The title, `>|✦ clai - (c)ommand (l)ine (a)rtificial (i)ntelligence`, is
`internal.Title`: the logo `internal.Logo` and `internal.Summary`. The usage,
`clai chat help` and the man page's DESCRIPTION open with it; the README's
header and setup.sh's last line carry the logo too.

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

## Hidden spellings

`commandTable` skips every command whose `Hidden() bool` reports true, so the
pre-existing `setup|s`, `profiles`, `confdir` and `cloud publish` spellings
never appear in the usage table, and the completion engine skips them for the
same reason. A subcommand table (`subcommands:` under a command's help) is
filtered the same way, so `clai profiles -h` and `clai cloud -h` list only the
verbs that still live there. The hidden spellings still dispatch, and
`clai help profiles` or `clai profiles -h` explain the alias as before (see
[cmd-dispatch.md](./cmd-dispatch.md#compatibility-hidden-spellings)).

## Key files

| File | Purpose |
|------|---------|
| `internal/cli` | `usageTemplate`: the command table, a pointer to each command's own `-h` for its examples, `-json`, how to investigate with `LOG_LEVEL`, and the subsystem table; a `text/template` over `usageText` (title, summary, synopsis, command and subsystem tables, dirs), rendered by `usageText.write` |
| `internal/brand.go` | `Logo`, `Summary` and `Title`, shared by every usage |
| `internal/cli/help.go` | `helpAsCommand`, `withHelp`, `commandTable`, `subsystemTable`: the help command and its flag spellings |
| `internal/runenv/types.go` | `Subsystems()`: the one place a subsystem is registered, with the purpose the usage prints; the diagnostic types and the names `LOG_LEVEL` accepts come from it |
| `internal/cli/config.go` | the `config` tree and the `hiddenAlias` wrapper the hidden spellings re-enter `Run` through |
| `internal/<domain>/cmd.go` | per-command help strings with Examples; `internal.Command.Help()` appends the flag list |
| `go_away_boilerplate/pkg/cmd` | `-h` routing, sub-table composition, sorted command table |

## Profile documentation

The old `clai help profile` doc (`internal.ProfileHelp`) lives in
`clai profiles -h`, which stays callable as a hidden alias.
