// Package voices is the library of named voice clips under <clai-config>/voices:
//
//	voices/<slug>/voice.json    {"name": "<the person's name>"}, created once
//	voices/<slug>/<id>.flac     one clip, 16 kHz mono, written once
//	voices/<slug>/events.jsonl  one appended line per fact (schema/event.schema.json)
//
// Names starting with "." are never part of the library. The package imports
// nothing from internal/audio.
package voices

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	Dir        = "voices"
	voiceFile  = "voice.json"
	eventsFile = "events.jsonl"
	ClipExt    = ".flac"
)

// Name is a person's name as voice.json stores it.
type Name string

// Slug is the directory name of a voice.
type Slug string

// ClipID names one clip within a voice: the first 12 hex digits of the
// sha256 of its FLAC bytes, so two writers never give one id to two files.
type ClipID string

// ClipRef names one clip in the library. Its text form is "<slug>/<id>".
type ClipRef struct {
	Slug Slug
	ID   ClipID
}

func (r ClipRef) String() string { return string(r.Slug) + "/" + string(r.ID) }

func (r ClipRef) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// Status says whether the user stands behind a clip.
type Status string

const (
	Confirmed   Status = "confirmed"
	Unconfirmed Status = "unconfirmed"
)

// Source says who enrolled a clip.
type Source string

const (
	SourceAdd      Source = "voices add"
	SourceRemember Source = "audio_remember_voice"
)

// NameRule is the rule a refused name broke.
type NameRule string

const (
	NameShape    NameRule = "shape"
	NameReserved NameRule = "reserved"
)

type VoiceNameError struct {
	Name string
	Rule NameRule
}

func (e *VoiceNameError) Error() string {
	rule := "a name is a letter followed by at most 38 letters, digits, spaces or . ' ’ -"
	if e.Rule == NameReserved {
		rule = "speaker-N and unknown-N are the labels a transcript gives"
	}
	return fmt.Sprintf("voice name %q breaks the %s rule: %s", e.Name, e.Rule, rule)
}

// ClipRefError refuses a text that is not "<slug>/<id>".
type ClipRefError struct{ Ref string }

func (e *ClipRefError) Error() string {
	return fmt.Sprintf("clip reference %q is not <slug>/<id> with an id of 12 lower-case hex digits", e.Ref)
}

// The name rule, as voice.schema.json spells it.
var (
	nameShape    = regexp.MustCompile(`^\p{L}[\p{L}\p{N} .'’-]{0,38}$`)
	nameReserved = regexp.MustCompile(`^([sS][pP][eE][aA][kK][eE][rR]|[uU][nN][kK][nN][oO][wW][nN])-\d+$`)
	clipIDShape  = regexp.MustCompile(`^[0-9a-f]{12}$`)
)

// ParseName returns raw unchanged when it is a name a voice may carry.
func ParseName(raw string) (Name, error) {
	if !nameShape.MatchString(raw) {
		return "", &VoiceNameError{Name: raw, Rule: NameShape}
	}
	if nameReserved.MatchString(raw) {
		return "", &VoiceNameError{Name: raw, Rule: NameReserved}
	}
	return Name(raw), nil
}

// SlugOf lower-cases the name and joins its runs of letters and digits with
// "-": "Jean-Luc P." is "jean-luc-p". Names that differ only in case or
// separators are one person.
func SlugOf(n Name) Slug {
	words := strings.FieldsFunc(strings.ToLower(string(n)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) == 0 {
		return "-"
	}
	return Slug(strings.Join(words, "-"))
}

func ClipIDOf(flac []byte) ClipID {
	sum := sha256.Sum256(flac)
	return ClipID(hex.EncodeToString(sum[:6]))
}

func ParseClipRef(s string) (ClipRef, error) {
	slug, id, found := strings.Cut(s, "/")
	if !found || slug == "" || !clipIDShape.MatchString(id) {
		return ClipRef{}, &ClipRefError{Ref: s}
	}
	return ClipRef{Slug: Slug(slug), ID: ClipID(id)}, nil
}
