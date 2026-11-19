package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
)

const (
	namingGap    = 500 * time.Millisecond
	namingMarker = "MARKER-AUDIO-7f1e"
)

// namingRegion is one clip of a naming request: a pick (L1, L2, S1, C1: the
// person's initial and the pick's place) or a candidate (cA, cB, …).
type namingRegion struct {
	key    string
	length time.Duration
}

// namingAnswer is a transcriber's answer to a request of regions with 0.5 s
// between neighbours. spec labels the regions: "L1=X cA=X:.8,Y:.2" gives
// all of L1 to X and splits cA; "_" is unlabelled time, and so is a region
// the spec leaves out.
func namingAnswer(t *testing.T, spec string, regions []namingRegion) []Segment {
	t.Helper()
	covers := map[string]string{}
	for field := range strings.FieldsSeq(spec) {
		key, cover, _ := strings.Cut(field, "=")
		covers[key] = cover
	}
	var segs []Segment
	at := -namingGap
	for _, r := range regions {
		at += namingGap
		pos := at
		for part := range strings.SplitSeq(covers[r.key], ",") {
			label, share, split := strings.Cut(part, ":")
			portion := 1.0
			if split {
				var err error
				if portion, err = strconv.ParseFloat(share, 64); err != nil {
					t.Fatalf("spec %q: %v", spec, err)
				}
			}
			d := time.Duration(math.Round(portion * float64(r.length)))
			segs = append(segs, Segment{Start: pos, End: pos + d, Speaker: strings.Trim(label, "_"), Text: "words"})
			pos += d
		}
		at += r.length
	}
	return segs
}

// namingRunner is ffmpeg: it writes the file its last argument names.
type namingRunner struct {
	missing bool
	// fail fails every run with stderr.
	fail   error
	stderr string
	// write replaces the 64 bytes written to the request file.
	write func(out string) error

	mu    sync.Mutex
	runs  [][]string
	looks int
}

func (r *namingRunner) LookPath(name string) (string, error) {
	r.mu.Lock()
	r.looks++
	r.mu.Unlock()
	if r.missing {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return "/fake/bin/" + name, nil
}

func (r *namingRunner) Run(_ context.Context, name string, args ...string) (string, string, error) {
	r.mu.Lock()
	r.runs = append(r.runs, append([]string{name}, args...))
	r.mu.Unlock()
	if r.fail != nil {
		return "", r.stderr, r.fail
	}
	out := args[len(args)-1]
	if r.write != nil {
		return "", "", r.write(out)
	}
	return "", "", os.WriteFile(out, []byte("fLaC request "+namingMarker+strings.Repeat("f", 33)), 0o600)
}

func (r *namingRunner) ran() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.runs)
}

// namingRun is one Namer.Name call. The usual one has speaker A for 0–8 s
// and B for 9–15 s, Lorentz with two confirmed picks (8.4 s, 6.1 s) and
// Syrus with one unconfirmed (7.0 s), by -voices. Whatever the call does, it
// leaves nothing in the temporary directory.
type namingRun struct {
	t           *testing.T
	recording   string
	segments    []Segment
	selection   voices.Selection
	budgets     Budgets
	runner      *namingRunner
	transcriber *fakeTranscriber
	// forward and reversed spec the two answers over the picks and then
	// candidates.
	forward, reversed string
	candidates        []namingRegion
	fail              [2]error
	// dress changes an answer before it is returned.
	dress func([]Segment) []Segment
	// onSend runs when the forward request arrives.
	onSend func()
}

func namingPick(slug voices.Slug, id voices.ClipID, status voices.Status, seconds float64) voices.Pick {
	ref := voices.ClipRef{Slug: slug, ID: id}
	return voices.Pick{Ref: ref, Status: status, Seconds: seconds, Path: "/config/voices/" + ref.String() + voices.ClipExt}
}

