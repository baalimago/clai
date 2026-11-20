package voicescmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voicescmd"
)

const emptyLibrary = "no voices yet; add one with clai audio voices add <wav> -name <name>\n"

func peopleTable() string {
	return "Index | Person    | Clips | Unconfirmed | Named | Wrong | Last named\n" +
		"0     | Christoph | 1     | 1           | 0     | 0     | never\n" +
		"1     | Lorentz   | 3     | 1           | 5     | 3     | " + today + "\n" +
		"2     | Syrus     | 2     | 0           | 1     | 0     | " + today + "\n"
}

func (ex example) lorentzTable() string {
	return "Lorentz · 3 clips, best first\n" +
		"Index | Clip         | Status      | Length | Named | Wrong | Last named | Added by\n" +
		"0     | " + string(ex.lorentz[0]) + " | confirmed   | 3.4 s  | 2     | 1     | " + today + " | you\n" +
		"1     | " + string(ex.lorentz[1]) + " | confirmed   | 6.1 s  | 3     | 2     | " + today + " | the agent\n" +
		"2     | " + string(ex.lorentz[2]) + " | unconfirmed | 3.7 s  | 0     | 0     | never      | the agent\n"
}

// thirdView is the view of Lorentz's third clip, which nothing has named.
func (ex example) thirdView() string {
	return "Lorentz · clip " + string(ex.lorentz[2]) + " · unconfirmed\n" +
		"  length  3.7 s\n" +
		"  added   " + today + " by the agent\n" +
		"  named   nobody yet\n" +
		"  rank    3 of 3, not picked for naming (the best 2 are)\n" +
		"  path    $(clai confdir)/voices/lorentz/" + string(ex.lorentz[2]) + ".flac\n"
}

func TestList_WithoutATerminalPrintsTheLevelOnce(t *testing.T) {
	f, ex := exampleFixture(t)
	first := "Lorentz · clip " + string(ex.lorentz[0]) + " · confirmed\n  length  3.4 s\n  added   " + today + " by you\n" +
		"  named   2 times, 1 wrong, last " + today + "\n  rank    1 of 3, picked for naming\n" +
		"  path    $(clai confdir)/voices/lorentz/" + string(ex.lorentz[0]) + ".flac\n"
	before := f.files()
	for _, row := range []struct {
		r    as
		args []string
		want string
	}{
		{as{}, []string{"list"}, peopleTable()},
		{as{stdoutTTY: true}, []string{"list"}, peopleTable()},
		{as{stdinTTY: true}, []string{"list", "lorentz"}, ex.lorentzTable()},
		{as{}, []string{"list", "lorentz", string(ex.lorentz[2])}, ex.thirdView()},
		{as{}, []string{"list", "lorentz", "0"}, first},
	} {
		if o := f.run(row.r, append([]string{"voices"}, row.args...)...); o != (outcome{stdout: row.want}) {
			t.Fatalf("%v with %+v: status %d, stderr %q, stdout:\n%s\nwant:\n%s", row.args, row.r, o.status, o.stderr, o.stdout, row.want)
		}
	}
	if !maps.Equal(before, f.files()) {
		t.Fatal("listing changed the library")
	}

	many := newFixture(t)
	for i := range 12 {
		many.enrol(fmt.Sprintf("Person %c", 'A'+i), 4, voices.Confirmed, voices.SourceAdd)
	}
	if out := many.ok("list"); strings.Count(out, "\n") != 13 || strings.Contains(out, "page") {
		t.Fatalf("12 people without a terminal are not one table of 12 rows:\n%s", out)
	}
}

