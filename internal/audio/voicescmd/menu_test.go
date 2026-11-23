package voicescmd_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/menu"
)

// The questions of the three levels.
const (
	peopleAsk    = "[u]nconfirmed only · [q]uit · or pick a number: "
	clipsAsk     = "[b]ack · [q]uit · or pick a number: "
	confirmedAsk = "[p]lay · [o]verrule · [t]rim · [m]ove to another person · [d]elete · [b]ack · [q]uit: "
	clipAsk      = "[p]lay · [c]onfirm · [o]verrule · [t]rim · [m]ove to another person · [d]elete · [b]ack · [q]uit: "
)

func sameDraws(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("the menu drew:\n%s\nwant:\n%s", strings.Join(got, "\n--\n"), strings.Join(want, "\n--\n"))
	}
}

func TestMenu_ShowsOneLevelAtATime(t *testing.T) {
	f, ex := exampleFixture(t)
	people, clips, clip := peopleTable()+peopleAsk, ex.lorentzTable()+clipsAsk, ex.thirdView()+clipAsk
	// The first draw starts on a row of its own. Every later one goes up over
	// the rows of the draw before it and of the echoed answer, and erases.
	wide := "\n" + people + "\x1b[5A\r\x1b[J" + clips + "\x1b[6A\r\x1b[J" + clip + "\x1b[7A\r\x1b[J" + clips + "\x1b[6A\r\x1b[J" + people +
		"\x1b[5A\r\x1b[J" + `"b" picks nothing here` + "\n" + people
	if o := f.on(&keyboard{answers: []string{"1", "2", "b", "back", "b", "quit"}}, "list"); o != (outcome{stdout: wide}) {
		t.Fatalf("the menu wrote and ended as\n%+v\nwant status 0 and\n%q", o, wide)
	}
	// At 30 columns the rows are counted as that width wraps them, and the
	// tables lose columns. The input ending leaves as q does, below the menu.
	f.deps.Width = func() int { return 30 }
	people = "Index | Person    | Clips\n0     | Christoph | 1\n1     | Lorentz   | 3\n2     | Syrus     | 2\n" + peopleAsk
	clips = fmt.Sprintf("Lorentz · 3 clips, best first\nIndex | Clip\n0     | %s\n1     | %s\n2     | %s\n", ex.lorentz[0], ex.lorentz[1], ex.lorentz[2]) + clipsAsk
	narrow := "\n" + people + "\x1b[6A\r\x1b[J" + clips + "\x1b[7A\r\x1b[J" + clip + "\x1b[14A\r\x1b[J" + clips + "\x1b[7A\r\x1b[J" + people + "\n"
	if o := f.on(&keyboard{answers: []string{"1", "2", "b", "b"}}, "list"); o != (outcome{stdout: narrow}) {
		t.Fatalf("at 30 columns the menu wrote and ended as\n%+v\nwant status 0 and\n%q", o, narrow)
	}
}

func TestMenu_AMissAndAnAnswerThatPicksNothingSaySoAboveTheLevel(t *testing.T) {
	f, ex := exampleFixture(t)
	third := string(ex.lorentz[2])
	people, clips := peopleTable()+peopleAsk, ex.lorentzTable()+clipsAsk
	before := f.files()
	for _, row := range []struct {
		path string
		// level is what the path opens: its miss, if any, above the level.
		level            string
		answers, notices []string
	}{
		{
			path: "", level: people, answers: []string{"7", "0,1", "x", "", "n"},
			notices: []string{`"7" picks nothing here`, `"0,1" picks nothing here`, `"x" picks nothing here`, "an empty answer picks nothing here", "the table has one page"},
		},
		{path: "lorentz", level: clips, answers: []string{"prev", "u"}, notices: []string{"the table has one page", `"u" picks nothing here`}},
		// A confirmed clip is not offered to be confirmed again.
		{path: "lorentz 0", level: f.ok("list", "lorentz", "0") + confirmedAsk, answers: []string{"c", "0"}, notices: []string{`"c" picks nothing here`, `"0" picks nothing here`}},
		{path: "ada", level: `no voice named "ada"` + "\n" + people},
		{path: "ada " + third + " delete", level: `no voice named "ada"` + "\n" + people},
		{path: "lorentz ffff", level: "Lorentz has no clip ffff\n" + clips},
		{path: "lorentz " + third + " sing", level: `no action "sing"; the actions are play, confirm, overrule, trim, move, delete` + "\n" + ex.thirdView() + clipAsk},
	} {
		want := []string{row.level}
		for _, notice := range row.notices {
			want = append(want, notice+"\n"+row.level)
		}
		want[len(want)-1] += "\n"
		sameDraws(t, f.menu(row.answers, append([]string{"list"}, strings.Fields(row.path)...)...), want)
	}
	if !maps.Equal(before, f.files()) {
		t.Fatal("a miss or an answer that picks nothing changed the library")
	}
}

