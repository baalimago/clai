package voicescmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voicescmd"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func now() time.Time { return time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC) }

// today is how a table shows the day of the fixture's clock.
var today = now().Local().Format(time.DateOnly)

// fixture is a real library in a temporary directory and the voices command
// over it, with a scripted Inspect and player. Every run dispatches the same
// command tree, as the runs of clai's own tests do.
type fixture struct {
	t    *testing.T
	dir  string
	lib  *voices.Library
	deps voicescmd.Deps
	tree *internal.Command
	// openErr fails the opener, and inspectErr every Inspect.
	openErr, inspectErr error
	opens               int
	inspected           []inspected
	player              *player
	// stderr is the stderr of the run under way, where the reporter writes
	// the run's warnings as plain lines, as the root composes it in text.
	stderr    bytes.Buffer
	reporter  diagnostic.Reporter
	presenter diag.Presenter
	levels    diag.ResolvedLevels
}

type inspected struct {
	file string
	cut  voices.Cut
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return fixtureAt(t, t.TempDir())
}

func fixtureAt(t *testing.T, dir string) *fixture {
	f := &fixture{t: t, dir: dir, player: &player{installed: []string{"play", "ffplay", "xdg-open", "open"}}}
	f.lib = voices.NewLibrary(f.dir, now)
	f.deps = voicescmd.Deps{Library: f.lib, Inspect: f.inspect, Runner: f.player, Width: func() int { return 200 }}
	f.tree = voicescmd.Command(func(context.Context) (voicescmd.Deps, error) {
		f.opens++
		return f.deps, f.openErr
	})
	f.presenter = diag.NewTextPresenter(&f.stderr, diag.Plain, diagnostic.LevelInfo)
	f.levels = diag.ResolvedLevels{"": diagnostic.LevelInfo}
	for _, ty := range runenv.Types() {
		f.levels[ty.Subsystem] = diagnostic.LevelInfo
	}
	var err error
	f.reporter, err = diag.NewReporter(runenv.Types(), f.levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
		diagnostic.Immediate: f.presenter, diagnostic.Buffered: f.presenter,
	})
	if err != nil {
		t.Fatalf("build the reporter: %v", err)
	}
	return f
}

// inspect answers as the Inspector does without measuring anything: a
// stored clip taken whole keeps its bytes, so its id; anything else is a new
// clip, as long as its cut, or 5 s.
func (f *fixture) inspect(_ context.Context, file string, cut voices.Cut) (voices.Inspection, error) {
	f.inspected = append(f.inspected, inspected{file, cut})
	if f.inspectErr != nil {
		return voices.Inspection{}, f.inspectErr
	}
	in := voices.Inspection{FLAC: fmt.Appendf(nil, "fLaC %s %v", file, cut), Seconds: 5, SpeechShare: 1, MeanDBFS: -20}
	if stored, err := os.ReadFile(file); err == nil && cut == (voices.Cut{}) {
		in.FLAC = stored
	}
	if cut.To != 0 {
		in.Seconds = (cut.To - cut.From).Seconds()
	}
	return in, nil
}

// idOf is the id of the clip enrol adds for name with seconds.
func idOf(name string, seconds float64) voices.ClipID {
	return voices.ClipIDOf(fmt.Appendf(nil, "fLaC %s %v", name, seconds))
}

// enrol adds a clip of seconds for name, as the agent's tool or `voices add`
// would, without the CLI.
func (f *fixture) enrol(name string, seconds float64, status voices.Status, source voices.Source) voices.ClipRef {
	f.t.Helper()
	in := voices.Inspection{FLAC: fmt.Appendf(nil, "fLaC %s %v", name, seconds), Seconds: seconds, SpeechShare: 1, MeanDBFS: -20}
	enrolled, err := f.lib.Enrol(voices.Enrolment{Name: name, Inspection: in, Status: status, Source: source})
	if err != nil {
		f.t.Fatalf("enrol for %s: %v", name, err)
	}
	return enrolled.Ref
}

// example names the clips of the library of the README's "The voices menu,
// by example", each person's best first.
type example struct {
	christoph voices.ClipID
	lorentz   [3]voices.ClipID
	syrus     [2]voices.ClipID
}

