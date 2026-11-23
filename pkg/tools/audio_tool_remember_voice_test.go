package tools

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// rememberAudio starts every clip the fixture's Inspect returns. No result
// and no error of the tool may carry it. (F8)
const rememberAudio = "fLaCpcm-samples-of-the-clip"

// rememberFixture is a real library behind a fake Inspect. It is the tool's
// library itself, to record enrolments, leave a file behind or fail an overrule.
type rememberFixture struct {
	t *testing.T
	*voices.Library
	asked       []context.Context
	files       []string // what Inspect was called with
	inspected   []voices.Cut
	enrolled    []voices.Enrolment
	inspectErr  error
	overruleErr error
	leftover    *voices.LibraryWriteError
	held        int
}

func newRememberFixture(t *testing.T) *rememberFixture {
	t.Helper()
	f := &rememberFixture{t: t}
	tick := 0
	f.Library = voices.NewLibrary(t.TempDir(), func() time.Time {
		tick++
		return time.Date(2026, 11, 18, 9, 0, tick, 0, time.UTC)
	})
	setRememberVoiceDeps(t, func(ctx context.Context) (RememberVoiceDeps, error) {
		f.asked = append(f.asked, ctx)
		return RememberVoiceDeps{Inspect: f.inspect, Library: f}, nil
	})
	return f
}

func setRememberVoiceDeps(t *testing.T, deps func(context.Context) (RememberVoiceDeps, error)) {
	t.Helper()
	previous := AudioRememberVoiceDeps
	AudioRememberVoiceDeps = deps
	t.Cleanup(func() { AudioRememberVoiceDeps = previous })
}

func (f *rememberFixture) inspect(_ context.Context, file string, cut voices.Cut) (voices.Inspection, error) {
	f.files, f.inspected = append(f.files, file), append(f.inspected, cut)
	if f.inspectErr != nil {
		return voices.Inspection{}, f.inspectErr
	}
	return voices.Inspection{FLAC: []byte(rememberAudio + file + cut.From.String()), Seconds: (cut.To - cut.From).Seconds()}, nil
}

func (f *rememberFixture) Enrol(e voices.Enrolment) (voices.Enrolled, error) {
	f.enrolled = append(f.enrolled, e)
	enrolled, err := f.Library.Enrol(e)
	if err == nil {
		enrolled.Leftover = f.leftover
	}
	return enrolled, err
}

func (f *rememberFixture) Overrule(ref voices.ClipRef) (voices.ClipSummary, error) {
	if f.overruleErr != nil {
		return voices.ClipSummary{}, f.overruleErr
	}
	return f.Library.Overrule(ref)
}

// call runs the tool on the good call for Ada (meeting.wav, 61.2 to 69),
// changed by changes, and checks that what comes back carries no clip audio.
func (f *rememberFixture) call(changes pub_models.Input) (string, error) {
	f.t.Helper()
	input := pub_models.Input{"file_path": "meeting.wav", "start": 61.2, "end": 69.0, "name": "Ada"}
	maps.Copy(input, changes)
	out, err := AudioRememberVoice.CallWithContext(context.Background(), input)
	text := out + fmt.Sprint(err)
	for _, audio := range []string{rememberAudio, hex.EncodeToString([]byte(rememberAudio)), base64.StdEncoding.EncodeToString([]byte(rememberAudio))} {
		if strings.Contains(text, audio) {
			f.t.Fatalf("F8: %q carries clip audio", text)
		}
	}
	return out, err
}

// added is the result line of the call that enrolled last.
func (f *rememberFixture) added(name string) string {
	id := voices.ClipIDOf(f.enrolled[len(f.enrolled)-1].Inspection.FLAC)
	return name + ": clip " + string(id) + " added (unconfirmed)"
}

// hold puts a clip the tool did not add into the library.
func (f *rememberFixture) hold(name string, status voices.Status) voices.ClipRef {
	f.t.Helper()
	f.held++
	clip := voices.Inspection{FLAC: fmt.Appendf(nil, "fLaC held %d", f.held), Seconds: 5}
	enrolled, err := f.Library.Enrol(voices.Enrolment{Name: name, Inspection: clip, Status: status, Source: voices.SourceAdd})
	if err != nil {
		f.t.Fatalf("hold a clip of %s: %v", name, err)
	}
	return enrolled.Ref
}

