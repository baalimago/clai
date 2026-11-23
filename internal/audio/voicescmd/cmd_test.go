package voicescmd_test

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voicescmd"
)

const (
	addUsage  = "usage: clai audio v|voices a|add <wav> [-from s] [-to s] [-name n]"
	listUsage = "usage: clai audio v|voices l|list [person [clip [action [args…]]]] [-json]"
)

// usageError fails the test unless o is a usage error with exactly line:
// exit 2, the line on stderr, nothing on stdout.
func usageError(t *testing.T, args []string, o outcome, line string) {
	t.Helper()
	if _, usage := errors.AsType[*voicescmd.UsageError](o.err); !usage || o.status != 2 || o.stdout != "" || o.stderr != line+"\n" {
		t.Fatalf("%v: status %d, err %T, stdout %q, stderr:\n%s\nwant a usage error, exit 2, saying:\n%s", args, o.status, o.err, o.stdout, o.stderr, line)
	}
}

// leaving is a library whose Enrol reports that the pruned clip's file could
// not be deleted.
type leaving struct {
	voicescmd.Library
	left *voices.LibraryWriteError
}

func (l leaving) Enrol(e voices.Enrolment) (voices.Enrolled, error) {
	enrolled, err := l.Library.Enrol(e)
	enrolled.Leftover = l.left
	return enrolled, err
}

// leave makes the fixture's library a leaving one and returns its warning.
func (f *fixture) leave() string {
	left := &voices.LibraryWriteError{Path: "voices/syrus/0123456789ab.flac", Err: fs.ErrPermission}
	f.deps.Library = leaving{Library: f.lib, left: left}
	return left.Error()
}

func TestVoices_WithoutAVerbPrintsTheSynopsis(t *testing.T) {
	f := newFixture(t)
	synopsis := "usage:\n  " + strings.TrimPrefix(addUsage, "usage: ") + "\n  " + strings.TrimPrefix(listUsage, "usage: ") + "\n\n  add     adds a clip of one person's voice"
	for _, words := range [][]string{{"voices"}, {"v"}, {"voices", "rm", "lorentz"}, {"v", "x"}} {
		o := f.run(as{}, words...)
		if !strings.HasPrefix(o.stderr, synopsis) || !strings.HasSuffix(o.stderr, "\n  -json   prints the level the path names as JSON\n") {
			t.Fatalf("%v: stderr is not the synopsis:\n%s", words, o.stderr)
		}
		usageError(t, words, o, strings.TrimSuffix(o.stderr, "\n"))
	}
	if f.opens != 0 {
		t.Fatalf("a usage error opened the library %d times", f.opens)
	}
}

func TestAdd_EnrolsAConfirmedClip(t *testing.T) {
	cut := voices.Cut{From: 61200 * time.Millisecond, To: 69 * time.Second}
	f := newFixture(t)
	for i, row := range []struct {
		args         []string
		person, name string
		file         string
		cut          voices.Cut
	}{
		{[]string{"/home/me/clips/christoph.wav"}, "christoph", "Christoph", "/home/me/clips/christoph.wav", voices.Cut{}},
		{[]string{"/tmp/CHRISTOPH.flac"}, "christoph", "CHRISTOPH", "/tmp/CHRISTOPH.flac", voices.Cut{}},
		{[]string{"meeting.wav", "-from", "61.2", "-to", "69", "-name", "Syrus"}, "syrus", "Syrus", "meeting.wav", cut},
		{[]string{"-name", "Syrus", "-from=61.2", "talk.wav", "-to", "69"}, "syrus", "Syrus", "talk.wav", cut},
		{[]string{"meeting.wav", "-from", "61.2", "-name", "Syrus"}, "syrus", "Syrus", "meeting.wav", voices.Cut{From: cut.From}},
		{[]string{"-name", "Syrus", "--", "-odd.wav"}, "syrus", "Syrus", "-odd.wav", voices.Cut{}},
		// The -name of the run before is not kept.
		{[]string{"/clips/nina-2.wav"}, "nina-2", "Nina-2", "/clips/nina-2.wav", voices.Cut{}},
	} {
		stdout := f.ok(append([]string{"add"}, row.args...)...)
		got := f.inspected[len(f.inspected)-1]
		if got != (inspected{row.file, row.cut}) || f.opens != i+1 {
			t.Fatalf("add %v: Inspect got %+v, want %s cut %v; the library was opened %d times, want %d", row.args, got, row.file, row.cut, f.opens, i+1)
		}
		added := slices.DeleteFunc(f.clips(voices.Slug(row.person)), func(c voices.ClipSummary) bool {
			return stdout != row.name+": clip "+string(c.ID)+" added (confirmed)\n"
		})
		if len(added) != 1 || added[0].Status != voices.Confirmed || added[0].Source != voices.SourceAdd {
			t.Fatalf("add %v printed %q; want the line of a confirmed clip of %s added by hand: %+v", row.args, stdout, row.person, added)
		}
	}
	if lib, _ := f.lib.List(); len(lib) != 3 || lib[0].Name != "Christoph" || len(lib[0].Clips) != 2 {
		t.Fatalf("the library is %+v; want CHRISTOPH.flac added to Christoph, under the stored name", lib)
	}
}

