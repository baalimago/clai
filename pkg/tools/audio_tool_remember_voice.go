package tools

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// RememberVoiceLibrary is the part of the voice library the tool writes to.
type RememberVoiceLibrary interface {
	Enrol(voices.Enrolment) (voices.Enrolled, error)
	Overrule(voices.ClipRef) (voices.ClipSummary, error)
}

// RememberVoiceDeps is what one audio_remember_voice call works through:
// the tool itself runs no program and reads no configuration.
type RememberVoiceDeps struct {
	// Inspect cuts and measures the clip.
	Inspect func(ctx context.Context, file string, cut voices.Cut) (voices.Inspection, error)
	Library RememberVoiceLibrary
}

// AudioRememberVoiceDeps is injected by the clai runtime, as
// AudioTranscribeEngine is, and asked once per call.
var AudioRememberVoiceDeps func(ctx context.Context) (RememberVoiceDeps, error)

// ArgumentProblem says what is wrong with one argument of a call.
type ArgumentProblem string

const (
	ArgumentMissing ArgumentProblem = "missing"
	ArgumentType    ArgumentProblem = "of the wrong type"
	ArgumentRange   ArgumentProblem = "out of range"
)

// RememberVoiceArgumentError refuses a call for one argument.
type RememberVoiceArgumentError struct {
	Argument string
	Problem  ArgumentProblem
}

func (e *RememberVoiceArgumentError) Error() string {
	must := map[string]string{
		"file_path": "a non-empty string, the path of the audio file the clip is cut from",
		"start":     "a finite number of seconds, 0 or more",
		"end":       "a finite number of seconds after start",
		"name":      "a non-empty string, the person's name",
		"overrules": "a string, the clip reference '<slug>/<id>' of a segment's voice.clip",
	}
	return "audio_remember_voice: argument " + strconv.Quote(e.Argument) + " is " + string(e.Problem) + ": it must be " + must[e.Argument]
}

// RememberVoiceUnavailableError says this runtime cannot remember voices:
// nothing was injected (Err is nil), or the injected function failed.
type RememberVoiceUnavailableError struct{ Err error }

func (e *RememberVoiceUnavailableError) Error() string {
	why := "no voice library is wired in"
	if e.Err != nil {
		why = e.Err.Error()
	}
	return "audio_remember_voice is not available in this runtime: " + why
}
func (e *RememberVoiceUnavailableError) Unwrap() error { return e.Err }

type AudioRememberVoiceTool pub_models.Specification

// AudioRememberVoice adds one unconfirmed clip of a person to the voice
// library. It has no argument that removes, replaces or confirms a clip.
var AudioRememberVoice = AudioRememberVoiceTool{
	Name:        "audio_remember_voice",
	Description: "Remember a person's voice, so that later transcripts name them: add one clip of that person speaking to the voice library. Use it when you know who a transcript speaker is, because they said their name or the user told you. Give the audio file, the person's name, and start and end in seconds taken from audio_transcribe's json segments: 3 to 10 seconds in which only that person speaks. If the transcript gave that speaker a wrong name from the library, also pass overrules with that segment's voice.clip. The clip stays unconfirmed until the user keeps it. Returns '<name>: clip <id> added (unconfirmed)'.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file_path": {Type: "string", Description: "Path to the local audio file the clip is cut from: the file that was transcribed."},
			"start":     {Type: "number", Description: "Where the clip starts, in seconds from the start of the file, as a segment's start gives it. Decimals are kept."},
			"end":       {Type: "number", Description: "Where the clip ends, in seconds from the start of the file. It must be after start; the clip must be 3 to 10 seconds long."},
			"name":      {Type: "string", Description: "The person's name, for example 'Christoph'. A name the library already holds adds the clip to that person."},
			"overrules": {Type: "string", Description: "Optional. The clip a wrong library name came from, '<slug>/<id>', copied from the segment's voice.clip. It counts that naming against that clip."},
		},
		Required: []string{"file_path", "start", "end", "name"},
	},
}

func (a AudioRememberVoiceTool) Call(input pub_models.Input) (string, error) {
	return a.CallWithContext(context.Background(), input)
}

func (a AudioRememberVoiceTool) Specification() pub_models.Specification {
	return pub_models.Specification(a)
}

