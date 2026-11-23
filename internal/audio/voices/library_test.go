package voices_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
)

var t0 = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

// env is a library over a temporary config directory, with a clock that
// moves on by step at every reading.
type env struct {
	t    *testing.T
	cfg  string
	now  time.Time
	step time.Duration
	lib  *voices.Library
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, cfg: t.TempDir(), now: t0, step: time.Minute}
	e.lib = voices.NewLibrary(e.cfg, func() time.Time {
		e.now = e.now.Add(e.step)
		return e.now
	})
	return e
}

// path is an entry of voices/: path("lorentz", "voice.json").
func (e *env) path(parts ...string) string {
	return filepath.Join(append([]string{e.cfg, "voices"}, parts...)...)
}

func (e *env) clip(ref voices.ClipRef) string {
	return e.path(string(ref.Slug), string(ref.ID)+".flac")
}

func flac(seed string) []byte { return []byte("fLaC" + seed) }

func enrolment(name, seed string, status voices.Status) voices.Enrolment {
	source := voices.SourceAdd
	if status == voices.Unconfirmed {
		source = voices.SourceRemember
	}
	return voices.Enrolment{Name: name, Inspection: voices.Inspection{FLAC: flac(seed), Seconds: 6.5}, Status: status, Source: source}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) enrol(name, seed string, status voices.Status) voices.ClipRef {
	e.t.Helper()
	got, err := e.lib.Enrol(enrolment(name, seed, status))
	if err != nil || got.Leftover != nil {
		e.t.Fatalf("Enrol(%s, %s) = %+v, %v", name, seed, got, err)
	}
	return got.Ref
}

func (e *env) named(ref voices.ClipRef, n int) {
	e.t.Helper()
	for range n {
		ok(e.t, e.lib.Tally([]voices.ClipOutcome{{Clip: ref, Landed: true, Named: []string{"A"}}}))
	}
}

func (e *env) overrule(ref voices.ClipRef, n int) {
	e.t.Helper()
	for range n {
		_, err := e.lib.Overrule(ref)
		ok(e.t, err)
	}
}

func (e *env) list() []voices.VoiceSummary {
	e.t.Helper()
	list, err := e.lib.List()
	ok(e.t, err)
	return list
}

func (e *env) clips(slug voices.Slug) []voices.ClipSummary {
	e.t.Helper()
	for _, p := range e.list() {
		if p.Slug == slug {
			return p.Clips
		}
	}
	return nil
}

func ids(clips []voices.ClipSummary) []voices.ClipID {
	out := []voices.ClipID{}
	for _, c := range clips {
		out = append(out, c.ID)
	}
	return out
}

// json renders v with the config directory cut from its paths, so two
// libraries compare.
func (e *env) json(v any) string {
	e.t.Helper()
	raw, err := json.Marshal(v)
	ok(e.t, err)
	return strings.ReplaceAll(string(raw), e.cfg, "")
}

// files is every entry under the config directory with its content: equal
// maps mean nothing was written.
func (e *env) files() map[string]string {
	e.t.Helper()
	out := map[string]string{}
	ok(e.t, filepath.WalkDir(e.cfg, func(path string, _ fs.DirEntry, err error) error {
		data, _ := os.ReadFile(path)
		out[path] = string(data)
		return err
	}))
	return out
}

func (e *env) unchanged(before map[string]string) {
	e.t.Helper()
	if after := e.files(); !maps.Equal(after, before) {
		e.t.Fatalf("the files changed\nbefore: %v\nafter:  %v", before, after)
	}
}

// log is a voice's events.jsonl, empty when there is none.
func (e *env) log(slug string) string {
	e.t.Helper()
	raw, err := os.ReadFile(e.path(slug, "events.jsonl"))
	if !errors.Is(err, fs.ErrNotExist) {
		ok(e.t, err)
	}
	return string(raw)
}

func put(t *testing.T, path, data string) {
	t.Helper()
	ok(t, os.MkdirAll(filepath.Dir(path), 0o755))
	ok(t, os.WriteFile(path, []byte(data), 0o644))
}

var addedClip = regexp.MustCompile(`"event":"added","clip":"([0-9a-f]{12})"`)

// write lays a voice down by hand, as any earlier clai wrote it: voice.json
// (none when voiceJSON is empty), more lines of events.jsonl, and a clip file
// for every clip an added line names.
func (e *env) write(slug, voiceJSON string, lines ...string) {
	e.t.Helper()
	ok(e.t, os.MkdirAll(e.path(slug), 0o755))
	if voiceJSON != "" {
		put(e.t, e.path(slug, "voice.json"), voiceJSON)
	}
	log := strings.Join(lines, "")
	for _, m := range addedClip.FindAllStringSubmatch(log, -1) {
		put(e.t, e.path(slug, m[1]+".flac"), string(flac(m[1])))
	}
	if log != "" {
		put(e.t, e.path(slug, "events.jsonl"), e.log(slug)+log)
	}
}