func TestAdd_UsageErrors(t *testing.T) {
	const needsName = "-from and -to cut a clip out of a recording, so the person must be named: add -name <person>"
	f := newFixture(t)
	for _, row := range []struct {
		args []string
		line string
	}{
		{[]string{"add"}, "add needs the audio file to take the clip from, <wav>; " + addUsage},
		{[]string{"add", "a.wav", "b.wav"}, "add takes one audio file, got 2: a.wav, b.wav; " + addUsage},
		{[]string{"add", "a.wav", "-from", "abc", "-name", "X"}, `-from "abc" is not a decimal number of seconds, such as 61.2; ` + addUsage},
		{[]string{"add", "a.wav", "-to", "1m", "-name", "X"}, `-to "1m" is not a decimal number of seconds, such as 61.2; ` + addUsage},
		{[]string{"add", "a.wav", "-from", "5", "-to=2", "-name", "X"}, "-to 2 ends the clip before -from 5 starts it; " + addUsage},
		{[]string{"add", "a.wav", "-to", "0", "-name", "X"}, "-to 0 ends the clip before -from 0 starts it; " + addUsage},
		{[]string{"add", "a.wav", "-loud"}, "-loud is not a flag of this verb; " + addUsage},
		{[]string{"add", "a.wav", "-name"}, "-name needs a value; " + addUsage},
		{[]string{"add", "meeting.wav", "-from", "61.2", "-to", "69"}, needsName},
		{[]string{"add", "meeting.wav", "-from", "61.2"}, needsName},
		{[]string{"add", "meeting.wav", "-to", "69"}, needsName},
		{[]string{"list", "lorentz", "-bogus"}, "-bogus is not a flag of this verb; " + listUsage},
	} {
		usageError(t, row.args, f.voices(row.args...), row.line)
	}
	if f.opens != 0 || len(f.inspected) != 0 || len(f.files()) != 0 {
		t.Fatalf("usage errors opened the library %d times, inspected %v and left %v", f.opens, f.inspected, f.files())
	}
}

func TestAdd_RefusalsExitOneAndChangeNothing(t *testing.T) {
	tooShort := &voices.ClipQualityError{Failed: []voices.QualityCheck{voices.CheckLength}, Seconds: 1.5}
	openFailed := errors.New("the config directory is not readable")
	for _, row := range []struct {
		name    string
		prepare func(f *fixture)
		args    []string
		message string
	}{
		{"a clip the inspection refuses", func(f *fixture) { f.inspectErr = tooShort }, []string{"add", "short.wav", "-name", "Ada"}, "clip too short: 1.5 s, a clip is 3–10 s"},
		{"ffprobe exits 2", func(f *fixture) {
			f.inspectErr = &voices.SourceError{File: "a.wav", Detail: "Invalid data found", Err: exitStatus(2)}
		}, []string{"add", "a.wav"}, "Invalid data found"},
		{"a name the rule refuses", func(*fixture) {}, []string{"add", "/clips/speaker-1.wav"}, `voice name "Speaker-1" breaks the reserved rule`},
		{"the same clip twice", func(f *fixture) { f.ok("add", "ada.wav") }, []string{"add", "ada.wav", "-name", "ada"}, "ada/"},
		{"the opener fails", func(f *fixture) { f.openErr = openFailed }, []string{"add", "ada.wav"}, openFailed.Error()},
		{"the opener fails with a status of its own", func(f *fixture) { f.openErr = exitStatus(7) }, []string{"list"}, "exit status 7"},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := newFixture(t)
			row.prepare(f)
			before := f.files()
			o := f.voices(row.args...)
			if o.status != 1 || o.stdout != "" || o.stderr != o.err.Error()+"\n" || !strings.Contains(o.stderr, row.message) {
				t.Fatalf("status %d, stdout %q, stderr %q; want exit 1 and the refusal holding %q", o.status, o.stdout, o.stderr, row.message)
			}
			if !maps.Equal(before, f.files()) {
				t.Fatalf("the refusal changed the library: %v", f.files())
			}
		})
	}
}

func TestAdd_WarnsWhenAPrunedClipsFileIsLeft(t *testing.T) {
	f := newFixture(t)
	warning := f.leave()
	o := f.voices("add", "meeting.wav", "-name", "Lorentz")
	clips := f.clips("lorentz")
	if len(clips) != 1 || o.status != 0 || o.stdout != "Lorentz: clip "+string(clips[0].ID)+" added (confirmed)\n" || o.stderr != "warn [audio] "+warning+"\n" {
		t.Fatalf("status %d, stdout %q, stderr %q; want the added line and the warning %q", o.status, o.stdout, o.stderr, warning)
	}
}
