package voicescmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/menu"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/utils"
)

const (
	emptyLibrary = "no voices yet; add one with clai audio voices add <wav> -name <name>"
	// pageSize is how many rows of a table the menu shows at once.
	pageSize = 10
)

// The column with the highest Drop goes first when a table is too wide.
var (
	peopleColumns = []menu.Column{
		{Title: "Person"},
		{Title: "Clips", Drop: 1},
		{Title: "Unconfirmed", Drop: 2},
		{Title: "Named", Drop: 3},
		{Title: "Wrong", Drop: 4},
		{Title: "Last named", Drop: 5},
	}
	clipColumns = []menu.Column{
		{Title: "Clip"},
		{Title: "Status", Drop: 1},
		{Title: "Length", Drop: 4},
		{Title: "Named", Drop: 2},
		{Title: "Wrong", Drop: 3},
		{Title: "Last named", Drop: 5},
		{Title: "Added by", Drop: 6},
	}
)

// NoMatchError says an argument of the path named nothing and nobody could
// be asked. Its message is the line a menu would show above the level.
type NoMatchError struct {
	Arg  string
	Line string
}

func (e *NoMatchError) Error() string { return e.Line }

// place is a level of the menu: the people (the zero value), one person's
// clips, or one clip.
type place struct {
	person voices.Slug
	clip   voices.ClipID
}

func (p place) up() place {
	if p.clip != "" {
		return place{person: p.person}
	}
	return place{}
}

// browser is one run of `voices list`.
type browser struct {
	deps Deps
	in   *sharedInput
	// out is the run's stdout, or the menu's screen while a menu runs.
	out    io.Writer
	screen *menu.Screen
	width  func() int
	// enterStops: stdin is a terminal, so Enter stops a playing clip.
	enterStops bool
}

func (v *command) list(ctx context.Context, c *internal.Command) error {
	defer func() { v.json = internal.BoolFlag{} }()
	path, err := positionals(c, listUsage)
	if err != nil {
		return err
	}
	env := runenv.From(ctx)
	asJSON := v.json.Value() || env.JSON()
	deps, err := v.open(ctx)
	if err != nil {
		return err
	}
	b := &browser{deps: deps, in: newSharedInput(env.Stdin()), out: env.Stdout(), width: deps.Width, enterStops: env.StdinIsTerminal()}
	if b.width == nil {
		b.width = func() int { return utils.SessionDimensions(env.Stdout()).Width }
	}
	lib, err := deps.Library.List()
	if err != nil {
		return err
	}
	at, rest, miss := walk(lib, path)
	interactive := !asJSON && env.StdinIsTerminal() && env.IsTerminal(stdio.Stdout)
	switch {
	case len(rest) > 0:
		return b.runArgument(ctx, lib, at, rest)
	case miss != nil && !interactive:
		return miss
	case asJSON:
		return env.WriteJSONDocument(levelJSON(lib, at))
	case !interactive:
		if _, err := io.WriteString(b.out, level(lib, at).printed(b.width())); err != nil {
			return &menu.OutputError{Err: err}
		}
		return nil
	}
	b.screen = &menu.Screen{In: b.in, Out: env.Stdout()}
	b.out = b.screen
	var notices []string
	if miss != nil {
		notices = append(notices, miss.Line)
	}
	return b.menu(ctx, at, notices)
}

// walk reads a path left to right: a person, by name or slug without case;
// a clip, by its number in the person's table or a unique prefix of its id;
// then an action with its arguments, returned as rest. An argument that
// names nothing ends the walk at the level it was read against.
func walk(lib []voices.VoiceSummary, path []string) (at place, rest []string, miss *NoMatchError) {
	if len(path) == 0 {
		return at, nil, nil
	}
	person := findPerson(lib, path[0])
	if person == nil {
		return at, nil, &NoMatchError{Arg: path[0], Line: fmt.Sprintf("no voice named %q", path[0])}
	}
	at.person = person.Slug
	if len(path) == 1 {
		return at, nil, nil
	}
	clip, err := findClip(*person, path[1])
	if err != nil {
		return at, nil, err
	}
	at.clip = clip.ID
	if len(path) == 2 {
		return at, nil, nil
	}
	if actionNamed(path[2]) == nil {
		line := fmt.Sprintf("no action %q; the actions are play, confirm, overrule, trim, move, delete", path[2])
		return at, nil, &NoMatchError{Arg: path[2], Line: line}
	}
	return at, path[2:], nil
}