// clips is every clip the library holds, by reference.
func (f *rememberFixture) clips() map[voices.ClipRef]voices.ClipSummary {
	f.t.Helper()
	people, err := f.List()
	if err != nil {
		f.t.Fatalf("list the library: %v", err)
	}
	clips := map[voices.ClipRef]voices.ClipSummary{}
	for _, person := range people {
		for _, clip := range person.Clips {
			clips[voices.ClipRef{Slug: person.Slug, ID: clip.ID}] = clip
		}
	}
	return clips
}

func TestAudioRememberVoice_Specification(t *testing.T) {
	spec := AudioRememberVoice.Specification()
	types := map[string]string{}
	for name, property := range spec.Inputs.Properties {
		types[name] = property.Type
	}
	// F5: these are all the arguments; none removes, replaces or confirms a clip.
	want := map[string]string{"file_path": "string", "start": "number", "end": "number", "name": "string", "overrules": "string"}
	required := []string{"file_path", "start", "end", "name"}
	if spec.Name != "audio_remember_voice" || spec.Inputs.Type != "object" || !maps.Equal(types, want) || !slices.Equal(spec.Inputs.Required, required) {
		t.Fatalf("name %q, properties %v, required %v; want audio_remember_voice with exactly %v", spec.Name, types, spec.Inputs.Required, want)
	}
	if !strings.Contains(spec.Description, "'<name>: clip <id> added (unconfirmed)'") {
		t.Fatalf("the description does not give the result line: %q", spec.Description)
	}
}

