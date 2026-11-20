package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voicescmd"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	pkgtools "github.com/baalimago/clai/pkg/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// knownRun is one config directory with its voice library, and a recording
// whose transcript is speaker A for 0–8 s and B for 9–15 s.
type knownRun struct {
	t          *testing.T
	confDir    string
	recording  string
	library    *voices.Library
	transcript []Segment
	// fail fails the recording's transcription.
	fail error
}

func newKnownRun(t *testing.T) *knownRun {
	t.Helper()
	dir := t.TempDir()
	f := &knownRun{t: t, confDir: filepath.Join(dir, "clai"), recording: filepath.Join(dir, "meeting.wav")}
	at := time.Date(2020, 11, 20, 9, 0, 0, 0, time.UTC)
	f.library = voices.NewLibrary(f.confDir, func() time.Time { return at })
	f.write(f.recording, "RIFF recording")
	f.transcript = []Segment{
		{Start: 0, End: 8 * time.Second, Speaker: "A", Text: "hello there"},
		{Start: 9 * time.Second, End: 15 * time.Second, Speaker: "B", Text: "hi"},
	}
	return f
}

func (f *knownRun) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// enrol adds a clip of fixed bytes, as the voices CLI would without
// inspecting anything.
func (f *knownRun) enrol(name string, seconds float64, status voices.Status) {
	f.t.Helper()
	inspection := voices.Inspection{FLAC: fmt.Appendf(nil, "fLaC %s %.1f", name, seconds), Seconds: seconds, SpeechShare: 1, MeanDBFS: -20}
	if _, err := f.library.Enrol(voices.Enrolment{Name: name, Inspection: inspection, Status: status, Source: voices.SourceAdd}); err != nil {
		f.t.Fatalf("enrol %s: %v", name, err)
	}
}

// usual is Lorentz with two confirmed clips and Syrus with an unconfirmed
// one.
func (f *knownRun) usual() *knownRun {
	f.enrol("Lorentz", 8.4, voices.Confirmed)
	f.enrol("Lorentz", 6.1, voices.Confirmed)
	f.enrol("Syrus", 7.0, voices.Unconfirmed)
	return f
}

// config writes audioConfig.json: the model, json output, and max-voices
// unless maxVoices is empty.
func (f *knownRun) config(model, maxVoices string) {
	body := `{"transcribe": {"model": "` + model + `", "output-format": "json"`
	if maxVoices != "" {
		body += `, "max-voices": ` + maxVoices
	}
	f.write(filepath.Join(f.confDir, "audioConfig.json"), body+"}}")
}

// tree is every path under voices/ with its size and time.
func (f *knownRun) tree() string {
	var b strings.Builder
	_ = filepath.WalkDir(filepath.Join(f.confDir, voices.Dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		fmt.Fprintf(&b, "%s %d %v %v\n", path, info.Size(), info.ModTime(), err)
		return nil
	})
	return b.String()
}

// placed counts the placed events of a person.
func (f *knownRun) placed(slug string) int {
	events, _ := os.ReadFile(filepath.Join(f.confDir, voices.Dir, slug, "events.jsonl"))
	return strings.Count(string(events), `"event":"placed"`)
}

// transcriber answers the recording with the transcript, and every other
// file, a naming request over sel's picks, as spec says in both orders.
func (f *knownRun) transcriber(sel voices.Selection, spec string) (*fakeTranscriber, *namingRun) {
	naming := newNamingRun(f.t).both(spec)
	naming.selection = sel
	return &fakeTranscriber{transcribe: func(ctx context.Context, path string) ([]Segment, error) {
		if path != f.recording {
			return naming.transcriber.Transcribe(ctx, path)
		}
		return slices.Clone(f.transcript), f.fail
	}}, naming
}

// query runs the transcription as the commands build it, in a run whose
// warnings are plain lines on stderr.
func (f *knownRun) query(known knownVoices, format OutputFormat, tr Transcriber, runner CommandRunner) (stdout, stderr string, err error) {
	splitter := NewSplitter(tr, runner)
	splitter.StatusOut = io.Discard
	var out, errOut bytes.Buffer
	env := procenv.WithStreams(strings.NewReader(""), &out, &errOut, diagnostic.LevelInfo)
	q := &TranscribeQuerier{Splitter: splitter, FilePath: f.recording, Format: format, known: known}
	err = q.Query(runenv.With(context.Background(), env))
	return out.String(), errOut.String(), err
}