func newNamingRun(t *testing.T) *namingRun {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Cleanup(func() {
		if left, err := os.ReadDir(tmp); err != nil || len(left) != 0 {
			t.Errorf("the naming left %v in the temporary directory (%v)", left, err)
		}
	})
	r := &namingRun{
		t:         t,
		recording: "/recordings/meeting.wav",
		segments: []Segment{
			{Start: 0, End: 8 * time.Second, Speaker: "A", Text: "hello there"},
			{Start: 9 * time.Second, End: 15 * time.Second, Speaker: "B", Text: "hi"},
		},
		selection: voices.Selection{Origin: voices.OriginFlag, Voices: []voices.SelectedVoice{
			{Name: "Lorentz", Slug: "lorentz", Picks: []voices.Pick{
				namingPick("lorentz", "9f3c2a1b0d4e", voices.Confirmed, 8.4),
				namingPick("lorentz", "e81d5530c6a9", voices.Confirmed, 6.1),
			}},
			{Name: "Syrus", Slug: "syrus", Picks: []voices.Pick{namingPick("syrus", "3d81f60a2b97", voices.Unconfirmed, 7.0)}},
		}},
		budgets:    defaultBudgets(),
		runner:     &namingRunner{},
		candidates: []namingRegion{{"cA", 8 * time.Second}, {"cB", 6 * time.Second}},
	}
	r.transcriber = &fakeTranscriber{transcribe: r.answer}
	return r
}

// both gives the two orders one answer.
func (r *namingRun) both(spec string) *namingRun {
	r.forward, r.reversed = spec, spec
	return r
}

// picks are the selection's picks in forward order, with their keys.
func (r *namingRun) picks() ([]namingRegion, map[voices.ClipRef]string) {
	var regions []namingRegion
	keys := map[voices.ClipRef]string{}
	for _, v := range r.selection.Voices {
		for i, p := range v.Picks {
			key := strings.ToUpper(string(v.Slug[:1])) + strconv.Itoa(i+1)
			regions = append(regions, namingRegion{key, time.Duration(math.Round(p.Seconds * float64(time.Second)))})
			keys[p.Ref] = key
		}
	}
	return regions, keys
}

func (r *namingRun) answer(ctx context.Context, path string) ([]Segment, error) {
	call := r.transcriber.callCount() - 1
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		r.t.Errorf("request %d: %s is no request file when it is sent (%v)", call, path, err)
	}
	if call > 1 {
		return nil, errors.New("a third naming request")
	}
	if call == 0 && r.onSend != nil {
		r.onSend()
		return nil, ctx.Err()
	}
	if r.fail[call] != nil {
		return nil, r.fail[call]
	}
	picks, _ := r.picks()
	spec := r.forward
	if call == 1 {
		slices.Reverse(picks)
		spec = r.reversed
	}
	segs := namingAnswer(r.t, spec, append(picks, r.candidates...))
	if r.dress != nil {
		segs = r.dress(segs)
	}
	return segs, nil
}

// name runs the naming and checks what holds of every naming: the segments
// keep their start, end and text; a segment has a name and a voice exactly
// when the report names its label; there is one outcome per pick, in
// forward order, exactly when both requests were answered; and no output
// carries the audio of a request.
func (r *namingRun) name(ctx context.Context) Naming {
	r.t.Helper()
	given := slices.Clone(r.segments)
	got := Namer{Transcriber: r.transcriber, Runner: r.runner, Budgets: r.budgets}.Name(ctx, r.recording, r.segments, r.selection)
	if !reflect.DeepEqual(r.segments, given) || len(got.Segments) != len(given) {
		r.t.Fatalf("segments: given %+v changed to %+v, named %d", given, r.segments, len(got.Segments))
	}
	named := map[string]SpeakerNaming{}
	for _, s := range got.Report.Speakers {
		named[s.Label] = s
		if (s.Name == "") != (s.Voice == nil) || (s.Name == "") == (s.Reason == "") {
			r.t.Fatalf("speaker %+v: want a name with its voice, or a reason", s)
		}
	}
	for i, s := range got.Segments {
		want, voice := given[i], (*Provenance)(nil)
		if n := named[want.Speaker]; n.Name != "" {
			want.Speaker, voice = string(n.Name), n.Voice
		}
		if s.Segment != want || !reflect.DeepEqual(s.Voice, voice) {
			r.t.Fatalf("segment %d: got %+v by %v, want %+v by %v", i, s.Segment, s.Voice, want, voice)
		}
	}
	picks, keys := r.picks()
	wantOutcomes := 0
	if got.Report.Status == NamingNamed || got.Report.Status == NamingNoAgreement {
		wantOutcomes = len(picks)
	}
	if len(got.Outcomes) != wantOutcomes {
		r.t.Fatalf("status %s: %d outcomes, want %d", got.Report.Status, len(got.Outcomes), wantOutcomes)
	}
	for i, o := range got.Outcomes {
		if keys[o.Clip] != picks[i].key {
			r.t.Fatalf("outcome %d is of %v, want pick %s", i, o.Clip, picks[i].key)
		}
	}
	report, err := json.Marshal(got.Report)
	if err != nil || strings.Contains(fmt.Sprintf("%+v %v %s", got, got.Report.Err, report), namingMarker) {
		r.t.Fatalf("the naming carries request audio (marshal: %v): %+v", err, got)
	}
	return got
}