func TestMenu_UnconfirmedOnlyFilter(t *testing.T) {
	f, ex := exampleFixture(t)
	if _, err := f.lib.Confirm(voices.ClipRef{Slug: "christoph", ID: ex.christoph}); err != nil {
		t.Fatal(err)
	}
	all := f.ok("list") + peopleAsk
	filtered := "Index | Person  | Clips | Unconfirmed | Named | Wrong | Last named\n" +
		"0     | Lorentz | 3     | 1           | 5     | 3     | " + today + "\n" +
		"[u]nconfirmed only (on) · [q]uit · or pick a number: "
	// A number picks among the rows the filter keeps: 0 is Lorentz.
	sameDraws(t, f.menu([]string{"u", "0", "b", "u"}, "list"), []string{all, filtered, ex.lorentzTable() + clipsAsk, filtered, all + "\n"})

	if _, err := f.lib.Confirm(voices.ClipRef{Slug: "lorentz", ID: ex.lorentz[2]}); err != nil {
		t.Fatal(err)
	}
	nobody := "nobody has an unconfirmed clip\n[u]nconfirmed only (on) · [q]uit: "
	sameDraws(t, f.menu([]string{"u", "0"}, "list")[1:], []string{nobody, `"0" picks nothing here` + "\n" + nobody + "\n"})
}

func TestMenu_PagesOfTenRows(t *testing.T) {
	f := newFixture(t)
	for i := range 12 {
		f.enrol(fmt.Sprintf("Person %c", 'A'+i), 4, voices.Confirmed, voices.SourceAdd)
	}
	page := func(n int, rows ...int) string {
		shown := "Index | Person   | Clips | Unconfirmed | Named | Wrong | Last named\n"
		for _, i := range rows {
			shown += fmt.Sprintf("%-5d | Person %c | 1     | 0           | 0     | 0     | never\n", i, 'A'+i)
		}
		return shown + fmt.Sprintf("[u]nconfirmed only · page %d/2 · [n]ext · [p]rev · [q]uit · or pick a number: ", n)
	}
	first, second := page(1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9), page(2, 10, 11)
	draws := f.menu([]string{"n", "next", "p", "prev", "N", "11"}, "list")
	sameDraws(t, draws[:6], []string{first, second, first, second, first, second})
	if !strings.HasPrefix(draws[6], "Person L · 1 clip, best first\n") {
		t.Fatalf("11 on the second page shows:\n%s", draws[6])
	}
}