func TestRememberVoice_RefusesBeforeAnythingIsAskedFor(t *testing.T) {
	type refused = RememberVoiceArgumentError
	problems := map[ArgumentProblem]string{ArgumentMissing: "missing", ArgumentType: "of the wrong type", ArgumentRange: "out of range"}
	mustBe := map[string]string{
		"file_path": "a non-empty string, the path of the audio file the clip is cut from",
		"start":     "a finite number of seconds, 0 or more",
		"end":       "a finite number of seconds after start",
		"name":      "a non-empty string, the person's name",
		"overrules": "a string, the clip reference '<slug>/<id>' of a segment's voice.clip",
	}
	for _, tc := range []struct {
		name    string
		changes pub_models.Input
		want    error
	}{
		{"file_path empty", pub_models.Input{"file_path": ""}, &refused{"file_path", ArgumentMissing}},
		{"file_path a number", pub_models.Input{"file_path": 7.0}, &refused{"file_path", ArgumentType}},
		{"start null", pub_models.Input{"start": nil}, &refused{"start", ArgumentMissing}},
		{"start empty", pub_models.Input{"start": ""}, &refused{"start", ArgumentMissing}},
		{"start a string", pub_models.Input{"start": "61.2"}, &refused{"start", ArgumentType}},
		{"start negative", pub_models.Input{"start": -1.0}, &refused{"start", ArgumentRange}},
		{"start not a number", pub_models.Input{"start": math.NaN()}, &refused{"start", ArgumentRange}},
		{"start infinite", pub_models.Input{"start": math.Inf(1)}, &refused{"start", ArgumentRange}},
		{"end empty", pub_models.Input{"end": ""}, &refused{"end", ArgumentMissing}},
		{"end a bool", pub_models.Input{"end": true}, &refused{"end", ArgumentType}},
		{"end at start", pub_models.Input{"start": 8, "end": int64(8)}, &refused{"end", ArgumentRange}},
		{"end before start", pub_models.Input{"start": 9.0, "end": 4.0}, &refused{"end", ArgumentRange}},
		{"end zero is not the end of the file", pub_models.Input{"start": 5.0, "end": 0.0}, &refused{"end", ArgumentRange}},
		{"end within a nanosecond of start", pub_models.Input{"start": 0.0, "end": 1e-12}, &refused{"end", ArgumentRange}},
		{"end infinite", pub_models.Input{"end": math.Inf(1)}, &refused{"end", ArgumentRange}},
		{"end not a number", pub_models.Input{"end": math.NaN()}, &refused{"end", ArgumentRange}},
		{"name null", pub_models.Input{"name": nil}, &refused{"name", ArgumentMissing}},
		{"name empty", pub_models.Input{"name": ""}, &refused{"name", ArgumentMissing}},
		{"name a number", pub_models.Input{"name": 3.0}, &refused{"name", ArgumentType}},
		{"overrules a number", pub_models.Input{"overrules": 3.0}, &refused{"overrules", ArgumentType}},
		{"overrules not a clip reference", pub_models.Input{"overrules": "lorentz"}, &voices.ClipRefError{Ref: "lorentz"}},
		{"overrules with an upper-case id", pub_models.Input{"overrules": "lorentz/9F3C"}, &voices.ClipRefError{Ref: "lorentz/9F3C"}},
		{"name a transcript label", pub_models.Input{"name": "speaker-2"}, &voices.VoiceNameError{Name: "speaker-2", Rule: voices.NameReserved}},
		{"name starting with a digit", pub_models.Input{"name": "2pac"}, &voices.VoiceNameError{Name: "2pac", Rule: voices.NameShape}},
		// Several wrong: the first in this order is the one refused.
		{"file_path before start", pub_models.Input{"file_path": nil, "start": "1", "end": nil, "name": nil}, &refused{"file_path", ArgumentMissing}},
		{"start before end", pub_models.Input{"start": "1", "end": nil, "name": nil, "overrules": 3.0}, &refused{"start", ArgumentType}},
		{"end before name", pub_models.Input{"start": -1.0, "end": nil, "name": nil, "overrules": 3.0}, &refused{"end", ArgumentMissing}},
		{"name before overrules", pub_models.Input{"start": -1.0, "name": 3.0, "overrules": 3.0}, &refused{"name", ArgumentType}},
		{"the type of overrules before the range", pub_models.Input{"start": -1.0, "name": "2pac", "overrules": 3.0}, &refused{"overrules", ArgumentType}},
		{"the range before the clip reference", pub_models.Input{"start": -1.0, "name": "2pac", "overrules": "x"}, &refused{"start", ArgumentRange}},
		{"the clip reference before the name rule", pub_models.Input{"name": "2pac", "overrules": "x"}, &voices.ClipRefError{Ref: "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRememberFixture(t)
			out, err := f.call(tc.changes)
			if out != "" || !reflect.DeepEqual(err, tc.want) {
				t.Fatalf("got %q, %T %v; want no result and %T %v", out, err, err, tc.want, tc.want)
			}
			if arg, ok := tc.want.(*refused); ok {
				want := "audio_remember_voice: argument " + strconv.Quote(arg.Argument) + " is " + problems[arg.Problem] + ": it must be " + mustBe[arg.Argument]
				if err.Error() != want {
					t.Fatalf("message %q, want %q", err, want)
				}
			}
			if len(f.asked) != 0 || len(f.inspected) != 0 || len(f.clips()) != 0 {
				t.Fatalf("asked for the dependencies %d times, inspected %v, library %v; want nothing touched", len(f.asked), f.inspected, f.clips())
			}
		})
	}
}

func TestRememberVoice_UnavailableWithoutDependencies(t *testing.T) {
	f := newRememberFixture(t)
	setRememberVoiceDeps(t, nil)
	out, err := f.call(nil)
	if want := "audio_remember_voice is not available in this runtime: no voice library is wired in"; out != "" ||
		!reflect.DeepEqual(err, &RememberVoiceUnavailableError{}) || err.Error() != want {
		t.Fatalf("nothing injected: got %q, %T %v; want %q", out, err, err, want)
	}
	failure := errors.New("no voice library configured")
	setRememberVoiceDeps(t, func(context.Context) (RememberVoiceDeps, error) { return RememberVoiceDeps{}, failure })
	out, err = f.call(nil)
	if want := "audio_remember_voice is not available in this runtime: no voice library configured"; out != "" ||
		!reflect.DeepEqual(err, &RememberVoiceUnavailableError{Err: failure}) || !errors.Is(err, failure) || err.Error() != want {
		t.Fatalf("the injected function fails: got %q, %T %v; want %q", out, err, err, want)
	}
	if len(f.inspected) != 0 || len(f.clips()) != 0 {
		t.Fatalf("inspected %v, library %v; want nothing touched", f.inspected, f.clips())
	}
}