// exampleFixture is a fixture over a copy of the README's example library:
// Christoph with one unconfirmed clip; Lorentz's first clip added by hand (2
// namings, 1 wrong), his second learned and confirmed (3 namings, 2 wrong),
// his third learned; Syrus with two clips added by hand, the first named
// once. The binary's first call builds the library, under its own TMPDIR.
func exampleFixture(t *testing.T) (*fixture, example) {
	t.Helper()
	ex := example{
		christoph: idOf("Christoph", 3.2),
		lorentz:   [3]voices.ClipID{idOf("Lorentz", 3.4), idOf("Lorentz", 6.1), idOf("Lorentz", 3.7)},
		syrus:     [2]voices.ClipID{idOf("Syrus", 3.8), idOf("Syrus", 3.3)},
	}
	template := filepath.Join(os.TempDir(), "voicescmd-example")
	if _, err := os.Stat(template); err != nil {
		b := fixtureAt(t, template)
		b.enrol("Christoph", 3.2, voices.Unconfirmed, voices.SourceRemember)
		l1 := b.enrol("Lorentz", 3.4, voices.Confirmed, voices.SourceAdd)
		l2 := b.enrol("Lorentz", 6.1, voices.Unconfirmed, voices.SourceRemember)
		b.enrol("Lorentz", 3.7, voices.Unconfirmed, voices.SourceRemember)
		s1 := b.enrol("Syrus", 3.8, voices.Confirmed, voices.SourceAdd)
		b.enrol("Syrus", 3.3, voices.Confirmed, voices.SourceAdd)
		named := func(ref voices.ClipRef) voices.ClipOutcome {
			return voices.ClipOutcome{Clip: ref, Landed: true, Named: []string{"speaker-1"}}
		}
		_, err := b.lib.Confirm(l2)
		err = errors.Join(err, b.lib.Tally([]voices.ClipOutcome{named(l1), named(l1), named(l2), named(l2), named(l2), named(s1)}))
		for _, ref := range []voices.ClipRef{l1, l2, l2} {
			_, overruled := b.lib.Overrule(ref)
			err = errors.Join(err, overruled)
		}
		if l := b.clips("lorentz"); err != nil || l[0].ID != ex.lorentz[0] || l[1].ID != ex.lorentz[1] || b.clips("syrus")[0].ID != ex.syrus[0] {
			t.Fatalf("build the example library: %v; Lorentz's clips rank %+v", err, l)
		}
	}
	f := newFixture(t)
	if err := os.CopyFS(f.dir, os.DirFS(template)); err != nil {
		t.Fatalf("copy the example library: %v", err)
	}
	return f, ex
}

// clips are a person's listed clips, best first.
func (f *fixture) clips(slug voices.Slug) []voices.ClipSummary {
	f.t.Helper()
	lib, err := f.lib.List()
	if err != nil {
		f.t.Fatalf("list: %v", err)
	}
	for _, v := range lib {
		if v.Slug == slug {
			return v.Clips
		}
	}
	return nil
}

// files is every file under the library's directory with its bytes.
func (f *fixture) files() map[string]string {
	f.t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(f.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		files[path] = string(data)
		return err
	})
	if err != nil {
		f.t.Fatalf("read the library: %v", err)
	}
	return files
}

// as is how one command is run: neither stream a terminal unless said, and
// a stdin that fails the test when it is read.
type as struct {
	ctx                 context.Context
	stdin               io.Reader
	stdinTTY, stdoutTTY bool
	globalJSON          bool
}

type outcome struct {
	stdout, stderr string
	status         int
	err            error
}

