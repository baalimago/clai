package voices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type eventKind string

const (
	eventAdded     eventKind = "added"
	eventConfirmed eventKind = "confirmed"
	eventOverruled eventKind = "overruled"
	eventRemoved   eventKind = "removed"
	eventPlaced    eventKind = "placed"
	eventNamed     eventKind = "named"
)

// event is one line of events.jsonl, a record of schema/event.schema.json.
// Status, Source and Seconds belong to an added event, Landed to a placed
// one and Speakers to a named one.
type event struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Kind     eventKind `json:"event"`
	Clip     ClipID    `json:"clip"`
	Status   Status    `json:"status,omitempty"`
	Source   Source    `json:"source,omitempty"`
	Seconds  float64   `json:"seconds,omitempty"`
	Landed   *bool     `json:"landed,omitempty"`
	Speakers []string  `json:"speakers,omitempty"`
}

// EventError refuses an event the schema does not allow. Field is the JSON
// field that is missing or wrong.
type EventError struct {
	ID    string
	Kind  string
	Field string
}

func (e *EventError) Error() string {
	return fmt.Sprintf("event %q (%s) is not a record of event.schema.json: field %q is missing or wrong", e.ID, e.Kind, e.Field)
}

// LibraryReadError says the library could not be read: Path is the entry
// that failed.
type LibraryReadError struct {
	Path string
	Err  error
}

func (e *LibraryReadError) Error() string {
	return fmt.Sprintf("read voice library %s: %v", e.Path, e.Err)
}
func (e *LibraryReadError) Unwrap() error { return e.Err }

var (
	eventIDShape = regexp.MustCompile(`^[0-9a-f]{16}$`)
	// eventTimeShape is the schema's pattern for at: UTC, written with Z.
	eventTimeShape = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$`)
)

// validate is the schema's rules for a decoded event.
func (e event) validate() error {
	bad := func(field string) error { return &EventError{ID: e.ID, Kind: string(e.Kind), Field: field} }
	added := e.Kind == eventAdded
	year := e.At.UTC().Year()
	switch {
	case !eventIDShape.MatchString(e.ID):
		return bad("id")
	case e.At.IsZero() || year < 0 || year > 9999:
		return bad("at")
	case !slices.Contains([]eventKind{eventAdded, eventConfirmed, eventOverruled, eventRemoved, eventPlaced, eventNamed}, e.Kind):
		return bad("event")
	case !clipIDShape.MatchString(string(e.Clip)):
		return bad("clip")
	case e.Status != Confirmed && e.Status != Unconfirmed && (added || e.Status != ""):
		return bad("status")
	case e.Source != SourceAdd && e.Source != SourceRemember && (added || e.Source != ""):
		return bad("source")
	case e.Seconds < 0 || math.IsNaN(e.Seconds) || math.IsInf(e.Seconds, 0) || added && e.Seconds == 0:
		return bad("seconds")
	case e.Kind == eventPlaced && e.Landed == nil:
		return bad("landed")
	case slices.Contains(e.Speakers, "") || e.Kind == eventNamed && len(e.Speakers) == 0:
		return bad("speakers")
	}
	return nil
}

// encode renders the line a writer appends: one JSON object and a newline.
func (e event) encode() ([]byte, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	e.At = e.At.UTC()
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, &EventError{ID: e.ID, Kind: string(e.Kind), Field: "speakers"}
	}
	return line.Bytes(), nil
}

// decodeEvent reads one complete line. A field given as an empty value is
// wrong, as the schema says, even where the field is optional.
func decodeEvent(line []byte) (event, error) {
	var raw struct {
		event
		At       string    `json:"at"`
		Status   *Status   `json:"status"`
		Source   *Source   `json:"source"`
		Seconds  *float64  `json:"seconds"`
		Speakers *[]string `json:"speakers"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return event{}, err
	}
	e := raw.event
	bad := func(field string) (event, error) {
		return event{}, &EventError{ID: e.ID, Kind: string(e.Kind), Field: field}
	}
	if !eventTimeShape.MatchString(raw.At) {
		return bad("at")
	}
	at, err := time.Parse(time.RFC3339Nano, raw.At)
	if err != nil {
		return bad("at")
	}
	e.At = at
	switch {
	case raw.Status != nil && *raw.Status == "":
		return bad("status")
	case raw.Source != nil && *raw.Source == "":
		return bad("source")
	case raw.Seconds != nil && *raw.Seconds == 0:
		return bad("seconds")
	case raw.Speakers != nil && len(*raw.Speakers) == 0:
		return bad("speakers")
	}
	if raw.Status != nil {
		e.Status = *raw.Status
	}
	if raw.Source != nil {
		e.Source = *raw.Source
	}
	if raw.Seconds != nil {
		e.Seconds = *raw.Seconds
	}
	if raw.Speakers != nil {
		e.Speakers = *raw.Speakers
	}
	return e, e.validate()
}