// CallWithContext inspects the clip, enrols it unconfirmed and, when
// overrules is given, counts that naming against the clip it names. Every
// argument is checked before anything is inspected or written.
//
// The first line of a successful result is frozen, since other programs
// read it: "<name>: clip <id> added (unconfirmed)". Two lines may follow
// it. "overrule of <slug>/<id> failed: <reason>" says the clip was added
// and the overrule was not recorded. "warning: <reason>" says the clip was
// added and the file of the clip pruned for it could not be deleted.
func (a AudioRememberVoiceTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	file, err := rememberVoiceString(input, "file_path")
	if err != nil {
		return "", err
	}
	start, err := rememberVoiceNumber(input, "start")
	if err != nil {
		return "", err
	}
	end, err := rememberVoiceNumber(input, "end")
	if err != nil {
		return "", err
	}
	name, err := rememberVoiceString(input, "name")
	if err != nil {
		return "", err
	}
	overrules, isString := input["overrules"].(string)
	if !isString && input["overrules"] != nil {
		return "", &RememberVoiceArgumentError{Argument: "overrules", Problem: ArgumentType}
	}
	cut, err := rememberVoiceCut(start, end)
	if err != nil {
		return "", err
	}
	var overruled voices.ClipRef
	if overrules != "" {
		if overruled, err = voices.ParseClipRef(overrules); err != nil {
			return "", err
		}
	}
	if _, err := voices.ParseName(name); err != nil {
		return "", err
	}
	deps, err := rememberVoiceDeps(ctx)
	if err != nil {
		return "", err
	}
	inspection, err := deps.Inspect(ctx, file, cut)
	if err != nil {
		return "", err
	}
	enrolled, err := deps.Library.Enrol(voices.Enrolment{
		Name: name, Inspection: inspection, Status: voices.Unconfirmed, Source: voices.SourceRemember,
	})
	if err != nil {
		return "", err
	}
	lines := []string{name + ": clip " + string(enrolled.Ref.ID) + " added (unconfirmed)"}
	if overrules != "" {
		if _, err := deps.Library.Overrule(overruled); err != nil {
			lines = append(lines, "overrule of "+overrules+" failed: "+oneLine(err.Error()))
		}
	}
	if enrolled.Leftover != nil {
		lines = append(lines, "warning: "+oneLine(enrolled.Leftover.Error()))
	}
	return strings.Join(lines, "\n"), nil
}

func rememberVoiceDeps(ctx context.Context) (RememberVoiceDeps, error) {
	if AudioRememberVoiceDeps == nil {
		return RememberVoiceDeps{}, &RememberVoiceUnavailableError{}
	}
	deps, err := AudioRememberVoiceDeps(ctx)
	if err != nil {
		return RememberVoiceDeps{}, &RememberVoiceUnavailableError{Err: err}
	}
	return deps, nil
}

// rememberVoiceString reads a required string argument; null and the empty
// string count as absent.
func rememberVoiceString(input pub_models.Input, arg string) (string, error) {
	s, isString := input[arg].(string)
	switch {
	case !isString && input[arg] != nil:
		return "", &RememberVoiceArgumentError{Argument: arg, Problem: ArgumentType}
	case s == "":
		return "", &RememberVoiceArgumentError{Argument: arg, Problem: ArgumentMissing}
	}
	return s, nil
}

func rememberVoiceNumber(input pub_models.Input, arg string) (float64, error) {
	switch v := input[arg].(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case nil:
		return 0, &RememberVoiceArgumentError{Argument: arg, Problem: ArgumentMissing}
	case string:
		if v == "" {
			return 0, &RememberVoiceArgumentError{Argument: arg, Problem: ArgumentMissing}
		}
	}
	return 0, &RememberVoiceArgumentError{Argument: arg, Problem: ArgumentType}
}

// rememberVoiceCut refuses an end that is not after start, so the Cut's
// zero To, the end of the file, is never asked for.
func rememberVoiceCut(start, end float64) (voices.Cut, error) {
	if start < 0 || math.IsNaN(start) || math.IsInf(start, 0) {
		return voices.Cut{}, &RememberVoiceArgumentError{Argument: "start", Problem: ArgumentRange}
	}
	cut := voices.Cut{From: secondsToDuration(start), To: secondsToDuration(end)}
	if math.IsNaN(end) || math.IsInf(end, 0) || end <= start || cut.To <= cut.From {
		return voices.Cut{}, &RememberVoiceArgumentError{Argument: "end", Problem: ArgumentRange}
	}
	return cut, nil
}

// secondsToDuration rounds to the nanosecond and holds at the longest
// Duration.
func secondsToDuration(seconds float64) time.Duration {
	ns := math.Round(seconds * float64(time.Second))
	if ns >= math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ns)
}

func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
}