func findPerson(lib []voices.VoiceSummary, arg string) *voices.VoiceSummary {
	for i := range lib {
		if arg != "" && (strings.EqualFold(string(lib[i].Name), arg) || strings.EqualFold(string(lib[i].Slug), arg)) {
			return &lib[i]
		}
	}
	return nil
}

// findClip gives a whole number below the clip count its row, whatever ids
// start with it.
func findClip(person voices.VoiceSummary, arg string) (*voices.ClipSummary, *NoMatchError) {
	if n := index(arg); n >= 0 && n < len(person.Clips) {
		return &person.Clips[n], nil
	}
	var hits []string
	var hit *voices.ClipSummary
	for i, c := range person.Clips {
		if arg != "" && strings.HasPrefix(string(c.ID), strings.ToLower(arg)) {
			hits, hit = append(hits, string(c.ID)), &person.Clips[i]
		}
	}
	switch len(hits) {
	case 1:
		return hit, nil
	case 0:
		return nil, &NoMatchError{Arg: arg, Line: fmt.Sprintf("%s has no clip %s", person.Name, arg)}
	}
	return nil, &NoMatchError{Arg: arg, Line: fmt.Sprintf("%s has %d clips whose id starts with %q (%s); give more of the id",
		person.Name, len(hits), arg, strings.Join(hits, ", "))}
}

// index is arg as a whole number, or -1 when it is not one.
func index(arg string) int {
	if arg == "" || len(arg) > 9 || strings.Trim(arg, "0123456789") != "" {
		return -1
	}
	n, _ := strconv.Atoi(arg)
	return n
}

// find is the person and the clip at names, nil for what the library does
// not hold (any more), and the clip's place among the person's clips.
func find(lib []voices.VoiceSummary, at place) (person *voices.VoiceSummary, clip *voices.ClipSummary, rank int) {
	for i := range lib {
		if lib[i].Slug == at.person {
			person = &lib[i]
		}
	}
	if person == nil {
		return nil, nil, 0
	}
	for i := range person.Clips {
		if person.Clips[i].ID == at.clip {
			return person, &person.Clips[i], i
		}
	}
	return person, nil, 0
}

// levelJSON is a level as -json prints it: always an array of people,
// everyone, the person alone, or the person with the clip alone.
func levelJSON(lib []voices.VoiceSummary, at place) []voices.VoiceSummary {
	person, clip, _ := find(lib, at)
	switch {
	case person == nil:
		return append([]voices.VoiceSummary{}, lib...)
	case clip == nil:
		return []voices.VoiceSummary{*person}
	}
	alone := *person
	alone.Clips = []voices.ClipSummary{*clip}
	return []voices.VoiceSummary{alone}
}

// row is one row of a table level and the level below it.
type row struct {
	cells []string
	leads place
	// unconfirmed: the person has a clip nobody confirmed.
	unconfirmed bool
}

// shownLevel is one level as text: a table of rows, or the view of a clip.
type shownLevel struct {
	title   string
	columns []menu.Column
	rows    []row
	// view is a clip's lines, and actions what the menu offers for it; a
	// level with a view has no table.
	view    string
	actions []string
}

func cells(rows []row) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r.cells
	}
	return out
}

// printed is the level written once, outside a menu.
func (l shownLevel) printed(cols int) string {
	switch {
	case l.view != "":
		return l.title + "\n" + l.view
	case len(l.rows) == 0:
		return emptyLibrary + "\n"
	case l.title != "":
		return l.title + "\n" + menu.Table(l.columns, cells(l.rows), 0, cols)
	}
	return menu.Table(l.columns, cells(l.rows), 0, cols)
}

