# Man page

Command: `clai man`, `clai man install [<man root>]`. First-run install: the first run of a
release in a terminal.

The renderer, the `man` command and the installer live in go_away_boilerplate `pkg/cmd`
(`RenderManPage`, `NewManCommand`, `ManInstaller`); every CLI on that package gets a generic
`man` the way it gets `completion`. clai registers its own, with its page spec, and decides
when a run installs.

## Content

`internal/cli/manpage.go` `manInstaller` builds the page from the run's command table (help,
completion and man included, hidden spellings left out). NAME and SYNOPSIS come from
the usage's `Summary` and `Synopsis` (`usageText`, which renders `usageTemplate`),
DESCRIPTION from the usage after its title, and COMMANDS has one subsection per command path
with its `-h` text; these cannot drift from `clai help`. ENVIRONMENT (`manEnvironment`) and SEE ALSO are written in
`manpage.go` and must be kept in step with the variables a user sets. Help text is kept as
preformatted blocks; the home directory is written as `~`, so a root install reads right for
every user. The version is `version.Resolve`; when it cannot be resolved, `clai man` fails
and the first run warns.

## Where it goes

`ManInstaller` writes one target:

1. `<dir>/../share/man/man1` when the first `clai` on PATH is this binary and `<dir>` is named
   `bin`. man-db searches `<dir>/../share/man` for every PATH entry (verified on 2.13.1;
   macOS's FreeBSD-derived man does the same for entries ending in `/bin`), so `man clai`
   works with no setup. The PATH entry, not the resolved executable, is used, so symlinked
   installs (Homebrew, nix profiles) map to the prefix man searches.
2. Otherwise `$XDG_DATA_HOME/man/man1`, else `$HOME/.local/share/man/man1`, each used only when
   absolute; when neither is, the install fails. man searches this target only when
   `~/.local/bin` is on PATH or the directory is on MANPATH.

`clai man install <man root>` writes `<man root>/man1/clai.1` instead. Writes are a temp file
`.clai.1.tmp-*` in the same directory, mode 0644, renamed over `clai.1`; a failure removes the
temp file.

## First-run install

`cli.run` calls `firstRunManPage` before dispatch when all hold:

- `Deps.ManPage` is set. `DefaultDeps` leaves it nil, so no test or embedding program installs
  a page; `main.go` sets `FirstRunManPage()`, which is nil in the `wasix` build. `pkg/agent`
  never reaches `cli.run` (`pkg/agent/man_page_test.go`).
- stderr is a terminal and the output is text, not `-json`.
- `CLAI_MANPAGE` is not `off`.
- The command, the first positional or the word after a leading `--`, is not `__complete`,
  `completion`, `help` or `man`, and no `-h`/`-help`/`--help` comes before `--`.
- The version is a release, `vMAJOR.MINOR.PATCH` (`version.IsRelease`). Dev builds, pseudo
  versions, `+dirty` and the flake's `unstable-*` never install.

`ManInstaller.Ensure` then reads the version from the installed page's `.TH` footer
(`"clai <v>"`), without rendering, and writes only when that version is older than this
release by semantic version precedence; a page with no version there counts as older. Only
the newest release writes, so two releases that share one page (setup.sh's in `~/.local/bin`
and another run by path) settle on the newer one, and a downgrade keeps the newer page until
`clai man install`. Two first runs at the same moment can leave the older one's page; the
newer one's next run replaces it. An existing page it cannot read is an error, never
overwritten; `clai man install` replaces whatever is there.

| State | Meaning | The run says |
|---|---|---|
| `ManPageCurrent` | the page documents this version | nothing |
| `ManPageNewer` | the page documents a newer version | nothing |
| `ManPagePackaged` | a compressed `clai.1.gz` (or `.bz2`, `.xz`, `.lzma`, `.lz`, `.zst`, `.Z`) belongs to a package manager | nothing |
| `ManPageInstalled` | no page was there | `installed the man page for clai <v> in <path>; read it with 'man clai'` |
| `ManPageUpdated` | an older version's page was there | `updated the man page in <path> to clai <v>; ...` |

The notices are info level in subsystem `clai`. When man would not find the page (`Reach` is
`ManPageOffManPath`: MANPATH set without the root and without an empty part, or no PATH
entry's `../share/man` or `../man` is the root), the hint is `read it with 'man <path>', or add
<root> to MANPATH for 'man clai'`. A failure is one warning naming the error, `clai man
install` and `CLAI_MANPAGE=off`; it never changes the exit status.

## Installers

- `setup.sh` runs `clai man install <prefix>/share/man` with the binary it installed; a
  failure prints `skipped: <first stderr line>` and the install still succeeds.
- `flake.nix` `postInstall` runs `clai man > clai.1; installManPage clai.1`.

## Key files

| File | Purpose |
|------|---------|
| `internal/cli/manpage.go` | page spec, `man` registration, run gating, notices |
| `internal/cli/manpage_hook.go`, `manpage_hook_wasix.go` | `FirstRunManPage`, nil under `wasix` |
| `internal/version/cmd.go` | `Resolve`, `IsRelease` |
| go_away_boilerplate `pkg/cmd/man*.go` | renderer, installer, `man` command |
