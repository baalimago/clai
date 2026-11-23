# Phase 6 — Menu

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Give clai one reusable menu: levels of tables entered at the depth a path of arguments names, with the
same answer to every input whatever the level holds, so that the voices CLI (phase 7) and, later,
`clai config` behave alike.

## Interface

Written to `internal/menu/menu.go`.

```go
// Package menu is a menu of levels, each a table or a view, entered by a path
// of arguments.
//
// - Package.1 — The package imports no domain package of clai: nothing under
//   internal/audio, internal/chat, internal/text or internal/profiles. What a
//   level holds is the caller's.
package menu

import (
	"context"
	"io"
)

// Opener returns a level as it is now. The Navigator calls it every time it
// shows the level.
type Opener func(ctx context.Context) (Level, error)

// Column is one column of a table level.
type Column struct {
	Title string
	// Drop orders the fitting: when a table is wider than the terminal the
	// column with the highest Drop goes first, the rightmost of equals
	// before the others. A column with Drop 0 is never dropped.
	Drop int
}

// Row is one row of a table level, and the level below it.
type Row struct {
	// Names are the texts an argument may name the row by, whole, without
	// case.
	Names []string
	// ID is the text an argument may name the row by with a unique prefix,
	// without case; empty when the row has none.
	ID string
	// Cells are the row's texts, one per Column of its level.
	Cells []string
	// Open returns the level below the row.
	Open Opener
}

// Field is one line of a view level: a label and its value.
type Field struct {
	Label string
	Value string
}

// Param is one argument an action takes.
type Param struct {
	// Name is how a usage line names it, such as "from".
	Name string
	// Ask is the question the menu asks for it when the action is picked.
	Ask string
}

// Result is what an action that ran reports.
type Result struct {
	// Line is the one line that says what was done; empty writes nothing.
	Line string
	// Return is the path, from the root, of the level the action changed.
	Return []string
	// Answers are the lines the action itself read from the session's
	// input while it ran, such as the Enter that stopped it. The terminal
	// echoed them, so they take rows on the screen.
	Answers []string
}

// Action is one thing a level can do.
type Action struct {
	// Key is what the menu reads to pick it, such as "d".
	Key string
	// Name is what an argument names it by, without case, such as "delete".
	Name string
	// Label is how the menu shows it, such as "[d]elete".
	Label string
	// Hidden keeps the action out of the menu; an argument still runs it.
	Hidden bool
	// Params are the arguments it takes, in order.
	Params []Param
	// Confirm, when set, makes the action destructive: it returns the
	// question the menu asks before running it.
	Confirm func(args []string) string
	// Run does it. w is the session's Out, for a line that must show
	// before the action ends.
	Run func(ctx context.Context, w io.Writer, args []string) (Result, error)
}

// Filter is a view of a table level that shows only some rows.
type Filter struct {
	Key   string
	Label string
	// Keep reports whether the row is shown while the filter is on.
	Keep func(Row) bool
	// Empty is the line shown when the filter keeps no row.
	Empty string
}

// Level is one level of a menu. A level with Columns is a table of Rows; a
// level without is a view of Fields.
type Level struct {
	// Title is the line above the level; empty for none.
	Title   string
	Columns []Column
	Rows    []Row
	// ByIndex lets an argument name a row by its number in the table.
	ByIndex bool
	Fields  []Field
	Actions []Action
	Filters []Filter
	// Empty is the line a table with no row is shown as.
	Empty string
	// Miss words the one line for an argument that names nothing here.
	// candidates are the rows an ambiguous ID prefix matched; none when
	// nothing matched.
	Miss func(arg string, candidates []Row) string
	// JSON is what the level encodes as.
	JSON any
}

// Session is where a menu runs.
type Session struct {
	// In is where answers are read, one line each.
	In io.Reader
	// Out receives everything the menu shows: the levels, the lines of
	// actions and, in a menu, the notices.
	Out io.Writer
	// Interactive says somebody can answer: In and Out are one terminal,
	// which echoes each answer and its newline.
	Interactive bool
	// Width returns the terminal's width in columns; it is asked once per
	// draw. Nil means the width of Out as utils.SessionDimensions reports
	// it.
	Width func() int
	// JSON, when set, receives the level in place of its text.
	JSON func(v any) error
	// Prompt, when set, shows each question in place of writing it to Out.
	Prompt func(line string) error
}

// Outcome says how a navigation ended.
type Outcome string

const (
	// OutcomeNone: it ended in an error.
	OutcomeNone Outcome = ""
	// OutcomeLeft: the menu was shown and left.
	OutcomeLeft Outcome = "left"
	// OutcomePrinted: one level was printed, and nothing was asked.
	OutcomePrinted Outcome = "printed"
	// OutcomeRan: an action given as an argument ran.
	OutcomeRan Outcome = "ran"
)

// NoMatchError says an argument named nothing and nobody could be asked.
// Its message is Line, the level's miss line.
type NoMatchError struct {
	// Arg is the argument; Depth its place in the path, from 0.
	Arg   string
	Depth int
	Line  string
}

func (e *NoMatchError) Error() string

// UsageError says the path cannot be run as written. Its message is Line.
type UsageError struct{ Line string }

func (e *UsageError) Error() string

// ExitCode is 2.
func (e *UsageError) ExitCode() int

// LevelFault names what is wrong with a level a caller built.
type LevelFault string

const (
	// FaultKey: two actions or filters of the level share a key or a name,
	// or one uses a key the menu keeps for itself.
	FaultKey LevelFault = "key"
	// FaultCells: a row does not have one cell per column.
	FaultCells LevelFault = "cells"
	// FaultOpener: the root Opener, or a row's Open, is nil.
	FaultOpener LevelFault = "opener"
	// FaultRun: an action's Run is nil.
	FaultRun LevelFault = "run"
)

// LevelError refuses a level a caller built wrong. Detail names the key, the
// row or the action; Title is empty for a nil root.
type LevelError struct {
	Title  string
	Fault  LevelFault
	Detail string
}

func (e *LevelError) Error() string

// OutputError says a write to the session failed.
type OutputError struct{ Err error }

func (e *OutputError) Error() string
func (e *OutputError) Unwrap() error

// InputError says a read of the session's In failed; In ending is not a
// failure (Navigate.23).
type InputError struct{ Err error }

func (e *InputError) Error() string
func (e *InputError) Unwrap() error

// Asking is a run's record of whether one of its menus waits for an answer.
// The zero value is ready, and it may be read while a menu runs.
type Asking struct {
	// unexported
}

// Waiting reports whether a menu of the run waits at a question now.
func (a *Asking) Waiting() bool

// WithAsking returns a ctx that carries a, for the Navigate calls made with
// it. (Navigate.31)
func WithAsking(ctx context.Context, a *Asking) context.Context

// Navigator runs a menu.
type Navigator interface {
	// Navigate enters the menu whose top level is root at the level path
	// names, and stays in it until it is left.
	//
	// - Navigate.1 — The path is read left to right, each argument against
	//   the level reached so far. The menu opens at the deepest level the
	//   path names; a level the path leaves out is asked in that level's
	//   table. An empty path opens root. (menu rule 1)
	// - Navigate.2 — An argument names the row that holds it among its
	//   Names, whole and without case; or the row of that number, when the
	//   level is ByIndex and the argument is a whole number below the row
	//   count; or the one row whose ID starts with it, without case. A
	//   number that is a row's number is that row, whatever IDs start with
	//   it. When no row is named, the argument names the level's action of
	//   that Name, without case. (menu rule 2)
	// - Navigate.3 — In an interactive session, an argument that names
	//   nothing, or whose ID prefix more than one row shares, shows the
	//   level's Miss line, one line, above the level that argument was
	//   read against, and the menu opens there, as if neither it nor any
	//   argument after it had been given. (menu rule 3)
	// - Navigate.4 — Where nobody can be asked (a session that is not
	//   Interactive, or one with JSON set), such an argument is a
	//   *NoMatchError holding it, its depth and that line. Nothing is
	//   written to Out. (menu rules 9, 10)
	// - Navigate.5 — An action named by an argument takes the arguments
	//   after it as its Params. With exactly as many as it has Params, Run
	//   is called once with them, at once: Confirm is not called, nothing
	//   is read from In and no level is shown, in every kind of session.
	//   Result.Line is written to Out as one line and the outcome is
	//   OutcomeRan. A Hidden action runs the same way. (menu rules 4, 9)
	// - Navigate.6 — With more or fewer arguments than Params, the result
	//   is a *UsageError whose line names the action and its Params, and
	//   Run is not called. An argument after the path's last level that has
	//   no action to name is a miss as in Navigate.3 and Navigate.4. (menu
	//   rule 10)
	// - Navigate.7 — An error from the Run of an action given as an
	//   argument is returned unchanged, with OutcomeNone; the Navigator
	//   writes nothing about it. (menu rule 10)
	// - Navigate.8 — In a table, a number picks that row and shows the
	//   level below it; "b" or "back" shows the level above, also when the
	//   menu was entered below the root by a path; "q" or "quit", at any
	//   question, leaves the menu with OutcomeLeft and a nil error. The
	//   root has no level above: there "b" is an answer that means nothing.
	//   A table longer than a page is paged by the table's own keys. (menu
	//   rule 5)
	// - Navigate.9 — An answer that means nothing at the level (no row of
	//   that number, several numbers, a key no action or filter has) says
	//   so in one line above the level, which is shown again and asks
	//   again. It never ends the menu and never picks a row.
	// - Navigate.10 — A view shows its Title, every Field on a line of its
	//   own, and the Labels of its actions that are not Hidden; an action
	//   is picked by its Key or its Name. "b" and "q" work as in a table.
	// - Navigate.11 — An action picked in the menu asks for each of its
	//   Params in order, by its Ask; an empty answer drops the action and
	//   shows the level again. Then, when Confirm is set, its question is
	//   asked, and only "y" or "yes", without case, runs the action; any
	//   other answer runs nothing and shows the level again. An action
	//   without Confirm runs without a question. (menu rule 4)
	// - Navigate.12 — After a picked action ran, the menu shows Result.Line
	//   above the level Result.Return names, walked from
	//   root as a path is. An argument of Return that names nothing any
	//   more stops the walk at the level above it, with no miss line.
	//   (menu rule 4)
	// - Navigate.13 — A picked action whose Run fails shows the error's
	//   message, one line, above its level, which is shown again. The menu
	//   goes on, and the failure does not change how Navigate ends. (menu
	//   rule 10)
	// - Navigate.14 — A Filter's Key shows only the rows it keeps, and the
	//   same Key again shows all of them. While it is on, a number picks
	//   among the rows shown. When it keeps none, its Empty line is shown.
	//   A path is matched against every row, whatever filter is on.
	// - Navigate.15 — A table is fitted to the session's Width: while any
	//   line of it is wider, the column with the highest Drop that is
	//   still shown is removed, header and cells, the rightmost of equals
	//   first. Columns with Drop 0 are never removed, and the row number
	//   is always shown. No cell, Field, Title or line of an action is
	//   ever shortened, wrapped or cut by the Navigator, also when what is
	//   left is wider than the terminal. (menu rule 8)
	// - Navigate.16 — In a session that is not Interactive, the level the
	//   path names is written to Out once (its Title, then the header and
	//   every row of a table, fitted as in Navigate.15, or every Field of
	//   a view) and the outcome is OutcomePrinted. No question is written
	//   and nothing is read from In. (menu rule 9)
	// - Navigate.17 — With JSON set, the level the path names is given to
	//   JSON once, as its Level.JSON, and nothing is written to Out; there
	//   is no menu, whatever Interactive says, and the outcome is
	//   OutcomePrinted. An action given as an argument still runs as in
	//   Navigate.5. (menu rule 9)
	// - Navigate.18 — A table with no row is shown as its Empty line. In a
	//   menu, an empty root ends the navigation with OutcomeLeft, and an
	//   empty level below the root shows the level above it. With JSON set
	//   an empty table is still its Level.JSON.
	// - Navigate.19 — Every time a level is shown or matched against, it
	//   is what its Opener returns then: root for the top, Row.Open for
	//   the others. A level shown after an action, or after "b", shows
	//   what the action changed.
	// - Navigate.20 — A nil error comes with OutcomeLeft, OutcomePrinted
	//   or OutcomeRan and is exit status 0. A *UsageError has ExitCode 2.
	//   Every other error has no exit code of its own, which is exit
	//   status 1. (menu rule 10)
	// - Navigate.21 — A level whose actions or filters share a Key or a
	//   Name, or use one the menu keeps ("b", "back", "q", "quit", and in
	//   a table also "n", "next", "p", "prev" and a Key starting with "/"),
	//   or whose row has not one cell per column (FaultCells), or has a nil
	//   Open (FaultOpener), or whose action has a nil Run (FaultRun), is a
	//   *LevelError naming the fault, before anything of that level is
	//   written and before any of its actions runs. A nil root is a
	//   *LevelError with FaultOpener, before anything is written.
	// - Navigate.22 — An error from an Opener is returned unchanged. A
	//   write to Out, a Prompt or a JSON call that fails is an
	//   *OutputError wrapping it. A read of In that fails, other than by
	//   In ending (Navigate.23), is an *InputError wrapping it. Every
	//   error Navigate returns that is not an Opener's or an action's own
	//   is one of the package's exported types.
	// - Navigate.23 — In is read one line per answer and never further, so
	//   a reader shared with the caller is where the caller left it. An In
	//   that ends at a question leaves the menu as "q" does. The one read a
	//   cancelled ctx abandoned (Navigate.30) may still take the next line
	//   of In; that line is dropped.
	// - Navigate.24 — In an interactive session the menu shows one level
	//   at a time: before it draws a level, it erases everything it wrote
	//   to Out since the navigation began or since its last erase (the
	//   level, its notice, the questions, what a picked action wrote to w)
	//   together with the answers the terminal echoed there, and draws in
	//   that place. A notice (a miss line, the line for a meaningless
	//   answer, an action's Result.Line or failure) is drawn above the
	//   level it comes with and goes when that level is replaced. (menu
	//   rule 8)
	// - Navigate.25 — It erases only its own rows: the rows are counted as
	//   utils.TerminalRows counts what it wrote and what was answered (to
	//   its own questions, and the Result.Answers of a picked action), at
	//   the width of the new draw, and the erase is exactly one cursor-up
	//   by that count ("ESC [ n A", left out when n is 0), a carriage
	//   return and one erase-to-end-of-screen ("ESC [ J"). Nothing that
	//   was on the screen before the navigation began is erased or
	//   overwritten, no other cursor movement is written and the alternate
	//   screen is not used. (menu rule 8)
	// - Navigate.26 — When the menu is left, nothing is erased: the last
	//   level drawn, with its notice, stays on the screen, and the cursor
	//   is on the row below it. (menu rule 8)
	// - Navigate.27 — Each draw is fitted to the width Width returns for
	//   that draw, so a level drawn after the terminal was resized fits
	//   the new width. A level is not redrawn while it waits for an
	//   answer. (menu rule 8)
	// - Navigate.28 — Outside an interactive session (not Interactive, or
	//   JSON set), and for an action given as an argument, nothing is
	//   erased and no escape sequence that moves the cursor or clears is
	//   written to Out. (menu rule 9)
	// - Navigate.29 — Everything goes through the session: the Navigator
	//   opens no terminal and reads no environment variable for a size or
	//   a device. With Prompt set, every question goes to Prompt and not
	//   to Out, and takes no row in the count of Navigate.25.
	// - Navigate.30 — In a menu, a ctx that is cancelled while a question
	//   waits for its answer, or that is found cancelled when the next
	//   question is due (after a picked action returned, say), leaves the
	//   menu exactly as "q" does: OutcomeLeft, a nil error, and on Out
	//   what "q" leaves there (Navigate.26) and nothing more. Navigate
	//   returns without waiting for the read of In it had started. After
	//   the cancel no answer is taken, whatever In yields, no action
	//   runs, no Confirm question is answered, and no level is opened or
	//   drawn. This is what the interrupt key does at a menu question.
	//   (menu rule 10)
	// - Navigate.31 — While a question of an interactive session waits
	//   for its answer, and at no other time, the Asking that ctx carries
	//   reports Waiting: it does not while a level is opened or drawn, nor
	//   while a picked action runs, nor after Navigate returned, nor in a
	//   session that is not Interactive or has JSON set. A ctx that
	//   carries none changes nothing. So a run can tell an interrupt at a
	//   menu question, which leaves the menu, from any other.
	Navigate(ctx context.Context, s Session, root Opener, path []string) (Outcome, error)
}

// New returns the Navigator.
//
// - New.1 — The Navigator holds no state: navigations do not affect each
//   other and may run at once.
func New() Navigator
```