// engine is one audio_transcribe call in a run with -voices flagVoices.
func (f *knownRun) engine(call pkgtools.AudioTranscribeCall, flagVoices ...string) (result, stderr string, err error) {
	f.t.Helper()
	f.t.Setenv("CLAI_CONFIG_DIR", f.confDir)
	f.t.Cleanup(ResetTranscribeOverrides)
	if err := SetTranscribeOverrides(ToolOverrides{Voices: flagVoices}); err != nil {
		f.t.Fatal(err)
	}
	call.FilePath = f.recording
	stderr = testboil.CaptureStderr(f.t, func(*testing.T) {
		result, err = transcribeEngine(procenv.Context(context.Background()), call)
	})
	return result, stderr, err
}

// knownDoc is the json output of a named run.
type knownDoc struct {
	Segments []struct {
		Speaker string
		Voice   *struct{ Clip, Status string }
		Text    string
	}
	Naming struct {
		Status, Origin, Error, Unrecorded string
		Voices                            []VoiceNaming
	}
}

func knownParse(t *testing.T, out string) knownDoc {
	t.Helper()
	var doc knownDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not {segments, naming}: %v\n%s", err, out)
	}
	return doc
}

func (d knownDoc) voices() string { return namingVoices(NamingReport{Voices: d.Naming.Voices}) }

func (d knownDoc) speakers() string {
	var out []string
	for _, s := range d.Segments {
		out = append(out, s.Speaker)
	}
	return strings.Join(out, ",")
}

func knownFlags(t *testing.T, args ...string) *Flags {
	t.Helper()
	f := &Flags{}
	fs := flag.NewFlagSet("transcribe", flag.ContinueOnError)
	f.Register(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return f
}

// F3: with nobody to name the run is what it was before there were voices.
func TestTranscribe_EmptySelectionChangesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		library   func(f *knownRun)
		maxVoices int
	}{
		"no library":                          {func(*knownRun) {}, 12},
		"only an unconfirmed clip":            {func(f *knownRun) { f.enrol("Lorentz", 5, voices.Unconfirmed) }, 12},
		"max-voices 0":                        {func(f *knownRun) { f.usual() }, 0},
		"an unreadable library, nobody named": {func(f *knownRun) { f.write(filepath.Join(f.confDir, voices.Dir), "not a directory") }, 12},
	} {
		t.Run(name, func(t *testing.T) {
			f := newKnownRun(t)
			tc.library(f)
			before := f.tree()
			runner := &namingRunner{missing: true}
			known, err := resolveKnownVoices(runner, f.library, flagAsked(&Flags{}, TranscribeConfig{MaxVoices: tc.maxVoices}))
			if err != nil || !known.selection.Empty() {
				t.Fatalf("selection %+v, err %v; want the empty selection", known.selection, err)
			}
			known.warn = true
			tr, _ := f.transcriber(known.selection, "")
			for _, format := range []OutputFormat{FormatVTT, FormatSRT, FormatText, FormatJSON} {
				want, _ := Render(f.transcript, format)
				if stdout, stderr, err := f.query(known, format, tr, runner); err != nil || stdout != want || stderr != "" {
					t.Fatalf("%s: stdout %q, stderr %q, err %v; want the bytes of a run without voices: %q", format, stdout, stderr, err, want)
				}
			}
			if tr.callCount() != 4 || runner.looks != 0 || len(runner.ran()) != 0 {
				t.Fatalf("%d requests for 4 runs, %d lookups, ran %v; want one request each and no ffmpeg", tr.callCount(), runner.looks, runner.ran())
			}
			if after := f.tree(); after != before {
				t.Fatalf("voices/ changed:\n%s\nwas\n%s", after, before)
			}
		})
	}
}