// level renders the level at a place the library holds.
func level(lib []voices.VoiceSummary, at place) shownLevel {
	person, clip, rank := find(lib, at)
	switch {
	case person == nil:
		l := shownLevel{columns: peopleColumns}
		for _, v := range lib {
			var unconfirmed, named, wrong int
			for _, c := range v.Clips {
				if c.Status == voices.Unconfirmed {
					unconfirmed++
				}
				named += c.Named
				wrong += c.Overruled
			}
			l.rows = append(l.rows, row{leads: place{person: v.Slug}, unconfirmed: unconfirmed > 0, cells: []string{
				string(v.Name), strconv.Itoa(len(v.Clips)), strconv.Itoa(unconfirmed),
				strconv.Itoa(named), strconv.Itoa(wrong), day(v.LastNamed),
			}})
		}
		return l
	case clip == nil:
		l := shownLevel{
			title:   fmt.Sprintf("%s · %s, best first", person.Name, count(len(person.Clips), "clip")),
			columns: clipColumns,
		}
		for _, c := range person.Clips {
			l.rows = append(l.rows, row{leads: place{person: person.Slug, clip: c.ID}, cells: []string{
				string(c.ID), string(c.Status), seconds(c.Seconds), strconv.Itoa(c.Named),
				strconv.Itoa(c.Overruled), day(c.LastNamed), addedBy(c.Source),
			}})
		}
		return l
	}
	picked := "picked for naming"
	if rank >= voices.PicksPerVoice {
		picked = fmt.Sprintf("not picked for naming (the best %d are)", voices.PicksPerVoice)
	}
	l := shownLevel{title: fmt.Sprintf("%s · clip %s · %s", person.Name, clip.ID, clip.Status)}
	for _, field := range [][2]string{
		{"length", seconds(clip.Seconds)},
		{"added", day(&clip.Added) + " by " + addedBy(clip.Source)},
		{"named", namings(*clip)},
		{"rank", fmt.Sprintf("%d of %d, %s", rank+1, len(person.Clips), picked)},
		{"path", "$(clai confdir)/" + voices.Dir + "/" + string(person.Slug) + "/" + string(clip.ID) + voices.ClipExt},
	} {
		l.view += "  " + menu.Pad(field[0], len("length")) + "  " + field[1] + "\n"
	}
	for _, a := range actions {
		if a.offeredFor(*clip) {
			l.actions = append(l.actions, a.label)
		}
	}
	return l
}

// tableView is what the menu shows of a table: the page, and for the people
// whether only those with an unconfirmed clip are shown.
type tableView struct {
	page            int
	unconfirmedOnly bool
}

// menu shows one level at a time until it is left. Every level is listed
// anew when it is shown, so it shows what an action changed.
func (b *browser) menu(ctx context.Context, at place, notices []string) error {
	var people, clips tableView
	// afterAction: the level an action returns to may be gone with what
	// the action removed; then the level above is shown without a word.
	afterAction := false
	for ctx.Err() == nil {
		lib, err := b.deps.Library.List()
		if err != nil {
			return err
		}
		person, clip, _ := find(lib, at)
		gone := at.person != "" && person == nil || at.clip != "" && clip == nil
		switch {
		case gone && afterAction:
			at = at.up()
			continue
		case gone && at.clip != "":
			return &voices.ClipNotFoundError{Ref: voices.ClipRef{Slug: at.person, ID: at.clip}}
		case gone:
			notices = append(notices, fmt.Sprintf("%s has no clips any more", at.person))
			at = place{}
			continue
		case len(lib) == 0:
			return b.screen.Draw(noticeLines(append(notices, emptyLibrary)), b.width())
		}
		afterAction = false
		view := &people
		if at.person != "" {
			view = &clips
		}
		cols := b.width()
		body, question, rows := level(lib, at).screen(at, view, cols)
		if err := b.screen.Draw(noticeLines(notices)+body, cols); err != nil {
			return err
		}
		answer, err := b.screen.Ask(ctx, question)
		if errors.Is(err, menu.ErrLeft) {
			return nil
		}
		if err != nil {
			return err
		}
		notices = nil
		key := strings.ToLower(answer)
		picked := index(answer)
		switch {
		case key == "b" || key == "back":
			if at == (place{}) {
				notices = []string{meaningless(answer)}
			}
			at = at.up()
		case at.clip != "":
			a := actionPicked(key, *clip)
			if a == nil {
				notices = []string{meaningless(answer)}
				break
			}
			t := target{lib: lib, voice: *person, clip: *clip}
			args, err := b.ask(ctx, *a, t)
			switch {
			case errors.Is(err, menu.ErrLeft):
				return nil
			case errors.Is(err, errNotRun):
				continue
			case err != nil:
				return err
			}
			line, err := a.run(ctx, b, t, args)
			if err != nil {
				notices = []string{err.Error()}
				break
			}
			notices, afterAction = []string{line}, true
			people, clips = tableView{}, tableView{}
			if a.leavesClip {
				at = at.up()
			}
		case key == "n" || key == "next" || key == "p" || key == "prev":
			pages := pageCount(len(rows))
			if pages < 2 {
				notices = []string{"the table has one page"}
				break
			}
			step := 1
			if key[0] == 'p' {
				step = -1
			}
			view.page = (view.page + step + pages) % pages
		case key == "u" && at == (place{}):
			people = tableView{unconfirmedOnly: !people.unconfirmedOnly}
		case picked >= 0 && picked < len(rows):
			if at == (place{}) {
				clips = tableView{}
			}
			at = rows[picked].leads
		default:
			notices = []string{meaningless(answer)}
		}
	}
	return nil
}

