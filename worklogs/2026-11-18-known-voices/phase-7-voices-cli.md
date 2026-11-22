# Phase 7 — voices CLI

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Let the user add a clip by hand and browse, play, keep, correct, trim, move and delete the clips of the
voice library from the command line, through the menu of "The voices menu, by example".

## Interface

A CLI. Its synopsis and clauses are below; the command tree that implements it is returned by
`VoicesCommand`, written to `internal/audio/voices_cmd.go`, and the root (phase 9) hangs it under
`audio` by `VoicesKey`, beside `transcribe` (amended 2026-11-19, A04). The synopsis writes each
short name beside its verb, as `clai audio -h` writes `t|transcribe` (amended 2026-11-19, R3-01: a
separate row `v, a, l    the short names of voices, add and list` is gone).

```text
clai audio v|voices a|add <wav> [-from s] [-to s] [-name n]
clai audio v|voices l|list [person [clip [action [args…]]]] [-json]

  <wav>      an audio file: a recording to cut a clip from, or a clip already cut
  -from s    where the clip starts in <wav>, in decimal seconds (default: its start)
  -to s      where the clip ends in <wav>, in decimal seconds (default: its end)
  -name n    the person; needed with -from or -to (default: the file's name)

  person     a person, by name or slug, without case
  clip       a clip of that person, by a unique prefix of its id or by its number in the table
  action     play | confirm | overrule | trim <from> <to> | move <person> | delete
  -json      print the level the path names as JSON; never a menu

  stdout     the levels, the JSON, the one line of an add or an action, and everything the menu shows
  stderr     every failure of the command, the line for an argument that matches nothing included
  exit       0  the menu was left, a level was printed, or an add or an action ran
             1  an add or an action failed, or, without a terminal, an argument matched nothing
             2  a usage error
```

Clauses:

- voices.1 — `voices` is a verb of `audio`, a sibling of `transcribe`, `calibrate` and `help`, and its
  short name is `v`: `clai audio voices …` and `clai audio v …` reach this CLI. `add`, short `a`, and
  `list`, short `l`, are its two verbs, and a short name is its verb in every respect, so
  `clai a v l lorentz 1a7d confirm` is `clai audio voices list lorentz 1a7d confirm`. This CLI is
  reached by no other path: after `transcribe` the word `voices` is a file (`Wire.4`).
  (`cmd-dispatch.md`, Subcommander descent; menu example 10; amended 2026-11-19, A04: it was a
  sub-verb of `transcribe` with no short names, and a file named `voices` had to be written
  `./voices`)
- voices.2 — `voices` with no verb, or with a word that is none of `add`, `a`, `list` and `l`, prints
  the synopsis on stderr and exits 2. The library is not opened. The synopsis names the commands as
  `clai audio v|voices a|add <wav> …` and `clai audio v|voices l|list [person …`, each short name
  beside its verb, and the usage line of `add` and of `list` in their usage errors takes the same
  form; the empty-library line of list.15 keeps the long form `clai audio voices add`, as the
  examples do. (menu rule 10; amended 2026-11-19, A04; amended 2026-11-19, R3-01: the synopsis and
  the usage lines named `clai audio voices add …` and `clai audio voices list …`)
- voices.3 — The exit status is 0 when the menu was left, a level was printed, or an add or an action
  ran; 1 when an add or an action failed, or when, without a terminal, an argument matched nothing; 2 on
  a usage error. A failure's message is on stderr and stdout stays as it was. (menu rule 10)
- voices.4 — No line, JSON document or error this CLI writes carries clip audio. The only programs it
  runs are the Inspector's and the player, both through the `Runner`; it opens no connection. (F8, F9)
- voices.5 — Flags are read wherever they stand after the verb, before or after its other arguments;
  `--` ends them. (menu examples 8, 9)
- voices.6 — A run context cancelled while the menu waits at a question, which is what Ctrl-C does
  there, ends `list` exactly as `q` does: the command returns no error (exit 0), nothing is written to
  stderr, stdout holds what `q` would have left, and nothing typed afterwards is acted on: no clip is
  deleted, moved, trimmed, confirmed or overruled by it. (menu rule 10, `Navigate.30`)

