package voicescmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/menu"
)

// target is the clip an action works on, as the level showed it.
type target struct {
	lib   []voices.VoiceSummary
	voice voices.VoiceSummary
	clip  voices.ClipSummary
}

func (t target) ref() voices.ClipRef { return voices.ClipRef{Slug: t.voice.Slug, ID: t.clip.ID} }

// String is how a line names the clip: "<Name>/<id>".
func (t target) String() string { return string(t.voice.Name) + "/" + string(t.clip.ID) }

func (t target) describe() string { return seconds(t.clip.Seconds) + ", " + string(t.clip.Status) }

// action is one thing to do with a clip. Named by an argument it runs at
// once; picked in the menu it first asks for its params and, when it
// changes the clip, whether to go on.
type action struct {
	key, name, label string
	// params names the arguments, and asks is the menu's question for each.
	params, asks []string
	// confirm words the menu's question before an action that removes,
	// moves or cuts the clip.
	confirm func(t target, args []string) string
	// leavesClip: the clip is gone afterwards; the menu shows its person.
	leavesClip bool
	run        func(ctx context.Context, b *browser, t target, args []string) (line string, err error)
}

var actions = []action{
	{key: "p", name: "play", label: "[p]lay", run: play},
	{key: "c", name: "confirm", label: "[c]onfirm", run: confirm},
	{key: "o", name: "overrule", label: "[o]verrule", run: overrule},
	{
		key: "t", name: "trim", label: "[t]rim", leavesClip: true, run: trim,
		params: []string{"from", "to"},
		asks:   []string{"Trim from, in seconds from the clip's start", "Trim to, in seconds from the clip's start"},
		confirm: func(t target, args []string) string {
			return fmt.Sprintf("Trim %s (%s) to the part from %s s to %s s?", t, t.describe(), args[0], args[1])
		},
	},
	{
		key: "m", name: "move", label: "[m]ove to another person", leavesClip: true, run: move,
		params: []string{"person"},
		asks:   []string{"Move to which person"},
		confirm: func(t target, args []string) string {
			name, known := t.destination(args[0])
			if !known {
				name += ", a new person"
			}
			return fmt.Sprintf("Move %s (%s) to %s?", t, t.describe(), name)
		},
	},
	{
		key: "d", name: "delete", label: "[d]elete", leavesClip: true, run: deleteClip,
		confirm: func(t target, _ []string) string { return fmt.Sprintf("Delete %s (%s)?", t, t.describe()) },
	},
}

// offeredFor: the menu does not offer to confirm a confirmed clip; an
// argument still may.
func (a action) offeredFor(c voices.ClipSummary) bool {
	return a.name != "confirm" || c.Status == voices.Unconfirmed
}

func actionNamed(arg string) *action {
	for i := range actions {
		if strings.EqualFold(actions[i].name, arg) {
			return &actions[i]
		}
	}
	return nil
}

// actionPicked is the action a menu answer, lower-cased, picks for the clip.
func actionPicked(key string, c voices.ClipSummary) *action {
	for i, a := range actions {
		if (a.key == key || a.name == key) && a.offeredFor(c) {
			return &actions[i]
		}
	}
	return nil
}

// runArgument runs the action a path names, at once: nothing is asked.
func (b *browser) runArgument(ctx context.Context, lib []voices.VoiceSummary, at place, rest []string) error {
	a, args := actionNamed(rest[0]), rest[1:]
	if len(args) != len(a.params) {
		var usage strings.Builder
		usage.WriteString(a.name)
		for _, p := range a.params {
			usage.WriteString(" <" + p + ">")
		}
		takes := map[int]string{0: "no arguments", 1: "1 argument", 2: "2 arguments"}[len(a.params)]
		return &UsageError{Line: fmt.Sprintf("usage: %s (%s, got %d)", usage.String(), takes, len(args))}
	}
	person, clip, _ := find(lib, at)
	line, err := a.run(ctx, b, target{lib: lib, voice: *person, clip: *clip}, args)
	if err != nil || line == "" {
		return err
	}
	if _, err := io.WriteString(b.out, oneLine(line)+"\n"); err != nil {
		return &menu.OutputError{Err: err}
	}
	return nil
}

// errNotRun says a picked action was dropped: a param was left empty, or
// the question whether to go on was not answered yes.
var errNotRun = errors.New("the action was not run")

// ask gathers what an action picked in the menu needs before it runs: its
// params, and a yes when it changes the clip.
func (b *browser) ask(ctx context.Context, a action, t target) ([]string, error) {
	args := make([]string, 0, len(a.params))
	for _, question := range a.asks {
		answer, err := b.screen.Ask(ctx, question+": ")
		if err != nil {
			return nil, err
		}
		if answer == "" {
			return nil, errNotRun
		}
		args = append(args, answer)
	}
	if a.confirm != nil {
		answer, err := b.screen.Ask(ctx, a.confirm(t, args)+" [y/N]: ")
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
			return nil, errNotRun
		}
	}
	return args, nil
}