func TestMenu_APickedActionAsksThenShowsItsLineAboveTheLevel(t *testing.T) {
	_, ex := exampleFixture(t)
	c, l1, l2, l3 := string(ex.christoph), string(ex.lorentz[0]), string(ex.lorentz[1]), string(ex.lorentz[2])
	const trimFrom, trimTo = "Trim from, in seconds from the clip's start: ", "Trim to, in seconds from the clip's start: "
	cut := voices.Cut{From: 800 * time.Millisecond, To: 5900 * time.Millisecond}
	deleteThird := "Delete Lorentz/" + l3 + " (3.7 s, unconfirmed)? [y/N]: "
	for _, row := range []struct {
		name, path string
		prepare    func(f *fixture)
		answers    []string
		// asks are the questions asked after the level's own, in order.
		asks string
		// notice is the line above the level the menu then shows, which is
		// the level `list back…` prints, asked with ask.
		notice func(f *fixture) string
		back   string
		ask    string
		// changed: the action ran, so the library is not as it was.
		changed bool
	}{
		{name: "delete answered with Enter", path: "lorentz " + l3, answers: []string{"delete", ""}, asks: deleteThird, back: "lorentz " + l3, ask: clipAsk},
		{name: "delete", path: "lorentz " + l3, answers: []string{"d", "y"}, asks: deleteThird, notice: is("Lorentz/" + l3 + ": deleted"), back: "lorentz", ask: clipsAsk, changed: true},
		{
			name: "delete of a person's last clip", path: "christoph 0", answers: []string{"D", "YES"}, asks: "Delete Christoph/" + c + " (3.2 s, unconfirmed)? [y/N]: ",
			notice: is("Christoph/" + c + ": deleted"), ask: peopleAsk, changed: true,
		},
		{
			name: "trim", path: "lorentz " + l2, answers: []string{"t", "0.8", "5.9", "y"},
			asks: trimFrom + trimTo + "Trim Lorentz/" + l2 + " (6.1 s, confirmed) to the part from 0.8 s to 5.9 s? [y/N]: ",
			notice: func(f *fixture) string {
				return "Lorentz/" + l2 + " trimmed to 5.1 s as " + f.trimmed("lorentz", ex.lorentz[1], cut) + "; the old clip is removed"
			},
			back: "lorentz", ask: clipsAsk, changed: true,
		},
		{name: "trim without a to", path: "lorentz " + l2, answers: []string{"t", "0.8", ""}, asks: trimFrom + trimTo, back: "lorentz " + l2, ask: confirmedAsk},
		{
			name: "trim that fails, on two lines", path: "lorentz " + l2, prepare: func(f *fixture) { f.inspectErr = errors.New("ffmpeg failed\nInvalid data") }, answers: []string{"trim", "2.0", "3.5", "yes"},
			asks:   trimFrom + trimTo + "Trim Lorentz/" + l2 + " (6.1 s, confirmed) to the part from 2.0 s to 3.5 s? [y/N]: ",
			notice: is("ffmpeg failed; Invalid data"), back: "lorentz " + l2, ask: confirmedAsk,
		},
		{
			name: "move to a new person answered n", path: "lorentz " + l3, answers: []string{"m", "Ada", "n"},
			asks: "Move to which person: Move Lorentz/" + l3 + " (3.7 s, unconfirmed) to Ada, a new person? [y/N]: ", back: "lorentz " + l3, ask: clipAsk,
		},
		{
			name: "move that leaves a pruned clip's file", path: "christoph " + c, prepare: func(f *fixture) { f.leave() }, answers: []string{"move", "SYRUS", "y"},
			asks:   "Move to which person: Move Christoph/" + c + " (3.2 s, unconfirmed) to Syrus? [y/N]: ",
			notice: is("Christoph/" + c + " moved to Syrus as " + c + " (unconfirmed); warning: " + newFixture(t).leave()), ask: peopleAsk, changed: true,
		},
		{name: "confirm", path: "lorentz " + l3, answers: []string{"c"}, notice: is("Lorentz/" + l3 + ": confirmed"), back: "lorentz " + l3, ask: confirmedAsk, changed: true},
		{
			name: "play without a player", path: "lorentz " + l1, prepare: func(f *fixture) { f.player.installed = nil }, answers: []string{"p"},
			notice: is("no player for the clip: none of play, ffplay, xdg-open or open is installed; install sox for play, or ffmpeg for ffplay"), back: "lorentz " + l1, ask: confirmedAsk,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			f, _ := exampleFixture(t)
			if row.prepare != nil {
				row.prepare(f)
			}
			before := f.files()
			level := f.ok(append([]string{"list"}, strings.Fields(row.path)...)...)
			draws := f.menu(row.answers, append([]string{"list"}, strings.Fields(row.path)...)...)
			if f.opens != 2 || maps.Equal(before, f.files()) == row.changed {
				t.Fatalf("the menu opened the library %d times, want once; it changed: %v, want %v", f.opens-1, !row.changed, row.changed)
			}
			after := f.ok(append([]string{"list"}, strings.Fields(row.back)...)...) + row.ask + "\n"
			if row.notice != nil {
				after = row.notice(f) + "\n" + after
			}
			ask := confirmedAsk
			if strings.Contains(level, " · unconfirmed\n") {
				ask = clipAsk
			}
			sameDraws(t, draws, []string{level + ask + row.asks, after})
		})
	}
}

