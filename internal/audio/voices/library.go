package voices

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/diag"
)

const (
	// PicksPerVoice is how many clips of one person go into a naming request.
	PicksPerVoice = 2
	// maxClips is how many clips the library stores per person.
	maxClips = 6
)

// Library is the voice library under <configDir>/voices. It keeps nothing
// between calls, so several over one directory, in one process or in
// several, see each other's writes: every fact is one appended event line,
// a clip file is written once and never rewritten, and voice.json is created
// exclusively.
type Library struct {
	dir string
	now func() time.Time
	// removeFile deletes a removed clip's file; a test replaces it.
	removeFile func(path string) error
}

func NewLibrary(configDir string, now func() time.Time) *Library {
	return &Library{dir: filepath.Join(configDir, Dir), now: now, removeFile: os.Remove}
}

// Enrolment is one clip to add to one person.
type Enrolment struct {
	Name       string
	Inspection Inspection
	Status     Status
	Source     Source
	// Replaces is the person's clip this one stands in for (a trim), or
	// empty. It does not count against the stored-clip cap and is never
	// pruned; removing it stays the caller's.
	Replaces ClipID
}

// Enrolled is a clip Enrol added.
type Enrolled struct {
	Ref ClipRef
	// Leftover is set when the clip pruned to make room is out of the
	// library but its file could not be deleted.
	Leftover *LibraryWriteError
}

// ClipSummary is one clip as `voices list -json` prints it.
type ClipSummary struct {
	ID        ClipID     `json:"id"`
	Status    Status     `json:"status"`
	Source    Source     `json:"source"`
	Seconds   float64    `json:"seconds"`
	Added     time.Time  `json:"added"`
	Named     int        `json:"named"`
	Overruled int        `json:"overruled"`
	LastNamed *time.Time `json:"last_named"`
	Path      string     `json:"path"`
}

// VoiceSummary is one person as `voices list -json` prints them, clips best
// first.
type VoiceSummary struct {
	Name      Name          `json:"name"`
	Slug      Slug          `json:"slug"`
	LastNamed *time.Time    `json:"last_named"`
	Clips     []ClipSummary `json:"clips"`
}

// Asked is what a run says about whom to name: the tool call's people
// outrank the flag's, and with neither the library picks its own.
type Asked struct {
	Tool []string
	Flag []string
	// MaxVoices caps the people the library picks when nobody is named.
	MaxVoices int
}

// Origin says who chose a selection's people.
type Origin string

const (
	OriginNone    Origin = ""
	OriginTool    Origin = "tool"
	OriginFlag    Origin = "flag"
	OriginLibrary Origin = "library"
)

// Pick is one clip that goes into a naming request.
type Pick struct {
	Ref     ClipRef
	Status  Status
	Seconds float64
	Path    string
}

// SelectedVoice is one person a run names, with their picked clips, best
// first.
type SelectedVoice struct {
	Name  Name
	Slug  Slug
	Picks []Pick
}

// Selection is the people a run names.
type Selection struct {
	Origin Origin
	Voices []SelectedVoice
	// Unknown are the named people the library does not hold, as given.
	Unknown []string
	// Unreadable is set when people were named and the library could not be
	// read.
	Unreadable *LibraryReadError
}

// Empty reports that the run names nobody and has nothing to say about it:
// it then does exactly what it did before there were voices.
func (s Selection) Empty() bool {
	return len(s.Voices) == 0 && len(s.Unknown) == 0 && s.Unreadable == nil
}

// ClipOutcome is how one picked clip fared in the naming of one recording.
type ClipOutcome struct {
	Clip ClipRef
	// Landed: the clip was recognised as one speaker in a naming request.
	Landed bool
	// Named are the transcript speaker labels that took the person's name
	// from this clip.
	Named []string
}

// Confirmation says what confirming a clip did.
type Confirmation string

const (
	NewlyConfirmed   Confirmation = "confirmed"
	AlreadyConfirmed Confirmation = "already-confirmed"
)

type ClipExistsError struct{ Ref ClipRef }

func (e *ClipExistsError) Error() string {
	return fmt.Sprintf("the library already holds clip %s", e.Ref)
}