// speakers reads "A=Lorentz<L1 confirmed> B:no-match".
func (r *namingRun) speakers(n Naming) string {
	_, keys := r.picks()
	var out []string
	for _, s := range n.Report.Speakers {
		if s.Name == "" {
			out = append(out, s.Label+":"+string(s.Reason))
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s<%s %s>", s.Label, s.Name, keys[s.Voice.Clip], s.Voice.Status))
	}
	return strings.Join(out, " ")
}

// outcomes reads "L1+A L2+ S1-": landed or not, then whom the clip named.
func (r *namingRun) outcomes(n Naming) string {
	_, keys := r.picks()
	var out []string
	for _, o := range n.Outcomes {
		landed := "-"
		if o.Landed {
			landed = "+"
		}
		out = append(out, keys[o.Clip]+landed+strings.Join(o.Named, ","))
	}
	return strings.Join(out, " ")
}

// namingVoices reads "Lorentz:named Syrus:absent".
func namingVoices(report NamingReport) string {
	var out []string
	for _, v := range report.Voices {
		out = append(out, v.Name+":"+string(v.Result))
	}
	return strings.Join(out, " ")
}

func TestNamer_NamesWhereBothOrdersAgree(t *testing.T) {
	const rest = " L2=Z S1=V cB=W"
	const lorentzOnly = "A=Lorentz<L1 confirmed> B:no-match | Lorentz:named Syrus:absent | L1+A L2+ S1+"
	for _, tc := range []struct {
		name string
		// unconfirmed names the picks of Lorentz made unconfirmed.
		unconfirmed string
		// reversed is forward when empty.
		forward, reversed string
		// want is the report's speakers, its voices and the outcomes.
		want string
	}{
		{"both orders point at the same people", "", "L1=X L2=Z S1=Y cA=X cB=Y", "", "A=Lorentz<L1 confirmed> B=Syrus<S1 unconfirmed> | Lorentz:named Syrus:named | L1+A L2+ S1+B"},
		{"one order points at nobody", "", "L1=X L2=Z S1=Y cA=X cB=W", "L1=X L2=Z S1=Y cA=Q cB=W", "A:disagreed B:no-match | Lorentz:absent Syrus:absent | L1+ L2+ S1+"},
		{"the orders point at different people", "", "L1=X L2=Z S1=Y cA=X cB=W", "L1=X L2=Z S1=Q cA=Q cB=W", "A:disagreed B:no-match | Lorentz:absent Syrus:absent | L1+ L2+ S1+"},
		{"two people share the label", "", "L1=X S1=X cA=X cB=W", "", "A:ambiguous B:no-match | Lorentz:absent Syrus:absent | L1+ L2- S1+"},
		{"two people share the label, one clip not landed", "", "L1=X S1=X:.5,Y:.3,Z:.2 cA=X cB=W", "", "A:ambiguous B:no-match | Lorentz:absent Syrus:not-placed | L1+ L2- S1-"},
		{"two clips of one person support each other", "", "L1=X L2=X S1=Y cA=X cB=W", "", lorentzOnly},
		{"two speakers take one name", "", "L1=X L2=X S1=Y cA=X cB=X", "", "A=Lorentz<L1 confirmed> B=Lorentz<L1 confirmed> | Lorentz:named Syrus:absent | L1+A,B L2+ S1+"},
		{"each order by another clip of the person: the first is the source", "", "L1=X L2=Y cA=X S1=V cB=W", "L1=Y L2=X cA=X S1=V cB=W", lorentzOnly},
		{"a confirmed clip is the source before an unconfirmed one", "L1", "L1=X L2=X cA=X S1=V cB=W", "", "A=Lorentz<L2 confirmed> B:no-match | Lorentz:named Syrus:absent | L1+ L2+A S1+"},
		{"a confirmed clip that supports in one order is the source", "L1", "L1=X L2=Z cA=X S1=V cB=W", "L1=X L2=X cA=X S1=V cB=W", "A=Lorentz<L2 confirmed> B:no-match | Lorentz:named Syrus:absent | L1+ L2+A S1+"},
		{"unconfirmed when the confirmed clip did not land", "L1", "L1=X L2=X:.6,Z:.4 cA=X S1=V cB=W", "", "A=Lorentz<L1 unconfirmed> B:no-match | Lorentz:named Syrus:absent | L1+A L2- S1+"},
		{"unconfirmed when the confirmed clip landed on another label", "L1", "L1=X cA=X" + rest, "", "A=Lorentz<L1 unconfirmed> B:no-match | Lorentz:named Syrus:absent | L1+A L2+ S1+"},
		{"unconfirmed when no clip is confirmed", "L1 L2", "L1=X L2=X cA=X S1=V cB=W", "", "A=Lorentz<L1 unconfirmed> B:no-match | Lorentz:named Syrus:absent | L1+A L2+ S1+"},
		{"a label over exactly 4/5 of a clip lands it", "", "L1=X:.8,Y:.2 cA=X" + rest, "", lorentzOnly},
		{"under 4/5 in one order", "", "L1=X:.79,Y:.21 cA=X" + rest, "L1=X:.8,Y:.2 cA=X" + rest, "A:disagreed B:no-match | Lorentz:absent Syrus:absent | L1+ L2+ S1+"},
		{"under 4/5 in both orders", "", "L1=X:.79,Y:.21 cA=X" + rest, "", "A:no-match B:no-match | Lorentz:absent Syrus:absent | L1- L2+ S1+"},
		{"a candidate tied between two labels", "", "L1=X cA=X:.5,Y:.5" + rest, "", "A:no-match B:no-match | Lorentz:absent Syrus:absent | L1+ L2+ S1+"},
		{"a candidate without a label", "", "L1=X cA=_" + rest, "", "A:no-match B:no-match | Lorentz:absent Syrus:absent | L1+ L2+ S1+"},
		{"unlabelled time is no label's", "", "L1=X:.4,_:.5,Y:.1 cA=X" + rest, "", lorentzOnly},
		{"a clip lands in one order only, another in neither", "", "L1=X cA=X cB=W", "L1=X L2=V cA=X cB=W", "A=Lorentz<L1 confirmed> B:no-match | Lorentz:named Syrus:not-placed | L1+A L2+ S1-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNamingRun(t).both(tc.forward)
			for i := range r.selection.Voices[0].Picks {
				if strings.Contains(tc.unconfirmed, "L"+strconv.Itoa(i+1)) {
					r.selection.Voices[0].Picks[i].Status = voices.Unconfirmed
				}
			}
			if tc.reversed != "" {
				r.reversed = tc.reversed
			}
			got := r.name(context.Background())
			status := NamingNoAgreement
			if strings.Contains(tc.want, "=") {
				status = NamingNamed
			}
			if got.Report.Status != status || got.Report.Err != nil || got.Report.Origin != voices.OriginFlag {
				t.Fatalf("report: %s by %q (%v), want %s by the flag", got.Report.Status, got.Report.Origin, got.Report.Err, status)
			}
			if s := r.speakers(got) + " | " + namingVoices(got.Report) + " | " + r.outcomes(got); s != tc.want {
				t.Fatalf("got  %s\nwant %s", s, tc.want)
			}
			if calls, runs := r.transcriber.callCount(), len(r.runner.ran()); calls != 2 || runs != 2 {
				t.Fatalf("%d requests built and %d sent, want 2 and 2", runs, calls)
			}
		})
	}
}