func TestList_EmptyLibrary(t *testing.T) {
	f := newFixture(t)
	for _, row := range []struct {
		r    as
		args []string
		want outcome
	}{
		{as{}, []string{"list"}, outcome{stdout: emptyLibrary}},
		// On a terminal the line is all the menu shows: nothing is asked.
		{as{stdinTTY: true, stdoutTTY: true}, []string{"list"}, outcome{stdout: "\n" + emptyLibrary}},
		{as{}, []string{"list", "-json"}, outcome{stdout: "[]\n"}},
		{as{}, []string{"list", "ada"}, outcome{stderr: "no voice named \"ada\"\n", status: 1}},
	} {
		o := f.run(row.r, append([]string{"voices"}, row.args...)...)
		if row.want.err = o.err; o != row.want {
			t.Fatalf("%v gave %+v, want %+v", row.args, o, row.want)
		}
	}
	if entries, err := os.ReadDir(f.dir); err != nil || len(entries) != 0 {
		t.Fatalf("listing an empty library wrote %v (%v)", entries, err)
	}
}

func TestList_JSONIsAlwaysAnArrayOfPeople(t *testing.T) {
	f, ex := exampleFixture(t)
	lib, err := f.lib.List()
	if err != nil {
		t.Fatal(err)
	}
	clip := lib[1]
	clip.Clips = clip.Clips[2:]
	terminal := as{stdinTTY: true, stdoutTTY: true}
	for _, row := range []struct {
		r    as
		args []string
		want []voices.VoiceSummary
	}{
		{as{}, []string{"list", "-json"}, lib},
		{terminal, []string{"list", "-json"}, lib},
		{as{stdinTTY: true, stdoutTTY: true, globalJSON: true}, []string{"list"}, lib},
		{terminal, []string{"list", "lorentz", "-json"}, lib[1:2]},
		{as{}, []string{"list", "-json", "LORENTZ"}, lib[1:2]},
		{terminal, []string{"list", "lorentz", string(ex.lorentz[2]), "-json"}, []voices.VoiceSummary{clip}},
	} {
		want, err := json.Marshal(row.want)
		if err != nil {
			t.Fatal(err)
		}
		if o := f.run(row.r, append([]string{"voices"}, row.args...)...); o != (outcome{stdout: string(want) + "\n"}) {
			t.Fatalf("%v: status %d, stderr %q, stdout:\n%s\nwant:\n%s", row.args, o.status, o.stderr, o.stdout, want)
		}
	}
	if path := clip.Clips[0].Path; path != filepath.Join(f.dir, "voices", "lorentz", string(ex.lorentz[2])+".flac") {
		t.Fatalf("the clip's path is %q, want its file", path)
	}
	if o := f.run(terminal, "voices", "list", "ada", "-json"); o.status != 1 || o.stdout != "" || o.stderr != "no voice named \"ada\"\n" {
		t.Fatalf("list ada -json: %+v", o)
	}
}

func TestList_PathMatching(t *testing.T) {
	f, ex := exampleFixture(t)
	f.enrol("Jean-Luc P.", 4.6, voices.Confirmed, voices.SourceAdd)
	// Nora's two clips both have ids that start with 1.
	for seconds, enrolled := 4.0, 0; enrolled < 2; seconds += 0.01 {
		if idOf("Nora", seconds)[0] == '1' {
			f.enrol("Nora", seconds, voices.Confirmed, voices.SourceAdd)
			enrolled++
		}
	}
	third := string(ex.lorentz[2])
	lorentz, view := "Lorentz · 3 clips, best first", "Lorentz · clip "+third+" · unconfirmed"
	before := f.files()
	for _, row := range []struct {
		path string
		// level is the first line printed, or miss the line of the failure.
		level, miss string
	}{
		{path: "lorentz", level: lorentz},
		{path: "Lorentz", level: lorentz},
		{path: "LORENTZ", level: lorentz},
		{path: "jean-luc-p", level: "Jean-Luc P. · 1 clip, best first"},
		{path: "JEAN-LUC P.", level: "Jean-Luc P. · 1 clip, best first"},
		{path: "lorentz/2", level: view},
		{path: "lorentz/" + third, level: view},
		{path: "lorentz/" + third[:4], level: view},
		{path: "lorentz/" + strings.ToUpper(third[:4]), level: view},
		{path: "lorentz/3", level: view},
		{path: "nora/1", level: "Nora · clip " + string(f.clips("nora")[1].ID) + " · confirmed"},
		{path: "ada", miss: `no voice named "ada"`},
		{path: "ada/" + third + "/delete", miss: `no voice named "ada"`},
		{path: "lorentz/ffff", miss: "Lorentz has no clip ffff"},
		{path: "lorentz/b", miss: fmt.Sprintf(`Lorentz has 2 clips whose id starts with "b" (%s, %s); give more of the id`, ex.lorentz[0], ex.lorentz[1])},
		{path: "lorentz/" + third + "/sing", miss: `no action "sing"; the actions are play, confirm, overrule, trim, move, delete`},
	} {
		o := f.voices(append([]string{"list"}, strings.Split(row.path, "/")...)...)
		first, _, _ := strings.Cut(o.stdout, "\n")
		_, noMatch := errors.AsType[*voicescmd.NoMatchError](o.err)
		switch {
		case row.miss == "" && (o.status != 0 || first != row.level):
			t.Fatalf("list %s: status %d, stderr %q, printed %q; want the level %q", row.path, o.status, o.stderr, first, row.level)
		case row.miss != "" && (o.status != 1 || !noMatch || o.stdout != "" || o.stderr != row.miss+"\n"):
			t.Fatalf("list %s: status %d, err %T, stdout %q, stderr %q; want exit 1 saying %q", row.path, o.status, o.err, o.stdout, o.stderr, row.miss)
		}
	}
	if !maps.Equal(before, f.files()) {
		t.Fatal("a path changed the library")
	}
}