// run dispatches `clai audio words…` as the root does: the failure's
// message goes to stderr and the status is the run's exit status.
func (f *fixture) run(r as, words ...string) outcome {
	var stdout bytes.Buffer
	f.stderr.Reset()
	format := jsonl.FormatText
	if r.globalJSON {
		format = jsonl.FormatJSON
	}
	if r.stdin == nil {
		r.stdin = unread{f.t}
	}
	if r.ctx == nil {
		r.ctx = context.Background()
	}
	streams := stdio.New(stdio.Config{
		Format: format, Stdin: r.stdin, Stdout: &stdout, Stderr: &f.stderr,
		StdinTerminal: r.stdinTTY, StdoutTerminal: r.stdoutTTY,
	})
	ctx, exit := internal.WithExitStatus(r.ctx)
	ctx = runenv.With(ctx, runenv.New(runenv.Config{Streams: streams, Reporter: f.reporter, Presenter: f.presenter, Levels: f.levels, Format: format}))
	audio := &internal.Command{Name: "audio", Subs: map[string]cmd.Command{voicescmd.Key: f.tree}}
	status, err := cmd.RunSilentTo(ctx, append([]string{"clai", "audio"}, words...), map[string]cmd.Command{"audio": audio}, io.Discard)
	if err != nil {
		fmt.Fprintln(&f.stderr, err.Error())
	}
	return outcome{stdout: stdout.String(), stderr: f.stderr.String(), status: exit.Resolve(status), err: err}
}

// voices runs `clai audio voices args…` without a terminal.
func (f *fixture) voices(args ...string) outcome {
	f.t.Helper()
	return f.run(as{}, append([]string{"voices"}, args...)...)
}

// ok runs as voices does and fails the test unless the command succeeded in
// silence on stderr; it returns stdout.
func (f *fixture) ok(args ...string) string {
	f.t.Helper()
	o := f.voices(args...)
	if o.status != 0 || o.err != nil || o.stderr != "" {
		f.t.Fatalf("%v: status %d, err %v, stderr %q; stdout:\n%s", args, o.status, o.err, o.stderr, o.stdout)
	}
	return o.stdout
}

// menu runs `clai audio voices args…` on a terminal whose user gives
// answers and then ends the input, and returns what the menu drew, one text
// per draw, once it was left with exit status 0.
func (f *fixture) menu(answers []string, args ...string) []string {
	f.t.Helper()
	o := f.on(&keyboard{answers: answers}, args...)
	if o.status != 0 || o.err != nil || o.stderr != "" {
		f.t.Fatalf("%v answered %q: status %d, err %v, stderr %q, stdout:\n%s", args, answers, o.status, o.err, o.stderr, o.stdout)
	}
	return erase.Split(strings.TrimPrefix(o.stdout, "\n"), -1)
}

// on runs `clai audio voices args…` on a terminal with the keyboard k.
func (f *fixture) on(k *keyboard, args ...string) outcome {
	return f.run(as{ctx: k.ctx, stdin: k, stdinTTY: true, stdoutTTY: true}, append([]string{"voices"}, args...)...)
}

// erase is what starts every draw of a menu but its first.
var erase = regexp.MustCompile(`\x1b\[\d+A\r\x1b\[J`)

// keyboard is the stdin of a menu: it gives one answer per read, so an
// answer is typed when the command asks for it, and then ends.
type keyboard struct {
	// ctx is the run's ctx; nil is the background.
	ctx     context.Context
	answers []string
	// before runs as the n-th answer is asked for, before it is given.
	before func(n int)
	reads  int
}

func (k *keyboard) Read(p []byte) (int, error) {
	n := k.reads
	k.reads++
	if k.before != nil {
		k.before(n)
	}
	if n >= len(k.answers) {
		return 0, io.EOF
	}
	return copy(p, k.answers[n]+"\n"), nil
}

type unread struct{ t *testing.T }

func (u unread) Read([]byte) (int, error) {
	u.t.Error("the command read stdin")
	return 0, io.EOF
}

// player is the Runner that finds and runs the players.
type player struct {
	installed []string
	// stderr and err are how every player ends.
	stderr string
	err    error
	// hold makes a player play until it is stopped.
	hold    bool
	started chan struct{}
	stopped int
	calls   [][]string
}

func (p *player) LookPath(name string) (string, error) {
	if slices.Contains(p.installed, name) {
		return "/usr/bin/" + name, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func (p *player) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	p.calls = append(p.calls, append([]string{name}, args...))
	if !p.hold {
		return "", p.stderr, p.err
	}
	p.started <- struct{}{}
	<-ctx.Done()
	p.stopped++
	return "", "Killed\n", errors.New("signal: killed")
}

// exitStatus is a program's exit as *exec.ExitError reports it: an error
// with an exit code of its own.
type exitStatus int

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(int(e)) }
func (e exitStatus) ExitCode() int { return int(e) }