// F6: the named segments carry their clip, and the library tallies how
// every picked clip fared.
func TestTranscribe_NamesKnownVoices(t *testing.T) {
	f := newKnownRun(t).usual()
	known, err := resolveKnownVoices(&namingRunner{}, f.library, flagAsked(knownFlags(t, "-voices", "Lorentz, syrus"), Default.Transcribe))
	if err != nil {
		t.Fatal(err)
	}
	known.warn = true
	tr, _ := f.transcriber(known.selection, "L1=X L2=X S1=Y cA=X cB=Y")
	stdout, stderr, err := f.query(known, FormatJSON, tr, &namingRunner{})
	if err != nil || stderr != "" {
		t.Fatalf("err %v, stderr %q; want a named run that warns of nothing", err, stderr)
	}
	doc := knownParse(t, stdout)
	lorentz, syrus := known.selection.Voices[0].Picks[0].Ref, known.selection.Voices[1].Picks[0].Ref
	if doc.Naming.Status != "named" || doc.Naming.Origin != "flag" || doc.speakers() != "Lorentz,Syrus" || doc.voices() != "Lorentz:named Syrus:named" {
		t.Fatalf("naming %+v, speakers %s", doc.Naming, doc.speakers())
	}
	for i, want := range []string{lorentz.String() + " confirmed hello there", syrus.String() + " unconfirmed hi"} {
		if s := doc.Segments[i]; s.Voice == nil || s.Voice.Clip+" "+s.Voice.Status+" "+s.Text != want {
			t.Fatalf("segment %d = %+v (voice %+v), want %s", i, s, s.Voice, want)
		}
	}
	if len(tr.calls) != 3 || tr.calls[0] != f.recording {
		t.Fatalf("requests %v, want the recording and then two naming requests", tr.calls)
	}
	listed, err := f.library.List()
	if err != nil || len(listed) != 2 {
		t.Fatalf("list: %+v, %v", listed, err)
	}
	for _, v := range listed {
		for _, c := range v.Clips {
			if source := c.ID == lorentz.ID || c.ID == syrus.ID; (c.Named == 1) != source {
				t.Fatalf("%s clip %s named %d times; want the two source clips named once", v.Name, c.ID, c.Named)
			}
		}
	}
	if l, s := f.placed("lorentz"), f.placed("syrus"); l != 2 || s != 1 {
		t.Fatalf("placed events: lorentz %d, syrus %d; want one per picked clip", l, s)
	}
	tr, _ = f.transcriber(known.selection, "L1=X L2=X S1=Y cA=X cB=Y")
	if text, _, err := f.query(known, FormatText, tr, &namingRunner{}); err != nil || text != "Lorentz: hello there\nSyrus (unconfirmed): hi\n" {
		t.Fatalf("text = %q, %v", text, err)
	}
}

func TestRenderNaming(t *testing.T) {
	lorentz := &Provenance{Clip: voices.ClipRef{Slug: "lorentz", ID: "9f3c2a1b0d4e"}, Status: voices.Confirmed}
	syrus := &Provenance{Clip: voices.ClipRef{Slug: "syrus", ID: "3d81f60a2b97"}, Status: voices.Unconfirmed}
	n := Naming{
		Segments: []NamedSegment{
			{Start: 0, End: 1500 * time.Millisecond, Speaker: "Lorentz", Text: "hello", Voice: lorentz},
			{Start: 1500 * time.Millisecond, End: 3 * time.Second, Speaker: "Syrus", Text: "hi", Voice: syrus},
			{Start: 3 * time.Second, End: 4 * time.Second, Speaker: "B", Text: "bye"},
		},
		Report: NamingReport{
			Status: NamingNamed, Origin: voices.OriginFlag,
			Speakers: []SpeakerNaming{{Label: "A", Name: "Lorentz", Voice: lorentz}, {Label: "B", Reason: ReasonNoMatch}},
			Voices:   []VoiceNaming{{Name: "Lorentz", Result: VoiceNamed}},
		},
	}
	for format, want := range map[OutputFormat]string{
		FormatText: "Lorentz: hello\nSyrus (unconfirmed): hi\nB: bye\n",
		FormatSRT:  "1\n00:00:00,000 --> 00:00:01,500\nLorentz: hello\n\n2\n00:00:01,500 --> 00:00:03,000\nSyrus (unconfirmed): hi\n\n3\n00:00:03,000 --> 00:00:04,000\nB: bye\n",
		FormatVTT:  "WEBVTT\n\n00:00:00.000 --> 00:00:01.500\n<v Lorentz>hello</v>\n\n00:00:01.500 --> 00:00:03.000\n<v Syrus (unconfirmed)>hi</v>\n\n00:00:03.000 --> 00:00:04.000\n<v B>bye</v>\n",
	} {
		if got, err := renderNaming(n, format); err != nil || got != want {
			t.Fatalf("%s (err %v):\n%s\nwant:\n%s", format, err, got, want)
		}
	}
	got, err := renderNaming(n, FormatJSON)
	if err != nil || !strings.HasPrefix(got, "{\n  \"segments\": [\n    {\n      \"start\": 0,\n") || !strings.HasSuffix(got, "\n    ]\n  }\n}\n") {
		t.Fatalf("json is not indented with one final newline (err %v):\n%s", err, got)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(got)); err != nil {
		t.Fatal(err)
	}
	want := `{"segments":[` +
		`{"start":0,"end":1.5,"speaker":"Lorentz","voice":{"clip":"lorentz/9f3c2a1b0d4e","status":"confirmed"},"text":"hello"},` +
		`{"start":1.5,"end":3,"speaker":"Syrus","voice":{"clip":"syrus/3d81f60a2b97","status":"unconfirmed"},"text":"hi"},` +
		`{"start":3,"end":4,"speaker":"B","text":"bye"}],` +
		`"naming":{"status":"named","origin":"flag","speakers":[` +
		`{"label":"A","name":"Lorentz","voice":{"clip":"lorentz/9f3c2a1b0d4e","status":"confirmed"}},{"label":"B","reason":"no-match"}],` +
		`"voices":[{"name":"Lorentz","result":"named"}]}}`
	if compact.String() != want {
		t.Fatalf("json:\n%s\nwant:\n%s", compact.String(), want)
	}
}