func confirm(_ context.Context, b *browser, t target, _ []string) (string, error) {
	confirmation, err := b.deps.Library.Confirm(t.ref())
	if err != nil {
		return "", err
	}
	if confirmation == voices.AlreadyConfirmed {
		return t.String() + ": already confirmed", nil
	}
	return t.String() + ": confirmed", nil
}

func overrule(_ context.Context, b *browser, t target, _ []string) (string, error) {
	c, err := b.deps.Library.Overrule(t.ref())
	switch {
	case err != nil:
		return "", err
	case c.Overruled <= c.Named:
		return fmt.Sprintf("%s: overruled (%d of %d namings wrong)", t, c.Overruled, c.Named), nil
	case c.Named == 0:
		return fmt.Sprintf("%s: overruled %s, never named", t, count(c.Overruled, "time")), nil
	}
	return fmt.Sprintf("%s: overruled %s, named %s", t, count(c.Overruled, "time"), count(c.Named, "time")), nil
}

func deleteClip(_ context.Context, b *browser, t target, _ []string) (string, error) {
	if err := b.deps.Library.Remove(t.ref()); err != nil {
		return "", err
	}
	return t.String() + ": deleted", nil
}

func trim(ctx context.Context, b *browser, t target, args []string) (string, error) {
	usage := func(why string) error {
		return &UsageError{Line: fmt.Sprintf("trim %s: %s; usage: trim <from> <to>, in decimal seconds from the clip's start",
			strings.Join(args, " "), why)}
	}
	var cut voices.Cut
	var err error
	if cut.From, err = parseSeconds(args[0]); err != nil {
		return "", usage(fmt.Sprintf("%q is not a decimal number of seconds", args[0]))
	}
	if cut.To, err = parseSeconds(args[1]); err != nil {
		return "", usage(fmt.Sprintf("%q is not a decimal number of seconds", args[1]))
	}
	if cut.To <= cut.From {
		return "", usage("the clip would end before it starts")
	}
	enrolled, length, err := b.replace(ctx, t, voices.Enrolment{Name: string(t.voice.Name), Replaces: t.clip.ID}, cut)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s trimmed to %s as %s; the old clip is removed", t, seconds(length), enrolled.Ref.ID) + leftover(enrolled), nil
}

// destination is the person a move to arg goes to: a person the library
// holds, under their stored name, or else a new person named arg.
func (t target) destination(arg string) (name string, known bool) {
	if v := findPerson(t.lib, arg); v != nil {
		return string(v.Name), true
	}
	if parsed, err := voices.ParseName(arg); err == nil {
		for _, v := range t.lib {
			if v.Slug == voices.SlugOf(parsed) {
				return string(v.Name), true
			}
		}
	}
	return arg, false
}

func move(ctx context.Context, b *browser, t target, args []string) (string, error) {
	name, known := t.destination(args[0])
	enrolled, _, err := b.replace(ctx, t, voices.Enrolment{Name: name}, voices.Cut{})
	if err != nil {
		return "", err
	}
	if !known {
		name += ", a new person,"
	}
	return fmt.Sprintf("%s moved to %s as %s (%s)", t, name, enrolled.Ref.ID, t.clip.Status) + leftover(enrolled), nil
}

// ReplaceError says a trim or a move enrolled New and could not remove Old:
// both are in the library.
type ReplaceError struct {
	New voices.ClipRef
	Old voices.ClipRef
	Err error
}

func (e *ReplaceError) Error() string {
	return fmt.Sprintf("added %s, but the old clip %s could not be removed, so both are in the library: %v", e.New, e.Old, e.Err)
}
func (e *ReplaceError) Unwrap() error { return e.Err }

// replace enrols the cut of the target's clip as into says, with the
// clip's status and source, and removes the clip: a clip file is never
// rewritten. It returns the new clip and its length.
func (b *browser) replace(ctx context.Context, t target, into voices.Enrolment, cut voices.Cut) (voices.Enrolled, float64, error) {
	inspection, err := b.deps.Inspect(ctx, t.clip.Path, cut)
	if err != nil {
		return voices.Enrolled{}, 0, err
	}
	into.Inspection, into.Status, into.Source = inspection, t.clip.Status, t.clip.Source
	enrolled, err := b.deps.Library.Enrol(into)
	if err != nil {
		return voices.Enrolled{}, 0, err
	}
	if err := b.deps.Library.Remove(t.ref()); err != nil {
		return voices.Enrolled{}, 0, &ReplaceError{New: enrolled.Ref, Old: t.ref(), Err: err}
	}
	return enrolled, inspection.Seconds, nil
}

func leftover(enrolled voices.Enrolled) string {
	if enrolled.Leftover == nil {
		return ""
	}
	return "; warning: " + strings.Join(strings.FieldsFunc(enrolled.Leftover.Error(), func(r rune) bool { return r == '\n' || r == '\r' }), " ")
}