// F1: only a segment's start, end and speaker are read, so any transcriber
// that hears the same labels names the same people.
func TestNamer_ReadsOnlyStartEndAndLabel(t *testing.T) {
	const spec = "L1=X L2=Z S1=Y cA=X cB=Y"
	quiet := newNamingRun(t).both(spec)
	quiet.dress = func(segs []Segment) []Segment {
		for i := range segs {
			segs[i].Text = ""
		}
		return segs
	}
	loud := newNamingRun(t).both(spec)
	loud.dress = func(segs []Segment) []Segment {
		for i := range segs {
			segs[i].Text = strings.Repeat("noise ", 40)
		}
		end := segs[len(segs)-1].End
		return append(segs,
			Segment{Start: 8450 * time.Millisecond, End: 8850 * time.Millisecond, Speaker: "X", Text: "in the silence"},
			Segment{Start: end + time.Second, End: end + 5*time.Second, Speaker: "Y", Text: "after the end"})
	}
	a, b := quiet.name(context.Background()), loud.name(context.Background())
	if a.Report.Status != NamingNamed || !reflect.DeepEqual(a, b) {
		t.Fatalf("namings differ, or nobody was named:\n%+v\n%+v", a, b)
	}
}

func TestNamer_RequestIsPicksThenCandidatesWithSilenceBetween(t *testing.T) {
	r := newNamingRun(t).both("L1=X cA=X")
	r.segments = []Segment{
		{Start: 0, End: 8 * time.Second, Speaker: "B", Text: "b"},
		{Start: 9 * time.Second, End: 15 * time.Second, Speaker: "A", Text: "a"},
		{Start: 16 * time.Second, End: 18 * time.Second, Speaker: "C", Text: "c"},
		{Start: 20 * time.Second, End: 24 * time.Second, Speaker: "A", Text: "a again"},
	}
	r.candidates = []namingRegion{{"cB", 8 * time.Second}, {"cA", 6 * time.Second}}

	got := r.name(context.Background())

	if s := r.speakers(got); s != "B:no-match A=Lorentz<L1 confirmed> C:no-candidate" {
		t.Fatalf("speakers: %s", s)
	}
	l1, l2, s1 := r.selection.Voices[0].Picks[0].Path, r.selection.Voices[0].Picks[1].Path, r.selection.Voices[1].Picks[0].Path
	cuts := " -ss 0 -t 8 -i " + r.recording + " -ss 9 -t 6 -i " + r.recording + " -filter_complex"
	wants := []string{
		"ffmpeg -nostdin -hide_banner -loglevel error -y -i " + l1 + " -i " + l2 + " -i " + s1 + cuts,
		"ffmpeg -nostdin -hide_banner -loglevel error -y -i " + s1 + " -i " + l2 + " -i " + l1 + cuts,
	}
	runs, sent := r.runner.ran(), r.transcriber.calls
	if len(runs) != 2 || len(sent) != 2 || sent[0] == sent[1] {
		t.Fatalf("built %d requests, sent %v; want two different files", len(runs), sent)
	}
	for i, run := range runs {
		at := slices.Index(run, "-filter_complex")
		if at < 0 || strings.Join(run[:at+1], " ") != wants[i] {
			t.Fatalf("request %d: ran %q\nwant %s …", i, run, wants[i])
		}
		graph, tail := run[at+1], strings.Join(run[at+2:], " ")
		if strings.Count(graph, "duration=0.5,") != 4 || !strings.HasSuffix(graph, ";[a0][g0][a1][g1][a2][g2][a3][g3][a4]concat=n=9:v=0:a=1[out]") {
			t.Fatalf("request %d: the sound is not clip, 0.5 s of silence, clip, …: %s", i, graph)
		}
		if tail != "-map [out] -map_metadata -1 -c:a flac "+sent[i] {
			t.Fatalf("request %d ends %q, want the file sent, %s, as its last argument", i, tail, sent[i])
		}
	}
}