func TestList_ColumnsDropToFitTheWidth(t *testing.T) {
	f, ex := exampleFixture(t)
	for _, row := range []struct {
		width         int
		people, clips string
	}{
		{200, "Index | Person    | Clips | Unconfirmed | Named | Wrong | Last named", "Index | Clip         | Status      | Length | Named | Wrong | Last named | Added by"},
		{83, "Index | Person    | Clips | Unconfirmed | Named | Wrong | Last named", "Index | Clip         | Status      | Length | Named | Wrong | Last named"},
		{68, "Index | Person    | Clips | Unconfirmed | Named | Wrong | Last named", "Index | Clip         | Status      | Length | Named | Wrong"},
		{58, "Index | Person    | Clips | Unconfirmed | Named | Wrong", "Index | Clip         | Status      | Named | Wrong"},
		{41, "Index | Person    | Clips | Unconfirmed", "Index | Clip         | Status"},
		{24, "Index | Person", "Index | Clip"},
	} {
		f.deps.Width = func() int { return row.width }
		people, _, _ := strings.Cut(f.ok("list"), "\n")
		clips := strings.SplitN(f.ok("list", "lorentz"), "\n", 3)[1]
		if people != row.people || clips != row.clips {
			t.Fatalf("at %d the headers are\n%s\n%s\nwant\n%s\n%s", row.width, people, clips, row.people, row.clips)
		}
	}
	// A path is never cut.
	if view := f.ok("list", "lorentz", "2"); view != ex.thirdView() {
		t.Fatalf("at 24 columns the clip's view is cut:\n%s", view)
	}
}

// noRemove is a library that cannot remove a clip.
type noRemove struct{ voicescmd.Library }

func (noRemove) Remove(voices.ClipRef) error {
	return &voices.LibraryWriteError{Path: "events.jsonl", Err: errors.New("read-only file system")}
}

// trimmed is the id of the clip a trim of the example's clip id to cut adds.
func (f *fixture) trimmed(slug voices.Slug, id voices.ClipID, cut voices.Cut) string {
	file := filepath.Join(f.dir, voices.Dir, string(slug), string(id)+voices.ClipExt)
	return string(voices.ClipIDOf(fmt.Appendf(nil, "fLaC %s %v", file, cut)))
}