func TestMenu_DeletingTheLastClipOfTheLibraryEndsTheMenu(t *testing.T) {
	f := newFixture(t)
	ref := f.enrol("Ada", 4, voices.Confirmed, voices.SourceAdd)
	k := &keyboard{answers: []string{"0", "0", "d", "y", "never asked for"}}
	o := f.on(k, "list")
	if want := "\x1b[8A\r\x1b[JAda/" + string(ref.ID) + ": deleted\n" + emptyLibrary; o.status != 0 || !strings.HasSuffix(o.stdout, want) || k.reads != 4 {
		t.Fatalf("status %d after %d answers; the menu wrote:\n%q\nwant it to end, after 4 answers, with:\n%q", o.status, k.reads, o.stdout, want)
	}
}

func TestMenu_EnterStopsAPlayingClip(t *testing.T) {
	f, ex := exampleFixture(t)
	f.player.hold, f.player.started = true, make(chan struct{}, 1)
	var asking menu.Asking
	k := &keyboard{ctx: menu.WithAsking(context.Background(), &asking), answers: []string{"p", "", "q"}}
	k.before = func(n int) {
		if playing := n == 1; playing {
			<-f.player.started
		}
		// The watch for Enter is no question: an interrupt then stops the run.
		if asking.Waiting() != (n != 1) {
			t.Errorf("at read %d a question waits: %v", n, asking.Waiting())
		}
	}
	view := f.ok("list", "lorentz", "0") + confirmedAsk
	// The erase counts the echoed p, the play line and the Enter that stopped the clip.
	want := "\n" + view + "▶ playing 3.4 s (play) · [Enter] stops\n\x1b[9A\r\x1b[J" + view
	if o := f.on(k, "list", "lorentz", string(ex.lorentz[0])); o != (outcome{stdout: want}) || f.player.stopped != 1 || asking.Waiting() {
		t.Fatalf("the player was stopped %d times, a question still waits: %v; the menu wrote and ended as\n%+v\nwant\n%q", f.player.stopped, asking.Waiting(), o, want)
	}
}

func TestMenu_AClipThatEndsByItselfSwallowsNoAnswer(t *testing.T) {
	f, ex := exampleFixture(t)
	drawn := make(chan struct{}, 8)
	f.deps.Width = func() int {
		drawn <- struct{}{}
		return 200
	}
	k := &keyboard{answers: []string{"p", "o", "q"}}
	k.before = func(n int) {
		if n == 1 {
			// The o is typed once the clip has ended and the view is drawn again.
			<-drawn
			<-drawn
		}
	}
	o := f.on(k, "list", "lorentz", string(ex.lorentz[0]))
	want := "Lorentz/" + string(ex.lorentz[0]) + ": overruled (2 of 2 namings wrong)\n"
	if o.status != 0 || !strings.Contains(o.stdout, "\x1b[J"+want) || len(f.player.calls) != 1 || f.player.stopped != 0 {
		t.Fatalf("status %d, the player ran %d times; the o typed after the clip ended did not overrule:\n%q", o.status, len(f.player.calls), o.stdout)
	}
}

func TestMenu_ACancelAtAQuestionLeavesAsQuit(t *testing.T) {
	_, ex := exampleFixture(t)
	third := string(ex.lorentz[2])
	for _, row := range []struct {
		name  string
		path  []string
		typed []string
	}{
		{"at the people", nil, nil},
		{"at the question of a delete", []string{"lorentz", third}, []string{"d"}},
		{"at a param of a trim", []string{"lorentz", third}, []string{"t", "0.5"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			args := append([]string{"list"}, row.path...)
			quit, _ := exampleFixture(t)
			left := quit.on(&keyboard{answers: append(slices.Clone(row.typed), "q")}, args...)

			f, _ := exampleFixture(t)
			before := f.files()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var asking menu.Asking
			// The y comes after the cancel: nothing may act on it.
			k := &keyboard{ctx: menu.WithAsking(ctx, &asking), answers: append(slices.Clone(row.typed), "y")}
			k.before = func(n int) {
				if !asking.Waiting() {
					t.Errorf("no question waits at read %d", n)
				}
				if n == len(row.typed) {
					cancel()
				}
			}
			o := f.on(k, args...)
			if o != left || o.status != 0 || o.err != nil || o.stderr != "" || asking.Waiting() {
				t.Fatalf("the cancel left\n%+v\nwant what q leaves, in silence with exit 0:\n%+v", o, left)
			}
			if !maps.Equal(before, f.files()) {
				t.Fatal("the y typed after the cancel changed the library")
			}
		})
	}
}