// F4: only the transcription's own failure fails the run. What the naming
// did not do is in the report, and on stderr for the command alone.
func TestTranscribe_NamingNeverFailsTheRun(t *testing.T) {
	const unmatched = "warn [audio] failed to find a match for %s, were they in the meeting?\n"
	for _, tc := range []struct {
		name     string
		voices   string
		spec     string
		prepare  func(naming *namingRun, known *knownVoices)
		status   NamingStatus
		speakers string
		tallied  bool
		// unrecorded is the report's, the tally's failure.
		unrecorded string
		// warnings are the stderr of the command; a %s in them is the
		// report's error.
		warnings string
	}{
		{
			name: "a naming request fails", voices: "Lorentz,Syrus", spec: "L1=X cA=X",
			prepare: func(naming *namingRun, _ *knownVoices) { naming.fail[0] = errors.New("429\n  rate limited") },
			status:  NamingRequestFailed, speakers: "A,B",
			warnings: "warn [audio] naming failed: naming request (forward order) failed to send: 429 rate limited; speakers left as labels\n",
		},
		{
			name: "the orders agree on nobody", voices: "Lorentz,Syrus", spec: "L1=X L2=X S1=Y cA=Z cB=W",
			status: NamingNoAgreement, speakers: "A,B", tallied: true,
			warnings: fmt.Sprintf(unmatched+unmatched, "Lorentz", "Syrus"),
		},
		{
			name: "voices is a regular file", voices: "Lorentz",
			status: NamingLibraryUnreadable, speakers: "A,B",
			warnings: "warn [audio] voice library unreadable: %s; speakers left as labels\n",
		},
		{
			name: "a person the library does not hold", voices: "Ada",
			status: NamingNoVoice, speakers: "A,B",
			warnings: "warn [audio] no voice \"Ada\" in the library\n",
		},
		{
			name: "the tally fails", voices: "Lorentz,Syrus", spec: "L1=X L2=X S1=Y cA=X cB=Y",
			prepare: func(_ *namingRun, known *knownVoices) {
				known.tally = func([]voices.ClipOutcome) error { return errors.New("disk full") }
			},
			status: NamingNamed, speakers: "Lorentz,Syrus", unrecorded: "disk full",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newKnownRun(t)
			if tc.status == NamingLibraryUnreadable {
				f.write(filepath.Join(f.confDir, voices.Dir), "not a directory")
			} else {
				f.usual()
			}
			known, err := resolveKnownVoices(&namingRunner{}, f.library, voices.Asked{Flag: internal.CommaList(tc.voices)})
			if err != nil {
				t.Fatal(err)
			}
			tr, naming := f.transcriber(known.selection, tc.spec)
			if tc.prepare != nil {
				tc.prepare(naming, &known)
			}
			known.warn = true
			stdout, stderr, err := f.query(known, FormatJSON, tr, &namingRunner{})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			doc := knownParse(t, stdout)
			if doc.Naming.Status != string(tc.status) || doc.speakers() != tc.speakers {
				t.Fatalf("status %q, speakers %s; want %q, %s", doc.Naming.Status, doc.speakers(), tc.status, tc.speakers)
			}
			if got := f.placed("lorentz") > 0; got != tc.tallied || doc.Naming.Unrecorded != tc.unrecorded {
				t.Fatalf("tallied = %v, unrecorded %q; a naming is tallied exactly when both orders were answered", got, doc.Naming.Unrecorded)
			}
			warnings := tc.warnings
			if strings.Contains(warnings, "%s") {
				warnings = fmt.Sprintf(warnings, doc.Naming.Error)
			}
			if stderr != warnings {
				t.Fatalf("stderr:\n%s\nwant:\n%s", stderr, warnings)
			}
		})
	}
	t.Run("a failed transcription fails as it does without voices", func(t *testing.T) {
		f := newKnownRun(t).usual()
		f.fail = errors.New("vendor exploded")
		var texts []string
		for _, known := range []knownVoices{{selection: f.library.Select(voices.Asked{Flag: []string{"Lorentz"}}), tally: f.library.Tally}, {}} {
			tr, _ := f.transcriber(known.selection, "")
			stdout, _, err := f.query(known, FormatJSON, tr, &namingRunner{})
			if !errors.Is(err, f.fail) || stdout != "" || tr.callCount() != 1 {
				t.Fatalf("err %v, stdout %q, %d requests; want the transcription's error alone", err, stdout, tr.callCount())
			}
			texts = append(texts, err.Error())
		}
		if texts[0] != texts[1] {
			t.Fatalf("errors differ: %q", texts)
		}
	})
}