func TestList_ActionArgumentsRunAtOnce(t *testing.T) {
	_, ex := exampleFixture(t)
	c, l1, l2, l3 := string(ex.christoph), string(ex.lorentz[0]), string(ex.lorentz[1]), string(ex.lorentz[2])
	const trimUsage = "; usage: trim <from> <to>, in decimal seconds from the clip's start"
	tooShort := &voices.ClipQualityError{Failed: []voices.QualityCheck{voices.CheckLength}, Seconds: 1.5}
	cut := voices.Cut{From: 800 * time.Millisecond, To: 5900 * time.Millisecond}
	leaves := func(f *fixture) { f.leave() }
	type holds map[voices.Slug]int
	for _, row := range []struct {
		path    string
		prepare func(f *fixture)
		// line is the one line on stdout, or on stderr when status is not 0.
		line   func(f *fixture) string
		status int
		// holds is how many clips people hold afterwards; without it the
		// library must be unchanged.
		holds holds
		// view is a line of the clip's view afterwards.
		view  string
		check func(t *testing.T, f *fixture)
	}{
		{path: "lorentz " + l3 + " confirm", line: is("Lorentz/" + l3 + ": confirmed"), holds: holds{"lorentz": 3}},
		{path: "LORENTZ 0 Confirm", line: is("Lorentz/" + l1 + ": already confirmed")},
		{path: "lorentz " + l2 + " overrule", line: is("Lorentz/" + l2 + ": overruled (3 of 3 namings wrong)"), holds: holds{"lorentz": 3}},
		{path: "lorentz " + l3 + " overrule", line: is("Lorentz/" + l3 + ": overruled 1 time, never named"), holds: holds{"lorentz": 3}, view: "  named   0 times, overruled 1 time\n"},
		{
			path: "lorentz " + l1 + " overrule", prepare: func(f *fixture) { f.ok("list", "lorentz", l1, "overrule") },
			line: is("Lorentz/" + l1 + ": overruled 3 times, named 2 times"), holds: holds{"lorentz": 3}, view: "  named   2 times, overruled 3 times, last " + today + "\n",
		},
		{path: "lorentz " + l3 + " delete", line: is("Lorentz/" + l3 + ": deleted"), holds: holds{"lorentz": 2, "christoph": 1, "syrus": 2}},
		{path: "christoph " + c + " delete", line: is("Christoph/" + c + ": deleted"), holds: holds{"lorentz": 3, "christoph": 0, "syrus": 2}},
		{
			path: "lorentz " + l2 + " trim 0.8 5.9",
			line: func(f *fixture) string {
				return "Lorentz/" + l2 + " trimmed to 5.1 s as " + f.trimmed("lorentz", ex.lorentz[1], cut) + "; the old clip is removed"
			},
			holds: holds{"lorentz": 3},
			check: func(t *testing.T, f *fixture) {
				old, clips := filepath.Join(f.dir, "voices", "lorentz", l2+".flac"), f.clips("lorentz")
				got := clips[slices.IndexFunc(clips, func(c voices.ClipSummary) bool { return !slices.Contains(ex.lorentz[:], c.ID) })]
				if got.Status != voices.Confirmed || got.Source != voices.SourceRemember || got.Named != 0 || got.Seconds != 5.1 {
					t.Fatalf("the trimmed clip is %+v; want the old clip's status and source, and no namings", got)
				}
				if !slices.Equal(f.inspected, []inspected{{old, cut}}) {
					t.Fatalf("Inspect got %+v, want the old clip's file and %v", f.inspected, cut)
				}
			},
		},
		{
			// At the cap of six clips the trimmed clip takes the old one's place: nothing is pruned.
			path: "lorentz " + l2 + " trim 0.8 5.9", holds: holds{"lorentz": 6},
			prepare: func(f *fixture) {
				for _, seconds := range []float64{4.1, 4.2, 4.3} {
					f.enrol("Lorentz", seconds, voices.Confirmed, voices.SourceAdd)
				}
			},
			line: func(f *fixture) string {
				return "Lorentz/" + l2 + " trimmed to 5.1 s as " + f.trimmed("lorentz", ex.lorentz[1], cut) + "; the old clip is removed"
			},
		},
		{
			path: "christoph " + c + " move SYRUS", line: is("Christoph/" + c + " moved to Syrus as " + c + " (unconfirmed)"),
			holds: holds{"lorentz": 3, "christoph": 0, "syrus": 3},
			check: func(t *testing.T, f *fixture) {
				moved := slices.DeleteFunc(f.clips("syrus"), func(c voices.ClipSummary) bool { return c.ID != ex.christoph })
				if len(moved) != 1 || moved[0].Status != voices.Unconfirmed || moved[0].Source != voices.SourceRemember {
					t.Fatalf("Syrus holds the clip as %+v", moved)
				}
				if len(f.inspected) != 1 || f.inspected[0].cut != (voices.Cut{}) || !strings.HasSuffix(f.inspected[0].file, c+".flac") {
					t.Fatalf("Inspect got %+v, want the clip's file, whole", f.inspected)
				}
			},
		},
		{path: "lorentz " + l3 + " move Ada", line: is("Lorentz/" + l3 + " moved to Ada, a new person, as " + l3 + " (unconfirmed)"), holds: holds{"lorentz": 2, "ada": 1}},
		{
			path: "christoph " + c + " move syrus", prepare: leaves, holds: holds{"christoph": 0, "syrus": 3},
			line: is("Christoph/" + c + " moved to Syrus as " + c + " (unconfirmed); warning: " + newFixture(t).leave()),
		},
		{path: "lorentz " + l2 + " trim 2.0 3.5", prepare: func(f *fixture) { f.inspectErr = tooShort }, status: 1, line: is("clip too short: 1.5 s, a clip is 3–10 s")},
		{path: "lorentz " + l1 + " move lorentz", status: 1, line: is("the library already holds clip lorentz/" + l1)},
		{
			path: "lorentz " + l2 + " trim 0.8 5.9", prepare: func(f *fixture) { f.deps.Library = noRemove{f.lib} }, status: 1, holds: holds{"lorentz": 4},
			line: func(f *fixture) string {
				return "added lorentz/" + f.trimmed("lorentz", ex.lorentz[1], cut) + ", but the old clip lorentz/" + l2 +
					" could not be removed, so both are in the library: write voice library events.jsonl: read-only file system"
			},
		},
		{path: "lorentz " + l2 + " trim 0.8", status: 2, line: is("usage: trim <from> <to> (2 arguments, got 1)")},
		{path: "lorentz " + l2 + " trim 1 1e3", status: 2, line: is(`trim 1 1e3: "1e3" is not a decimal number of seconds` + trimUsage)},
		{path: "lorentz " + l2 + " trim 5 2", status: 2, line: is("trim 5 2: the clip would end before it starts" + trimUsage)},
		{path: "lorentz " + l3 + " confirm now", status: 2, line: is("usage: confirm (no arguments, got 1)")},
		{path: "christoph " + c + " move", status: 2, line: is("usage: move <person> (1 argument, got 0)")},
	} {
		f, _ := exampleFixture(t)
		if row.prepare != nil {
			row.prepare(f)
		}
		before := f.files()
		// On a terminal too nothing is asked: reading stdin fails the test.
		o := f.run(as{stdinTTY: true, stdoutTTY: true}, append([]string{"voices", "list"}, strings.Fields(row.path)...)...)
		want := outcome{stdout: row.line(f) + "\n", err: o.err}
		if row.status != 0 {
			want = outcome{stderr: row.line(f) + "\n", status: row.status, err: o.err}
		}
		if _, usage := errors.AsType[*voicescmd.UsageError](o.err); o != want || (o.err != nil) != (row.status != 0) || usage != (row.status == 2) {
			t.Fatalf("list %s gave\n%+v\nwant\n%+v", row.path, o, want)
		}
		lib, _ := f.lib.List()
		for slug, n := range row.holds {
			held := slices.IndexFunc(lib, func(v voices.VoiceSummary) bool { return v.Slug == slug && len(v.Clips) == n }) >= 0
			if held == (n == 0) {
				t.Fatalf("after list %s %s does not hold %d clips: %+v", row.path, slug, n, lib)
			}
		}
		if row.holds == nil && (!maps.Equal(before, f.files()) || len(f.inspected) != 0 && row.status == 2) {
			t.Fatalf("list %s changed the library, or inspected for a usage error: %v", row.path, f.inspected)
		}
		if view := strings.Fields(row.path)[:2]; row.view != "" && !strings.Contains(f.ok("list", view[0], view[1]), row.view) {
			t.Fatalf("after list %s the clip's view lacks %q", row.path, row.view)
		}
		if row.check != nil {
			row.check(t, f)
		}
	}
}