// VoiceFullError: the person holds the most clips the library stores and
// every one is confirmed, so none may go.
type VoiceFullError struct {
	Name   Name
	Stored int
}

func (e *VoiceFullError) Error() string {
	return fmt.Sprintf("%s holds %d clips, all confirmed: delete one before adding another", e.Name, e.Stored)
}

type ClipNotFoundError struct{ Ref ClipRef }

func (e *ClipNotFoundError) Error() string { return fmt.Sprintf("the library holds no clip %s", e.Ref) }

type LibraryWriteError struct {
	Path string
	Err  error
}

func (e *LibraryWriteError) Error() string {
	return fmt.Sprintf("write voice library %s: %v", e.Path, e.Err)
}
func (e *LibraryWriteError) Unwrap() error { return e.Err }

func (l *Library) voiceDir(slug Slug) string { return filepath.Join(l.dir, string(slug)) }

func (l *Library) clipPath(ref ClipRef) string {
	return filepath.Join(l.voiceDir(ref.Slug), string(ref.ID)+ClipExt)
}

// Enrol adds one inspected clip to the person named, creating the person
// with the name as given when the library does not hold them. A person at
// the stored-clip cap loses their lowest-ranked unconfirmed clip first, and
// is refused with a *VoiceFullError when every clip is confirmed. A refusal
// writes nothing.
func (l *Library) Enrol(e Enrolment) (Enrolled, error) {
	name, err := ParseName(e.Name)
	if err != nil {
		return Enrolled{}, err
	}
	if len(e.Inspection.FLAC) == 0 {
		return Enrolled{}, &EventError{Kind: string(eventAdded), Field: "clip"}
	}
	ref := ClipRef{Slug: SlugOf(name), ID: ClipIDOf(e.Inspection.FLAC)}
	dir := l.voiceDir(ref.Slug)
	v, err := readVoice(dir)
	if err != nil {
		return Enrolled{}, err
	}
	if v.clip(ref.ID) != nil {
		return Enrolled{}, &ClipExistsError{Ref: ref}
	}
	now := l.now()
	pruned, err := v.makeRoom(e.Replaces, now)
	if err != nil {
		return Enrolled{}, err
	}
	added := event{Kind: eventAdded, Clip: ref.ID, Status: e.Status, Source: e.Source, Seconds: e.Inspection.Seconds}
	events := []event{added}
	if pruned != nil {
		events = []event{{Kind: eventRemoved, Clip: pruned.ref.ID}, added}
	}
	lines, err := v.stamp(events, now)
	if err != nil {
		return Enrolled{}, err
	}
	if v == nil {
		if err := checkVoiceDir(dir, ref.Slug); err != nil {
			return Enrolled{}, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Enrolled{}, &LibraryWriteError{Path: dir, Err: err}
	}
	if err := placeClip(dir, ref.ID, e.Inspection.FLAC); err != nil {
		return Enrolled{}, err
	}
	if err := appendEvents(dir, lines); err != nil {
		return Enrolled{}, err
	}
	if v == nil {
		if err := createVoice(dir, name); err != nil {
			return Enrolled{}, err
		}
	}
	enrolled := Enrolled{Ref: ref}
	if pruned != nil {
		enrolled.Leftover = l.deleteClip(pruned.ref)
	}
	return enrolled, nil
}

// makeRoom returns the clip to prune so one more fits under the cap, nil
// when there is room.
func (v *voice) makeRoom(replaces ClipID, now time.Time) (*clip, error) {
	if v == nil {
		return nil, nil
	}
	counted := slices.DeleteFunc(slices.Clone(v.clips), func(c clip) bool { return c.ref.ID == replaces })
	if len(counted) < maxClips {
		return nil, nil
	}
	ranked := rank(counted, now)
	for i := len(ranked) - 1; i >= 0; i-- {
		if ranked[i].status == Unconfirmed {
			return &ranked[i], nil
		}
	}
	return nil, &VoiceFullError{Name: v.name, Stored: len(counted)}
}

// List returns every person who holds a clip, in name order, each person's
// clips best first.
func (l *Library) List() ([]VoiceSummary, error) {
	lib, err := readLibrary(l.dir)
	if err != nil {
		return nil, err
	}
	now := l.now()
	people := []VoiceSummary{}
	for _, v := range lib {
		if len(v.clips) == 0 {
			continue
		}
		person := VoiceSummary{Name: v.name, Slug: v.slug, Clips: make([]ClipSummary, 0, len(v.clips))}
		for _, c := range rank(v.clips, now) {
			summary := l.summary(c)
			if summary.LastNamed != nil && (person.LastNamed == nil || summary.LastNamed.After(*person.LastNamed)) {
				person.LastNamed = summary.LastNamed
			}
			person.Clips = append(person.Clips, summary)
		}
		people = append(people, person)
	}
	slices.SortFunc(people, func(a, b VoiceSummary) int {
		if c := strings.Compare(strings.ToLower(string(a.Name)), strings.ToLower(string(b.Name))); c != 0 {
			return c
		}
		return strings.Compare(string(a.Slug), string(b.Slug))
	})
	return people, nil
}

func (l *Library) summary(c clip) ClipSummary {
	s := ClipSummary{
		ID: c.ref.ID, Status: c.status, Source: c.source, Seconds: c.seconds, Added: c.added,
		Path: l.clipPath(c.ref),
	}
	for _, e := range c.events {
		switch e.Kind {
		case eventNamed:
			s.Named++
			if s.LastNamed == nil || e.At.After(*s.LastNamed) {
				s.LastNamed = &e.At
			}
		case eventOverruled:
			s.Overruled++
		}
	}
	return s
}

// Select resolves whom a run names and picks their clips. It has no error:
// what went wrong is in the Selection. With nobody named, a library that
// cannot be read or holds no confirmed clip is the empty selection.
func (l *Library) Select(asked Asked) Selection {
	named, origin := asked.Tool, OriginTool
	if len(named) == 0 {
		named, origin = asked.Flag, OriginFlag
	}
	if len(named) == 0 && asked.MaxVoices < 1 {
		return Selection{}
	}
	lib, err := readLibrary(l.dir)
	if err != nil && len(named) == 0 {
		return Selection{}
	}
	if readErr, ok := errors.AsType[*LibraryReadError](err); ok {
		return Selection{Origin: origin, Unreadable: readErr}
	}
	now := l.now()
	if len(named) == 0 {
		return l.selectConfirmed(lib, asked.MaxVoices, now)
	}
	sel := Selection{Origin: origin}
	for _, person := range named {
		i := slices.IndexFunc(lib, func(v voice) bool {
			return len(v.clips) > 0 && (strings.EqualFold(person, string(v.name)) || strings.EqualFold(person, string(v.slug)))
		})
		switch {
		case i < 0 && !slices.ContainsFunc(sel.Unknown, func(u string) bool { return strings.EqualFold(u, person) }):
			sel.Unknown = append(sel.Unknown, person)
		case i >= 0 && !slices.ContainsFunc(sel.Voices, func(s SelectedVoice) bool { return s.Slug == lib[i].slug }):
			sel.Voices = append(sel.Voices, l.selected(lib[i], rank(lib[i].clips, now)))
		}
	}
	return sel
}

// selectConfirmed is everyone with a confirmed clip, in the rank order of
// each person's best clip, up to maxVoices people.
func (l *Library) selectConfirmed(lib []voice, maxVoices int, now time.Time) Selection {
	ranked := map[Slug][]clip{}
	var bests []clip
	for _, v := range lib {
		if slices.ContainsFunc(v.clips, func(c clip) bool { return c.status == Confirmed }) {
			ranked[v.slug] = rank(v.clips, now)
			bests = append(bests, ranked[v.slug][0])
		}
	}
	if len(bests) == 0 {
		return Selection{}
	}
	sel := Selection{Origin: OriginLibrary}
	for _, best := range rank(bests, now)[:min(maxVoices, len(bests))] {
		i := slices.IndexFunc(lib, func(v voice) bool { return v.slug == best.ref.Slug })
		sel.Voices = append(sel.Voices, l.selected(lib[i], ranked[best.ref.Slug]))
	}
	return sel
}

func (l *Library) selected(v voice, ranked []clip) SelectedVoice {
	out := SelectedVoice{Name: v.name, Slug: v.slug}
	for _, c := range ranked[:min(PicksPerVoice, len(ranked))] {
		out.Picks = append(out.Picks, Pick{Ref: c.ref, Status: c.status, Seconds: c.seconds, Path: l.clipPath(c.ref)})
	}
	return out
}

// Tally appends how each picked clip fared in one naming: whether it was
// placed, and whom it named. An outcome whose clip the library no longer
// holds is skipped. Nothing is appended unless every line could be built.
func (l *Library) Tally(outcomes []ClipOutcome) error {
	now := l.now()
	var slugs []Slug
	events := map[Slug][]event{}
	people := map[Slug]*voice{}
	for _, o := range outcomes {
		v, read := people[o.Clip.Slug]
		if !read {
			var err error
			if v, err = readVoice(l.voiceDir(o.Clip.Slug)); err != nil {
				return err
			}
			people[o.Clip.Slug] = v
		}
		if v.clip(o.Clip.ID) == nil {
			continue
		}
		if events[o.Clip.Slug] == nil {
			slugs = append(slugs, o.Clip.Slug)
		}
		events[o.Clip.Slug] = append(events[o.Clip.Slug], event{Kind: eventPlaced, Clip: o.Clip.ID, Landed: &o.Landed})
		if len(o.Named) > 0 {
			events[o.Clip.Slug] = append(events[o.Clip.Slug], event{Kind: eventNamed, Clip: o.Clip.ID, Speakers: o.Named})
		}
	}
	lines := make([][]byte, len(slugs))
	for i, slug := range slugs {
		var err error
		if lines[i], err = people[slug].stamp(events[slug], now); err != nil {
			return err
		}
	}
	for i, slug := range slugs {
		if err := appendEvents(l.voiceDir(slug), lines[i]); err != nil {
			return err
		}
	}
	return nil
}

// Confirm makes an unconfirmed clip confirmed; on a confirmed clip it
// changes nothing.
func (l *Library) Confirm(ref ClipRef) (Confirmation, error) {
	v, c, err := l.held(ref)
	if err != nil {
		return "", err
	}
	if c.status == Confirmed {
		return AlreadyConfirmed, nil
	}
	return NewlyConfirmed, l.record(v, ref, eventConfirmed)
}

// Overrule counts one wrong naming against the clip and returns the clip as
// List now shows it. The clip's status does not change.
func (l *Library) Overrule(ref ClipRef) (ClipSummary, error) {
	v, c, err := l.held(ref)
	if err != nil {
		return ClipSummary{}, err
	}
	if err := l.record(v, ref, eventOverruled); err != nil {
		return ClipSummary{}, err
	}
	summary := l.summary(*c)
	summary.Overruled++
	return summary, nil
}

// Remove takes one clip out of the library and deletes its file. Once the
// removal is recorded the clip is gone, even when the deletion fails.
func (l *Library) Remove(ref ClipRef) error {
	v, _, err := l.held(ref)
	if err != nil {
		return err
	}
	if err := l.record(v, ref, eventRemoved); err != nil {
		return err
	}
	if left := l.deleteClip(ref); left != nil {
		return left
	}
	return nil
}

func (l *Library) held(ref ClipRef) (*voice, *clip, error) {
	v, err := readVoice(l.voiceDir(ref.Slug))
	if err != nil {
		return nil, nil, err
	}
	c := v.clip(ref.ID)
	if c == nil {
		return nil, nil, &ClipNotFoundError{Ref: ref}
	}
	return v, c, nil
}

// record appends one event about a clip v holds.
func (l *Library) record(v *voice, ref ClipRef, kind eventKind) error {
	lines, err := v.stamp([]event{{Kind: kind, Clip: ref.ID}}, l.now())
	if err != nil {
		return err
	}
	return appendEvents(l.voiceDir(ref.Slug), lines)
}

func (l *Library) deleteClip(ref ClipRef) *LibraryWriteError {
	path := l.clipPath(ref)
	if err := l.removeFile(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &LibraryWriteError{Path: path, Err: err}
	}
	return nil
}

// stamp gives each event a fresh id and its time, and renders the lines of
// one append. The time is now, or one nanosecond after the clip's latest
// event in the log when that is not before now: so the event takes effect
// when the log is folded, also when the clock stepped back or stands still.
func (v *voice) stamp(events []event, now time.Time) ([]byte, error) {
	var lines []byte
	for _, e := range events {
		e.At = now
		if v != nil {
			if last, logged := v.latest[e.Clip]; logged && !last.Before(now) {
				e.At = last.Add(time.Nanosecond)
			}
		}
		var id [8]byte
		_, _ = rand.Read(id[:])
		e.ID = hex.EncodeToString(id[:])
		line, err := e.encode()
		if err != nil {
			return nil, err
		}
		lines = append(lines, line...)
	}
	return lines, nil
}

// appendEvents appends whole lines to a voice's log in one write. A last
// line a failed writer left without its newline is closed first, so it
// cannot swallow the first line appended after it.
func appendEvents(dir string, lines []byte) error {
	path := filepath.Join(dir, eventsFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return &LibraryWriteError{Path: path, Err: err}
	}
	if info, statErr := f.Stat(); statErr == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			lines = append([]byte{'\n'}, lines...)
		}
	}
	_, err = f.Write(lines)
	if err := errors.Join(err, f.Close()); err != nil {
		return &LibraryWriteError{Path: path, Err: err}
	}
	return nil
}