func TestNamingWarnings(t *testing.T) {
	people := []VoiceNaming{{"Lorentz", VoiceNamed}, {"Ada", VoiceUnknown}, {"Syrus", VoiceAbsent}, {"Bo", VoiceUnknown}, {"Christoph", VoiceNotPlaced}}
	ada, bo := `no voice "Ada" in the library`, `no voice "Bo" in the library`
	failure := errors.New("429\n\trate limited")
	for _, tc := range []struct {
		name   string
		report NamingReport
		want   []string
	}{
		{"named: the unknown, then the unmatched", NamingReport{Status: NamingNamed, Voices: people}, []string{
			ada, bo, "failed to find a match for Syrus, were they in the meeting?", "failed to find a match for Christoph, were they in the meeting?",
		}},
		{"no agreement", NamingReport{Status: NamingNoAgreement, Voices: people[2:3]}, []string{"failed to find a match for Syrus, were they in the meeting?"}},
		{"a failed request", NamingReport{Status: NamingRequestFailed, Voices: people, Err: failure}, []string{ada, bo, "naming failed: 429 rate limited; speakers left as labels"}},
		{"a failed request without a reason", NamingReport{Status: NamingRequestFailed}, []string{"naming failed: no reason was given; speakers left as labels"}},
		{"an unreadable library", NamingReport{Status: NamingLibraryUnreadable, Err: failure}, []string{"voice library unreadable: 429 rate limited; speakers left as labels"}},
		{"no labels", NamingReport{Status: NamingNotDiarized, Voices: people[1:2]}, []string{ada, "the transcript has no speaker labels; nobody named"}},
		{"nobody talks long enough", NamingReport{Status: NamingNoCandidate}, []string{"no speaker talks long enough to compare; nobody named"}},
		{"only people the library lacks", NamingReport{Status: NamingNoVoice, Voices: people[1:2]}, []string{ada}},
		{"not asked", NamingReport{Status: NamingNotAsked}, nil},
	} {
		for _, origin := range []voices.Origin{voices.OriginFlag, voices.OriginTool, voices.OriginLibrary} {
			tc.report.Origin = origin
			want := tc.want
			if origin == voices.OriginLibrary {
				// The library's own selection warns only of a failed request.
				want = nil
				if tc.report.Status == NamingRequestFailed {
					want = tc.want[len(tc.want)-1:]
				}
			}
			if got := namingWarnings(tc.report); !slices.Equal(got, want) {
				t.Fatalf("%s, by %s:\ngot  %q\nwant %q", tc.name, origin, got, want)
			}
		}
	}
}