func is(line string) func(*fixture) string { return func(*fixture) string { return line } }

func TestList_Play(t *testing.T) {
	f, ex := exampleFixture(t)
	first := string(ex.lorentz[0])
	file := filepath.Join(f.dir, "voices", "lorentz", first+".flac")
	play := func(r as) outcome { return f.run(r, "voices", "list", "lorentz", first, "play") }
	for _, row := range []struct {
		installed []string
		runs      []string
	}{
		{[]string{"open", "xdg-open", "ffplay", "play"}, []string{"play", file}},
		{[]string{"open", "xdg-open", "ffplay"}, []string{"ffplay", "-nodisp", "-autoexit", file}},
		{[]string{"open", "xdg-open"}, []string{"xdg-open", file}},
		{[]string{"open"}, []string{"open", file}},
	} {
		f.player = &player{installed: row.installed}
		f.deps.Runner = f.player
		// Without a terminal stdin is not read: reading it fails the test.
		if o := play(as{}); o != (outcome{stdout: "▶ playing 3.4 s (" + row.runs[0] + ")\n"}) {
			t.Fatalf("with %v: %+v", row.installed, o)
		}
		if len(f.player.calls) != 1 || !slices.Equal(f.player.calls[0], row.runs) {
			t.Fatalf("with %v the player ran as %q, want %q", row.installed, f.player.calls, row.runs)
		}
	}
	if o := play(as{stdin: strings.NewReader("x"), stdinTTY: true, stdoutTTY: true}); o != (outcome{stdout: "▶ playing 3.4 s (open) · [Enter] stops\n"}) {
		t.Fatalf("on a terminal: %+v", o)
	}

	f.player.installed = nil
	o := play(as{})
	none, _ := errors.AsType[*voicescmd.NoPlayerError](o.err)
	if o.status != 1 || o.stdout != "" || none == nil || !slices.Equal(none.Tried, []string{"play", "ffplay", "xdg-open", "open"}) ||
		o.stderr != "no player for the clip: none of play, ffplay, xdg-open or open is installed; install sox for play, or ffmpeg for ffplay\n" {
		t.Fatalf("without a player: %+v", o)
	}

	// A player's exit status is never clai's, and its stderr reaches the
	// user as its last line of printable text, cut short: never as audio.
	for _, row := range []struct {
		stderr string
		err    error
		detail string
	}{
		{"playing\nplay FAIL formats: can't open input file\n", exitStatus(2), "play FAIL formats: can't open input file"},
		{"", exitStatus(7), "exit status 7"},
		{"fLaC\x00\x01\xff\xfe\n\x1b[31m\x07" + strings.Repeat("é", 300) + "\n", errors.New("exit status 1"), "[31m" + strings.Repeat("é", 236)},
	} {
		f.player = &player{installed: []string{"play", "open"}, stderr: row.stderr, err: row.err}
		f.deps.Runner = f.player
		o := play(as{})
		failed, _ := errors.AsType[*voicescmd.PlayError](o.err)
		if o.status != 1 || failed == nil || failed.Player != "play" || !errors.Is(o.err, row.err) || len(f.player.calls) != 1 ||
			o.stdout != "▶ playing 3.4 s (play)\n" || o.stderr != "the player play failed: "+row.detail+"\n" {
			t.Fatalf("a player failing with %q, %v: %+v after %d runs", row.stderr, row.err, o, len(f.player.calls))
		}
	}
}