Consumes:

- `go_away_boilerplate/pkg/table:Table` (`New`, `SlicePaginator`, `WithHeader`, `WithInput`, `WithWriter`,
  `WithPrompter`, `WithActions`, `WithSingleSelect`, `WithPageSize`, `Run`), `TableAction` (its `Action`
  and `Filter`), `ErrBack`, `ErrUserInitiatedExit`, `ReadUserInputFrom` (import)
- `internal/utils:SessionDimensions` (`dimensions.Dimensions`), `TerminalRows` (import)
- `internal/testenv:Main` (import)

No change to `go_away_boilerplate`, and no new dependency. `clai config` is not moved onto the menu here
(README, phase 6 row). The table is run on the session's reader, where it clears nothing itself; replacing
a level on the screen is the Navigator's own (Navigate.24–Navigate.26). And the fitted columns of `chat list` are not consumed: Navigate.15 is this
phase's own.

## Acceptance criteria

| #   | Criterion                                                                                                             | Proves                                        | Proof                                                             |
| --- | --------------------------------------------------------------------------------------------------------------------- | --------------------------------------------- | ----------------------------------------------------------------- |
| 1   | A path of 0, 1, 2 and 3 arguments opens the root, the level below a row, the view below that, and runs an action       | `Navigate.1`, `Navigate.5`, menu rule 1       | `TestNavigate_EntersAtDepth`                                      |
| 2   | A row is named by a name without case, by a unique id prefix, or by its number                                         | `Navigate.2`, menu rule 2                     | `TestNavigate_MatchesRows`                                        |
| 3   | An argument that names nothing says so in one line and opens the level above, as if it had not been given              | `Navigate.3`, menu rule 3                     | `TestNavigate_MissOpensLevelAbove`                                |
| 4   | With nobody to ask, the same argument is a typed error with that line, and nothing is printed                          | `Navigate.4`, menu rules 9, 10                | `TestNavigate_MissWithoutTerminal`                                |
| 5   | An action given as an argument runs at once with nothing asked, prints one line, and its failure is the result         | `Navigate.5`, `Navigate.6`, `Navigate.7`, menu rule 4 | `TestNavigate_ActionArgumentRunsAtOnce`, `TestNavigate_ActionArgumentUsage`, `TestNavigate_ActionArgumentFails` |
| 6   | A number goes one level down, `b` one up, `q` leaves; a meaningless answer asks again                                  | `Navigate.8`, `Navigate.9`, `Navigate.10`, menu rule 5 | `TestNavigate_NumberBackQuit`, `TestNavigate_MeaninglessAnswerAsksAgain`, `TestNavigate_View` |
| 7   | An action picked in the menu asks for its arguments, asks before a destructive one, and returns to the level it changed | `Navigate.11`, `Navigate.12`, `Navigate.13`, `Navigate.19`, menu rule 4 | `TestNavigate_PickedActionAsksFirst`, `TestNavigate_PickedActionReturns`, `TestNavigate_PickedActionFails` |
| 8   | Columns fit the width, the least important dropping first, and nothing is ever cut                                     | `Navigate.15`, menu rule 8                    | `TestNavigate_ColumnsFit`                                         |
| 9   | Without a terminal the named level is printed once; with JSON it is encoded once; an action still runs                 | `Navigate.16`, `Navigate.17`, `Navigate.18`, menu rule 9 | `TestNavigate_WithoutTerminalPrintsOnce`, `TestNavigate_JSON`, `TestNavigate_EmptyLevel` |
| 10  | The result maps to exit status 0, 1 and 2                                                                              | `Navigate.20`, menu rule 10                   | `TestNavigate_ExitStatus`                                         |
| 11  | A filter narrows a table and a path still reaches every row                                                            | `Navigate.14`                                 | `TestNavigate_Filter`                                             |
| 12  | A level built wrong (a nil `Opener` or `Run` included), a failing opener, a failing writer and a failing reader are four typed endings | `Navigate.21`, `Navigate.22`                  | `TestNavigate_MalformedLevel`, `TestNavigate_Failures`            |
| 13  | The menu runs on a fake input and writer alone, reads one line per answer and holds no state                           | `Navigate.23`, `Navigate.29`, `New.1`, `Package.1` | `TestNavigate_ReadsOneLinePerAnswer`, `TestNavigate_OnlyTheSession`, `TestNew_Stateless`, `TestPackage_ImportsNoDomain` |
| 14  | On a terminal each level replaces the one before it, and only what the menu itself drew is ever erased                 | `Navigate.24`, `Navigate.25`, menu rule 8     | `TestNavigate_ReplacesTheLevel`, `TestNavigate_ErasesOnlyItsOwnRows` |
| 15  | Leaving the menu keeps the last view and its result line on the screen                                                 | `Navigate.26`, menu rule 8                    | `TestNavigate_LeavesTheLastView`                                  |
| 16  | A level drawn after a resize fits the new width                                                                        | `Navigate.27`, menu rule 8                    | `TestNavigate_FitsTheWidthOfEachDraw`                             |
| 17  | Without a terminal, with JSON, and for an action argument, nothing is erased and the cursor is never moved             | `Navigate.28`, menu rule 9                    | `TestNavigate_NothingErasedWithoutTerminal`                       |
| 18  | A cancelled context at a question leaves the menu as `q` does, without waiting for the read, and nothing typed afterwards is acted on | `Navigate.30`, `Navigate.23`, menu rule 10    | `TestNavigate_CancelLeavesAsQuit`                                 |
| 19  | The run's `Asking` reports a waiting question exactly while one waits                                                  | `Navigate.31`                                 | `TestNavigate_AskingOnlyAtAQuestion`                              |