// F4: ffmpeg is what naming people needs, so its absence stops the run
// before any request, and only when people are named.
func TestTranscribe_NamedPeopleNeedFFmpeg(t *testing.T) {
	const byFlag = "-voices needs ffmpeg to name people, and ffmpeg is not on PATH: install ffmpeg, or transcribe without -voices"
	const byTool = "voices needs ffmpeg to name people, and ffmpeg is not on PATH: install ffmpeg, or transcribe without voices"
	check := func(t *testing.T, err error, origin voices.Origin, text string) {
		t.Helper()
		stop, ok := errors.AsType[*VoicesNeedFFmpegError](err)
		missing, wraps := errors.AsType[*voices.MissingToolError](err)
		if !ok || !wraps || stop.Origin != origin || missing.Tool != "ffmpeg" || err.Error() != text {
			t.Fatalf("err = %v\nwant a *VoicesNeedFFmpegError by %s: %s", err, origin, text)
		}
	}
	f := newKnownRun(t).usual()
	f.config("test-diarize", "")
	before := f.tree()
	for _, tc := range []struct {
		asked  voices.Asked
		origin voices.Origin
		text   string
	}{
		{voices.Asked{Flag: []string{"Lorentz"}, MaxVoices: 12}, voices.OriginFlag, byFlag},
		{voices.Asked{Flag: []string{"Ada"}}, voices.OriginFlag, byFlag},
		{voices.Asked{Tool: []string{"Lorentz"}, Flag: []string{"Syrus"}}, voices.OriginTool, byTool},
	} {
		_, err := resolveKnownVoices(&namingRunner{missing: true}, f.library, tc.asked)
		check(t, err, tc.origin, tc.text)
	}

	t.Setenv("PATH", t.TempDir())
	_, err := setupTranscribeQuerier(procenv.Context(context.Background()), f.confDir, knownFlags(t, "-voices", "Lorentz"), []string{f.recording})
	check(t, err, voices.OriginFlag, byFlag)
	result, _, err := f.engine(pkgtools.AudioTranscribeCall{Voices: []string{"Lorentz"}})
	check(t, err, voices.OriginTool, byTool)
	flagged, _, err := f.engine(pkgtools.AudioTranscribeCall{}, "Lorentz")
	check(t, err, voices.OriginFlag, byFlag)
	if result != "" || flagged != "" || f.tree() != before {
		t.Fatalf("results %q and %q, tree\n%s\nwant no result and voices/ as it was\n%s", result, flagged, f.tree(), before)
	}

	t.Run("with nobody named the run goes on and its naming fails", func(t *testing.T) {
		runner := &namingRunner{missing: true}
		known, err := resolveKnownVoices(runner, f.library, flagAsked(knownFlags(t, "-voices", ""), Default.Transcribe))
		if err != nil || known.selection.Origin != voices.OriginLibrary {
			t.Fatalf("selection %+v, err %v; want the library's own", known.selection, err)
		}
		known.warn = true
		tr, _ := f.transcriber(known.selection, "")
		stdout, stderr, err := f.query(known, FormatJSON, tr, runner)
		doc := knownParse(t, stdout)
		if err != nil || doc.Naming.Status != "request-failed" || doc.speakers() != "A,B" || tr.callCount() != 1 {
			t.Fatalf("err %v, naming %+v, speakers %s, %d requests", err, doc.Naming, doc.speakers(), tr.callCount())
		}
		if want := "warn [audio] naming failed: ffmpeg is not installed; inspecting a clip needs ffmpeg and ffprobe; speakers left as labels\n"; stderr != want {
			t.Fatalf("stderr %q, want %q", stderr, want)
		}
	})
}

// F11: the tool call's people, else the run's -voices, else everyone with a
// confirmed clip up to transcribe.max-voices.
func TestTranscribe_SelectionPrecedence(t *testing.T) {
	f := newKnownRun(t)
	for _, name := range []string{"Lorentz", "Syrus", "Christoph", "Ada"} {
		f.enrol(name, 5, voices.Confirmed)
	}
	f.enrol("Bea", 5, voices.Unconfirmed)
	testenv.FakeBins(t, ffmpegBin)
	named := func(maxVoices string, tool []string, flagVoices ...string) knownDoc {
		t.Helper()
		f.config("test", maxVoices)
		got, stderr, err := f.engine(pkgtools.AudioTranscribeCall{OutputFormat: "json", Voices: tool}, flagVoices...)
		if err != nil {
			t.Fatalf("engine: %v (stderr %q)", err, stderr)
		}
		return knownParse(t, got)
	}
	everyone := named("12", []string{})
	if people := strings.Fields(everyone.voices()); everyone.Naming.Origin != "library" || len(people) != 4 || slices.Contains(people, "Bea:not-compared") {
		t.Fatalf("nobody named: by %q, %v; want the library's four with a confirmed clip", everyone.Naming.Origin, people)
	}
	firstTwo := strings.Join(strings.Fields(everyone.voices())[:2], " ")
	for _, tc := range []struct {
		name      string
		maxVoices string
		tool      []string
		flag      []string
		origin    string
		voices    string
	}{
		{"the tool's people outrank -voices", "12", []string{"Syrus", "Bo"}, []string{"Lorentz"}, "tool", "Syrus:not-compared Bo:unknown-voice"},
		{"-voices answers a call that names nobody", "12", nil, []string{"Lorentz"}, "flag", "Lorentz:not-compared"},
		{"max-voices caps the library's own", "2", nil, nil, "library", firstTwo},
		{"max-voices is 12 when the file has none", "", nil, nil, "library", everyone.voices()},
		{"max-voices 0 never caps the people named", "0", nil, []string{"Lorentz", "Bea"}, "flag", "Lorentz:not-compared Bea:not-compared"},
	} {
		if doc := named(tc.maxVoices, tc.tool, tc.flag...); doc.Naming.Origin != tc.origin || doc.voices() != tc.voices || doc.Naming.Status != "not-diarized" {
			t.Fatalf("%s: %s by %q (%s), want %s by %q", tc.name, doc.voices(), doc.Naming.Origin, doc.Naming.Status, tc.voices, tc.origin)
		}
	}
	f.config("test", "0")
	bare := newKnownRun(t)
	bare.config("test", "")
	plain, _, _ := bare.engine(pkgtools.AudioTranscribeCall{OutputFormat: "json"})
	if got, _, err := f.engine(pkgtools.AudioTranscribeCall{OutputFormat: "json"}); err != nil || got != plain || !strings.HasPrefix(got, "[") {
		t.Fatalf("max-voices 0 with nobody named: %v\n%s\nwant the output of a run without voices\n%s", err, got, plain)
	}

	f.config("test", "-1")
	const negative = "transcribe.max-voices must not be negative, got: -1; 0 names nobody unless -voices or the tool call names people"
	_, _, engineErr := f.engine(pkgtools.AudioTranscribeCall{})
	_, loadErr := loadTranscribeConfig(procenv.Context(context.Background()), f.confDir, &Flags{})
	for _, err := range []error{engineErr, loadErr} {
		if refused, ok := errors.AsType[*MaxVoicesError](err); !ok || refused.Value != -1 || err.Error() != negative {
			t.Fatalf("err = %v, want a *MaxVoicesError: %s", err, negative)
		}
	}

	if got := flagAsked(knownFlags(t, "-voices", "Lorentz, Christoph ,,syrus"), Default.Transcribe); !slices.Equal(got.Flag, []string{"Lorentz", "Christoph", "syrus"}) || got.MaxVoices != 12 {
		t.Fatalf("-voices reads as %+v", got)
	}
}