// errOtherWriter marks a step that found another writer mid-write.
var errOtherWriter = errors.New("another writer holds the file")

// awaitWriter repeats step while it waits on another writer, 63 ms in all.
func awaitWriter(step func() error) error {
	for wait := time.Millisecond; ; wait *= 2 {
		err := step()
		if !errors.Is(err, errOtherWriter) || wait > 32*time.Millisecond {
			return err
		}
		time.Sleep(wait)
	}
}

// placeClip writes a clip file once: exclusively as .<id>.flac.tmp, then
// renamed into place, so a reader never sees a partial clip.
func placeClip(dir string, id ClipID, flac []byte) error {
	path := filepath.Join(dir, string(id)+ClipExt)
	tmp := filepath.Join(dir, "."+string(id)+ClipExt+".tmp")
	return awaitWriter(func() error {
		if isFile(path) {
			return nil
		}
		err := writeExclusive(tmp, flac)
		if errors.Is(err, fs.ErrExist) {
			return &LibraryWriteError{Path: tmp, Err: errors.Join(errOtherWriter, err)}
		}
		if err != nil {
			return &LibraryWriteError{Path: tmp, Err: err}
		}
		if err := os.Rename(tmp, path); err != nil {
			return &LibraryWriteError{Path: path, Err: errors.Join(err, os.Remove(tmp))}
		}
		return nil
	})
}