func (e *env) chmod(path string, mode os.FileMode) {
	e.t.Helper()
	ok(e.t, os.Chmod(path, mode))
	e.t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// ev is one line of events.jsonl, written by hand.
func ev(n int, at time.Time, kind string, clip voices.ClipID, extra string) string {
	return fmt.Sprintf(`{"id":"%016x","at":"%s","event":"%s","clip":"%s"%s}`+"\n", n, at.UTC().Format(time.RFC3339Nano), kind, clip, extra)
}

func confirmed(seconds float64) string {
	return fmt.Sprintf(`,"status":"confirmed","source":"voices add","seconds":%v`, seconds)
}

func learned(seconds float64) string {
	return fmt.Sprintf(`,"status":"unconfirmed","source":"audio_remember_voice","seconds":%v`, seconds)
}

const spoke = `,"speakers":["A"]`

// at is a time m minutes into the morning of the 18th.
func at(m int) time.Time { return time.Date(2026, 11, 18, 9, m, 0, 0, time.UTC) }

func errAs[E error](t *testing.T, err error) E {
	t.Helper()
	got, isE := errors.AsType[E](err)
	if !isE {
		t.Fatalf("got %v (%T), want a %T", err, err, got)
	}
	return got
}

// TestLibrary_WritesTheFormat also proves that an event takes effect when the
// clock steps back or stands still: it is stamped a nanosecond after the
// clip's latest event.
func TestLibrary_WritesTheFormat(t *testing.T) {
	e := newEnv(t)
	e.now = time.Date(2026, 11, 26, 13, 0, 0, 0, time.FixedZone("CET", 3600))
	first := e.enrol("Lorentz", "one", voices.Confirmed)
	second := e.enrol("LORENTZ", "two", voices.Unconfirmed)
	e.now, e.step = t0.Add(-time.Hour), 0
	ok(t, e.lib.Tally([]voices.ClipOutcome{{Clip: first, Landed: true, Named: []string{"A", "speaker_1"}}, {Clip: second}}))
	confirm, err := e.lib.Confirm(second)
	again, _ := e.lib.Confirm(second)
	if err != nil || confirm != voices.NewlyConfirmed || again != voices.AlreadyConfirmed {
		t.Fatalf("Confirm = %q, %v, and then %q", confirm, err, again)
	}
	e.overrule(first, 1)
	ok(t, e.lib.Remove(second))
	e.enrol("Lorentz", "two", voices.Unconfirmed)

	sum := sha256.Sum256(flac("one"))
	if want := (voices.ClipRef{Slug: "lorentz", ID: voices.ClipID(hex.EncodeToString(sum[:])[:12])}); first != want {
		t.Fatalf("ref = %v, want the first 12 hex digits of the sha256, %v", first, want)
	}
	layout := slices.Sorted(maps.Keys(e.files()))
	if want := slices.Sorted(slices.Values([]string{e.cfg, e.path(), e.path("lorentz"), e.clip(first), e.clip(second), e.path("lorentz", "events.jsonl"), e.path("lorentz", "voice.json")})); !slices.Equal(layout, want) {
		t.Fatalf("layout = %v, want %v", layout, want)
	}
	if got, _ := os.ReadFile(e.path("lorentz", "voice.json")); string(got) != `{"name":"Lorentz"}` {
		t.Fatalf("voice.json = %s, want the first writer's spelling", got)
	}
	if got, _ := os.ReadFile(e.clip(first)); !bytes.Equal(got, flac("one")) {
		t.Fatalf("the clip file holds %q", got)
	}
	eventID := regexp.MustCompile(`"id":"[0-9a-f]{16}"`)
	if found := eventID.FindAllString(e.log("lorentz"), -1); len(slices.Compact(slices.Sorted(slices.Values(found)))) != 9 {
		t.Fatalf("event ids %v, want nine distinct ones", found)
	}
	want := fmt.Sprintf(`{"id":"ID","at":"2026-11-26T12:01:00Z","event":"added","clip":"%[1]s","status":"confirmed","source":"voices add","seconds":6.5}
{"id":"ID","at":"2026-11-26T12:02:00Z","event":"added","clip":"%[2]s","status":"unconfirmed","source":"audio_remember_voice","seconds":6.5}
{"id":"ID","at":"2026-11-26T12:01:00.000000001Z","event":"placed","clip":"%[1]s","landed":true}
{"id":"ID","at":"2026-11-26T12:01:00.000000001Z","event":"named","clip":"%[1]s","speakers":["A","speaker_1"]}
{"id":"ID","at":"2026-11-26T12:02:00.000000001Z","event":"placed","clip":"%[2]s","landed":false}
{"id":"ID","at":"2026-11-26T12:02:00.000000002Z","event":"confirmed","clip":"%[2]s"}
{"id":"ID","at":"2026-11-26T12:01:00.000000002Z","event":"overruled","clip":"%[1]s"}
{"id":"ID","at":"2026-11-26T12:02:00.000000003Z","event":"removed","clip":"%[2]s"}
{"id":"ID","at":"2026-11-26T12:02:00.000000004Z","event":"added","clip":"%[2]s","status":"unconfirmed","source":"audio_remember_voice","seconds":6.5}
`, first.ID, second.ID)
	if got := eventID.ReplaceAllString(e.log("lorentz"), `"id":"ID"`); got != want {
		t.Fatalf("events.jsonl =\n%s\nwant\n%s", got, want)
	}
	clips := e.clips("lorentz")
	if len(clips) != 2 || clips[1].ID != first.ID || clips[1].Named != 1 || clips[1].Overruled != 1 || clips[0].Status != voices.Unconfirmed {
		t.Fatalf("clips = %s, want the first named and overruled once, the second in a new life", e.json(clips))
	}
}

// TestLibrary_ReadsTheSchemasExamples also holds List to exactly the fields
// `voices list -json` prints.
func TestLibrary_ReadsTheSchemasExamples(t *testing.T) {
	raw, err := os.ReadFile("schema/event.schema.json")
	var schema struct{ Examples []json.RawMessage }
	ok(t, errors.Join(err, json.Unmarshal(raw, &schema)))
	var log []string
	for _, example := range schema.Examples {
		var line bytes.Buffer
		ok(t, json.Compact(&line, example))
		log = append(log, line.String()+"\n")
	}
	e := newEnv(t)
	e.write("lorentz", `{ "name": "Lorentz" }`, log...)
	e.write("jean-luc-p", `{ "name": "Jean-Luc P." }`, ev(1, at(0), "added", "aaaaaaaaaaaa", learned(5)))
	put(t, e.path("lorentz", "e81d5530c6a9.flac"), "never added, then removed")
	before := e.files()
	want := `[{"name":"Jean-Luc P.","slug":"jean-luc-p","last_named":null,"clips":[` +
		`{"id":"aaaaaaaaaaaa","status":"unconfirmed","source":"audio_remember_voice","seconds":5,"added":"2026-11-18T09:00:00Z",` +
		`"named":0,"overruled":0,"last_named":null,"path":"/voices/jean-luc-p/aaaaaaaaaaaa.flac"}]},` +
		`{"name":"Lorentz","slug":"lorentz","last_named":"2026-11-25T10:00:03Z","clips":[` +
		`{"id":"9f3c2a1b0d4e","status":"confirmed","source":"voices add","seconds":8.4,"added":"2026-11-18T09:12:44.120394Z",` +
		`"named":1,"overruled":0,"last_named":"2026-11-25T10:00:03Z","path":"/voices/lorentz/9f3c2a1b0d4e.flac"},` +
		`{"id":"1a7d0f23c9b8","status":"confirmed","source":"audio_remember_voice","seconds":9.7,"added":"2026-11-24T16:02:10Z",` +
		`"named":0,"overruled":0,"last_named":null,"path":"/voices/lorentz/1a7d0f23c9b8.flac"}]}]`
	if got := e.json(e.list()); got != want {
		t.Fatalf("the examples read as\n%s\nwant\n%s", got, want)
	}
	e.unchanged(before)
}

func TestLibrary_FoldsTheLog(t *testing.T) {
	const x, y, z, v, r, w, q, h voices.ClipID = "a1a1a1a1a1a1", "b2b2b2b2b2b2", "c3c3c3c3c3c3", "d4d4d4d4d4d4",
		"e5e5e5e5e5e5", "f6f6f6f6f6f6", "070707070707", "181818181818"
	log := []string{
		ev(1, at(0), "added", x, learned(9.7)),
		ev(2, at(1), "named", x, spoke),
		ev(3, at(2), "overruled", x, ""),
		ev(4, at(3), "confirmed", x, `,"mood":"a field this reader does not know"`),
		ev(5, at(0), "added", y, learned(5)), // y is removed and added again: a new life
		ev(6, at(1), "named", y, spoke),
		ev(7, at(2), "removed", y, ""),
		ev(8, at(3), "added", y, confirmed(6)),
		ev(9, at(0), "added", z, confirmed(4)),
		ev(10, at(1), "removed", z, ""),
		ev(11, at(5), "added", v, confirmed(4)), // removed at the instant it was added
		ev(12, at(5), "removed", v, ""),
		ev(13, at(0), "added", r, learned(4)), // an earlier life's confirmation is not carried
		ev(14, at(1), "confirmed", r, ""),
		ev(15, at(2), "removed", r, ""),
		ev(16, at(3), "added", r, learned(7)),
		ev(17, at(1), "named", w, spoke), // never added
	}
	library := func(lines []string, noise func(e *env)) *env {
		e := newEnv(t)
		e.step = 0
		e.write("lorentz", `{"name":"Lorentz"}`, lines...)
		put(t, e.path("lorentz", "events.jsonl"), e.log("lorentz")+ev(18, at(0), "added", "0123456789ab", confirmed(4)))
		for _, unlogged := range []voices.ClipID{w, q} {
			put(t, e.clip(voices.ClipRef{Slug: "lorentz", ID: unlogged}), "a file no added event names")
		}
		if noise != nil {
			noise(e)
		}
		return e
	}
	clean := library(log, nil)
	want := map[voices.ClipID]string{x: "confirmed 9.7 s, named 1, overruled 1", y: "confirmed 6 s, named 0, overruled 0", r: "unconfirmed 7 s, named 0, overruled 0"}
	got := map[voices.ClipID]string{}
	for _, c := range clean.clips("lorentz") {
		got[c.ID] = fmt.Sprintf("%s %v s, named %d, overruled %d", c.Status, c.Seconds, c.Named, c.Overruled)
	}
	if !maps.Equal(got, want) {
		t.Fatalf("clips = %v\nwant %v: no removed clip, none without a file or an added event", got, want)
	}

	// Each refused line would add the clip q if it were read.
	base := ev(50, at(9), "added", q, confirmed(8))
	edit := func(old, text string) string { return strings.Replace(base, old, text, 1) }
	refused := []string{
		"{}\n", "[]\n", "null\n", `"added"` + "\n", "12\n", "{\n", " \n", "\n", "not json\n",
		edit(`"0000000000000032"`, `"000000000000032"`), edit(`"0000000000000032"`, `"00000000000000AB"`),
		edit(`"0000000000000032"`, `50`), edit(`"0000000000000032"`, `null`),
		edit(`09:09:00Z`, `09:09:00+02:00`), edit(`09:09:00Z`, `09:09:00`), edit(`09:09:00Z`, `09:09:00.1234567890Z`),
		edit(`T09:09`, ` 09:09`), edit(`"2026-`, `"26-`),
		edit(`"added"`, `"sung"`), edit(string(q), "XYZ"),
		edit(`"status":"confirmed"`, `"status":"maybe"`), edit(`"status":"confirmed"`, `"status":""`), edit(`"status":"confirmed",`, ``),
		edit(`"source":"voices add"`, `"source":"cli"`), edit(`"source":"voices add",`, ``),
		edit(`"seconds":8`, `"seconds":0`), edit(`"seconds":8`, `"seconds":-1`), edit(`"seconds":8`, `"seconds":"8"`), edit(`,"seconds":8`, ``),
		edit(`}`, `,"landed":"yes"}`), edit(`}`, `,"speakers":[]}`), edit(`}`, `,"speakers":[""]}`), edit(`}`, `,"speakers":[1]}`),
		ev(51, at(9), "placed", x, ""), ev(52, at(9), "named", x, ""),
		strings.TrimSuffix(ev(53, at(9), "removed", x, ""), "\n"),
	}
	reversed := slices.Clone(log)
	slices.Reverse(reversed)
	for name, other := range map[string]*env{
		"line order does not matter": library(reversed, nil),
		"an event id counts once":    library(slices.Concat(log, log), nil),
		"the union of two copies, each with lines the other lacks, is their merge": library(slices.Concat(log[:12], log[5:], log[:3]), nil),
		"lines that are not events and an unfinished last line are skipped":        library(slices.Concat(log, refused), nil),
		"dot-names and directories that are no voice are ignored": library(log, func(e *env) {
			added := ev(1, at(0), "added", q, confirmed(4))
			for slug, voiceJSON := range map[string]string{
				".lorentz": `{"name":"Lorentz"}`, "ghost": "", "someone": `{"name":"Lorentz"}`, "syrus": `{"name":"Syrus","age":3}`,
				"speaker-1": `{"name":"speaker-1"}`, "nameless": `{"name":null}`, "list": `["list"]`, "broken": `{"name":`,
			} {
				e.write(slug, voiceJSON, added)
			}
			for name, data := range map[string]string{
				"lorentz/." + string(h) + ".flac": "hidden", "lorentz/.1a7d0f23c9b8.flac.tmp": "another writer's",
				"lorentz/.events.jsonl": ev(9, at(9), "removed", x, ""), "notes.txt": "not a voice", ".DS_Store": "x",
				"lorentz/events.jsonl": e.log("lorentz") + ev(70, at(2), "added", h, confirmed(4)),
			} {
				put(t, e.path(name), data)
			}
		}),
	} {
		before := other.files()
		if got, want := other.json(other.list()), clean.json(clean.list()); got != want {
			t.Fatalf("%s: the library reads as\n%s\nwant, as the plain log\n%s", name, got, want)
		}
		other.unchanged(before)
	}
	one, other := ev(60, at(20), "named", x, spoke), ev(60, at(21), "overruled", x, "")
	first, second := library(slices.Concat(log, []string{one, other}), nil), library(slices.Concat([]string{other}, log, []string{one}), nil)
	if got, want := first.json(first.list()), second.json(second.list()); got != want {
		t.Fatalf("one id on two different lines reads by line order:\n%s\n%s", got, want)
	}
}

func TestNames_SlugsAndRefs(t *testing.T) {
	raw, err := os.ReadFile("schema/voice.schema.json")
	var schema struct {
		Properties struct {
			Name struct {
				Pattern string
				Not     struct{ Pattern string }
			}
		}
	}
	ok(t, errors.Join(err, json.Unmarshal(raw, &schema)))
	shape, reserved := regexp.MustCompile(schema.Properties.Name.Pattern), regexp.MustCompile(schema.Properties.Name.Not.Pattern)
	long39 := "Å" + strings.Repeat("ø", 38)
	for name, want := range map[string]string{
		"Lorentz": "", "Jean-Luc P.": "", "O’Brien": "", "Åsa 2": "", long39: "", "speaker-": "", "speakers-1": "", "unknown-1a": "", "speaker 3": "",
		"": "shape", long39 + "x": "shape", "a/b": "shape", ".x": "shape", " Lorentz": "shape", "Lorentz\n": "shape", "Lo\tr": "shape", "\xffab": "shape",
		"2pac":      `voice name "2pac" breaks the shape rule: a name is a letter followed by at most 38 letters, digits, spaces or . ' ’ -`,
		"Speaker-1": `voice name "Speaker-1" breaks the reserved rule: speaker-N and unknown-N are the labels a transcript gives`,
		"speaker-3": "reserved", "unknown-12": "reserved", "sPeAkEr-007": "reserved",
	} {
		got, err := voices.ParseName(name)
		if bySchema := shape.MatchString(name) && !reserved.MatchString(name); bySchema != (err == nil) {
			t.Errorf("ParseName(%q) = %v, but voice.schema.json accepts it: %v", name, err, bySchema)
		}
		if refusal, refused := errors.AsType[*voices.VoiceNameError](err); want == "" && string(got) != name ||
			want != "" && (!refused || got != "" || refusal.Name != name || string(refusal.Rule) != want && err.Error() != want) {
			t.Errorf("ParseName(%q) = %q, %v, want %q", name, got, err, want)
		}
	}
	for name, want := range map[voices.Name]voices.Slug{
		"Lorentz": "lorentz", "LORENTZ": "lorentz", "Jean-Luc P.": "jean-luc-p", "Jean Luc P": "jean-luc-p",
		"O’Brien": "o-brien", "Søren 2": "søren-2", "Åsa  .- 2.": "åsa-2", "": "-", "..": "-", "../etc": "etc", `a\b`: "a-b",
	} {
		if got := voices.SlugOf(name); got != want {
			t.Errorf("SlugOf(%q) = %q, want %q", name, got, want)
		}
	}
	ref, err := voices.ParseClipRef("søren-2/9f3c2a1b0d4e")
	if text, _ := ref.MarshalText(); err != nil || ref != (voices.ClipRef{Slug: "søren-2", ID: "9f3c2a1b0d4e"}) || string(text) != "søren-2/9f3c2a1b0d4e" {
		t.Fatalf("ParseClipRef = %+v, %v", ref, err)
	}
	for _, text := range []string{"lorentz", "/9f3c2a1b0d4e", "a/b/c", "", "lorentz/9F3C2A1B0D4E", "lorentz/9f3c", "lorentz/9f3c2a1b0d4e0", "lorentz/9f3c2a1b0d4g"} {
		ref, err := voices.ParseClipRef(text)
		want := fmt.Sprintf("clip reference %q is not <slug>/<id> with an id of 12 lower-case hex digits", text)
		if refusal, refused := errors.AsType[*voices.ClipRefError](err); !refused || refusal.Ref != text || err.Error() != want || ref != (voices.ClipRef{}) {
			t.Errorf("ParseClipRef(%q) = %+v, %v", text, ref, err)
		}
	}
}

func TestEnrol_Refusals(t *testing.T) {
	e := newEnv(t)
	for name, tc := range map[string]struct {
		edit  func(*voices.Enrolment)
		field string
	}{
		"a label a transcript gives": {func(en *voices.Enrolment) { en.Name = "speaker-2" }, ""},
		"a name that is no name":     {func(en *voices.Enrolment) { en.Name = "2pac" }, ""},
		"no audio":                   {func(en *voices.Enrolment) { en.Inspection.FLAC = nil }, "clip"},
		"no length":                  {func(en *voices.Enrolment) { en.Inspection.Seconds = 0 }, "seconds"},
	} {
		en := enrolment("Lorentz", "refused", voices.Confirmed)
		tc.edit(&en)
		got, err := e.lib.Enrol(en)
		if tc.field == "" {
			errAs[*voices.VoiceNameError](t, err)
		} else if refusal := errAs[*voices.EventError](t, err); refusal.Field != tc.field || !strings.Contains(err.Error(), `field "`+tc.field+`" is missing or wrong`) {
			t.Fatalf("%s: refused with %v, want the field %s", name, err, tc.field)
		}
		if got != (voices.Enrolled{}) || len(e.files()) != 1 {
			t.Fatalf("%s: the refused enrolment returned %+v or wrote %v", name, got, e.files())
		}
	}
	ref := e.enrol("Lorentz", "held", voices.Confirmed)
	before := e.files()
	_, err := e.lib.Enrol(enrolment("LORENTZ", "held", voices.Unconfirmed))
	if got := errAs[*voices.ClipExistsError](t, err); got.Ref != ref || err.Error() != "the library already holds clip "+ref.String() {
		t.Fatalf("the same clip twice: %v", err)
	}
	e.unchanged(before)
}

// cappedVoice gives Lorentz four confirmed and two unconfirmed clips. The
// lowest-ranked clip of all is confirmed; badFirst says whether the worse
// unconfirmed clip is the older one.
func cappedVoice(e *env, badFirst bool) (worse voices.ClipRef) {
	e.t.Helper()
	e.named(e.enrol("Lorentz", "c1", voices.Confirmed), 3)
	worst := e.enrol("Lorentz", "c2", voices.Confirmed)
	e.named(worst, 1)
	e.overrule(worst, 3)
	e.enrol("Lorentz", "c3", voices.Confirmed)
	e.enrol("Lorentz", "c4", voices.Confirmed)
	worse, better := e.enrol("Lorentz", "u1", voices.Unconfirmed), e.enrol("Lorentz", "u2", voices.Unconfirmed)
	if !badFirst {
		worse, better = better, worse
	}
	ok(e.t, e.lib.Tally([]voices.ClipOutcome{{Clip: worse}}))
	e.named(better, 2)
	if clips := e.clips("lorentz"); clips[5].ID != worst.ID {
		e.t.Fatalf("the fixture's lowest clip is %s, want the confirmed %s", clips[5].ID, worst)
	}
	return worse
}

func TestEnrol_AtTheCapPrunesTheLowestUnconfirmedClip(t *testing.T) {
	for status, keepFile := range map[voices.Status]bool{voices.Unconfirmed: false, voices.Confirmed: true} {
		e := newEnv(t)
		worse := cappedVoice(e, keepFile)
		want := slices.DeleteFunc(ids(e.clips("lorentz")), func(id voices.ClipID) bool { return id == worse.ID })
		e.lib.KeepFiles(func(path string) bool { return keepFile && path == e.clip(worse) })
		e.now, e.step = t0.Add(-time.Hour), 0 // the removal takes effect also when the clock stepped back
		added, err := e.lib.Enrol(enrolment("Lorentz", "seventh", status))
		ok(t, err)
		got := ids(e.clips("lorentz"))
		slices.Sort(got)
		if want = slices.Sorted(slices.Values(append(want, added.Ref.ID))); !slices.Equal(got, want) {
			t.Fatalf("after the seventh: %v, want %v", got, want)
		}
		if log := e.log("lorentz"); strings.Count(log, `"removed"`) != 1 || !strings.Contains(log, `"event":"removed","clip":"`+string(worse.ID)+`"`) {
			t.Fatalf("want one removed line, for %s:\n%s", worse, log)
		}
		_, statErr := os.Stat(e.clip(worse))
		if !keepFile && (added.Leftover != nil || !errors.Is(statErr, fs.ErrNotExist)) {
			t.Fatalf("leftover %v, the pruned file: %v; want it gone", added.Leftover, statErr)
		}
		if keepFile && (added.Leftover == nil || added.Leftover.Path != e.clip(worse) || !errors.Is(added.Leftover, fs.ErrPermission) || statErr != nil) {
			t.Fatalf("leftover %v, the pruned file: %v; want the file that cannot be deleted left and named", added.Leftover, statErr)
		}
	}
}

func TestEnrol_ConfirmedClipsAreKept(t *testing.T) {
	e := newEnv(t)
	var held []voices.ClipRef
	for i := range 6 {
		held = append(held, e.enrol("Lorentz", fmt.Sprint("kept", i), voices.Confirmed))
	}
	full := func(en voices.Enrolment) {
		t.Helper()
		before := e.files()
		got, err := e.lib.Enrol(en)
		refusal := errAs[*voices.VoiceFullError](t, err)
		if *refusal != (voices.VoiceFullError{Name: "Lorentz", Stored: 6}) || got != (voices.Enrolled{}) ||
			err.Error() != "Lorentz holds 6 clips, all confirmed: delete one before adding another" {
			t.Fatalf("Enrol = %+v, %v", got, err)
		}
		e.unchanged(before)
	}
	full(enrolment("lorentz", "seventh", voices.Confirmed))
	full(enrolment("lorentz", "seventh", voices.Unconfirmed))

	trim := enrolment("Lorentz", "trimmed", voices.Unconfirmed)
	trim.Replaces = held[2].ID
	trimmed, err := e.lib.Enrol(trim)
	if log := e.log("lorentz"); err != nil || len(e.clips("lorentz")) != 7 || strings.Count(log, "\n") != 7 {
		t.Fatalf("the trim: %v; want seven clips, for the one it replaces is not counted, and nothing removed:\n%s", err, log)
	}
	again := enrolment("Lorentz", "trimmed again", voices.Confirmed)
	again.Replaces = trimmed.Ref.ID
	full(again) // the unconfirmed clip it replaces is not pruned to make room
}

func TestLibrary_ConcurrentWritersLoseNothing(t *testing.T) {
	e := newEnv(t) // six libraries over one directory
	errs := make(chan error, 6)
	var wg sync.WaitGroup
	for i := range 6 {
		lib := voices.NewLibrary(e.cfg, func() time.Time { return t0 })
		wg.Go(func() {
			got, err := lib.Enrol(enrolment([]string{"Lorentz", "LORENTZ"}[i%2], fmt.Sprint("writer", i), voices.Confirmed))
			if err == nil {
				err = lib.Tally([]voices.ClipOutcome{{Clip: got.Ref, Landed: true, Named: []string{"A"}}})
			}
			for range 10 {
				if err == nil {
					_, err = lib.Overrule(got.Ref)
				}
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		ok(t, err)
	}
	clips, lines := e.clips("lorentz"), strings.SplitAfter(e.log("lorentz"), "\n")
	if len(clips) != 6 || slices.ContainsFunc(clips, func(c voices.ClipSummary) bool { return c.Named != 1 || c.Overruled != 10 }) {
		t.Fatalf("clips = %s, want six, each named once and overruled ten times", e.json(clips))
	}
	if len(lines) != 6+2*6+60+1 || slices.ContainsFunc(lines[:78], func(line string) bool { return !json.Valid([]byte(line)) }) {
		t.Fatalf("%d lines, want 78 whole ones:\n%s", len(lines)-1, e.log("lorentz"))
	}
	if got, _ := os.ReadFile(e.path("lorentz", "voice.json")); string(got) != `{"name":"Lorentz"}` && string(got) != `{"name":"LORENTZ"}` || len(e.files()) != 3+6+2 {
		t.Fatalf("voice.json = %s among %v; want one writer's spelling, six clips and no temporary file", got, slices.Sorted(maps.Keys(e.files())))
	}

	// A line torn by a failed writer is closed before the next.
	e = newEnv(t)
	ref := e.enrol("Lorentz", "torn", voices.Confirmed)
	put(t, e.path("lorentz", "events.jsonl"), e.log("lorentz")+`{"id":"00000000000000ff","at":"2026-11-`)
	got, err := e.lib.Overrule(ref)
	if log := e.log("lorentz"); err != nil || got.Overruled != 1 || e.clips("lorentz")[0].Overruled != 1 || strings.Count(log, "\n") != 3 {
		t.Fatalf("after a torn line: %+v, %v, log\n%s", got, err, log)
	}
}

func TestSelect_WhoIsNamed(t *testing.T) {
	e := newEnv(t)
	first, best := e.enrol("Lorentz", "l1", voices.Confirmed), e.enrol("Lorentz", "l2", voices.Confirmed)
	e.named(best, 9)
	ok(t, e.lib.Tally([]voices.ClipOutcome{{Clip: e.enrol("Lorentz", "l3", voices.Unconfirmed)}}))
	e.named(e.enrol("christoph", "c", voices.Confirmed), 3)
	e.named(e.enrol("Syrus", "s", voices.Confirmed), 5)
	ada := e.enrol("Ada", "a", voices.Unconfirmed)
	e.named(ada, 12)
	e.enrol("Dan", "d", voices.Confirmed)
	before := e.files()
	for _, tc := range []struct {
		name  string
		asked voices.Asked
		want  string
	}{
		{"the tool's people hide the flag's", voices.Asked{Tool: []string{"SYRUS"}, Flag: []string{"Lorentz"}, MaxVoices: 12}, "tool: Syrus"},
		{"the flag's people in the order asked, once each, confirmed or not", voices.Asked{Flag: []string{"lorentz", "Christoph", "Lorentz", "ada"}, MaxVoices: 12}, "flag: Lorentz christoph Ada"},
		{"nobody asked for: everyone with a confirmed clip, best clip first", voices.Asked{MaxVoices: 12}, "library: Lorentz Syrus christoph Dan"},
		{"max-voices caps the library's own choice", voices.Asked{MaxVoices: 2}, "library: Lorentz Syrus"},
		{"max-voices 0 names nobody", voices.Asked{}, ":"},
		{"people the library does not hold are listed as given, once", voices.Asked{Flag: []string{"Lorentz", "Bo", "speaker-1", "BO"}}, "flag: Lorentz; unknown Bo speaker-1"},
		{"nobody known is still something to say", voices.Asked{Tool: []string{"Bo"}}, "tool:; unknown Bo"},
	} {
		sel := e.lib.Select(tc.asked)
		got := string(sel.Origin) + ":"
		for _, v := range sel.Voices {
			got += " " + string(v.Name)
		}
		if len(sel.Unknown) > 0 {
			got += "; unknown " + strings.Join(sel.Unknown, " ")
		}
		if got != tc.want || sel.Unreadable != nil || sel.Empty() != (tc.want == ":") {
			t.Errorf("%s: Select = %s (%+v), want %s", tc.name, got, sel, tc.want)
		}
	}
	pick := func(ref voices.ClipRef) voices.Pick {
		return voices.Pick{Ref: ref, Status: voices.Confirmed, Seconds: 6.5, Path: e.clip(ref)}
	}
	picked := e.lib.Select(voices.Asked{Flag: []string{"Lorentz", "Christoph"}}).Voices
	if !slices.Equal(picked[0].Picks, []voices.Pick{pick(best), pick(first)}) || len(picked[1].Picks) != 1 || voices.PicksPerVoice != 2 || !filepath.IsAbs(picked[0].Picks[0].Path) {
		t.Fatalf("picked %+v, want Lorentz's two best clips of three and Christoph's only one", picked)
	}
	inNameOrder := regexp.MustCompile(`^\[{"name":"Ada".*{"name":"christoph".*{"name":"Dan".*{"name":"Lorentz".*{"name":"Syrus"`)
	if got := e.json(e.list()); !inNameOrder.MatchString(got) {
		t.Fatalf("listed %s, want the people in name order whatever the case", got)
	}
	e.unchanged(before)
	if got, err := e.lib.Confirm(ada); err != nil || got != voices.NewlyConfirmed || e.lib.Select(voices.Asked{MaxVoices: 1}).Voices[0].Name != "Ada" {
		t.Fatalf("Confirm = %q, %v, want Ada confirmed and now named by default", got, err)
	}
}

// TestLibrary_WithoutVoicesNothingChanges: with nobody to name there is
// nothing to say, and reading and selecting write nothing.
func TestLibrary_WithoutVoicesNothingChanges(t *testing.T) {
	gone := []string{ev(1, at(0), "added", "aaaaaaaaaaaa", confirmed(5)), ev(2, at(1), "removed", "aaaaaaaaaaaa", "")}
	for name, tc := range map[string]struct {
		setup func(e *env)
		list  string
	}{
		"no voices directory":         {func(*env) {}, "[]"},
		"an empty voices directory":   {func(e *env) { e.write(".", "") }, "[]"},
		"a person whose clip is gone": {func(e *env) { e.write("ada", `{"name":"Ada"}`, gone...) }, "[]"},
		"only unconfirmed clips":      {func(e *env) { e.enrol("Lorentz", "l", voices.Unconfirmed) }, `[{"name":"Lorentz"`},
	} {
		e := newEnv(t)
		tc.setup(e)
		before := e.files()
		ok(t, e.lib.Tally(nil))
		if sel := e.lib.Select(voices.Asked{MaxVoices: 12}); !reflect.DeepEqual(sel, voices.Selection{}) || !sel.Empty() {
			t.Fatalf("%s: Select = %+v, want the empty selection", name, sel)
		}
		if got := e.json(e.list()); !strings.HasPrefix(got, tc.list) {
			t.Fatalf("%s: List as JSON = %s, want %s", name, got, tc.list)
		}
		e.unchanged(before)
	}
}

// TestOverruleAndRemove: an overrule counts against the clip and leaves its
// status; a removed clip is out of the library, also when its file stays.
func TestOverruleAndRemove(t *testing.T) {
	e := newEnv(t)
	gone, kept, stuck := e.enrol("Lorentz", "r1", voices.Confirmed), e.enrol("Lorentz", "r2", voices.Confirmed), e.enrol("Lorentz", "r3", voices.Unconfirmed)
	syrus := e.enrol("Syrus", "s1", voices.Confirmed)
	ok(t, e.lib.Tally([]voices.ClipOutcome{{Clip: stuck, Landed: true, Named: []string{"A"}}, {Clip: syrus}}))
	e.named(stuck, 3)
	for i := 1; i <= 2; i++ {
		got, err := e.lib.Overrule(stuck)
		if listed := e.clips("lorentz"); err != nil || got.Overruled != i || got.Named != 4 || got.Status != voices.Unconfirmed || e.json(got) != e.json(listed[slices.Index(ids(listed), stuck.ID)]) {
			t.Fatalf("overrule %d = %+v, %v\nlisted: %s", i, got, err, e.json(listed))
		}
	}
	other := e.log("syrus")
	ok(t, e.lib.Remove(gone))
	sel := e.lib.Select(voices.Asked{Flag: []string{"Lorentz"}})
	if _, err := os.Stat(e.clip(gone)); !errors.Is(err, fs.ErrNotExist) || !slices.Equal(ids(e.clips("lorentz")), []voices.ClipID{stuck.ID, kept.ID}) || len(sel.Voices[0].Picks) != 2 ||
		e.log("syrus") != other || strings.Count(other, "\n") != 2 {
		t.Fatalf("after the removal: clips %v, picks %+v, the file: %v, Syrus's log:\n%s", ids(e.clips("lorentz")), sel.Voices, err, e.log("syrus"))
	}
	e.lib.KeepFiles(func(path string) bool { return path == e.clip(stuck) })
	err := e.lib.Remove(stuck)
	want := "write voice library " + e.clip(stuck) + ": remove " + e.clip(stuck) + ": permission denied"
	if got := errAs[*voices.LibraryWriteError](t, err); got.Path != e.clip(stuck) || !errors.Is(err, fs.ErrPermission) || err.Error() != want {
		t.Fatalf("Remove = %v, want %s", err, want)
	}
	if _, statErr := os.Stat(e.clip(stuck)); statErr != nil || !slices.Equal(ids(e.clips("lorentz")), []voices.ClipID{kept.ID}) {
		t.Fatalf("a clip whose file cannot be deleted is still held, or its file is gone (%v)", statErr)
	}

	before := e.files()
	for _, ref := range []voices.ClipRef{gone, {Slug: "ada", ID: "ffffffffffff"}, {Slug: "lorentz", ID: "ffffffffffff"}} {
		_, confirm := e.lib.Confirm(ref)
		_, overrule := e.lib.Overrule(ref)
		for member, err := range map[string]error{"Confirm": confirm, "Overrule": overrule, "Remove": e.lib.Remove(ref)} {
			if got := errAs[*voices.ClipNotFoundError](t, err); got.Ref != ref || err.Error() != "the library holds no clip "+ref.String() {
				t.Fatalf("%s(%s) = %v", member, ref, err)
			}
		}
		ok(t, e.lib.Tally([]voices.ClipOutcome{{Clip: ref, Landed: true, Named: []string{"A"}}}))
	}
	e.unchanged(before) // the outcome of a clip no longer held is skipped
}

// writes runs every member that writes, about one clip and one new one.
func writes(lib *voices.Library, ref voices.ClipRef) map[string]error {
	_, confirm := lib.Confirm(ref)
	_, overrule := lib.Overrule(ref)
	_, enrol := lib.Enrol(enrolment("Lorentz", "another", voices.Confirmed))
	tally := lib.Tally([]voices.ClipOutcome{{Clip: ref, Landed: true}})
	return map[string]error{"Confirm": confirm, "Overrule": overrule, "Remove": lib.Remove(ref), "Enrol": enrol, "Tally": tally}
}

func TestLibrary_ReadAndWriteFailures(t *testing.T) {
	ref := voices.ClipRef{Slug: "lorentz", ID: voices.ClipIDOf(flac("kept"))}
	// The entry a read error names, and the files that put a file where a
	// directory goes, or a directory where a file goes.
	for unreadable, files := range map[string][]string{"": {""}, "lorentz/events.jsonl": {"lorentz/voice.json", "lorentz/events.jsonl/x"}} {
		e := newEnv(t)
		for _, file := range files {
			put(t, e.path(file), `{"name":"Lorentz"}`)
		}
		path, before := e.path(unreadable), e.files()
		list, err := e.lib.List()
		if got := errAs[*voices.LibraryReadError](t, err); list != nil || got.Path != path || got.Unwrap() == nil || !strings.HasPrefix(err.Error(), "read voice library "+path+": ") {
			t.Fatalf("List = %v, %v, want a read error naming %s", list, err, path)
		}
		for member, err := range writes(e.lib, ref) {
			if got := errAs[*voices.LibraryReadError](t, err); !strings.HasPrefix(got.Path, e.path()) {
				t.Fatalf("%s = %v, want a read error", member, err)
			}
		}
		sel := e.lib.Select(voices.Asked{Flag: []string{"Lorentz"}, MaxVoices: 12})
		if sel.Origin != voices.OriginFlag || len(sel.Voices)+len(sel.Unknown) != 0 || sel.Empty() || sel.Unreadable == nil || sel.Unreadable.Path != path {
			t.Fatalf("people named: Select = %+v, want Unreadable for %s alone", sel, path)
		}
		if sel := e.lib.Select(voices.Asked{MaxVoices: 12}); !reflect.DeepEqual(sel, voices.Selection{}) {
			t.Fatalf("nobody named: Select = %+v, want the empty selection", sel)
		}
		e.unchanged(before)
	}
	e := newEnv(t) // a log that cannot be appended to
	e.enrol("Lorentz", "kept", voices.Unconfirmed)
	listed, log := e.json(e.list()), e.path("lorentz", "events.jsonl")
	e.chmod(log, 0o444)
	for member, err := range writes(e.lib, ref) {
		if got := errAs[*voices.LibraryWriteError](t, err); got.Path != log || !strings.HasPrefix(err.Error(), "write voice library "+log+": ") {
			t.Fatalf("%s = %v, want a write error naming the log", member, err)
		}
	}
	if _, err := os.Stat(e.clip(ref)); err != nil || e.json(e.list()) != listed {
		t.Fatalf("a failed write changed the library, or deleted the clip (%v)", err)
	}
	clipID := string(voices.ClipIDOf(flac("new")))
	for name, tc := range map[string]struct {
		setup func(e *env)
		path  string
	}{
		"a voice directory that cannot be written":  {func(e *env) { e.enrol("Lorentz", "kept", voices.Confirmed); e.chmod(e.path("lorentz"), 0o555) }, "lorentz/." + clipID + ".flac.tmp"},
		"a voices directory that cannot be written": {func(e *env) { e.enrol("Syrus", "kept", voices.Confirmed); e.chmod(e.path(), 0o555) }, "lorentz"},
		"a directory where the clip file goes": {func(e *env) {
			e.write("lorentz", `{"name":"Lorentz"}`)
			put(e.t, e.path("lorentz", clipID+".flac", "x"), "")
		}, "lorentz/" + clipID + ".flac"},
		"a voice directory that is a symbolic link":  {func(e *env) { e.write("elsewhere", ""); ok(e.t, os.Symlink(e.path("elsewhere"), e.path("lorentz"))) }, "lorentz"},
		"another writer's unfinished temporary file": {func(e *env) { put(e.t, e.path("lorentz", "."+clipID+".flac.tmp"), "fLaC") }, "lorentz/." + clipID + ".flac.tmp"},
		"an empty voice.json":                        {func(e *env) { put(e.t, e.path("lorentz", "voice.json"), "") }, "lorentz/voice.json"},
		"a voice.json cut short":                     {func(e *env) { e.write("lorentz", `{"na`) }, "lorentz/voice.json"},
		"a voice.json after a byte order mark":       {func(e *env) { e.write("lorentz", "\xef\xbb\xbf"+`{"name":"Lorentz"}`) }, "lorentz/voice.json"},
		"a voice.json of another person":             {func(e *env) { e.write("lorentz", `{"name":"Lorenzo"}`) }, "lorentz/voice.json"},
	} {
		e := newEnv(t)
		tc.setup(e)
		before := e.files()
		got, err := e.lib.Enrol(enrolment("Lorentz", "new", voices.Confirmed))
		if refusal := errAs[*voices.LibraryWriteError](t, err); refusal.Path != e.path(tc.path) || refusal.Unwrap() == nil || got != (voices.Enrolled{}) {
			t.Fatalf("%s: Enrol = %+v, %v, want a write error naming %s", name, got, err, tc.path)
		}
		e.unchanged(before)
	}
}