// The command and the tool as the runtime builds them, over the mock
// transcriber's script and an ffmpeg on PATH that writes its last argument.
func TestTranscribe_CommandWarnsAndToolDoesNot(t *testing.T) {
	f := newKnownRun(t).usual()
	f.config("test-diarize", "")
	bin := t.TempDir()
	f.write(filepath.Join(bin, "ffmpeg"), "#!/bin/sh\nfor a; do last=$a; done\nprintf 'fLaC' > \"$last\"\n")
	if err := os.Chmod(filepath.Join(bin, "ffmpeg"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	people := []string{"Lorentz", "Syrus"}
	const spec = "L1=X L2=X S1=Y cA=X cB=W"
	naming := &namingRun{selection: f.library.Select(voices.Asked{Flag: people})}
	picks, _ := naming.picks()
	candidates := []namingRegion{{"cA", 8 * time.Second}, {"cB", 6 * time.Second}}
	reversed := slices.Clone(picks)
	slices.Reverse(reversed)
	replies := [][]renderSegment{nil, nil, nil}
	for i, segs := range [][]Segment{
		f.transcript,
		namingAnswer(t, spec, append(picks, candidates...)),
		namingAnswer(t, spec, append(reversed, candidates...)),
	} {
		for _, s := range segs {
			replies[i] = append(replies[i], renderSegmentOf(s, nil))
		}
	}
	script, err := json.Marshal(replies)
	if err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(bin, "script.json"), string(script))
	t.Setenv(mockScriptEnv, filepath.Join(bin, "script.json"))

	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			ctx := procenv.Context(context.Background())
			q, err := setupTranscribeQuerier(ctx, f.confDir, knownFlags(t, "-voices", "Lorentz,Syrus"), []string{f.recording})
			if err == nil {
				err = q.Query(ctx)
			}
			if err != nil {
				t.Errorf("clai audio transcribe: %v", err)
			}
		})
	})
	doc := knownParse(t, stdout)
	if doc.speakers() != "Lorentz,B" || doc.voices() != "Lorentz:named Syrus:absent" || doc.Naming.Origin != "flag" {
		t.Fatalf("the command named %s (%s by %q)", doc.speakers(), doc.voices(), doc.Naming.Origin)
	}
	if want := "warn [audio] failed to find a match for Syrus, were they in the meeting?\n"; !strings.HasSuffix(stderr, want) || strings.Count(stderr, "warn [audio]") != 1 {
		t.Fatalf("stderr %q, want the one warning %q", stderr, want)
	}
	result, stderr, err := f.engine(pkgtools.AudioTranscribeCall{OutputFormat: "json", Voices: people})
	if tool := knownParse(t, result); err != nil || tool.voices() != doc.voices() || tool.Naming.Origin != "tool" || strings.Contains(stderr, "warn [audio]") {
		t.Fatalf("the tool: err %v, %s by %q, stderr %q; want the same naming and no warning", err, tool.voices(), tool.Naming.Origin, stderr)
	}
}