## Test cases

Every row runs `New().Navigate` on the fixture menu of L6.1 with a `Session` built in the test: `In` a
`strings.Reader` of the row's answers, `Out` a buffer, `Width` returning 120 unless the row says
otherwise, `NO_COLOR` set. "Interactive" in a row means `Session.Interactive` is true; no row uses a tty.
"The screen" in a row is the fake terminal of L6.3, which is then `Out` and echoes what `In` yields.

| Test                                      | Level          | Proves                                    | Given                                                                                                                         | Then                                                                                                                                       | Must not                                             |
| ----------------------------------------- | -------------- | ----------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------- |
| `TestNavigate_EntersAtDepth`              | contract suite | `Navigate.1`, AC 1                        | Interactive, answer `q`, with paths `[]`, `[lorentz]`, `[lorentz 1a7d]`; then path `[lorentz 1a7d confirm]`                     | The first level written is the people table, Lorentz's clips table, the clip view; the fourth writes no level, calls `confirm` once, `OutcomeRan` | Show a level above the one the path names            |
| `TestNavigate_MatchesRows`                | contract suite | `Navigate.2`, AC 2                        | Not interactive; paths `[Lorentz]`, `[LORENTZ]`, `[lorentz]`; `[lorentz 1a7d]`, `[lorentz 1A7D0F23C9B8]`, `[lorentz 2]`; a person whose clips have ids `2b…` and `9f…`, path `[<person> 1]` and `[<person> 2b]`; `[1]` at the people level, which is not `ByIndex` | The first three print Lorentz's clips; the next three print clip `1a7d0f23c9b8`; `1` is the second row and `2b` the first; `[1]` is a `NoMatchError` | Match a name by prefix, or an id over a row's number |
| `TestNavigate_MissOpensLevelAbove`        | contract suite | `Navigate.3`, AC 3                        | Interactive, answer `q`; paths `[ada]`, `[lorentz ffff]`, `[lorentz 1a7d sing]`, `[ada 1a7d delete]`, and `[lorentz 9]` where two ids start with `9` | `Out` holds the level's `Miss` line exactly once (with two candidates for `9`), before the people table, Lorentz's clips, the clip view, the people table, Lorentz's clips; `OutcomeLeft`, nil error | Run `delete`, or show a level below the miss         |
| `TestNavigate_MissWithoutTerminal`        | contract suite | `Navigate.4`, AC 4                        | The same five paths, not interactive; then interactive with `JSON` set                                                         | `NoMatchError` with `Arg`, `Depth` 0, 1, 2, 0, 1 and `Line` equal to the miss line; `Out` is empty; `JSON` is not called                    | Print the level above, or read `In`                  |
| `TestNavigate_ActionArgumentRunsAtOnce`   | contract suite | `Navigate.5`, AC 5                        | Paths `[lorentz 1a7d delete]`, `[lorentz e81d trim 0.8 5.9]`, `[lorentz 9f3c CONFIRM]` (hidden), each interactive, not interactive and with `JSON` set; `In` holds `n` | `Run` called once with `[]`, `[0.8 5.9]`, `[]`; `Confirm` never called; `In` unread; `Out` is exactly `Result.Line` and a newline; `OutcomeRan` | Ask, show a level, or call `JSON`                    |
| `TestNavigate_ActionArgumentUsage`        | contract suite | `Navigate.6`, AC 5                        | Paths `[lorentz e81d trim 0.8]`, `[lorentz e81d trim]`, `[lorentz 1a7d delete now]`                                             | `UsageError` whose line names `trim`, `from` and `to` (or `delete`); `ExitCode()` is 2; `Run` not called; `Out` empty                      | Ask for the missing argument                         |
| `TestNavigate_ActionArgumentFails`        | contract suite | `Navigate.7`, AC 5                        | Path `[lorentz e81d trim 2.0 3.5]` whose `Run` returns a test error                                                            |  That very error (`errors.Is`), `OutcomeNone`; `Out` is empty                                                                    | Wrap the error, or print it                          |
| `TestNavigate_NumberBackQuit`             | contract suite | `Navigate.8`, AC 6                        | Interactive, path `[]`, answers `1`, `2`, `b`, `b`, `b`, `q`; then path `[lorentz 1a7d]`, answers `b`, `b`, `quit`; then 12 people and answers `n`, `11`, `q` | People, Lorentz's clips, the clip view, Lorentz's clips, people, a line for the meaningless `b`, people again, then `OutcomeLeft`; the second shows the view, the clips, the people; the third opens the twelfth person | Leave on `b` at the root, or return an error on `q`  |
| `TestNavigate_MeaninglessAnswerAsksAgain` | contract suite | `Navigate.9`, AC 6                        | Interactive, people level with three rows, answers `7`, `3`, `0,1`, `0:2`, `x`, an empty line, `q`                              | Each of the six writes one line and the table asks again; no lower level is shown; `OutcomeLeft`                                           | Pick a row, or end with an error                     |
| `TestNavigate_View`                       | contract suite | `Navigate.10`, AC 6                       | Interactive, path `[lorentz 9f3c]` (a confirmed clip, whose `confirm` is `Hidden`), answers `o`, `overrule`, `c`, `q`           | Title, one line per field, the labels of the shown actions; `overrule` ran twice; `c` is a meaningless answer; `[c]onfirm` is not among the labels | Offer or run a hidden action in the menu             |
| `TestNavigate_PickedActionAsksFirst`      | contract suite | `Navigate.11`, AC 7                       | Interactive, path `[lorentz 1a7d]`; answers `d`, `n`; `d`, an empty line; `d`, `Y`; then at `e81d`: `t`, `0.8`, an empty line; `t`, `0.8`, `5.9`, `yes`; `o` | `delete` runs once, after `Y`, and its `Confirm` question was shown three times; `trim` runs once with `[0.8 5.9]` after asking `from`, `to` and its question; `overrule` runs with no question | Run a destructive action on any answer but `y`/`yes` |
| `TestNavigate_PickedActionReturns`        | contract suite | `Navigate.12`, `Navigate.19`, AC 7        | Interactive: `delete` of one of three clips returning `[lorentz]`; `delete` of a person's only clip returning `[<person>]`; `confirm` returning `[lorentz 1a7d]` | `Result.Line` on `Out`, then Lorentz's table with two rows; then the people table without that person and no miss line; then the view saying `confirmed` | Show the level as it was before the action           |
| `TestNavigate_PickedActionFails`          | contract suite | `Navigate.13`, AC 7                       | Interactive, path `[lorentz e81d]`, `trim` failing with a test error, answers `t`, `2.0`, `3.5`, `y`, `q`                       | `Out` holds the error's message, one line, then the view again; `OutcomeLeft` and a nil error                                          | End the menu, or return the action's error           |
| `TestNavigate_Filter`                     | contract suite | `Navigate.14`, AC 11                      | Interactive people level with a filter `u`; answers `u`, `1`, `q`; then `u`, `u`, `q`; then a filter keeping nobody; then not interactive, path `[syrus]`, Syrus not kept by the filter | The second kept person's level opens; the second run shows all rows again; the filter's `Empty` line is shown; Syrus's clips are printed   | Number the kept rows by their place in the full table |
| `TestNavigate_ColumnsFit`                 | contract suite | `Navigate.15`, AC 8                       | The clips table (`Drop` 0 for the id) at widths 200, 60, 40 and 10, interactive and not; the clip view, whose path field is 90 columns, at width 40 | At 200 every column; at 60 and 40 the columns with the highest `Drop` are gone, header and cells, in order, and every line fits; at 10 only the row number and the id remain, wider than 10 and whole; the path line is whole | Shorten a cell, add `…`, or wrap a line              |
| `TestNavigate_WithoutTerminalPrintsOnce`  | contract suite | `Navigate.16`, AC 9                       | Not interactive, `In` holding `1`; paths `[]`, `[lorentz]`, `[lorentz 1a7d]`; then 12 people                                    | The level once: title, header and every row (all 12), or the fields; no question and no action label; `OutcomePrinted`; `In` unread        | Page the rows, or print the level twice              |
| `TestNavigate_JSON`                       | contract suite | `Navigate.17`, AC 9                       | `JSON` set to a recorder, interactive and not; paths `[]`, `[lorentz]`, `[lorentz 1a7d]`, `[lorentz 1a7d confirm]`              | `JSON` called once with that level's `Level.JSON`; `Out` empty; `OutcomePrinted`; for the action, `JSON` is not called, `Result.Line` is on `Out` and the outcome is `OutcomeRan` | Show a menu, or write text beside the JSON           |
| `TestNavigate_EmptyLevel`                 | contract suite | `Navigate.18`, AC 9                       | A root with no row, interactive, not interactive and with `JSON`; an interactive pick of a row whose level has no row           | The `Empty` line once and `OutcomeLeft`; the line and `OutcomePrinted`; `JSON` called with the level's `JSON` value; the `Empty` line, then the level above | Ask at a table with no row                           |
| `TestNavigate_ExitStatus`                 | contract suite | `Navigate.20`, AC 10                      | A left menu, a printed level, an action that ran; a `UsageError`; a `NoMatchError`, an action's own error, a `LevelError`       | Nil errors for the first three; `ExitCode()` 2 for the fourth; the last three do not implement `ExitCode() int`                             | Give a failure exit status 0                         |
| `TestNavigate_MalformedLevel`             | contract suite | `Navigate.21`, AC 12                      | Levels with two actions on `d`; an action and a filter named alike; an action on `q`; a table action on `p` (and the same key on a view, which is allowed); a row with one cell too few; a nil root; a table with a row whose `Open` is nil, interactive and not, and as a path through that table to another row; a view with an action whose `Run` is nil, shown interactively with that action's key as the answer, and named as an argument | `LevelError` with `FaultKey` three times, then `FaultKey` for the table and no error for the view, then `FaultCells`; then `FaultOpener` with an empty `Title` for the nil root and with the level's `Title` and the row in `Detail` for the row; then `FaultRun` naming the action, both times; `Out` holds nothing of that level, no question was asked and no `Run` or `Confirm` was called | Draw the level, drop the action silently, return an error that is not a `*LevelError`, or find the nil `Run` only when the action is picked |
| `TestNavigate_Failures`                   | contract suite | `Navigate.22`, AC 12                      | A root `Opener` and a `Row.Open` returning a test error; an `Out` that fails; a `Prompt` that fails; a `JSON` that fails; an `In` whose read returns a test error at the first question, and at the `Confirm` question of a picked `delete` | The opener's error unchanged; `OutputError` wrapping the write, prompt and JSON errors; `InputError` wrapping the read's error (`errors.Is`) both times, with `delete` not run | Return a nil error, an error of an unexported type, or take a failed read for the end of input |
| `TestNavigate_CancelLeavesAsQuit`         | contract suite | `Navigate.30`, `Navigate.23`, `Navigate.26`, AC 18 | Interactive, on the screen of L6.3; `In` a pipe the test writes. In turn, the ctx is cancelled: while the people table waits; while the clip view waits; at the `Confirm` question of a picked `delete`, and `y` and a newline are written to the pipe afterwards; at the `to` question of a picked `trim`; by a picked action's own `Run` before it returns; before `Navigate` is called. For the first four, the same walk left with `q` in place of the cancel, for comparison | `Navigate` returns `OutcomeLeft` and a nil error each time, while the pipe is still open and unwritten (the test fails if the return waits for a line). The four cancels at a question: the screen and `Out` equal those of the walk left with `q` at that question, but for the echoed `q`; `delete` and `trim` did not run and the clip is still in the fixture after the late `y`. The action that cancels: its `Run` was called once, no `Opener` was called and nothing was written to `Out` after it returned, so the screen holds the clip view the action was picked in, with the picked key, and no line of the action. Cancelled before `Navigate`: no `Opener` was called, `Out` is empty and the screen is as it was. In every case no `Opener`, `Confirm` or `Run` is called after the cancel | Return an error matching `ctx.Err()`, block until a line arrives, act on a line typed after the cancel, or write a notice about the cancel |
| `TestNavigate_AskingOnlyAtAQuestion`      | contract suite | `Navigate.31`, AC 19                      | A ctx with `WithAsking(ctx, a)`; an interactive walk over a pipe: `Waiting` read inside every `Opener`, inside a picked action's `Run` and its `Confirm`, while each question waits (the pipe not yet written), and after `Navigate` returned; then the same path not interactive, with `JSON` set, and as an action given as an argument; then an interactive walk with a ctx that carries no `Asking` | `Waiting` is true at every waiting question, the `Params` and `Confirm` questions included, and false at every other read; false throughout the three runs with nobody to ask; the walk without an `Asking` behaves as the first | Report `Waiting` while an action runs or after the menu was left, or need an `Asking` to run |
| `TestNavigate_ReadsOneLinePerAnswer`      | contract suite | `Navigate.23`, AC 13                      | `In` holding `q\nrest\n`; then an `In` holding `1\n` and nothing more; then an empty `In`                                       | After the first run `In` still yields `rest`; the second shows Lorentz's level and ends `OutcomeLeft`, nil error; the third ends the same way at the root | Buffer `In` ahead, or fail on end of input           |
| `TestNavigate_OnlyTheSession`             | contract suite | `Navigate.29`, AC 13                      | An interactive walk with `TTY` pointing at a path that does not exist and `COLUMNS` set to 10, `Width` returning 120; then the same walk with `Prompt` set to a recorder | The walk succeeds with every column shown; every question went to the recorder and none to `Out`                                           | Open `/dev/tty`, or read `COLUMNS`                   |
| `TestNavigate_ReplacesTheLevel`           | contract suite | `Navigate.24`, `Navigate.25`, AC 14       | The screen, interactive, path `[]`, answers `1`, `2`, `o`, `x`, `b`, and the screen read after each; then path `[ada]`; then `[lorentz e81d]` with `t`, `0.8`, `5.9`, `y` | After each answer the screen holds one level and nothing of the one before: Lorentz's clips; the clip view; the overrule line above the view; the meaningless-answer line above the view, without the overrule line; Lorentz's clips with no notice. The miss line is above the people table. After the trim, its questions and answers are gone and its line is above Lorentz's clips. `Out` holds no escape sequence but `ESC [ n A` and `ESC [ J` | Pile a level under the one before, or keep an old notice |
| `TestNavigate_ErasesOnlyItsOwnRows`       | contract suite | `Navigate.25`, AC 14                      | The screen, 80 by 24 and then 30 by 24, pre-filled with five lines of a shell, the last a prompt line without a newline end; walks of six draws with a 90-column path field, a 40-row table on a 24-row screen, an action writing three lines to `w`, and an action that reads one line from `In` itself and returns it in `Result.Answers` | After every draw the five shell lines are intact, on the screen or in its scrollback, and the rows below them hold only the current level; wrapped lines are counted (the 30-column walk leaves no remainder of the path); each erase is one cursor-up, a carriage return and one `ESC [ J` | Erase or overwrite a shell line, use the alternate screen (`ESC [ ? 1049 h`), or home the cursor |
| `TestNavigate_LeavesTheLastView`          | contract suite | `Navigate.26`, AC 15                      | The screen: a walk to the clip view left with `q`; a picked `confirm` then `q`; a picked `delete` then `q`; a walk ended by `In` ending | The screen ends with the shell lines, then the last level drawn (the view; the view under `confirmed`; Lorentz's clips under the deleted line), whole, and the cursor on the row below | Erase on leaving, or leave the cursor inside the level |
| `TestNavigate_FitsTheWidthOfEachDraw`     | contract suite | `Navigate.27`, AC 16                      | The screen; `Width` returning 120 for the first draw and 40 from the second on, the screen resized to match; path `[lorentz]`, answers `x`, `q` | The first draw has every column; the second has only the columns that fit 40 and no line wider than 40; `Width` was called once per draw    | Keep the first draw's columns, or redraw before an answer |
| `TestNavigate_NothingErasedWithoutTerminal` | contract suite | `Navigate.28`, AC 17                    | Not interactive: paths `[]`, `[lorentz]`, `[lorentz 1a7d]`; `JSON` set, interactive; `[lorentz 1a7d delete]` interactive                 | `Out` holds no `ESC` byte and no carriage return in any of them                                                                            | Write an escape sequence to a pipe                   |
| `TestNew_Stateless`                       | unit           | `New.1`, AC 13                            | One Navigator, eight goroutines navigating two different menus with their own sessions                                          | Each `Out` holds only its own menu; clean under `-race`                                                                                    | Share state between navigations                      |
| `TestPackage_ImportsNoDomain`             | unit           | `Package.1`, AC 13                        | The package's import list (`go list -deps`, or `go/build`)                                                                      | No path under `internal/audio`, `internal/chat`, `internal/text` or `internal/profiles`                                                    | Import a domain package                              |

## Error management

| Failure                                                            | Expected outcome                                                                                   | Test                                    |
| ------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------- | --------------------------------------- |
| An argument names nothing, or an id prefix is shared, in a menu    | Not an error: the `Miss` line above the level above it, which opens                                 | `TestNavigate_MissOpensLevelAbove`      |
| The same, with nobody to ask                                       | `*NoMatchError` with the argument, its depth and the line; exit status 1                            | `TestNavigate_MissWithoutTerminal`      |
| An action argument with too few or too many arguments              | `*UsageError` naming the action and its `Params`; `ExitCode()` 2; `Run` not called                  | `TestNavigate_ActionArgumentUsage`      |
| An action given as an argument fails                               | The action's own error, unchanged; exit status 1                                                    | `TestNavigate_ActionArgumentFails`      |
| An action picked in the menu fails                                 | Not an error of `Navigate`: the message above the level, which is shown again                       | `TestNavigate_PickedActionFails`        |
| An answer that means nothing at the level                          | Not an error: one line, the level asks again                                                        | `TestNavigate_MeaninglessAnswerAsksAgain` |
| A destructive action not answered `y` or `yes`                     | Not an error: nothing runs, the level is shown again                                                | `TestNavigate_PickedActionAsksFirst`    |
| A level with a shared or reserved key, or a row with the wrong cell count | `*LevelError` with `FaultKey` or `FaultCells`, before the level is written                   | `TestNavigate_MalformedLevel`           |
| A nil root `Opener`, or a row with a nil `Open`                    | `*LevelError` with `FaultOpener`, before anything of the level is written                           | `TestNavigate_MalformedLevel`           |
| An action with a nil `Run`                                         | `*LevelError` with `FaultRun` naming the action, before the level is written or the action is asked for | `TestNavigate_MalformedLevel`           |
| A read of `In` fails, other than by its end                        | `*InputError` wrapping it                                                                           | `TestNavigate_Failures`                 |
| An `Opener` fails                                                  | Its error, unchanged                                                                                | `TestNavigate_Failures`                 |
| A write, a `Prompt` or a `JSON` call fails                         | `*OutputError` wrapping it                                                                          | `TestNavigate_Failures`                 |
| The context is cancelled at a question, or found cancelled when one is due | Not an error: `OutcomeLeft`, as `q`; the read is not waited for and nothing typed afterwards is acted on | `TestNavigate_CancelLeavesAsQuit`       |
| `In` ends at a question                                            | Not an error: `OutcomeLeft`                                                                         | `TestNavigate_ReadsOneLinePerAnswer`    |

## Golden diff

None.

## Removes

None.

## Local facts

**L6.1 — Fixture menu.** The suite's menu is built in the test package from plain values and mirrors
"The voices menu, by example" without importing `voices`: a people table (not `ByIndex`; rows named
`Christoph`/`christoph`, `Lorentz`/`lorentz`, `Syrus`/`syrus`; a filter `u`), per person a clips table
(`ByIndex`; row ids `9f3c2a1b0d4e`, `e81d5530c6a9`, `1a7d0f23c9b8` for Lorentz; seven columns of which
the id has `Drop` 0), and per clip a view with a 90-column path field and the actions `play`, `confirm`
(`Hidden` on a confirmed clip), `overrule`, `trim` (`Params` `from`, `to`; `Confirm` set), `move`
(`Params` `person`; `Confirm` set) and `delete` (`Confirm` set). Every `Run` records its call and
changes the fixture's own data, so a level opened afterwards shows the change. (Phase-authored; signed
with this phase's interface.)

**L6.2 — What a test may assert.** The README leaves the look to the implementer, so a test asserts
which level was written (by its title and its rows' ids), which lines exist, their stream and their
count, and what was called; it never asserts a separator, a colour, the wording of a question or the
text of the line for a meaningless answer. (Phase-authored; signed with this phase's interface.)

**L6.3 — Fake terminal.** The rows on replacing a level run on a screen written in the test package: an
`io.Writer` of a set width and height that keeps its rows and a scrollback, wraps a line at its width,
scrolls at its last row, and understands a newline, a carriage return, `ESC [ n A` (cursor up, stopping
at the top row), `ESC [ J` (erase from the cursor to the end of the screen) and colour sequences, which
take no cell; any other escape sequence fails the test. The row's `In` is wrapped so that each line read from it,
by the Navigator or by an action, is written to the screen first, as a terminal echoes an answer and its newline. A
level taller than the screen leaves its scrolled-off rows in the scrollback; that is the terminal's,
not an error of the menu. (Phase-authored; signed with this phase's interface.)

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                                                                                                                                                                                                 | Result                                                                                                                                  |
| ----- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| start | 1550dec+wip | `go test ./internal/menu/... -race -count=3 -cover -timeout=30s -p 4`; `go vet ./internal/menu/...`; `staticcheck ./internal/menu/...`; `gofumpt -l internal/menu`; `dupl -t 80 .` (phase-scoped, Q18) | package absent: test, vet and staticcheck "lstat ./internal/menu/: no such file or directory", gofumpt the same; dupl 49 clone groups |
| end   | 1550dec+wip | the same                                                                                                                                                                                                | 33 tests (130 subtests) passed ×3 under -race, 0 failed, coverage 96.0%; vet, staticcheck clean; gofumpt prints nothing; dupl 49 groups, none in internal/menu |
| end   | 1550dec+wip | the same                                                                                                                                                                                                | 33 tests (130 subtests) passed ×3 under -race, 0 failed, coverage 96.0%; vet, staticcheck clean; gofumpt prints nothing; dupl 49 groups, none in internal/menu |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/voices/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, staticcheck and `gofumpt -l` on `internal/audio/voices/...`, `internal/menu/...`; `dupl -t 80 .` (R1-07, R1-08) | all ok: voices 6.0 s 92.4 %, voicestest 2.2 s 94.4 %, menu 1.4 s 96.0 %, diagnostic 2.9 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |
| fix-end | 1550dec+wip | the same | all ok (load average 34): voices 8.4 s 95.4 %, voicestest 2.6 s 94.4 %, menu 1.6 s 96.0 %, diagnostic 3.1 s 93.3 %; voices 63 tests, 248 with subtests, menu 33 and 166, per count; vet, staticcheck, gofumpt clean; dupl 49 groups, none in voices or menu |
| fix-end | 1550dec+wip | the same with `-shuffle=on` | all ok (load average 21): voices 8.4 s 95.4 %, voicestest 2.4 s 94.4 %, menu 1.5 s 96.0 %, diagnostic 3.0 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/menu/... -race -count=3 -cover -timeout=30s -p 4`; `go vet ./internal/menu/...`; `staticcheck ./internal/menu/...`; `gofumpt -l internal/menu` (R1-15, R1-12 menu part; scoped, Q18) | all ok: menu 1.4 s 96.0 %; vet, staticcheck clean; gofumpt prints nothing |
| fix-end | 1550dec+wip | the same | all ok (load average 1.3): menu 1.5 s 97.2 %, 35 tests, 187 with subtests, per count; vet, staticcheck clean; gofumpt prints nothing. Also `go test ./internal/menu/ -race -count=100 -shuffle=on -run 'Cancel\|Asking\|Failures\|Malformed\|ReadsOneLine\|Erases\|Replaces\|LeavesTheLast'`: ok 7.9 s |
| fix-end | 1550dec+wip | the same with `-shuffle=on` | all ok (load average 1.4): menu 1.5 s 97.2 %; vet, staticcheck clean; gofumpt prints nothing |

Andon: none.
Surprises:
- `table.Table.Run` is not used: on its reader it reprints the rows below the old ones on paging, filters and bad answers, takes an index equal to the row count, and ends with an untyped error on `0,1` or `0:2`; the Navigator draws, pages (`n`/`p`, 10 rows) and reads itself, through `table.ReadUserInputFrom` and `table.ErrUserInitiatedExit`.
- The first draw in a menu starts with a newline that is never erased, so a shell line left without a newline end survives every erase (`TestNavigate_ErasesOnlyItsOwnRows`); on a terminal it leaves one blank row between the command and the menu.
- A nil `Opener` or `Run` is an error of the unexported type `missingError` (the interface declares no fault for it); a read error from `In` other than its end is returned wrapped with `%w`.
- gofumpt re-indents the list items of the package and `New` doc comments; the tokens are unchanged.
- R1-15 (2026-11-19): `missingError` is gone, so the nil `Opener`/`Run` surprise above no longer holds. A nil `Run` or row `Open` is now a `*LevelError` (`FaultRun`, `FaultOpener`), found by `check` when the level opens. A nil root is refused before anything else, and a failed read is an `*InputError`. `check` tests keys first, then `Run`, then each row's cells and then its `Open`.
- R1-12, menu part (2026-11-19): each answer is read on a goroutine of its own, which hands its line and error back on a buffered channel. A cancel therefore returns at once, and the read it leaves behind ends when `In` next yields a line or ends. The run's `Asking` counts `Waiting` from just before that goroutine starts until `read` returns.
- A ctx that is already cancelled when `Navigate` is called in an interactive session without `JSON` leaves as `q` before the path is read. An action named by that path therefore does not run: Navigate.30 forbids opening the levels the path needs to find that action.
- `TestNavigate_ErasesOnlyItsOwnRows`'s screen probe now reports with `Errorf`, because `In`'s callbacks now run on the reading goroutine.

## Review findings

### Review 2, 2026-11-19

Root review round 2 at `1550dec+wip`. No finding.

- **R1-12 (menu part), R1-15 resolved.** `run.read` (`internal/menu/navigate.go:312-329`) reads each answer on its own goroutine and returns `cancelled{}` on `ctx.Done()` without waiting; `halt` guards `open`, `ask`, the reply that races the cancel, and the step after a picked action's `Run` (`:109`, `:117`, `:263`, `:324`, `:389`), so nothing is opened, asked, confirmed or run after the cancel (`Navigate.30`). `Asking` counts from just before the read starts until `read` returns (`Navigate.31`). `InputError`, `FaultOpener`, `FaultRun` and the nil-root check are in (`Navigate.21`, `.22`); `missingError` is gone.
- **note** `TestNavigate_CancelLeavesAsQuit`, `TestNavigate_AskingOnlyAtAQuestion`, `TestNavigate_Failures`, `TestNavigate_MalformedLevel`: 40 shuffled race runs, all ok.
- **note** On a pty, Ctrl-C at the people question, at a delete's `[y/N]` and at a trim's first parameter exits 0 with stderr empty; `y`, and `0 0 d y`, typed afterwards delete nothing. `q` and Ctrl-D leave as before, and a level still replaces the one before it.