func TestNamer_NothingIsSentWithoutSomeoneToCompare(t *testing.T) {
	unreadable := &voices.LibraryReadError{Path: "/config/voices", Err: fs.ErrPermission}
	unlabelled := []Segment{{Start: 0, End: 8 * time.Second, Text: "a"}, {Start: 9 * time.Second, End: 15 * time.Second, Text: "b"}}
	short := []Segment{{Start: 0, End: 2 * time.Second, Speaker: "A"}, {Start: 3 * time.Second, End: 5 * time.Second, Speaker: "B"}, {Start: 6 * time.Second, End: 8 * time.Second, Speaker: "A"}}
	// Each row also holds what the rows after it name: a status outranks them.
	for _, tc := range []struct {
		status   NamingStatus
		setup    func(r *namingRun)
		speakers string
		voices   string
	}{
		{NamingNotAsked, func(r *namingRun) { r.selection, r.segments = voices.Selection{}, unlabelled }, "", ""},
		{NamingLibraryUnreadable, func(r *namingRun) {
			r.selection, r.segments = voices.Selection{Origin: voices.OriginFlag, Unknown: []string{"Ada"}, Unreadable: unreadable}, unlabelled
		}, "", "Ada:unknown-voice"},
		{NamingNoVoice, func(r *namingRun) {
			r.selection, r.segments = voices.Selection{Origin: voices.OriginTool, Unknown: []string{"Ada"}}, unlabelled
		}, "", "Ada:unknown-voice"},
		{NamingNotDiarized, func(r *namingRun) { r.segments = unlabelled }, "", "Lorentz:not-compared Syrus:not-compared"},
		{NamingNoCandidate, func(r *namingRun) { r.segments = short }, "A:no-candidate B:no-candidate", "Lorentz:not-compared Syrus:not-compared"},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			r := newNamingRun(t)
			r.runner.missing = true
			tc.setup(r)
			got := r.name(context.Background())
			if got.Report.Status != tc.status || got.Report.Origin != r.selection.Origin {
				t.Fatalf("status %s by %q, want %s by %q", got.Report.Status, got.Report.Origin, tc.status, r.selection.Origin)
			}
			if wantErr := tc.status == NamingLibraryUnreadable; (got.Report.Err != nil) != wantErr || wantErr && !errors.Is(got.Report.Err, fs.ErrPermission) {
				t.Fatalf("Err = %v", got.Report.Err)
			}
			if s, v := r.speakers(got), namingVoices(got.Report); s != tc.speakers || v != tc.voices {
				t.Fatalf("got %q | %q, want %q | %q", s, v, tc.speakers, tc.voices)
			}
			if len(r.runner.ran()) != 0 || r.transcriber.callCount() != 0 {
				t.Fatalf("ran %v and sent %v, want nothing", r.runner.ran(), r.transcriber.calls)
			}
		})
	}
}

