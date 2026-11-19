package e2etest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
)

// SeedVoice enrols one clip of fixed bytes for name into the library under
// confDir, as the voices CLI would without inspecting anything, so the clip's
// id is the same on every machine. Events carry a time before any run's wall
// clock.
func SeedVoice(t *testing.T, confDir, name string, seconds float64, status voices.Status) voices.ClipRef {
	t.Helper()
	at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	library := voices.NewLibrary(confDir, func() time.Time { return at })
	inspection := voices.Inspection{
		FLAC:    fmt.Appendf(nil, "fLaC e2e %s %.1f %s", name, seconds, status),
		Seconds: seconds, SpeechShare: 1, MeanDBFS: -20,
	}
	enrolled, err := library.Enrol(voices.Enrolment{Name: name, Inspection: inspection, Status: status, Source: voices.SourceAdd})
	if err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	return enrolled.Ref
}

// NamingStubs puts the programs a naming run needs first on PATH: an ffmpeg
// that writes a few bytes to the file its last argument names (L5.4) and logs
// each invocation, and an ffprobe that prints a duration. It returns the log.
func NamingStubs(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "ffmpeg.log")
	WriteExecutable(t, bin, "ffmpeg", fmt.Appendf(nil, "#!/bin/sh\necho ffmpeg >> '%s'\nfor a; do last=$a; done\nprintf 'fLaC' > \"$last\"\n", log))
	WriteExecutable(t, bin, "ffprobe", []byte("#!/bin/sh\necho 16.0\n"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// NamingPick is one picked clip of a naming request: its length and the
// label the scripted answer gives its region.
type NamingPick struct {
	Seconds float64
	Label   string
}

// NamingScript writes the CLAI_MOCK_TRANSCRIBE_SCRIPT of one named
// transcription and sets the variable: first the recording's transcript,
// speaker A for 0–8 s and B for 9–15 s, then the forward and the reversed
// naming answers. A request is the picks, in the selection's order and then
// reversed, then A's and B's candidates, with 0.5 s between neighbours, and
// each region answers with its label.
func NamingScript(t *testing.T, picks []NamingPick, labelA, labelB string) {
	t.Helper()
	type turn struct {
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
		Speaker string  `json:"speaker"`
		Text    string  `json:"text"`
	}
	answer := func(regions []NamingPick) []turn {
		var turns []turn
		at := 0.0
		for _, r := range append(regions, NamingPick{8, labelA}, NamingPick{6, labelB}) {
			turns = append(turns, turn{Start: at, End: at + r.Seconds, Speaker: r.Label, Text: "words"})
			at += r.Seconds + 0.5
		}
		return turns
	}
	reversed := slices.Clone(picks)
	slices.Reverse(reversed)
	script := [][]turn{
		{{0, 8, "A", "hello there"}, {9, 15, "B", "hi"}},
		answer(slices.Clone(picks)),
		answer(reversed),
	}
	raw, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal the naming script: %v", err)
	}
	path := filepath.Join(t.TempDir(), "naming-script.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write the naming script: %v", err)
	}
	t.Setenv("CLAI_MOCK_TRANSCRIBE_SCRIPT", path)
}

// WriteRecording writes a small fake recording named name in the working
// directory, removed when the test ends, and returns its path.
func WriteRecording(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(wd, name)
	if err := os.WriteFile(path, []byte("RIFF-fake-wav"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}