func TestRememberVoice_EnrolsOneUnconfirmedClip(t *testing.T) {
	f := newRememberFixture(t)
	good := pub_models.Input{"file_path": "meeting.wav", "start": 61.2, "end": 69.0, "name": "Christoph"}
	out, err := AudioRememberVoice.Call(good)
	if err != nil || out != f.added("Christoph") || len(f.asked) != 1 || f.asked[0] != context.Background() {
		t.Fatalf("Call = %q, %v on the contexts %v; want %q on context.Background()", out, err, f.asked, f.added("Christoph"))
	}
	if len(f.inspected) != 1 || f.files[0] != "meeting.wav" {
		t.Fatalf("inspected %v, want meeting.wav once", f.files)
	}
	cut := f.inspected[0]
	if (cut.From-61200*time.Millisecond).Abs() > time.Microsecond || (cut.To-69*time.Second).Abs() > time.Microsecond {
		t.Fatalf("inspected the cut %v, want 61.2 s to 69 s within a microsecond", cut)
	}
	want := voices.Enrolment{Name: "Christoph", Inspection: f.enrolled[0].Inspection, Status: voices.Unconfirmed, Source: voices.SourceRemember}
	if len(f.enrolled) != 1 || !reflect.DeepEqual(f.enrolled[0], want) || !strings.HasPrefix(string(want.Inspection.FLAC), rememberAudio) {
		t.Fatalf("enrolled %+v; want once, the inspected clip, unconfirmed, by audio_remember_voice, replacing nothing", f.enrolled)
	}
	// Whole numbers are numbers, a null overrules is none, another case is the same person.
	out, err = f.call(pub_models.Input{"name": "CHRISTOPH", "start": 20, "end": int64(25), "overrules": nil})
	if err != nil || out != f.added("CHRISTOPH") {
		t.Fatalf("got %q, %v; want %q", out, err, f.added("CHRISTOPH"))
	}
	if f.inspected[1] != (voices.Cut{From: 20 * time.Second, To: 25 * time.Second}) {
		t.Fatalf("inspected the cut %v, want 20 s to 25 s", f.inspected[1])
	}
	clips := f.clips()
	for _, enrolled := range f.enrolled {
		clip := clips[voices.ClipRef{Slug: "christoph", ID: voices.ClipIDOf(enrolled.Inspection.FLAC)}]
		if clip.Status != voices.Unconfirmed || clip.Source != voices.SourceRemember || clip.Seconds != enrolled.Inspection.Seconds {
			t.Fatalf("the library lists %+v, want the clip unconfirmed, by audio_remember_voice", clip)
		}
	}
	if len(clips) != 2 || len(f.asked) != 2 {
		t.Fatalf("the library holds %d clips and the dependencies were asked for %d times; want 2 and once per call", len(clips), len(f.asked))
	}
}

func TestRememberVoice_ResultLines(t *testing.T) {
	const unheld = "lorentz/ffffffffffff"
	leftover := &voices.LibraryWriteError{Path: "/voices/ada/0123456789ab.flac", Err: errors.New("device busy,\r\ntry again")}
	const warning = "\nwarning: write voice library /voices/ada/0123456789ab.flac: device busy, try again"
	for _, tc := range []struct {
		name        string
		person      string
		overrules   string // "held" is the clip of Lorentz the fixture holds; empty is none
		overruleErr error
		leftover    *voices.LibraryWriteError
		wantAfter   string
		wantRuled   int
	}{
		{name: "a clip added", person: "Christoph"},
		{name: "an overrule recorded says nothing more", person: "Jean-Luc P.", overrules: "held", wantRuled: 1},
		{name: "an overrule of a clip not held", person: "O’Brien", overrules: unheld, wantAfter: "\noverrule of " + unheld + " failed: the library holds no clip " + unheld},
		{name: "a pruned clip's file left behind", person: "Ada", leftover: leftover, wantAfter: warning},
		{name: "an overrule recorded and a file left behind", person: "Ada", overrules: "held", leftover: leftover, wantAfter: warning, wantRuled: 1},
		{
			name: "a failed overrule, then the file left behind", person: "Ada", overrules: "held", leftover: leftover,
			overruleErr: errors.New("events.jsonl:\nread-only\rfile"),
			wantAfter:   "\noverrule of lorentz/%s failed: events.jsonl: read-only file" + warning,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRememberFixture(t)
			held := f.hold("Lorentz", voices.Confirmed)
			f.overruleErr, f.leftover = tc.overruleErr, tc.leftover
			out, err := f.call(pub_models.Input{"name": tc.person, "overrules": strings.Replace(tc.overrules, "held", held.String(), 1)})
			want := f.added(tc.person) + strings.Replace(tc.wantAfter, "%s", string(held.ID), 1)
			if err != nil || out != want {
				t.Fatalf("got %q, %v; want %q", out, err, want)
			}
			clips := f.clips()
			if got := clips[held]; got.Overruled != tc.wantRuled || got.Status != voices.Confirmed {
				t.Fatalf("the held clip is %+v, want %d overrules and still confirmed", got, tc.wantRuled)
			}
			if len(clips) != 2 {
				t.Fatalf("the library holds %d clips, want the held one and the new one", len(clips))
			}
		})
	}
}