func TestNamer_RequestBudget(t *testing.T) {
	// Nine speakers whose runs are 10, 3, 7, 4, 9, 5, 7, 6 and 8 s long,
	// one second apart.
	crowd := func(r *namingRun) {
		r.segments = nil
		at := time.Duration(0)
		for i, seconds := range []int{10, 3, 7, 4, 9, 5, 7, 6, 8} {
			d := time.Duration(seconds) * time.Second
			r.segments = append(r.segments, Segment{Start: at, End: at + d, Speaker: fmt.Sprintf("%d-%c", i/3+1, 'A'+i%3)})
			at += d + time.Second
		}
	}
	t.Run("candidates are left out least speech first", func(t *testing.T) {
		r := newNamingRun(t).both("L1=X c1-A=X")
		crowd(r)
		// 58.5 s with the four longest; the fifth does not fit, and the
		// 3 s one that would is not tried after it.
		r.budgets.MaxRequestDuration = 62 * time.Second
		r.candidates = []namingRegion{{"c1-A", 10 * time.Second}, {"c1-C", 7 * time.Second}, {"c2-B", 9 * time.Second}, {"c3-C", 8 * time.Second}}
		got := r.name(context.Background())
		want := "1-A=Lorentz<L1 confirmed> 1-B:not-compared 1-C:no-match 2-A:not-compared 2-B:no-match 2-C:not-compared 3-A:not-compared 3-B:not-compared 3-C:no-match"
		if s := r.speakers(got); s != want {
			t.Fatalf("speakers: %s\nwant      %s", s, want)
		}
		in := " -i " + r.recording
		cuts := " -ss 0 -t 10" + in + " -ss 15 -t 7" + in + " -ss 28 -t 9" + in + " -ss 59 -t 8" + in + " -filter_complex"
		for i, run := range r.runner.ran() {
			if !strings.Contains(strings.Join(run, " "), voices.ClipExt+cuts) {
				t.Fatalf("request %d: %q\nwant the four longest candidates, in transcript order: %s", i, run, cuts)
			}
		}
	})
	t.Run("the default budget takes every candidate", func(t *testing.T) {
		r := newNamingRun(t)
		crowd(r)
		r.name(context.Background())
		for i, run := range r.runner.ran() {
			if n := strings.Count(strings.Join(run, " "), "-i "+r.recording); n != 9 {
				t.Fatalf("request %d cuts %d candidates, want 9", i, n)
			}
		}
	})
	for _, tc := range []struct {
		name  string
		setup func(r *namingRun)
		want  NamingBudgetError
		text  string
		runs  int
	}{
		{
			name:  "the picks and one candidate are over the duration",
			setup: func(r *namingRun) { r.budgets.MaxRequestDuration = 20 * time.Second },
			want:  NamingBudgetError{NeedDuration: 31 * time.Second, LimitDuration: 20 * time.Second},
			text:  "naming request (forward order) failed to build: naming request needs 31s with one candidate, over the request budget of 20s (transcribe.max-request-seconds)",
		},
		{
			name: "the request file is over the bytes",
			setup: func(r *namingRun) {
				r.budgets.MaxRequestBytes = 1 << 10
				r.runner.write = func(out string) error { return os.WriteFile(out, make([]byte, 4<<10), 0o600) }
			},
			want: NamingBudgetError{NeedBytes: 4 << 10, LimitBytes: 1 << 10},
			text: "naming request (forward order) failed to build: naming request is 4096 bytes, over the request budget of 1024 bytes (transcribe.max-request-bytes)",
			runs: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNamingRun(t)
			tc.setup(r)
			got := r.name(context.Background())
			budget, ok := errors.AsType[*NamingBudgetError](got.Report.Err)
			if got.Report.Status != NamingRequestFailed || !ok || *budget != tc.want || got.Report.Err.Error() != tc.text {
				t.Fatalf("got %s: %v\nwant request-failed: %s", got.Report.Status, got.Report.Err, tc.text)
			}
			if s := r.speakers(got); s != "A:not-compared B:not-compared" || len(r.runner.ran()) != tc.runs || r.transcriber.callCount() != 0 {
				t.Fatalf("speakers %s, %d requests built, %d sent; want nobody compared, %d built, none sent", s, len(r.runner.ran()), r.transcriber.callCount(), tc.runs)
			}
		})
	}
}