- add.1 — `add <wav>` inspects the file with `Inspector.Inspect`, from `-from` to `-to` (the whole file
  when neither is given, one bound alone leaving the other at the file's edge, bounds not rounded),
  enrols the result with `Enroller.Enrol` as `Confirmed` with source `SourceAdd`, prints the line
  `<name>: clip <id> added (confirmed)` on stdout, `<name>` being the name as given (the form the
  `audio_remember_voice` tool answers in), and exits 0. (Flow "Enrol a clip by hand", F5, menu example
  9)
- add.2 — Without `-name`, a clip already cut is named after its file: the file's base name without
  its extension, its first letter upper-cased, so `~/clips/christoph.wav` is Christoph, and that is the
  name given. It then passes the name rule like any other. (menu example 9)
- add.3 — `-from` or `-to` without `-name` is a usage error: the line `-from and -to cut a clip out of
  a recording, so the person must be named: add -name <person>` on stderr, exit 2. Nothing is inspected
  and nothing is enrolled. (menu example 9, menu rule 10)
- add.4 — No `<wav>`, more than one, or a `-from` or `-to` that is not a decimal number of seconds, is
  a usage error naming what is wrong, exit 2, with nothing inspected. (menu rule 10)
- add.5 — A clip the inspection refuses (`*ClipQualityError`, with its message, such as `clip too
  short: 1.5 s, a clip is 3–10 s`), a cut outside the file (`*CutError`), a missing program
  (`*MissingToolError`), a file that is not audio (`*SourceError`), a refused name (`*VoiceNameError`),
  a clip the person already holds (`*ClipExistsError`), a full voice (`*VoiceFullError`) and a library
  that cannot be read or written each print that error's message on stderr and exit 1. Nothing is
  enrolled. (Flow "Enrol a clip by hand", F5)
- add.6 — One enrolment error is not a failure of the add: when `Enrol` returns the new clip's
  reference with a `*LibraryWriteError` (`Enrol.9`: the clip is added, and the file of the clip pruned
  for it could not be deleted), `add` prints the line of add.1 on stdout, reports one warning of the
  run whose message is that error's message, which names the leftover file (`runenv.Warnf`, subsystem
  `audio`: in text the line `warn [audio] <message>` on stderr, the form of phase 9's `Output.5`), and
  exits 0. (`Enrol.9`, `Remove.5`, F5)

- list.1 — `list` is a menu of three levels run by `menu:Navigator`: the people, one person's clips,
  one clip. The path's arguments pick the levels and the menu opens at the deepest one the path names.
  (menu rule 1, `Navigate.1`)
- list.2 — The people level has one row per person of `Lister.List`, in its order, showing the name,
  the number of clips, of unconfirmed clips, of namings (the sum of `Named`), of wrong namings (the sum
  of `Overruled`) and when the person was last named (`never` when not). (menu rule 6)
- list.3 — A person's level has one row per clip, in `List`'s order, which is best first, numbered
  from 0, showing the clip's id, status, length, namings, wrong namings, last named and who added it
  (`you` for `SourceAdd`, `the agent` for `SourceRemember`). (menu rule 6, F7)
- list.4 — A clip's level shows those, when it was added, its place in the person's ranking and
  whether `Selector.Select` picks it for that person, and its path as the text
  `$(clai confdir)/voices/<slug>/<id>.flac`, on one line, whole. (menu rule 6, L4.1)
- list.5 — A person is named by the stored name or the slug, without case. A clip is named by a
  unique prefix of its id, or by its number in the person's table; a whole number below the person's
  clip count is always the number. (menu rule 2, `Navigate.2`)
- list.6 — An argument that names nothing says so in one line, shown above the level (`no voice named "ada"`;
  `Lorentz has no clip ffff`; for a prefix several clips share, a line saying so; `no action "sing"; the
  actions are play, confirm, overrule, trim, move, delete`) and the menu opens the level above it, as if
  the argument and those after it had not been given. (menu rule 3, `Navigate.3`)
- list.7 — An action given as an argument runs at once: nothing is asked, no level is shown, one line
  is printed and the command exits, with or without a terminal. (menu rule 4, `Navigate.5`)
- list.8 — In the menu, a number picks a row and shows the level below, `b` the level above, `q`
  leaves with exit 0. The clip's level offers play, confirm, overrule, trim, move, delete, back and
  quit; confirm is offered only while the clip is unconfirmed. (menu rules 5, 7, `Navigate.8`)
- list.9 — An action picked in the menu asks for what it needs (trim: from and to; move: the person),
  and delete, move and trim then ask a question that names the clip and run only on `y` or `yes`. Play,
  confirm and overrule run unasked. Afterwards the menu shows the level the action changed: the clip
  after play, confirm and overrule; the person's clips after delete, trim and move, or the people when
  the person has no clip left. (menu rule 4, `Navigate.11`, `Navigate.12`)
- list.10 — A picked action that fails shows its message above the clip's level and the menu stays
  there; leaving it later still exits 0. (menu rule 10, `Navigate.13`)
- list.11 — The people level has one filter, `u`, that shows only the people with an unconfirmed
  clip, and shows everyone when given again. (menu example 1, `Navigate.14`)
- list.12 — Tables fit the terminal's width by dropping whole columns in the order of L7.1; the
  person's name and the clip's id are never dropped, and no cell and no path is cut. (menu rule 8,
  `Navigate.15`)
- list.13 — There is a menu only when stdin and stdout are both a terminal and `-json` is not given.
  Otherwise the level the path names is printed once on stdout, with every row and no question, and the
  command exits 0; an argument that names nothing prints its line on stderr, nothing on stdout, and
  exits 1; an action given as an argument still runs. (menu rule 9, `Navigate.4`, `Navigate.16`)
- list.14 — `-json`, given after `list` or as the run's global `-json`, prints the level as one JSON
  document on stdout: always an array of `VoiceSummary` with the keys of `List.3` and each clip's
  absolute `path`. With no path it holds every person; with a person, that person alone; with a clip,
  that person with that clip alone. (menu rule 9, menu example 8)
- list.15 — An empty library is not an error. The people level is the line `no voices yet; add one
  with clai audio voices add <wav> -name <name>`, nothing is asked, `-json` prints `[]`, and
  the exit status is 0. A person or a clip named in an empty library matches nothing. (menu example 9;
  amended 2026-11-19, A04: the line names `clai audio voices add`)
- list.16 — More or fewer arguments than an action takes (`confirm x`, `trim 0.8`, `move`), and a
  trim bound that is not a decimal number of seconds, are usage errors: a line naming the action and
  its arguments on stderr, exit 2, and the action does not run. (menu rule 10, `Navigate.6`)
- list.17 — Browsing writes nothing: without an action, `list` leaves every file under `voices/` as
  it was. A library that cannot be read is its `*LibraryReadError` on stderr, exit 1. (F3, `List.5`)
- list.18 — On a terminal the menu shows one level at a time: each level, with the line of the action
  or the miss that led to it, replaces the one before it on the screen, and nothing that was on the
  screen before the command started is erased. When the menu is left, the last level and its line
  stay. Without a terminal, with `-json`, and for an action given as an argument, nothing is erased and
  no cursor movement is written. (menu rules 8, 9, `Navigate.24`–`Navigate.28`)

- play.1 — Plays the clip's file with the first of `play`, `ffplay -nodisp -autoexit`, `xdg-open` and
  `open` that `Runner.LookPath` resolves, the file's path as the last argument, through `Runner.Run`.
  A line saying the clip's length and the player is printed before the player starts; when stdin is a
  terminal it ends `· [Enter] stops`, and otherwise it has no such hint. In the menu the clip's level
  is shown again when the player ends. (Flow "The user plays a clip", L7.3)
- play.2 — With none of them, the action fails with a `*NoPlayerError` whose message names `play`,
  `ffplay` and the OS opener; a player that fails is a `*PlayError` naming it. Nothing under `voices/`
  changes either way. (Flow "The user plays a clip", F8)
- play.3 — When stdin is a terminal, a completed line on it while the clip plays stops the clip: the
  player's `Runner.Run` is cancelled through its context. Only a line that ends in a newline is an
  Enter press; input that ends, or a line not yet ended, stops nothing. Stopping is not a failure,
  whatever the cancelled player returns: in the menu the clip's level is shown again, and for the
  action given as an argument the command exits 0 with the play line as its only output. (Flow "The
  user plays a clip")
- play.4 — A clip that ends by itself swallows no line: the next line typed after it is the menu's
  next answer, as if nothing had been waiting for an Enter. For the action given as an argument the
  command exits 0 when the player ends, without waiting for a line. (Flow "The user plays a clip")
- play.5 — When stdin is not a terminal, nothing is read from it while the clip plays, and the clip
  plays to its end. (Flow "The user plays a clip", menu rule 9)
- confirm.1 — Calls `Reviewer.Review(ref, Keep)` and prints `<Name>/<id>: confirmed`. On a clip that
  is confirmed already it succeeds as well (`ReviewAlreadyConfirmed`): a line says so, nothing is
  appended, exit 0. (Flow "The user keeps a learned clip", F5, `Review.1`, `Review.2`)
- overrule.1 — Calls `Reviewer.Review(ref, Overrule)` and prints a line with the clip's wrong namings
  and namings as `Reviewed.Clip` has them. Every call counts once more against the clip; the clip stays.
  (Flow "The user corrects a name", F6, `Review.3`)
- delete.1 — Calls `Remover.Remove(slug, id)` and prints `<Name>/<id>: deleted`, for a confirmed clip
  as for an unconfirmed one. Deleting a person's last clip removes the person from every level. (Flow
  "The user deletes a clip", F5, `Remove.1`)
- trim.1 — `trim <from> <to>`, in decimal seconds from the clip's start: inspects the clip's file with
  `Inspect(file, Cut{from, to})`, enrols the result for the same person with the old clip's status and
  source and `Replaces` set to the old clip, then removes the old clip. The line names the old clip, the
  new length and the new id. The new clip has no namings. (Flow "The user trims a clip", F5, F10)
- trim.2 — A trim never prunes another clip and is not refused at the cap, whatever the person's
  clips are. (L4.2, `Enrol.5`, `Enrol.6`)
- trim.3 — A trim the Inspector or `Enrol` refuses changes nothing: the old clip stays as it was, with
  its namings, and the error's message is the failure (`clip too short: 1.5 s, a clip is 3–10 s`). (menu
  example 7)
- trim.4 — When the new clip is enrolled and the old one cannot be removed, the failure is a
  `*ReplaceError` naming both, and both are in the library. (F5)
- move.1 — `move <person>`: inspects the clip's whole file with `Inspect(file, Cut{})`, enrols it for
  the other person with the old clip's status and source, then removes it from the first person. The
  clip keeps its id and starts with no namings there. The line names both people, the id and the status.
  (Flow "The user moves a clip to another person", F5, `Inspect.3`)
- move.2 — The other person is named as in list.5; a name the library does not hold creates that
  person, by the name rule, and the result line then says the person is new, as in `Christoph/<id>
  moved to Ada, a new person (unconfirmed)`, so a mistyped name is seen. In the menu, the question asked
  before the move says so too. The other person's cap applies as for any enrolment. (`Enrol.3`,
  `Enrol.5`, `Enrol.6`)
- move.3 — A move the Inspector or `Enrol` refuses (the other person holds the clip already, is the
  same person, or is full; the name is refused) changes nothing; a move whose removal fails is a
  `*ReplaceError` as in trim.4. (F5)
- move.4 — When the enrolment for the other person returns the new reference with a
  `*LibraryWriteError` (`Enrol.9`: a clip of that person was pruned and its file could not be deleted),
  the move goes on as move.1 says: the clip is removed from the first person, and the move's line ends
  with `; warning: ` and that error's message, still one line. Nothing is printed apart from that
  line, on stderr or elsewhere: the menu draws its notices itself (`Navigate.24`). It is a move that
  ran: exit 0 as an argument, and in the menu the line shows above the level the move changed. A trim never prunes
  (trim.2), so it never meets this. (`Enrol.9`, `Navigate.5`, `Navigate.12`)

```go
package audio

import (
	"context"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio/voices"
)

// VoicesKey is the key `audio` holds this command under: the verb and its
// short name, as the dispatcher reads a key. (amended 2026-11-19, A04: it
// was "voices", the key `transcribe` held the command under)
const VoicesKey = "voices|v"

// VoicesDeps is what the voices CLI works with.
type VoicesDeps struct {
	Librarian voices.Librarian
	Inspector voices.Inspector
	Runner    voices.Runner
	// Width returns the terminal's width; nil means the width of the run's
	// stdout. It is the menu Session's Width.
	Width func() int
}

// VoicesOpener returns the dependencies of one run.
type VoicesOpener func(ctx context.Context) (VoicesDeps, error)

// VoicesUsageError says a voices command cannot be run as written. Its
// message is Line.
type VoicesUsageError struct{ Line string }

func (e *VoicesUsageError) Error() string

// ExitCode is 2.
func (e *VoicesUsageError) ExitCode() int

// NoPlayerError says the machine has none of the players. Tried are their
// names, in the order they were looked for.
type NoPlayerError struct{ Tried []string }

func (e *NoPlayerError) Error() string

// PlayError says the player failed. Detail is its own last message, never
// audio.
type PlayError struct {
	Player string
	Detail string
	Err    error
}

func (e *PlayError) Error() string
func (e *PlayError) Unwrap() error

// ReplaceError says a trim or a move enrolled New and could not remove Old:
// both are in the library.
type ReplaceError struct {
	New voices.ClipRef
	Old voices.ClipRef
	Err error
}

func (e *ReplaceError) Error() string
func (e *ReplaceError) Unwrap() error

// VoicesCommand returns the `voices` command with its verbs `add`, short
// `a`, and `list`, short `l` (voices.1).
//
// - VoicesCommand.1 — open is called once per run of `add` or `list`, when
//   the verb starts, and never for a usage error or for help. Its error is
//   the run's failure, unchanged.
// - VoicesCommand.2 — The command holds nothing between runs but open, and
//   reads its streams, their terminal state and the global -json from the
//   run's Env.
func VoicesCommand(open VoicesOpener) *internal.Command
```