func TestMockTranscriber_Script(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "script.json")
	malformed := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(malformed, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{missing, malformed} {
		t.Setenv(mockScriptEnv, path)
		_, _, _, err := transcribe(t, seedAudioAliases(t, "test", nil), []string{sparseFile(t, 8)})
		want := fmt.Sprintf("CLAI_MOCK_TRANSCRIBE_SCRIPT=%q is not a script of replies, a JSON array of replies, each an array of {start, end, speaker, text}: ", path)
		if refused, ok := errors.AsType[*MockScriptError](err); !ok || refused.Path != path || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant a *MockScriptError: %s…", err, want)
		}
	}
	if err := os.WriteFile(malformed, []byte(`[[{"start": 1, "end": 2.5, "speaker": "Q", "text": "scripted"}]]`), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := loadMockScript()
	if err != nil {
		t.Fatal(err)
	}
	mock, audio := MockTranscriber{script: script}, sparseFile(t, 8)
	first, _ := mock.Transcribe(context.Background(), audio)
	second, err := mock.Transcribe(context.Background(), audio)
	if want := []Segment{{Start: time.Second, End: 2500 * time.Millisecond, Speaker: "Q", Text: "scripted"}}; !slices.Equal(first, want) {
		t.Fatalf("first reply = %+v, want the script's %+v", first, want)
	}
	if err != nil || len(second) != 2 || second[0].Text != "mock transcription" {
		t.Fatalf("after the script's last reply: %+v, %v; want the mock's own answer", second, err)
	}
}

// The runtime's voices: one library under the clai config directory for the
// voices command and the audio_remember_voice tool.
func TestRuntimeVoices(t *testing.T) {
	ctx := context.Background()
	confDir := filepath.Join(t.TempDir(), "clai")
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	tool, err := pkgtools.AudioRememberVoiceDeps(ctx)
	if err != nil || tool.Inspect == nil || tool.Library == nil {
		t.Fatalf("AudioRememberVoiceDeps = %+v, %v", tool, err)
	}
	cli, err := openVoices(ctx)
	if _, exec := cli.Runner.(ExecRunner); err != nil || !exec || cli.Inspect == nil || cli.Width != nil {
		t.Fatalf("openVoices = %+v, %v", cli, err)
	}
	if _, err := os.Stat(confDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asking for the dependencies created %s: %v", confDir, err)
	}
	inspection := voices.Inspection{FLAC: []byte("fLaC Lorentz"), Seconds: 4, SpeechShare: 1, MeanDBFS: -20}
	if _, err := tool.Library.Enrol(voices.Enrolment{Name: "Lorentz", Inspection: inspection, Status: voices.Confirmed, Source: voices.SourceAdd}); err != nil {
		t.Fatal(err)
	}
	listed, err := cli.Library.List()
	if err != nil || len(listed) != 1 || !strings.HasPrefix(listed[0].Clips[0].Path, filepath.Join(confDir, voices.Dir, "lorentz")) {
		t.Fatalf("the command lists %+v (%v), want the tool's clip under %s", listed, err, confDir)
	}

	audio := Command(CommandDeps{})
	transcribe := audio.Subcommands()["transcribe|t"].(*internal.Command)
	if help := transcribe.Help(); !strings.Contains(help, "-voices") || !strings.Contains(namespaceHelp, "-voices") || !strings.Contains(namespaceHelp, "v|voices") {
		t.Fatalf("the help of audio and of transcribe must name -voices, and audio's the voices verb:\n%s", help)
	}
	for _, name := range []string{"CLAI_CONFIG_DIR", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(name, "")
	}
	_, toolErr := pkgtools.AudioRememberVoiceDeps(ctx)
	_, cliErr := cmd.RunSilentTo(procenv.Context(ctx), []string{"clai", "audio", "voices", "list"}, map[string]cmd.Command{"audio": audio}, io.Discard)
	for _, err := range []error{toolErr, cliErr} {
		if err == nil || !strings.Contains(err.Error(), "find the voice library") {
			t.Fatalf("err = %v, want the config directory's failure", err)
		}
	}
	if _, held := audio.Subcommands()[voicescmd.Key]; !held {
		t.Fatalf("audio has no %s", voicescmd.Key)
	}
}

// F9: pkg/tools reaches internal/audio only through voices, and voices
// reaches nothing of internal/audio but itself.
func TestVoices_ImportDirection(t *testing.T) {
	const audioPkg, voicesPkg = "github.com/baalimago/clai/internal/audio", "github.com/baalimago/clai/internal/audio/voices"
	for dir, allowed := range map[string]string{
		filepath.Join("..", "..", "pkg", "tools"): voicesPkg,
		"voices":                              voicesPkg,
		filepath.Join("voices", "voicestest"): voicesPkg,
	} {
		pkg, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, path := range slices.Concat(pkg.Imports, pkg.TestImports, pkg.XTestImports) {
			if (path == audioPkg || strings.HasPrefix(path, audioPkg+"/")) && path != allowed && !strings.HasPrefix(path, allowed+"/") {
				t.Fatalf("%s imports %s", dir, path)
			}
		}
	}
}