func TestRememberVoice_ErrorsComeBackUnchanged(t *testing.T) {
	sameCut := func(f *rememberFixture) { _, _ = f.call(nil) }
	// F5: a person whose clips are all confirmed keeps every one of them.
	full := func(f *rememberFixture) {
		for range 6 {
			f.hold("Ada", voices.Confirmed)
		}
	}
	for _, tc := range []struct {
		name    string
		end     float64
		arrange func(f *rememberFixture)
		want    error // an inspection error is the fake's own; of an enrol error the type is
	}{
		{"a clip the inspection refuses", 62.7, nil, &voices.ClipQualityError{Failed: []voices.QualityCheck{voices.CheckLength}, Seconds: 1.5}},
		{"an end past any file is the inspection's to refuse", 1e300, nil, &voices.CutError{}},
		{"a cancelled inspection", 69, nil, context.Canceled},
		{"the same cut twice", 69, sameCut, &voices.ClipExistsError{}},
		{"a person at the cap, all confirmed", 69, full, &voices.VoiceFullError{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRememberFixture(t)
			held := f.hold("Lorentz", voices.Confirmed)
			if tc.arrange != nil {
				tc.arrange(f)
			} else {
				f.inspectErr = tc.want
			}
			before, inspected := f.clips(), len(f.inspected)
			out, err := f.call(pub_models.Input{"end": tc.end, "overrules": held.String()})
			if out != "" || reflect.TypeOf(err) != reflect.TypeOf(tc.want) || (f.inspectErr != nil && err != f.inspectErr) {
				t.Fatalf("got %q, %T %v; want no result and %T unchanged", out, err, err, tc.want)
			}
			if after := f.clips(); !reflect.DeepEqual(after, before) || after[held].Overruled != 0 {
				t.Fatalf("the library is %v, was %v; want nothing enrolled, removed or overruled", after, before)
			}
			if len(f.inspected) != inspected+1 || (tc.end == 1e300 && f.inspected[0].To != math.MaxInt64) {
				t.Fatalf("inspected %v; want once more, an end past any Duration held at the longest", f.inspected)
			}
		})
	}
}

// F5: the tool removes nothing of its own. A full person loses the clip any
// enrolment prunes, their lowest-ranked unconfirmed one, and no other.
func TestRememberVoice_PrunesLikeAnyOtherEnrolment(t *testing.T) {
	f := newRememberFixture(t)
	oldest := f.hold("Lorentz", voices.Unconfirmed)
	kept := []voices.ClipRef{f.hold("Lorentz", voices.Unconfirmed)}
	for range 4 {
		kept = append(kept, f.hold("Lorentz", voices.Confirmed))
	}
	if _, err := f.call(pub_models.Input{"name": "Lorentz"}); err != nil {
		t.Fatalf("the call: %v", err)
	}
	clips := f.clips()
	kept = append(kept, voices.ClipRef{Slug: "lorentz", ID: voices.ClipIDOf(f.enrolled[0].Inspection.FLAC)})
	if gone := slices.DeleteFunc(kept, func(ref voices.ClipRef) bool { _, holds := clips[ref]; return holds }); len(gone) != 0 {
		t.Fatalf("%v are gone", gone)
	}
	if _, holds := clips[oldest]; holds || len(clips) != 6 {
		t.Fatalf("the library holds %d clips, the lowest-ranked unconfirmed one among them: %t; want 6 without it", len(clips), holds)
	}
}