Consumes:

- `Navigator`, `New`, `Session`, `Level`, `Column`, `Row`, `Field`, `Action`, `Param`, `Filter`,
  `Result`, `Opener`, `Outcome`, `UsageError`, `NoMatchError` (phase 6)
- `Librarian` (`Enrol`, `List`, `Select`, `Review`, `Remove`), `Enrolment`, `VoiceSummary`,
  `ClipSummary`, `Asked`, `Selection`, `Keep`, `Overrule`, `Reviewed`, `ReviewConfirmed`,
  `ReviewAlreadyConfirmed`, `ReviewOverruled`, `ClipExistsError`, `VoiceFullError`,
  `VoiceNotFoundError`, `ClipNotFoundError`, `LibraryWriteError`, `NewLibrarian` (phase 4); the
  `Enrol.9` result of a reference beside a `*LibraryWriteError` (phase 4)
- `Inspector`, `Inspection`, `Cut`, `Runner`, `ClipQualityError`, `CutError`, `MissingToolError`,
  `SourceError` (phase 3); `NewInspector` (phase 3), by the tests
- `internal/audio/voices/voicestest:NewRunner`, `Runner.Serve`, `Runner.Fail`, `Runner.Missing`,
  `Runner.Calls`, `Speech`, `Tone`, `Silence`, `SampleRate` (phase 3), by the tests: the runner under
  the suite's real Inspector (L7.2)
- `NewRanker` (phase 2), for the suite's Librarian
- `ClipRef`, `ClipID`, `Name`, `Slug`, `Status`, `Confirmed`, `Unconfirmed`, `Source`, `SourceAdd`,
  `SourceRemember`, `SlugOf`, `VoiceNameError`, `LibraryReadError` (phase 1)
- `internal:Command` (`Subs`, `Register`, `OnSetup`, `OnRun`, `Args`), `StringFlag`, `BoolFlag`,
  `ExitCoder`; `go_away_boilerplate/pkg/cmd:Subcommander` (import)
- `internal/runenv:Env` (`Stdin`, `Stdout`, `Stderr`, `StdinIsTerminal`, `IsTerminal`, `JSON`,
  `WriteJSONDocument`, `TablePrompter`) (import)
- `internal/runenv:Warnf`, `SubsystemAudio` (import): add.6's warning; the suite's Env reports
  warnings to the stderr the row captures
- `internal/testenv:Main` (import)

The real `Librarian`, `Inspector` and `audio:ExecRunner` are wired by the root (phase 9), which also
proves voices.1 on the real `audio` tree (amended 2026-11-19, A04).

## Acceptance criteria

| #   | Criterion                                                                                                              | Proves                                              | Proof                                                              |
| --- | ---------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------- | ------------------------------------------------------------------ |
| 1   | `voices` is a verb of `audio`, reached by `voices` and `v`, its verbs by `add`, `a`, `list` and `l`; after `transcribe` the word `voices` is a file; a missing verb is a usage error (amended 2026-11-19, A04) | voices.1, voices.2                                  | `TestVoices_Verb`, `TestVoices_NoVerb`                             |
| 2   | Menu example 9: a clip already cut is named after its file, a cut needs `-name`, and both are enrolled confirmed        | add.1, add.2, add.3, voices.5                       | `TestVoicesAdd_PreSliced`, `TestVoicesAdd_Cut`, `TestVoicesAdd_CutNeedsName`, `TestVoicesAdd_FlagsAnywhere` |
| 3   | A bad `add` is exit 2 when it is written wrong and exit 1 when the clip or the library refuses it; nothing is enrolled  | add.4, add.5, voices.3                              | `TestVoicesAdd_Usage`, `TestVoicesAdd_Refusals`                    |
| 4   | Menu example 1 and rules 1, 5, 6: browsing from the top walks people, clips, clip, with the README's columns            | list.1, list.2, list.3, list.4, list.8              | `TestVoicesList_BrowseFromTop`                                     |
| 5   | Menu examples 2, 3 and rule 2: a person by name or slug without case, a clip by id prefix or number                     | list.5                                              | `TestVoicesList_StraightToPerson`, `TestVoicesList_StraightToClip` |
| 6   | Menu example 4 and rules 4, 7: each of the six actions runs at once as an argument and prints one line                  | list.7, play.1, confirm.1, overrule.1, delete.1, trim.1, move.1 | `TestVoicesList_ActionArguments`                       |
| 7   | Menu example 5 and rule 4: a picked delete, move or trim asks first and returns to the level it changed                 | list.9, list.10                                     | `TestVoicesList_PickedActionAsksFirst`, `TestVoicesList_PickedActionFails` |
| 8   | Menu example 6 and rule 3: a name that matches nothing says so and opens the level above                                | list.6                                              | `TestVoicesList_NoMatchOpensLevelAbove`                            |
| 9   | Menu example 7: a failed action prints why, changes nothing and exits 1                                                 | trim.3, voices.3                                    | `TestVoicesList_ActionFails`                                       |
| 10  | Menu example 8 and rule 9: without a terminal the level is printed once, a miss exits 1, `-json` prints the level       | list.13, list.14                                    | `TestVoicesList_WithoutTerminal`, `TestVoicesList_JSON`            |
| 11  | Menu rule 8: columns drop in order, the clip's id and path are never cut, and on a terminal each level replaces the one before | list.12, list.4, list.18                     | `TestVoicesList_ColumnsFit`, `TestVoicesList_OneLevelAtATime`      |
| 12  | Menu rule 10: exit status 0, 1 and 2                                                                                    | voices.3, list.16                                   | `TestVoicesList_ExitStatus`, `TestVoicesList_Usage`                |
| 13  | Confirm is offered only while unconfirmed, and confirming a confirmed clip succeeds                                     | list.8, confirm.1                                   | `TestVoicesList_ConfirmOnlyWhileUnconfirmed`, `TestVoicesList_ConfirmAlreadyConfirmed` |
| 14  | A trim and a move go through Inspector, `Enrol` and `Remove`, keep status and source, and never lose a clip             | trim.1–trim.4, move.1–move.3, F5                    | `TestVoicesList_Trim`, `TestVoicesList_TrimAtTheCap`, `TestVoicesList_Move`, `TestVoicesList_ReplaceHalfDone` |
| 15  | Play uses the first player the machine has, and says which three it looked for when there is none                       | play.1, play.2                                      | `TestVoicesList_Play`                                              |
| 16  | An empty library, the `u` filter, and a delete of the last clip behave as the README says                               | list.15, list.11, delete.1                          | `TestVoicesList_EmptyLibrary`, `TestVoicesList_UnconfirmedOnly`, `TestVoicesList_Delete` |
| 17  | Browsing writes nothing, no output carries audio, and the dependencies are opened once                                  | list.17, voices.4, VoicesCommand.1, VoicesCommand.2 | `TestVoicesList_ReadOnly`, `TestVoices_NoAudioLeaves`, `TestVoicesCommand_OpensOnce` |
| 18  | On a terminal Enter stops a playing clip and that is a success; a clip that ends by itself swallows no line; without a terminal it plays to its end | play.1, play.3, play.4, play.5 | `TestVoicesList_PlayStopsOnEnter`, `TestVoicesList_PlayEndsByItself`, `TestVoicesList_PlayWithoutTerminal` |
| 19  | Ctrl-C at a menu question leaves as `q` does: exit 0, silent, and a `y` typed afterwards deletes nothing                | voices.6, menu rule 10                              | `TestVoicesList_InterruptLeavesAsQuit`                             |
| 20  | A clip added while a pruned clip's file could not be deleted is reported as added, with a warning that names the leftover file | add.6, move.4, `Enrol.9`                     | `TestVoicesAdd_PruneLeavesAFile`, `TestVoicesList_MovePruneLeavesAFile` |

## Test cases