// screen is the level as the menu draws it, the question it asks, and the
// rows a number picks among.
func (l shownLevel) screen(at place, view *tableView, cols int) (body, question string, rows []row) {
	ask := func(parts ...string) string { return strings.Join(parts, " · ") + ": " }
	if l.view != "" {
		return l.printed(cols), ask(append(l.actions, "[b]ack", "[q]uit")...), nil
	}
	var parts []string
	rows = l.rows
	if at == (place{}) {
		parts = []string{"[u]nconfirmed only"}
		if view.unconfirmedOnly {
			parts = []string{"[u]nconfirmed only (on)"}
			rows = slices.DeleteFunc(slices.Clone(rows), func(r row) bool { return !r.unconfirmed })
		}
	}
	pages := pageCount(len(rows))
	view.page = min(max(view.page, 0), max(pages-1, 0))
	if pages > 1 {
		parts = append(parts, fmt.Sprintf("page %d/%d", view.page+1, pages), "[n]ext", "[p]rev")
	}
	if at != (place{}) {
		parts = append(parts, "[b]ack")
	}
	parts = append(parts, "[q]uit")
	if l.title != "" {
		body = l.title + "\n"
	}
	if len(rows) == 0 {
		return body + "nobody has an unconfirmed clip\n", ask(parts...), nil
	}
	first := view.page * pageSize
	body += menu.Table(l.columns, cells(rows[first:min(first+pageSize, len(rows))]), first, cols)
	return body, ask(append(parts, "or pick a number")...), rows
}

func pageCount(rows int) int { return (rows + pageSize - 1) / pageSize }

func meaningless(answer string) string {
	if answer == "" {
		return "an empty answer picks nothing here"
	}
	return fmt.Sprintf("%q picks nothing here", answer)
}

// noticeLines is the notices above a level, each on one line of its own.
func noticeLines(notices []string) string {
	var b strings.Builder
	for _, n := range notices {
		if n != "" {
			b.WriteString(oneLine(n) + "\n")
		}
	}
	return b.String()
}

func oneLine(s string) string {
	return strings.Join(strings.Split(strings.TrimRight(s, "\r\n"), "\n"), "; ")
}

func day(at *time.Time) string {
	if at == nil {
		return "never"
	}
	return at.Local().Format(time.DateOnly)
}

func addedBy(source voices.Source) string {
	switch source {
	case voices.SourceAdd:
		return "you"
	case voices.SourceRemember:
		return "the agent"
	}
	return string(source)
}

func seconds(s float64) string { return strconv.FormatFloat(s, 'f', 1, 64) + " s" }

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// namings is the clip view's line of how often the clip named someone.
func namings(c voices.ClipSummary) string {
	switch {
	case c.Named == 0 && c.Overruled == 0:
		return "nobody yet"
	case c.Overruled <= c.Named:
		return fmt.Sprintf("%s, %d wrong, last %s", count(c.Named, "time"), c.Overruled, day(c.LastNamed))
	case c.LastNamed != nil:
		return count(c.Named, "time") + ", overruled " + count(c.Overruled, "time") + ", last " + day(c.LastNamed)
	}
	return count(c.Named, "time") + ", overruled " + count(c.Overruled, "time")
}