func TestNamer_FailedRequestLeavesTheTranscript(t *testing.T) {
	down := errors.New("transcriber down")
	const forward = "naming request (forward order) failed to "
	for _, tc := range []struct {
		name  string
		setup func(r *namingRun)
		// cancel is when the run is cancelled: "before" it, or when the
		// forward request is "sent".
		cancel string
		// text starts the report's error; is is a cause it wraps.
		text       string
		is         error
		runs, sent int
	}{
		{"ffmpeg is missing", func(r *namingRun) { r.runner.missing = true }, "", "ffmpeg is not installed", nil, 0, 0},
		{
			"ffmpeg fails", func(r *namingRun) {
				r.runner.fail, r.runner.stderr = down, "Guessed layout\nmeeting.wav: Invalid data found \n"
			}, "",
			forward + "build: ffmpeg: transcriber down: meeting.wav: Invalid data found", down, 1, 0,
		},
		{"ffmpeg writes no file", func(r *namingRun) { r.runner.write = func(string) error { return nil } }, "", forward + "build: ffmpeg wrote no naming request: ", fs.ErrNotExist, 1, 0},
		{"ffmpeg writes an empty file", func(r *namingRun) { r.runner.write = func(out string) error { return os.WriteFile(out, nil, 0o600) } }, "", forward + "build: ffmpeg wrote an empty naming request ", nil, 1, 0},
		{"the forward request is not answered, and the reversed one is not sent", func(r *namingRun) { r.fail[0] = down }, "", forward + "send: transcriber down", down, 1, 1},
		{"the reversed request is not answered", func(r *namingRun) { r.fail[1] = down }, "", "naming request (reversed order) failed to send: transcriber down", down, 2, 2},
		{"cancelled while the forward request is out", func(*namingRun) {}, "sent", forward + "send: context canceled", context.Canceled, 1, 1},
		{"cancelled before", func(*namingRun) {}, "before", forward + "build: context canceled", context.Canceled, 0, 0},
		{"cancelled before, and ffmpeg is missing", func(r *namingRun) { r.runner.missing = true }, "before", forward + "build: context canceled", context.Canceled, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNamingRun(t).both("L1=X cA=X")
			r.selection.Unknown = []string{"Ada"}
			tc.setup(r)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch tc.cancel {
			case "before":
				cancel()
			case "sent":
				r.onSend = cancel
			}
			got := r.name(ctx)
			err := got.Report.Err
			if got.Report.Status != NamingRequestFailed || err == nil || !strings.HasPrefix(err.Error(), tc.text) || tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("got %s: %v\nwant request-failed: %s…", got.Report.Status, err, tc.text)
			}
			if _, missing := errors.AsType[*voices.MissingToolError](err); missing != (tc.name == "ffmpeg is missing") {
				t.Fatalf("Err is %T", err)
			}
			if s, v := r.speakers(got), namingVoices(got.Report); s != "A:not-compared B:not-compared" || v != "Lorentz:not-compared Syrus:not-compared Ada:unknown-voice" {
				t.Fatalf("report: %s | %s, want nobody compared", s, v)
			}
			if runs, sent := len(r.runner.ran()), r.transcriber.callCount(); runs != tc.runs || sent != tc.sent {
				t.Fatalf("%d requests built and %d sent, want %d and %d", runs, sent, tc.runs, tc.sent)
			}
		})
	}
}