Every row runs the command of `VoicesCommand` as L7.2 says, on the library of L7.2. "Terminal" means the
run's Env reports stdin and stdout as terminals; stdin is then a `strings.Reader` of the row's answers,
except in a row that plays a clip: there the answers after `p` are written to a pipe once the clip's level
shows again, as in `TestVoicesList_PlayEndsByItself`, so no answer is waiting while the clip plays (play.3).
Clips are named by their place in the README's example (Lorentz's first, second, third); their ids are
read from `List`.

`TestVoicesList_OneLevelAtATime` asserts only what this phase adds to list.18: that `voices list` on a
terminal gives the menu a terminal session, and that the runs without one (no terminal, `-json`, an
action given as an argument) write no cursor or clear sequence. What an erase removes and what it leaves
on the screen is the Navigator's (`Navigate.24`–`Navigate.26`), proven by phase 6's suite on the fake
terminal of L6.3 and certified on a real terminal by the README's human check for node 7. That fake
terminal is not copied into this test package.

| Test                                         | Level          | Proves                                         | Given                                                                                                                                   | Then                                                                                                                                                | Must not                                              |
| -------------------------------------------- | -------------- | ---------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| `TestVoices_Verb` (amended 2026-11-19, A04; was `TestVoices_SubVerb`) | contract suite | voices.1, AC 1 | A stand-in `audio` level that holds `VoicesCommand` under `VoicesKey` beside a stand-in `transcribe`, over the library of L7.2. In turn: `voices list`, `v list`, `voices l`, `v l`; `v l lorentz` and `voices list lorentz`; `v a <wav> -name Ada`; `transcribe voices`, `transcribe voices.wav`, `transcribe v` | The first four print the people level, with the same stdout; the next two print Lorentz's clips, with the same stdout; the add prints `Ada: clip <id> added (confirmed)` and `List` shows the clip; the last three reach the stand-in `transcribe` with the word as its file argument, and `open` is not called for them | Reach the voices CLI through `transcribe`; read `a` or `l` as a person, a clip or a file; need `./` before a file named `voices` |
| `TestVoices_NoVerb`                          | contract suite | voices.2, AC 1                                 | `voices`; `v`; `voices rm lorentz`; `v x` (amended 2026-11-19, A04: the short name's rows)                                               | The synopsis on stderr, naming `clai audio v\|voices a\|add <wav>` and `clai audio v\|voices l\|list [person` (amended 2026-11-19, R3-01), exit 2, stdout empty; `open` not called | Default to `list`, or open the library                |
| `TestVoicesAdd_PreSliced`                    | contract suite | add.1, add.2, AC 2                             | `voices add <home>/clips/christoph.wav`; then `voices add <dir>/LORENTZ.flac` with Lorentz in the library                                 | `Inspect` got the file and the zero `Cut`; stdout is exactly `Christoph: clip <id> added (confirmed)`; `List` shows the clip under `christoph`, confirmed, added by `voices add`; exit 0; the second prints `LORENTZ: clip <id> added (confirmed)` and the clip joins Lorentz, whose stored name stays | Ask anything, or enrol unconfirmed                    |
| `TestVoicesAdd_Cut`                          | contract suite | add.1, AC 2                                    | `voices add meeting.wav -from 61.2 -to 69 -name Syrus`; then `-from 61.2 -name Syrus` alone                                               | `Inspect` got `Cut{61.2 s, 69 s}`, then `Cut{61.2 s, 0}`; the line is `Syrus: clip <id> added (confirmed)`; `List` shows a 7.8 s clip, confirmed, added by `voices add`    | Round a bound                                         |
| `TestVoicesAdd_CutNeedsName`                 | contract suite | add.3, AC 2                                    | `voices add meeting.wav -from 61.2 -to 69`; `-from 61.2` alone; `-to 69` alone                                                            | stderr holds `-from and -to cut a clip out of a recording, so the person must be named: add -name <person>`; exit 2; no `Inspect` call               | Name the person `Meeting`                             |
| `TestVoicesAdd_FlagsAnywhere`                | contract suite | voices.5, AC 2                                 | `voices add -name Syrus -from 61.2 meeting.wav -to 69`; `voices add -name Syrus -- -odd.wav`; `voices list lorentz -json` and `voices list -json lorentz` | The first equals `TestVoicesAdd_Cut`; the second inspects `-odd.wav`; the two lists print the same JSON                                              | Read `-to` as a second file                           |
| `TestVoicesAdd_Usage`                        | contract suite | add.4, voices.3, AC 3                          | `voices add`; `voices add a.wav b.wav`; `voices add a.wav -from abc -name X`; `voices add a.wav -to 1m -name X`                            | A line naming what is wrong on stderr, exit 2 each time; no `Inspect` call                                                                           | Exit 1, or enrol                                      |
| `TestVoicesAdd_Refusals`                     | contract suite | add.5, voices.3, F5, L4.2, AC 3                | A file served as `Speech(1.5)`; `-from 30 -to 35` on a 20 s file; `Missing("ffmpeg")`; a file given to `Fail` with `Invalid data found when processing input`; a file named `2024.wav`; the same clip twice; a person at the cap (L4.2), all confirmed; `voices` that is a regular file | stderr holds each error's message (`clip too short: 1.5 s, a clip is 3–10 s` for the first; then a `CutError`, `MissingToolError`, `SourceError`, `VoiceNameError`, `ClipExistsError`, `VoiceFullError`, `LibraryReadError`); exit 1; the library's files are as before              | Enrol, or exit 2                                      |
| `TestVoicesAdd_PruneLeavesAFile`             | contract suite | add.6, voices.3, `Enrol.9`, AC 20              | `voices add <wav> -name Lorentz` over the real Librarian wrapped so that `Enrol` enrols and then returns the reference it got with a `*LibraryWriteError` naming a file under `voices/lorentz/`; the same wrapper returning the zero reference with that error | stdout is exactly `Lorentz: clip <id> added (confirmed)`; stderr is one line, `warn [audio] ` and the error's message with that file's path; exit 0; `List` shows the clip. With the zero reference: the error's message on stderr, stdout empty, exit 1 (add.5) | Exit 1 for a clip that was added, drop the added line, print the warning on stdout, or write a plain `warning: ` line past the run's reporter |
| `TestVoicesList_MovePruneLeavesAFile`        | contract suite | move.4, `Enrol.9`, AC 20                       | `… christoph <clip> move syrus` over the same wrapper around `Enrol`; then the same move picked in the menu, answers `m`, `syrus`, `y`, `q` | One line on stdout: the move line of move.1, then `; warning: ` and the error's message; stderr empty; exit 0; Syrus holds the clip and Christoph does not. In the menu the same one line shows above the people level, exit 0 | Leave the clip with both people, report a `ReplaceError`, exit 1, or spread the line over two |
| `TestVoicesList_InterruptLeavesAsQuit`       | contract suite | voices.6, voices.3, AC 19                      | Terminal, stdin a pipe the test writes, a run context the test cancels: `voices list`, cancelled while the people level waits; `voices list lorentz <third>`, answer `d`, cancelled while the delete question waits, then `y` and a newline written to the pipe; the same with `m`, `syrus`, cancelled at the move question, then `y`. Each walk again left with `q` in place of the cancel | The command returns no error and the exit status is 0 while the pipe is still open; stderr is empty; stdout equals that of the walk left with `q`; after the late `y` the third clip is still listed under Lorentz with its file, and no `removed` or `added` line was appended | Exit non-zero, print anything about the cancel, wait for a line before returning, or delete or move on the late `y` |
| `TestVoicesList_BrowseFromTop`               | contract suite | list.1, list.2, list.3, list.4, list.8, play.1, AC 4 | Terminal, `voices list`, answers `1`, `2`, `p`, `b`, `b`, `q`; width 200                                                              | People rows Christoph 1/1/0/0/`never`, Lorentz 3/1/17/3 with a date, Syrus 2/0/9/0; Lorentz's clips in `List`'s order with status, length, namings, wrong, last named, `you`/`the agent`; the third clip's view with `unconfirmed`, its rank 3 of 3, not picked, and `$(clai confdir)/voices/lorentz/<id>.flac`; the player Runner ran one player; then the clips, the people; exit 0 | Print an absolute path in the view, or sort the clips itself |
| `TestVoicesList_StraightToPerson`            | contract suite | list.5, AC 5                                   | Terminal, answer `q`: `voices list lorentz`, `Lorentz`, `LORENTZ`; a person `Jean-Luc P.` by `jean-luc-p` and by `jean-luc p.`             | Each opens that person's clips level first                                                                                                          | Show the people level first                           |
| `TestVoicesList_StraightToClip`              | contract suite | list.5, AC 5                                   | Terminal, answer `q`: `voices list lorentz <4-digit prefix of the third clip>`, the full id, the prefix upper-cased, and `voices list lorentz 2`; a library where a clip's id starts with `1`, and `voices list <person> 1` | The first four open the third clip's view; the last opens the clip numbered 1                                                                       | Read `2` as an id prefix                              |
| `TestVoicesList_ActionArguments`             | contract suite | list.7, confirm.1, overrule.1, delete.1, trim.1, move.1, play.1, AC 6 | Terminal with stdin holding `n`, then not a terminal: `… lorentz <third> confirm`; `… lorentz <second> overrule`; `… christoph <clip> move syrus`; `… lorentz <second> trim 0.8 5.9`; `… lorentz <third> delete`; `… lorentz <first> play`, play without a terminal only (its terminal form is `TestVoicesList_PlayStopsOnEnter`) | Each prints exactly one line on stdout and exits 0 with stdin unread: `Lorentz/<id>: confirmed`; the overrule line with the new wrong count; the move line with Christoph, Syrus, the same id, `unconfirmed`; the trim line with the old id, `5.1 s` and a new id; `Lorentz/<id>: deleted`; the play line with the length and the player. `List` shows each change | Ask, show a level, or need a terminal                 |
| `TestVoicesList_PickedActionAsksFirst`       | contract suite | list.9, AC 7                                   | Terminal, `voices list lorentz <third>`: answers `d`, `n`, `d`, `y`, `q`; then on the second clip `t`, `0.8`, `5.9`, `y`, `q`; on Christoph's clip `m`, `syrus`, `y`, `q`; on the first clip `o`, `c`… `q` | The delete question names `Lorentz/<id>`, its length and status; nothing is removed after `n`; after `y` the line, then Lorentz's table with two rows. Trim: asked from, to and a question, then Lorentz's table with the new id. Move: asked the person and a question, then the people level without Christoph. Overrule and play run unasked and show the clip again | Remove, trim or move on any answer but `y`/`yes`      |
| `TestVoicesList_PickedActionFails`           | contract suite | list.10, AC 7                                  | Terminal, on Lorentz's second clip: `t`, `2.0`, `3.5`, `y`, `q` (a 1.5 s clip, which the Inspector refuses); then `p`, `q` with no player           | stdout holds the message, then the clip's view again with the clip unchanged; stderr empty; exit 0                                                            | Leave the menu, or exit 1                             |
| `TestVoicesList_NoMatchOpensLevelAbove`      | contract suite | list.6, AC 8                                   | Terminal, answer `q`: `voices list ada`; `voices list lorentz ffff`; `voices list lorentz <third> sing`; `voices list ada <third> delete`; a prefix two of Lorentz's clips share | One line on stdout, before the level, naming `ada`; `ffff` and Lorentz; `sing` and the six actions; `ada`; the shared prefix. Then the people level, Lorentz's clips, the clip's view, the people level, Lorentz's clips; stderr empty; exit 0 | Delete anything, or print the line twice              |
| `TestVoicesList_ActionFails`                 | contract suite | trim.3, voices.3, AC 9                         | `voices list lorentz <second> trim 2.0 3.5`, which the Inspector refuses as 1.5 s; then a trim from 0 to the clip's own length, which `Enrol` refuses as the clip the person holds | stderr holds `clip too short: 1.5 s, a clip is 3–10 s`, then the `ClipExistsError` message; stdout empty; exit 1; the old clip is listed with the same namings and its file is unchanged          | Remove the old clip, or exit 2                        |
| `TestVoicesList_WithoutTerminal`             | contract suite | list.13, AC 10                                 | stdout not a terminal: `voices list`, `voices list lorentz`, `voices list lorentz <third>`; stdin not a terminal, stdout a terminal: `voices list ada`, `voices list lorentz ffff`; a library of 12 people | The level once with every row (all 12) and no question, exit 0, stdin unread; for the misses the line on stderr, stdout empty, exit 1                | Show a menu, or print the level above after a miss    |
| `TestVoicesList_JSON`                        | contract suite | list.14, AC 10                                 | Terminal: `voices list -json`; `voices list lorentz -json`; `voices list lorentz <third> -json`; the first again with the run's global `-json`; `voices list ada -json` | One JSON document on stdout each: the array equal to `List()` encoded; an array holding Lorentz alone with three clips; Lorentz alone with the third clip; every `path` absolute and existing; the miss is a line on stderr, stdout empty, exit 1 | Show a menu, print text beside the JSON, or print `null` |
| `TestVoicesList_ColumnsFit`                  | contract suite | list.12, list.4, AC 11                         | Not a terminal, `VoicesDeps.Width` returning 200, 60, 40 and 20: `voices list`, `voices list lorentz`, `voices list lorentz <third>`                          | At 200 every column of L7.1; narrower, the columns go in L7.1's order and each line fits; at 20 the names and the full ids remain; the view's path line is whole at every width | Shorten an id, a name or the path                     |
| `TestVoicesList_OneLevelAtATime`             | contract suite | list.18, AC 11                                 | Terminal, stdout a plain buffer: `voices list`, answers `1`, `2`, `c`, `b`, `q`. Then, each with the same library: not a terminal, `voices list lorentz`; a terminal, `voices list lorentz -json`; a terminal, `voices list lorentz <third> confirm` | The terminal walk handed the menu a terminal session: its stdout holds the menu's erase sequence (`Navigate.25`) between every two levels it drew. The other three write no cursor-up (`ESC [ n A`) and no erase (`ESC [ J`) sequence | Write a cursor or clear sequence without a terminal, with `-json`, or for an action given as an argument |
| `TestVoicesList_ExitStatus`                  | contract suite | voices.3, AC 12                                | A menu left with `q`; a printed level; an action that ran; an action that failed; a miss without a terminal; a usage error                | 0, 0, 0, 1, 1, 2, through `internal.ExitStatus.Resolve`                                                                                             | Exit 0 on a failure                                   |
| `TestVoicesList_Usage`                       | contract suite | list.16, AC 12                                 | `… lorentz <third> confirm now`; `… <second> trim 0.8`; `… trim a b`; `… <clip> move`; `… delete x`                                        | A line naming the action and its arguments on stderr, exit 2; no `Inspect` call, and the library's files are as before                                   | Ask for the missing argument, or run                  |
| `TestVoicesList_ConfirmOnlyWhileUnconfirmed` | contract suite | list.8, AC 13                                  | Terminal: the view of Lorentz's first (confirmed) and third (unconfirmed) clip, answers `c`, `q`                                           | The confirmed clip's view offers no confirm and `c` means nothing there; the unconfirmed clip is confirmed and its view then offers no confirm       | Offer confirm on a confirmed clip                     |
| `TestVoicesList_ConfirmAlreadyConfirmed`     | contract suite | confirm.1, AC 13                               | `voices list lorentz <first> confirm`, twice                                                                                              | Exit 0 both times, one line each saying it is confirmed already; `events.jsonl` byte for byte as before                                             | Fail, or append an event                              |
| `TestVoicesList_Trim`                        | contract suite | trim.1, F5, F10, AC 14                         | `… lorentz <second> trim 0.8 5.9` on a confirmed clip added by the agent, with namings                                                    | `Inspect` got the clip's `Path` and `Cut{0.8 s, 5.9 s}`; `List` shows a new 5.1 s clip under Lorentz, confirmed, source `audio_remember_voice`, `Named` 0, and the old one gone with a `removed` event; the old file is deleted, not rewritten; the line names the old id, `5.1 s` and the new id | Rewrite a clip file, or confirm an unconfirmed clip   |
| `TestVoicesList_TrimAtTheCap`                | contract suite | trim.2, L4.2, AC 14                            | A person at the cap (L4.2), all confirmed; one at the cap with one unconfirmed clip and the rest confirmed; `trim` of a confirmed clip in each | Both succeed; each person is still at the cap afterwards; the unconfirmed clip is still there | Refuse with `VoiceFullError`, or prune                |
| `TestVoicesList_Move`                        | contract suite | move.1, move.2, move.3, F5, L4.2, AC 14        | `… christoph <clip> move SYRUS`; `… lorentz <third> move Ada`; in a terminal, the third clip's view with `m`, `Ada`, `n`, `q`; `… lorentz <first> move lorentz`; a move into a person at the cap (L4.2), all confirmed; `… move speaker-1` | `Inspect` got the `Path` and the zero `Cut`; Syrus holds the clip with the same id, `unconfirmed`, source kept, `Named` 0, and Christoph is no longer listed, and the line names Christoph, Syrus, the id and `unconfirmed` without `new`; Ada is created and the line says she is a new person; the menu's question says Ada is a new person, and after `n` there is no Ada; the last three print `ClipExistsError`, `VoiceFullError`, `VoiceNameError` messages, exit 1, with nothing changed and no `Remove` | Remove the clip before it is enrolled                 |
| `TestVoicesList_ReplaceHalfDone`             | contract suite | trim.4, move.3, AC 14                          | A move whose first person's `events.jsonl` is read-only; a trim over the real Librarian wrapped so that only its `Remove` fails with a `LibraryWriteError` | `ReplaceError` naming the new and the old ref on stderr, exit 1; `List` shows both clips                                                            | Report success, or remove the new clip                |
| `TestVoicesList_Play`                        | contract suite | play.1, play.2, F8, AC 15                      | The player Runner resolving all four; only `ffplay` and `open`; only `xdg-open` and `open`; only `open`; none; `play` exiting 1 with a message | Ran `play <path>`; `ffplay -nodisp -autoexit <path>`; `xdg-open <path>`; `open <path>`, each once with the clip's absolute path last; `NoPlayerError` whose message names `play`, `ffplay` and the opener, exit 1; `PlayError` with the player and its message, exit 1 | Run two players, or run a program outside the Runner  |
| `TestVoicesList_PlayStopsOnEnter`            | contract suite | play.1, play.3, AC 18                          | Terminal; a player Runner whose `Run` signals that it started and returns only when its ctx ends, then with an error; stdin a pipe the test writes after that signal. In the menu: `p`, then an empty line once the player started, then `q`. As an argument: `… lorentz <first> play`, then an empty line; the same with `x` and no newline, then the pipe closed, the player released by the test | The play line ends `· [Enter] stops`; the player's ctx was cancelled after the line and its error is not reported; the menu shows the clip's level again, stderr empty, exit 0; the play action's `Result.Answers` holds the line that stopped it (`Navigate.25`); the argument form prints the play line alone and exits 0; `x` without a newline and the end of input cancel nothing, and the clip plays until released | Report a `PlayError` for a stopped clip, exit 1, or stop on input that ends |
| `TestVoicesList_PlayEndsByItself`            | contract suite | play.4, AC 18                                  | Terminal; a player Runner whose `Run` returns at once; stdin a pipe. In the menu: `p`, and once the clip's level is shown again `o`, then `q`. As an argument: `… lorentz <first> play` with a pipe that never yields a line | `overrule` ran exactly once, from the `o` typed after the clip ended, and the clip's `Overruled` grew by one; no line is missing from what the menu answered to; the argument form exits 0 while the pipe is still open | Swallow the line after the clip, or wait for an Enter that is not coming |
| `TestVoicesList_PlayWithoutTerminal`         | contract suite | play.1, play.5, AC 18                          | stdin not a terminal and holding two empty lines; a player Runner that returns when the test releases it; `… lorentz <first> play`              | The play line has no `[Enter]` hint; the command is still running, and the player's ctx live, until the test releases the player; then exit 0; stdin still yields both lines | Read stdin, or cancel the player                      |
| `TestVoicesList_EmptyLibrary`                | contract suite | list.15, AC 16                                 | No `voices/`: `voices list` in a terminal and not; `voices list -json`; `voices list ada` without a terminal                               | The line `no voices yet; add one with clai audio voices add <wav> -name <name>`, nothing asked, exit 0; `[]`; the miss line and exit 1    | Create `voices/`, or print `null`                     |
| `TestVoicesList_UnconfirmedOnly`             | contract suite | list.11, AC 16                                 | Terminal, `voices list`, answers `u`, `1`, `q`; then `u`, `u`, `q`                                                                        | After `u` only Christoph and Lorentz are shown and `1` opens Lorentz; the second `u` shows Syrus again                                              | Hide a person from `voices list syrus`                |
| `TestVoicesList_Delete`                      | contract suite | delete.1, F5, AC 16                            | `… lorentz <first> delete` (confirmed); `… christoph <clip> delete` (his only clip); terminal: the same picked, answered `y`                | `Remove` got the slug and the id; the clip is gone from `List`; Christoph is not listed; in the menu the people level is shown next                  | Touch another clip                                    |
| `TestVoicesList_ReadOnly`                    | contract suite | list.17, AC 17                                 | A listing and hash of every file under `voices/` before and after: a terminal walk through all three levels, the printed levels, the JSON; then `voices` that is a regular file | No file changed; then the `LibraryReadError` message on stderr, exit 1                                                                              | Write an event while browsing                         |
| `TestVoices_NoAudioLeaves`                   | contract suite | voices.4, F8, AC 17                            | Clips whose first samples are a marker run, searched for as bytes of the stored FLAC; stdout and stderr of every row above that prints a level, a line, JSON or an error                        | The marker is in none of them; the player Runner saw only player calls and the `voicestest` Runner only `ffmpeg` and `ffprobe`                                                                                | Print clip bytes                                      |
| `TestVoicesCommand_OpensOnce`                | contract suite | VoicesCommand.1, VoicesCommand.2, AC 17        | A counting `VoicesOpener`: one `list` walk with three actions; one `add`; a usage error; `open` failing with a test error; two runs of one command | 1, 1, 0 calls; the test error unchanged (`errors.Is`) and exit 1; the second run sees the first's changes                                           | Open per level or per action                          |

## Error management

| Failure                                                              | Expected outcome                                                                                           | Test                                     |
| -------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- | ---------------------------------------- |
| `voices` with no verb or an unknown one                              | `*VoicesUsageError`, the synopsis on stderr, exit 2                                                         | `TestVoices_NoVerb`                      |
| `-from` or `-to` without `-name`                                     | `*VoicesUsageError` with the README's line, exit 2; nothing inspected                                       | `TestVoicesAdd_CutNeedsName`             |
| No `<wav>`, two of them, or a bound that is not decimal seconds      | `*VoicesUsageError` naming it, exit 2                                                                       | `TestVoicesAdd_Usage`                    |
| The inspection refuses the clip                                      | `*ClipQualityError`, its message on stderr, exit 1; nothing enrolled or removed                             | `TestVoicesAdd_Refusals`, `TestVoicesList_ActionFails` |
| A cut outside the file, a missing program, a file that is not audio  | `*CutError`, `*MissingToolError`, `*SourceError`, exit 1                                                    | `TestVoicesAdd_Refusals`                 |
| A refused name (from `-name`, the file's name, or `move`)            | `*VoiceNameError`, exit 1                                                                                   | `TestVoicesAdd_Refusals`, `TestVoicesList_Move` |
| The person already holds the clip (add, trim to the same bytes, move) | `*ClipExistsError`, exit 1; nothing changed                                                                | `TestVoicesAdd_Refusals`, `TestVoicesList_Move` |
| A person at the cap (L4.2), all confirmed (add, move)                | `*VoiceFullError`, exit 1; nothing changed                                                                  | `TestVoicesAdd_Refusals`, `TestVoicesList_Move` |
| A person, a clip or an action that matches nothing, in a terminal    | Not an error: one line above the level above it, which opens; exit 0 when left                              | `TestVoicesList_NoMatchOpensLevelAbove`  |
| The same, without a terminal or with `-json`                         | `*menu.NoMatchError`, the line on stderr, stdout empty, exit 1                                              | `TestVoicesList_WithoutTerminal`, `TestVoicesList_JSON` |
| An action with the wrong number of arguments, or a trim bound that is not a number | `*menu.UsageError`, exit 2; the action does not run                                          | `TestVoicesList_Usage`                   |
| `confirm` on a confirmed clip                                        | Not an error: `ReviewAlreadyConfirmed`, a line saying so, exit 0                                            | `TestVoicesList_ConfirmAlreadyConfirmed` |
| A trim or a move enrolled the new clip and could not remove the old  | `*ReplaceError` naming both, exit 1; both clips listed                                                      | `TestVoicesList_ReplaceHalfDone`         |
| No player on the machine                                             | `*NoPlayerError` naming `play`, `ffplay` and the OS opener, exit 1                                          | `TestVoicesList_Play`                    |
| The player fails                                                     | `*PlayError` with the player and its message, exit 1                                                        | `TestVoicesList_Play`                    |
| The player returns an error after Enter stopped it                   | Not an error: the clip's level again, or exit 0 for the action given as an argument                         | `TestVoicesList_PlayStopsOnEnter`        |
| An action picked in the menu fails                                   | Not a failure of the run: the message above the clip's level, shown again; exit 0 when left                 | `TestVoicesList_PickedActionFails`       |
| The library cannot be read or written                                | `*LibraryReadError` or `*LibraryWriteError`, exit 1                                                         | `TestVoicesList_ReadOnly`, `TestVoicesAdd_Refusals` |
| `Enrol` added the clip and could not delete the pruned clip's file (add, move) | Not a failure: the added or moved line, a warning naming the leftover file (`warn [audio] …` on stderr for `add`; the end of the move's own line for `move`), exit 0 | `TestVoicesAdd_PruneLeavesAFile`, `TestVoicesList_MovePruneLeavesAFile` |
| The run context is cancelled at a menu question (Ctrl-C)             | Not a failure: the menu is left as by `q`, exit 0, stderr empty; a line typed afterwards does nothing        | `TestVoicesList_InterruptLeavesAsQuit`   |
| `VoicesOpener` fails                                                 | Its error, unchanged, exit 1                                                                                | `TestVoicesCommand_OpensOnce`            |

## Golden diff

None.

## Removes

None.

## Local facts

**L7.1 — Column importance.** The README names the columns (menu rule 6) and leaves their order of
dropping to this phase (menu rule 8). Least important first:

| Table  | Dropped first → last                                          | Never dropped        |
| ------ | ------------------------------------------------------------- | -------------------- |
| People | Last named, Wrong, Named, Unconfirmed, Clips                  | Index, Person        |
| Clips  | Added by, Last named, Length, Wrong, Named, Status            | Index, Clip (its id) |

(Phase-authored; signed with this phase's interface.)

**L7.2 — Test seam.** The suite dispatches the command of `VoicesCommand` through
`go_away_boilerplate/pkg/cmd` with a context carrying an `internal.ExitStatus` and a `runenv.Env` whose
stdin, stdout, stderr and terminal states the row sets; no row uses a tty or an installed program. Its
`VoicesDeps` are all real but the player: `voices.NewLibrarian(dir, voices.NewRanker(), clock)` over a
temporary config directory under the `testenv` home; `voices.NewInspector(voicestest.NewRunner())`, the
real Inspector over phase 3's runner double, with source files scripted by `Serve`, `Fail` and
`Missing` and stored clips read back by `voicestest:Run.3`; and, as `VoicesDeps.Runner`, a test-local
fake of `voices.Runner` for the player only, which resolves the names the row gives and records each
`Run` (`voicestest.Runner` resolves `ffmpeg` and `ffprobe` alone, so it cannot stand in for a player).
No row scripts an Inspector. Where a row says what `Inspect` got, the real Inspector is wrapped so that
its calls are recorded and passed on. The library of "The voices menu, by example" is built through
`voices add` runs, `Tally` and `Review`; clip ids are content hashes, so the README's ids stand for
"Lorentz's first, second, third clip". (Phase-authored; signed with this phase's interface.)

**L7.3 — Players.** In order: `play <file>`, `ffplay -nodisp -autoexit <file>`, `xdg-open <file>`,
`open <file>`. The README's play flow fixes the first two and "the OS opener (`xdg-open`, `open`)"; the
order of the two openers is this phase's. (Phase-authored; signed with this phase's interface.)

**L7.4 — What a test may assert.** As L6.2: a test asserts which level is shown, the facts a line
carries (the person's stored name, the clip's id, its status, the counts, the length), its stream, its
count and the exit status. Wording is asserted only where the README or a consumed clause fixes it for
the user to act on: the `added` line of add.1 (shared with the `audio_remember_voice` tool), the usage
line of add.3, the empty-library line of list.15, the path of list.4, the `warn [audio] ` form of add.6, the `; warning: ` of move.4, and the messages of consumed errors (`Inspect.4`).

## Human required

None. The README's human checks for node 3 (`voices add`) and node 7 (the menu on a real terminal) are
run by the maintainer once every phase is `Complete`.

## Implementation notes

| Run   | Commit      | Command                                                                                                                                                                                                                 | Result                                                                                                                       |
| ----- | ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| start | 1550dec+wip | `go test ./internal/audio/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4`; `go vet ./internal/audio/...`; staticcheck `./internal/audio/...`; `gofumpt -l internal/audio`; `dupl -t 80 .` | 6 packages ok (`internal/audio` 13.9 s); vet, staticcheck, gofumpt clean; 49 clone groups                                     |
| end   | 1550dec+wip | the same                                                                                                                                                                                                                | 6 packages ok (`internal/audio` 22.3 s, 92.2 %); 38 phase 7 tests, 91.4 % of the 441 statements of `voices_cmd*.go`; vet, staticcheck, gofumpt clean; 49 clone groups |
| end   | 1550dec+wip | the same, `go test … -shuffle=on`                                                                                                                                                                                      | 6 packages ok (`internal/audio` 22.1 s, 92.2 %); vet, staticcheck, gofumpt clean; 49 clone groups                              |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/ ./internal/capture/... ./internal/... ./e2e/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 6`; `go vet ./...`; staticcheck and `gofumpt -l` on `internal/audio`, `internal/capture`, `e2e/...`; `dupl -t 80 .` | 101 packages ok (`internal/audio` 17.6 s, `internal/capture` 14.3 s, e2e 3.0–10.2 s); vet, staticcheck, gofumpt clean; 49 clone groups |
| fix-end   | 1550dec+wip | the same                                                                                                                                                                                                                | 101 packages ok (`internal/audio` 18.6 s, 92.4 %; `internal/capture` 17.6 s; e2e 4.3–11.1 s); vet, staticcheck, gofumpt clean; 49 clone groups (R1-02, R1-10) |
| fix-end   | 1550dec+wip | the same, `go test … -shuffle=on`                                                                                                                                                                                      | 101 packages ok (`internal/audio` 18.1 s, `internal/capture` 17.3 s, e2e 3.9–11.3 s); vet, staticcheck clean; gofumpt clean after formatting `voices_cmd_support_test.go`; 49 clone groups |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/ ./pkg/tools/... -race -count=3 -cover -timeout=30s -p 4`; `go vet ./internal/audio/... ./pkg/tools/...`; staticcheck and `gofumpt -l` on `internal/audio`, `pkg/tools` | 2 packages ok (`internal/audio` 17.6 s, 92.4 %; `pkg/tools` 13.0 s, 92.3 %); vet, staticcheck, gofumpt clean |
| fix-end   | 1550dec+wip | the same | 2 packages ok (`internal/audio` 18.0 s, 92.4 %; `pkg/tools` 13.9 s, 92.3 %); vet, staticcheck, gofumpt clean (R1-14; R1-12's phase 7 part) |
| fix-end   | 1550dec+wip | the same, `go test … -shuffle=on` | 2 packages ok (`internal/audio` 18.1 s; `pkg/tools` 13.4 s); vet, staticcheck, gofumpt clean |
| replan | 1550dec+wip | A04 (maintainer, 2026-11-19; major): the voices CLI is `clai audio v\|voices` with `a\|add` and `l\|list`, no longer a sub-verb of `transcribe`. Amended in place: the synopsis, `voices.1`, `voices.2`, `list.15`, `VoicesKey` (`voices\|v`), AC 1, `TestVoices_Verb` (was `TestVoices_SubVerb`), `TestVoices_NoVerb`, the `TestVoicesList_EmptyLibrary` line | documents only, no code changed or reverted; the body follows by R1-18; the amended interface waits for re-validation and the maintainer's sign-off |
| fix-start | 1550dec+wip | R1-18 (with R1-19..R1-21, one run): `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` | exit 0, 106 packages ok (`internal/audio` 24.9 s, `internal/cli` 8.2 s, `e2e/interactive` 9.5 s); vet exit 0; 49 groups |
| fix-end   | 1550dec+wip | the same three | exit 0, 106 packages ok (`internal/audio` 25.5 s, 92.4 %; `internal/cli` 8.3 s; `e2e/interactive` 9.3 s); vet exit 0; 49 groups (R1-18) |
| fix-start | 1550dec+wip | R3-01, R3-02, R3-03 (with R3-04 and the audio `Desc`, one run): `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` | exit 0, 106 packages ok (`internal/audio` 22.3 s, `internal/cli` 6.5 s, `e2e/interactive` 7.2 s); vet exit 0; 49 groups |
| fix-end   | 1550dec+wip | the same three | exit 0, 106 packages ok (`internal/audio` 23.5 s, 92.4 %; `internal/cli` 8.7 s; `e2e/interactive` 8.9 s); vet exit 0; 49 groups (R3-01..R3-03) |

Andon: none.
Surprises:
- The suite's example library has 5 and 1 namings where the README's has 17 and 9: phase 1's reader decodes every event line on every `List` (about 3 ms per read under `-race`), and the README's counts alone cost seconds of the package's 30 s; the rows assert the counts the fixture sets.
- `internal/audio` takes 22 s of its 30 s under the scoped gate with phases 5 and 7; most of phase 7's 2.3 s per count is `storeDecodeEvent` (one `json.Unmarshal` per field per line).
- `voices list` matches a path against one listing: the browser keeps the last `List` until it reads an answer or an action has run, so each level shown is still read anew (Navigate.19).
- The dispatcher stops parsing a verb's flags at its first positional, so `add` and `list` read the flags among their positionals themselves; a flag-shaped first positional means the dispatcher consumed `--`.
- `trim -1 5` given as arguments is a usage error (the bound reads as a flag), as are `-to` at or before `-from` and a trim whose `to` is at or before its `from`.
- The command returns every failure and writes none to stderr itself: the root's presenter prints it (the suite stands in for it); `voices` without a verb fails with the multi-line synopsis.
- `voicesCmdLastLine` (a player's last stderr line, printable and cut short) does what phase 3's unexported `inspectLastLine` does; a shared helper would need phase 3's file.
- The example library is built once per test binary under its TMPDIR (removed by testenv) and copied per test.
- R1-02: a player's or ffmpeg's `*exec.ExitError` is an `internal.ExitCoder`, so a failure that wraps one set clai's status (a stub `play` exiting 7 gave 7); the suite's fakes returned plain errors and missed it.
- R1-14: `voicesCmdEnrol` reads `Enrol.9` once for `add` and `move`: only the zero reference beside an error is a failure, and a reference beside an error is the added clip with its leftover file. `add` warns with `runenv.Warnf(ctx, runenv.SubsystemAudio, …)`; `move` ends its line `; warning: ` and the message with line breaks joined. `trim` is unchanged: it sets `Replaces`, so it never prunes (trim.2).
- R1-14: the suite's Env had no `Reporter`, so `runenv.Warnf` printed nothing. Every run of the fixture now gets a real one, `diag.NewReporter` over `diag.NewTextPresenter` on the run's stderr at info. No other row's stderr changed.
- R1-12 (voices.6): no body change was needed. Wave 1's `Navigate.30` already leaves on a cancelled ctx, and `runList` hands the run's ctx to `Navigate` unchanged. `TestVoicesList_InterruptLeavesAsQuit` cancels once the watched stdin's read count and `menu.Asking.Waiting` show the question waiting, and checks that the shown text ends in `[y/N]:` for the two confirm questions. Run with `Navigate(context.WithoutCancel(ctx), …)` as a scratch mutation, all three walks hang until the 10 s wait fails them. The fixture's `voicesCmdRun` gained a `ctx` for this.
- R1-18 (A04): `VoicesCommand` hangs under `audio` by `VoicesKey = "voices|v"` (`cmd.go`), its verbs keyed `add|a` and `list|l`; the command's `Name` is the literal `voices`, since the key would name it `voices|v` in the dispatcher's hints. The suite's fixture mounts it beside the stand-in `transcribe` in the dispatched map; `voices_cmd_play_test.go`'s five walks dropped their `transcribe` word as well. The synopsis gains a `v, a, l` line; usage, synopsis and the empty-library line name `clai audio voices …`.
- R3-01 (2026-11-19): the synopsis, `voicesCmdAddUsage` and `voicesCmdListUsage` read `clai audio v|voices a|add …` and `clai audio v|voices l|list …`, and the `v, a, l` row is gone; the empty-library line keeps `clai audio voices add`. The form is the orchestrator's (journal); phase 7's synopsis, `voices.2` and the `TestVoices_NoVerb` row were amended first.
- R3-02: `VoicesCommand`'s doc reads as the Interface block. R3-03: the suite's `commands()` returns one stand-in `audio` whose `Subs` hold the stand-in `transcribe` and `VoicesKey`, and `start` dispatches `clai audio words…`; no row builds its words without `start`, so no row changed.

## Review findings

### Review 2, 2026-11-19

Root review round 2 at `1550dec+wip`. No finding.

- **R1-14 (add, move), R1-12 (voices.6) resolved.** `voicesCmdEnrol` (`internal/audio/voices_cmd_tree.go:155-161`) takes a ref beside an error as a clip added: `add` prints its line and warns through `runenv.Warnf` (`:140-142`, `add.6`); `move` goes on and ends its line `; warning: …` (`voices_cmd_levels.go:451-453`, `move.4`). `TestVoicesAdd_PruneLeavesAFile`, `TestVoicesList_MovePruneLeavesAFile` and `TestVoicesList_InterruptLeavesAsQuit` pass 5 shuffled race runs.
- **note** Every example of "The voices menu, by example" was run on the built binary with the real ffmpeg: add (pre-sliced, cut, `-from` without `-name` exit 2), the three levels by name, slug, index and id prefix, a miss at each level (exit 1 without a terminal, the level above with one), each action as an argument, usage errors exit 2, `-json` in all three positions, the empty library, and the menu on a pty. Play with Enter returns to the clip view; an interrupt while a clip plays is exit 130 as before.

### Review 3, 2026-11-19

Review round 3 of the A04 change at `1550dec+wip` (R1-18..R1-21), phase 7's part. Three findings.

- **R1-18 (phase 7 part) resolved.** `VoicesKey = "voices|v"` (`internal/audio/voices_cmd.go:12`) keys the command in `audio`'s `Subs` (`internal/audio/cmd.go:197`), its `Name` is the literal `voices` and its verbs are `add|a`, `list|l` (`internal/audio/voices_cmd_tree.go:88-91`); `transcribe` holds no command. On the built binary: `clai a v` and `clai audio voices x` print the synopsis on stderr, exit 2; `clai a v l` prints the empty-library line naming `clai audio voices add`, exit 0, and `clai a v l -json` prints `[]`; `clai a v -h` lists `add|a` and `list|l`; completion offers `v voices` after `clai audio` and `a add l list` after `clai audio v`, and nothing after `clai audio transcribe`. `TestVoices_Verb`, `TestVoices_NoVerb` pass (0.07 s and under 0.01 s alone under `-race`).
- **R3-01 (minor, owner plan)** · `voices.2`, phase 7 synopsis (amended 2026-11-19, A04) · `internal/audio/voices_cmd_tree.go:27-32` · The synopsis `clai audio voices`, `clai a v`, `clai a v x` print writes the usage lines with the long names (`clai audio voices add <wav> …`, `clai audio voices list …`) and adds a separate row, `v, a, l the short names of voices, add and list: clai a v l is clai audio voices list`. clai's other helps write a short name inline beside its verb: `t|transcribe <file>` and `v|voices` in `clai audio -h`, `c|continue`, `l|list` in `clai chat`, and the dispatcher's own `add|a`, `list|l` under `clai a v -h`. The code follows the contract as signed: `voices.2` says the synopsis and the usage lines name the commands as `clai audio voices add …` and `clai audio voices list …`, `TestVoices_NoVerb` and phase 9's `Test_e2e_AUDIO_voices_dispatch` assert those substrings (`voices_cmd_add_test.go:75`, `e2e/interactive/audio_voices_e2e_test.go:229-230`), and this phase's synopsis keeps the row (line 20) even though its own usage lines are inline (lines 17-18). The maintainer's decision (journal, 2026-11-19: every verb and sub-verb has a short form like its siblings) reads the inline form.
  - [ ] Plan: amend this phase's synopsis (drop the `v, a, l` row), `voices.2` (the synopsis names `clai audio v|voices a|add <wav> …` and `clai audio v|voices l|list …`; say whether the `add` and `list` usage lines follow), the `TestVoices_NoVerb` row, and phase 9's `Test_e2e_AUDIO_voices_dispatch` row; then the code: `voicesCmdSynopsis` (`voices_cmd_tree.go:27-32`), and the substrings at `voices_cmd_add_test.go:75` and `audio_voices_e2e_test.go:229-230`.
- **R3-02 (minor, owner fix)** · Interface (`VoicesCommand`'s doc, amended 2026-11-19, A04) · `internal/audio/voices_cmd.go:64` · The Interface block's doc reads `VoicesCommand returns the voices command with its verbs add, short a, and list, short l (voices.1).`; the code still reads the old `… with its verbs add and list.` The written interface does not match the block; the behaviour is right.
  - [ ] Write the doc comment of `VoicesCommand` as the Interface block states it.
- **R3-03 (minor, owner fix)** · `TestVoices_Verb` row (amended 2026-11-19, A04), voices.1, AC 1 · `internal/audio/voices_cmd_support_test.go:99-113` · The row's Given is a stand-in `audio` level that holds `VoicesCommand` under `VoicesKey` beside a stand-in `transcribe`; the fixture puts both at the top of the dispatched map, with no `audio` level, so voices.1 is shown at the root, not as a sub-level of a parent command. The real tree's descent is shown by phase 9's `Test_e2e_AUDIO_voices_dispatch` (`audio v list`, `a v l`) and was seen on the built binary, so the behaviour is right and the evidence differs from the row.
  - [ ] In `commands()`, hang the stand-in `transcribe` and `VoicesKey` as `Subs` of a stand-in `audio` `internal.Command`, and dispatch the suite's words under it (`start` prefixes `audio`), so every row, `TestVoices_Verb` included, runs the command at the level the real tree mounts it.