// clip is one clip a voice holds, with what was said about it since it was
// last added.
type clip struct {
	ref     ClipRef
	status  Status
	source  Source
	seconds float64
	added   time.Time
	// events are the clip's events after its latest added event, in time
	// order.
	events []event
}

// voice is one voice as its files hold it.
type voice struct {
	name  Name
	slug  Slug
	clips []clip
	// latest is the time of each clip id's last event, whatever its kind:
	// the next event of that clip is stamped after it.
	latest map[ClipID]time.Time
}

func (v *voice) clip(id ClipID) *clip {
	if v == nil {
		return nil
	}
	for i := range v.clips {
		if v.clips[i].ref.ID == id {
			return &v.clips[i]
		}
	}
	return nil
}

// readLibrary reads every voice under root in slug order. A missing root is
// an empty library. Entries that are not voices, unfinished last lines and
// lines that are not events are skipped: they are another writer's work in
// progress, or not ours.
func readLibrary(root string) ([]voice, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &LibraryReadError{Path: root, Err: err}
	}
	var lib []voice
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
			continue
		}
		v, err := readVoice(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		if v != nil {
			lib = append(lib, *v)
		}
	}
	return lib, nil
}

// readVoice reads one voice directory; nil when it holds no voice.json that
// names this directory's slug.
func readVoice(dir string) (*voice, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &LibraryReadError{Path: dir, Err: err}
	}
	files := make(map[string]bool, len(entries))
	for _, entry := range entries {
		files[entry.Name()] = entry.Type().IsRegular()
	}
	if _, ok := files[voiceFile]; !ok {
		return nil, nil
	}
	slug := Slug(filepath.Base(dir))
	name, err := readVoiceName(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if _, undecoded := errors.AsType[*LibraryReadError](err); undecoded {
		return nil, err
	}
	if err != nil || SlugOf(name) != slug {
		return nil, nil
	}
	path := filepath.Join(dir, eventsFile)
	log, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, &LibraryReadError{Path: path, Err: err}
	}
	v := fold(slug, readEvents(log))
	v.name = name
	v.clips = slices.DeleteFunc(v.clips, func(c clip) bool { return !files[string(c.ref.ID)+ClipExt] })
	return &v, nil
}

// readVoiceName reads voice.json: one object whose only field is the name.
func readVoiceName(dir string) (Name, error) {
	path := filepath.Join(dir, voiceFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err != nil {
		return "", &LibraryReadError{Path: path, Err: err}
	}
	var record map[string]*string
	if err := json.Unmarshal(raw, &record); err != nil {
		return "", err
	}
	if len(record) != 1 || record["name"] == nil {
		return "", &VoiceNameError{Rule: NameShape}
	}
	return ParseName(*record["name"])
}

// readEvents returns the events of a log's complete lines in time order.
// Events with one id count once, whichever copy of the line sorts first, so
// the union of two copies of a log reads as their merge.
func readEvents(log []byte) []event {
	type kept struct {
		event event
		line  []byte
	}
	byID := map[string]kept{}
	complete := log[:bytes.LastIndexByte(log, '\n')+1]
	for line := range bytes.Lines(complete) {
		line = line[:len(line)-1]
		e, err := decodeEvent(line)
		if err != nil {
			continue
		}
		if prev, ok := byID[e.ID]; !ok || bytes.Compare(line, prev.line) < 0 {
			byID[e.ID] = kept{event: e, line: line}
		}
	}
	events := make([]event, 0, len(byID))
	for _, k := range byID {
		events = append(events, k.event)
	}
	slices.SortFunc(events, func(a, b event) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return events
}

// fold turns a voice's events, in time order, into the clips it holds. A
// clip's life starts at its latest added event: what was said before is not
// counted, and a removed event at or after it ends the life. A confirmed
// event confirms the clip.
func fold(slug Slug, events []event) voice {
	v := voice{slug: slug, latest: map[ClipID]time.Time{}}
	byClip := map[ClipID][]event{}
	for _, e := range events {
		v.latest[e.Clip] = e.At
		byClip[e.Clip] = append(byClip[e.Clip], e)
	}
	for id, own := range byClip {
		added := -1
		for i, e := range own {
			if e.Kind == eventAdded {
				added = i
			}
		}
		if added < 0 {
			continue
		}
		first := own[added]
		c := clip{ref: ClipRef{Slug: slug, ID: id}, status: first.Status, source: first.Source, seconds: first.Seconds, added: first.At}
		removed := false
		for i, e := range own {
			if i == added || e.At.Before(c.added) {
				continue
			}
			switch e.Kind {
			case eventRemoved:
				removed = true
			case eventConfirmed:
				c.status = Confirmed
			}
			c.events = append(c.events, e)
		}
		if !removed {
			v.clips = append(v.clips, c)
		}
	}
	slices.SortFunc(v.clips, func(a, b clip) int {
		if c := a.added.Compare(b.added); c != 0 {
			return c
		}
		return strings.Compare(string(a.ref.ID), string(b.ref.ID))
	})
	return v
}