func TestNamingReport_JSON(t *testing.T) {
	r := newNamingRun(t).both("L1=X cA=X S1=W cB=V")
	r.selection.Voices = append(r.selection.Voices, voices.SelectedVoice{
		Name: "Christoph", Slug: "christoph", Picks: []voices.Pick{namingPick("christoph", "4be07c19a2f1", voices.Confirmed, 5.0)},
	})
	r.selection.Unknown = []string{"Ada"}
	failed := NamingReport{
		Status: NamingRequestFailed, Origin: voices.OriginTool,
		Speakers:   []SpeakerNaming{{Label: "A", Reason: ReasonNotCompared}},
		Voices:     []VoiceNaming{{Name: "Lorentz", Result: VoiceNotCompared}},
		Err:        &NamingRequestError{Order: OrderReversed, Stage: StageSend, Err: errors.New("transcriber down")},
		Unrecorded: errors.New("tally failed"),
	}
	for _, tc := range []struct {
		name   string
		report NamingReport
		want   string
	}{
		{"named", r.name(context.Background()).Report, `{"status":"named","origin":"flag",` +
			`"speakers":[{"label":"A","name":"Lorentz","voice":{"clip":"lorentz/9f3c2a1b0d4e","status":"confirmed"}},{"label":"B","reason":"no-match"}],` +
			`"voices":[{"name":"Lorentz","result":"named"},{"name":"Syrus","result":"absent"},{"name":"Christoph","result":"not-placed"},{"name":"Ada","result":"unknown-voice"}]}`},
		{"failed and unrecorded", failed, `{"status":"request-failed","origin":"tool","speakers":[{"label":"A","reason":"not-compared"}],` +
			`"voices":[{"name":"Lorentz","result":"not-compared"}],` +
			`"error":"naming request (reversed order) failed to send: transcriber down","unrecorded":"tally failed"}`},
		{"empty", NamingReport{Status: NamingNotAsked}, `{"status":"not-asked","origin":"","speakers":[],"voices":[]}`},
	} {
		got, err := json.Marshal(tc.report)
		if err != nil || !bytes.Equal(got, []byte(tc.want)) {
			t.Fatalf("%s (err %v):\ngot  %s\nwant %s", tc.name, err, got, tc.want)
		}
	}
}