// players are the programs that play a file given as their last argument,
// in the order they are looked for.
var players = [][]string{{"play"}, {"ffplay", "-nodisp", "-autoexit"}, {"xdg-open"}, {"open"}}

// NoPlayerError says the machine has none of the players.
type NoPlayerError struct{ Tried []string }

func (e *NoPlayerError) Error() string {
	last := len(e.Tried) - 1
	return fmt.Sprintf("no player for the clip: none of %s or %s is installed; install sox for play, or ffmpeg for ffplay",
		strings.Join(e.Tried[:last], ", "), e.Tried[last])
}

// PlayError says the player failed. Detail is its last message, never
// audio.
type PlayError struct {
	Player string
	Detail string
	Err    error
}

func (e *PlayError) Error() string {
	return fmt.Sprintf("the player %s failed: %s", e.Player, e.Detail)
}
func (e *PlayError) Unwrap() error { return e.Err }

// play plays the clip to its end, or, when stdin is a terminal, until a
// line is entered.
func play(ctx context.Context, b *browser, t target, _ []string) (string, error) {
	var player, tried []string
	for _, p := range players {
		tried = append(tried, p[0])
		if _, err := b.deps.Runner.LookPath(p[0]); err == nil {
			player = p
			break
		}
	}
	if player == nil {
		return "", &NoPlayerError{Tried: tried}
	}
	line := "▶ playing " + seconds(t.clip.Seconds) + " (" + player[0] + ")"
	if b.enterStops {
		line += " · [Enter] stops"
	}
	if _, err := io.WriteString(b.out, line+"\n"); err != nil {
		return "", diag.Errorf("voices play: write the play line: %w", err)
	}
	playing, stop := context.WithCancel(ctx)
	defer stop()
	type played struct {
		stderr string
		err    error
	}
	done := make(chan played, 1)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_, stderr, err := b.deps.Runner.Run(playing, player[0], slices.Concat(player[1:], []string{t.clip.Path})...)
		done <- played{stderr, err}
	}()
	if b.enterStops {
		if typed, entered := b.in.line(ended); entered {
			stop()
			<-done
			if b.screen != nil {
				b.screen.Echoed(typed)
			}
			return "", nil
		}
	}
	result := <-done
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if result.err != nil {
		return "", &PlayError{Player: player[0], Detail: lastLine(result.stderr, result.err), Err: result.err}
	}
	return "", nil
}

// lastLine is a program's last stderr line, printable text only and cut
// short, so a Detail carries words and never audio.
func lastLine(stderr string, err error) string {
	all := strings.TrimSpace(stderr)
	var kept []rune
	for _, r := range strings.ToValidUTF8(all[strings.LastIndexByte(all, '\n')+1:], "") {
		if unicode.IsPrint(r) && len(kept) < 240 {
			kept = append(kept, r)
		}
	}
	if line := strings.TrimSpace(string(kept)); line != "" {
		return line
	}
	return err.Error()
}

// sharedInput is the run's stdin, shared by the menu's questions and the
// watch for Enter while a clip plays. A read the watch started and the
// clip's end overtook stays for the menu's next answer, so a clip that ends
// by itself swallows no line.
type sharedInput struct {
	src     io.Reader
	mu      sync.Mutex
	buf     []byte
	err     error
	landing chan struct{}
}

func newSharedInput(src io.Reader) *sharedInput {
	if src == nil {
		src = strings.NewReader("")
	}
	return &sharedInput{src: src}
}

func (in *sharedInput) Read(p []byte) (int, error) {
	for {
		in.mu.Lock()
		if len(in.buf) > 0 || in.err != nil {
			n := copy(p, in.buf)
			in.buf = in.buf[n:]
			err := in.err
			in.mu.Unlock()
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		landing := in.fetch()
		in.mu.Unlock()
		<-landing
	}
}

// line waits for a whole line and takes it, or returns false when stop
// closes or the input ends first; a line not yet ended stays.
func (in *sharedInput) line(stop <-chan struct{}) (string, bool) {
	for {
		in.mu.Lock()
		if i := bytes.IndexByte(in.buf, '\n'); i >= 0 {
			line := string(in.buf[:i])
			in.buf = in.buf[i+1:]
			in.mu.Unlock()
			return strings.TrimSuffix(line, "\r"), true
		}
		if in.err != nil {
			in.mu.Unlock()
			return "", false
		}
		landing := in.fetch()
		in.mu.Unlock()
		select {
		case <-landing:
		case <-stop:
			return "", false
		}
	}
}

// fetch starts one read of src unless one is under way; mu is held.
func (in *sharedInput) fetch() <-chan struct{} {
	if in.landing == nil {
		landing := make(chan struct{})
		in.landing = landing
		go func() {
			p := make([]byte, 512)
			n, err := in.src.Read(p)
			in.mu.Lock()
			in.buf = append(in.buf, p[:n]...)
			if err != nil {
				in.err = err
			}
			in.landing = nil
			in.mu.Unlock()
			close(landing)
		}()
	}
	return in.landing
}