// createVoice creates voice.json exclusively, so the first writer's name is
// kept, and reads back what is in place.
func createVoice(dir string, name Name) error {
	path := filepath.Join(dir, voiceFile)
	record, err := json.Marshal(struct {
		Name Name `json:"name"`
	}{name})
	if err == nil {
		err = writeExclusive(path, record)
	}
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return &LibraryWriteError{Path: path, Err: err}
	}
	return checkVoiceDir(dir, SlugOf(name))
}

// checkVoiceDir checks that nothing but this voice stands at dir: no file,
// and no voice.json of another name. An empty voice.json is another
// writer's exclusive create, read again.
func checkVoiceDir(dir string, slug Slug) error {
	path := filepath.Join(dir, voiceFile)
	return awaitWriter(func() error {
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err == nil && !info.IsDir() {
			err = fmt.Errorf("not a directory (mode %v)", info.Mode())
		}
		if err != nil {
			return &LibraryWriteError{Path: dir, Err: err}
		}
		name, err := readVoiceName(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err == nil && SlugOf(name) != slug:
			err = fmt.Errorf("it names %q, whose slug is not %q", name, slug)
		case err != nil:
			if info, statErr := os.Stat(path); statErr == nil && info.Size() == 0 {
				err = errors.Join(errOtherWriter, err)
			}
			err = diag.Errorf("not a record of voice.schema.json: %w", err)
		}
		if err != nil {
			return &LibraryWriteError{Path: path, Err: err}
		}
		return nil
	})
}

// writeExclusive creates path, which must not exist, and fills it; a file it
// could not fill is removed.
func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err := errors.Join(err, f.Close()); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}

func isFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}
