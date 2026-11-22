package interactive

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voices/voicestest"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

const (
	wantMockSRT  = "1\n00:00:00,000 --> 00:00:01,500\nmock transcription\n\n2\n00:00:01,500 --> 00:00:03,000\nof an audio file\n"
	wantMockJSON = "[\n  {\n    \"start\": 0,\n    \"end\": 1.5,\n    \"text\": \"mock transcription\"\n  },\n  {\n    \"start\": 1.5,\n    \"end\": 3,\n    \"text\": \"of an audio file\"\n  }\n]\n"
)

// configListing is every file under confDir but audioConfig.json, with its
// bytes.
func configListing(t *testing.T, confDir string) map[string]string {
	t.Helper()
	listing := map[string]string{}
	err := filepath.WalkDir(confDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(confDir, path)
		if rel == "audioConfig.json" {
			return nil
		}
		if d.IsDir() {
			listing[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(path)
		listing[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatalf("list %s: %v", confDir, err)
	}
	return listing
}

// preUpgradeAudioConfig is today's audioConfig.json without max-voices, as a
// file written before the key existed.
func preUpgradeAudioConfig(t *testing.T, confDir string) {
	t.Helper()
	raw, err := json.Marshal(audio.Default)
	if err != nil {
		t.Fatal(err)
	}
	var conf map[string]map[string]any
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatal(err)
	}
	delete(conf["transcribe"], "max-voices")
	e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "audioConfig.json"), conf)
}

// Test_e2e_AUDIO_unchanged_without_voices is F3 on both routes: with nobody
// to name, every format and the tool answer with today's bytes, and nothing
// under voices/ is touched.
func Test_e2e_AUDIO_unchanged_without_voices(t *testing.T) {
	for _, library := range []struct {
		name string
		seed func(t *testing.T, confDir string)
	}{
		{"no voices directory", func(*testing.T, string) {}},
		{"only unconfirmed clips", func(t *testing.T, confDir string) {
			e2etest.SeedVoice(t, confDir, "Lorentz", 5, voices.Unconfirmed)
		}},
		{"voices is a regular file", func(t *testing.T, confDir string) {
			if err := os.WriteFile(filepath.Join(confDir, voices.Dir), []byte("not a library"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(library.name, func(t *testing.T) {
			confDir := e2etest.SetupMainTestConfigDir(t)
			library.seed(t, confDir)
			preUpgradeAudioConfig(t, confDir)
			ffmpegLog := e2etest.NamingStubs(t)
			audioFile := e2etest.WriteAudioFile(t)
			pristine := configListing(t, confDir)
			var before map[string]string
			notices := 0
			for i, row := range []struct{ format, want string }{
				{"vtt", wantMockVTT}, {"srt", wantMockSRT}, {"text", e2etest.MockTranscription}, {"json", wantMockJSON},
			} {
				status, stdout, stderr := harness.RunAudio(t, "audio transcribe -am test -af "+row.format+" "+audioFile)
				testboil.FailTestIfDiff(t, status, 0)
				testboil.FailTestIfDiff(t, stdout, row.want)
				for line := range strings.Lines(stderr) {
					switch {
					case strings.HasPrefix(line, "transcribing f.wav · 1 request"), strings.HasPrefix(line, "transcribed · 2 segments"):
					case strings.Contains(line, "added new field(s) to audioConfig.json: transcribe.max-voices\n"):
						notices++
					default:
						t.Fatalf("%s: stderr gained %q", row.format, line)
					}
				}
				// The first run's config prep writes today's other config
				// files; from then on a run changes nothing but audioConfig.json.
				if i == 0 {
					before = configListing(t, confDir)
				}
			}
			if notices != 1 {
				t.Fatalf("the upgrade notice naming transcribe.max-voices came %d times, want once", notices)
			}
			if after := configListing(t, confDir); !maps.Equal(before, after) {
				t.Fatalf("the config directory changed:\nbefore %v\nafter  %v", before, after)
			}
			if after := libraryEntries(configListing(t, confDir)); !maps.Equal(libraryEntries(pristine), after) {
				t.Fatalf("the library changed: %v, was %v", after, libraryEntries(pristine))
			}

			t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)
			status, stdout, _ := harness.RunAudio(t, "-r -cm mock_test -t=audio_transcribe -am test q tool_audio_transcribe")
			testboil.FailTestIfDiff(t, status, 0)
			if !strings.Contains(stdout, "\n"+e2etest.MockTranscription) || strings.Contains(stdout, `"naming"`) {
				t.Fatalf("tool result = %q, want today's transcript", stdout)
			}
			if _, err := os.Stat(ffmpegLog); !os.IsNotExist(err) {
				t.Fatalf("ffmpeg ran: %v", err)
			}
			if after := libraryEntries(configListing(t, confDir)); !maps.Equal(libraryEntries(pristine), after) {
				t.Fatalf("the tool run touched the library: %v", after)
			}
		})
	}
}

// libraryEntries are the listing's entries under voices, or voices itself.
func libraryEntries(listing map[string]string) map[string]string {
	entries := map[string]string{}
	for path, content := range listing {
		if path == voices.Dir || strings.HasPrefix(path, voices.Dir+"/") {
			entries[path] = content
		}
	}
	return entries
}

// Test_e2e_AUDIO_voices_without_ffmpeg is Run.8: people named on a machine
// without ffmpeg stop the run before any request, and nobody named needs no
// ffmpeg.
func Test_e2e_AUDIO_voices_without_ffmpeg(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	e2etest.SeedVoice(t, confDir, "Lorentz", 5, voices.Confirmed)
	audioFile := e2etest.WriteAudioFile(t)
	t.Setenv("PATH", t.TempDir())

	status, stdout, stderr := harness.RunAudio(t, "audio transcribe -am test-diarize -voices Lorentz "+audioFile)
	testboil.FailTestIfDiff(t, status, 1)
	testboil.FailTestIfDiff(t, stdout, "")
	testboil.AssertStringContains(t, stderr, "-voices needs ffmpeg")
	if strings.Contains(stderr, "transcribing ") || strings.Contains(stderr, "transcribed ") {
		t.Fatalf("stderr %q, want nothing transcribed", stderr)
	}

	if err := os.RemoveAll(filepath.Join(confDir, voices.Dir)); err != nil {
		t.Fatal(err)
	}
	e2etest.SeedVoice(t, confDir, "Lorentz", 5, voices.Unconfirmed)
	status, stdout, _ = harness.RunAudio(t, "audio transcribe -am test-diarize "+audioFile)
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, stdout, wantMockDiarizedVTT)
}

const wantMockDiarizedVTT = `WEBVTT

00:00:00.000 --> 00:00:01.500
<v A>mock transcription</v>

00:00:01.500 --> 00:00:03.000
<v B>of an audio file</v>
`

// speechStubs puts first on PATH an ffprobe and an ffmpeg that answer the
// Inspector with 4 s of speech whatever the file, and returns the id of the
// clip that ffmpeg encodes.
func speechStubs(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	var pcm bytes.Buffer
	if err := binary.Write(&pcm, binary.LittleEndian, voicestest.Speech(4)); err != nil {
		t.Fatal(err)
	}
	e2etest.WriteExecutable(t, bin, "speech.pcm", pcm.Bytes())
	e2etest.WriteExecutable(t, bin, "ffprobe", []byte(`#!/bin/sh
echo '{"streams":[{"codec_name":"pcm_s16le","sample_rate":"16000","channels":1}],"format":{"format_name":"wav","duration":"4.0"}}'
`))
	e2etest.WriteExecutable(t, bin, "ffmpeg", fmt.Appendf(nil, `#!/bin/sh
for a; do last=$a; done
case $last in pipe:1) cat '%s/speech.pcm' ;; *) printf 'fLaC e2e' > "${last#file:}" ;; esac
`, bin))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return string(voices.ClipIDOf([]byte("fLaC e2e")))
}

// Test_e2e_AUDIO_voices_dispatch is Wire.4 on the real command tree, and one
// pass through the voices CLI: add, list, the actions and their exit statuses,
// by the long names and the short.
func Test_e2e_AUDIO_voices_dispatch(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	e2etest.SeedVoice(t, confDir, "Lorentz", 5, voices.Confirmed)
	e2etest.ChdirTemp(t)
	if err := os.WriteFile("voices", []byte("RIFF-fake-wav"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, stdout, _ := harness.RunAudio(t, "audio transcribe -am test voices")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, stdout, wantMockVTT)

	id := speechStubs(t)
	people := "Index | Person  | Clips | Unconfirmed | Named | Wrong | Last named\n0     | Lorentz | 1     | 0           | 0     | 0     | never\n"
	const synopsis = "clai audio v|voices a|add <wav> [-from s] [-to s] [-name n]\n    clai audio v|voices l|list [person [clip [action [args…]]]] [-json]\n"
	for _, row := range []struct {
		args   string
		status int
		// stdout is all of stdout, and stderr a part of stderr.
		stdout, stderr string
	}{
		{"audio voices list", 0, people, ""},
		{"audio v list", 0, people, ""},
		{"a v l", 0, people, ""},
		{"a v l lorentz", 0, "", ""},
		{"audio voices", 2, "", synopsis},
		{"a v", 2, "", synopsis},
		{"a v a rec.wav -from 0.5 -to 4 -name Ada", 0, "Ada: clip " + id + " added (confirmed)\n", ""},
		{"audio voices add rec.wav -name Ada", 1, "", "the library already holds clip ada/" + id},
		{"a v l ada 0 overrule", 0, "Ada/" + id + ": overruled 1 time, never named\n", ""},
		{"audio voices list ADA " + id[:4] + " move lorentz", 0, "Ada/" + id + " moved to Lorentz as " + id + " (confirmed)\n", ""},
		{"a v l lorentz " + id + " trim 1", 2, "", "usage: trim <from> <to> (2 arguments, got 1)"},
		{"a v l lorentz " + id + " delete", 0, "Lorentz/" + id + ": deleted\n", ""},
		{"a v l ada", 1, "", `no voice named "ada"`},
		// Wire.5: add's flags are read after the file.
		{"audio voices add rec.wav -from 61.2 -to 69", 2, "", "so the person must be named: add -name <person>"},
		{"a v a rec.wav -from abc -name Syrus", 2, "", `-from "abc" is not a decimal number of seconds`},
	} {
		status, stdout, stderr := harness.RunAudio(t, row.args)
		if row.args == "a v l lorentz" {
			row.stdout = stdout
			testboil.AssertStringContains(t, stdout, "Lorentz · 1 clip, best first\n")
		}
		if status != row.status || stdout != row.stdout || !strings.Contains(stderr, row.stderr) || strings.Contains(stdout+stderr, "transcrib") || strings.Contains(stderr, "hint") {
			t.Fatalf("%s: status %d, stdout %q, stderr %q; want %d, %q and %q, nothing transcribed and no hint", row.args, status, stdout, stderr, row.status, row.stdout, row.stderr)
		}
	}

	// Wire.5: -json is read before the command and after the path; it prints
	// List's shape.
	listed, err := voices.NewLibrary(confDir, time.Now).List()
	want, _ := json.Marshal(listed)
	for _, args := range []string{"audio voices list -json", "-json audio voices list", "a v l lorentz -json"} {
		status, stdout, stderr := harness.RunAudio(t, args)
		if err != nil || status != 0 || stdout != string(want)+"\n" || len(listed) != 1 || stderr != "" {
			t.Fatalf("%s: status %d, stderr %q, stdout %q; want the library as JSON: %s (%v)", args, status, stderr, stdout, want, err)
		}
	}
}

// Test_e2e_AUDIO_transcribe_without_file is Wire.7: no file prints the help of
// audio transcribe -h with the error on stderr, exit 1.
func Test_e2e_AUDIO_transcribe_without_file(t *testing.T) {
	e2etest.SetupMainTestConfigDir(t)
	status, help, _ := harness.RunAudio(t, "audio transcribe -h")
	testboil.FailTestIfDiff(t, status, 0)
	for _, part := range []string{"audio transcribe [flags] <file>", "Examples:", "clai a t meeting.wav", "  -voices "} {
		testboil.AssertStringContains(t, help, part)
	}
	for _, args := range []string{"audio transcribe", "a t", "audio transcribe -am test", "audio transcribe -voices Lorentz -af json"} {
		status, stdout, stderr := harness.RunAudio(t, args)
		testboil.FailTestIfDiff(t, status, 1)
		testboil.FailTestIfDiff(t, stdout, "")
		testboil.AssertStringContains(t, stderr, help)
		if n := strings.Count(stderr, "missing audio file argument"); n != 1 {
			t.Fatalf("%s: %d lines say the file is missing, want 1:\n%s", args, n, stderr)
		}
		if strings.Contains(stderr, e2etest.MockTranscription) || strings.Contains(stderr, "transcribing ") {
			t.Fatalf("%s: transcribed:\n%s", args, stderr)
		}
	}
}

// Test_e2e_voices_flags_anywhere is Wire.5 outside the voices verbs (the
// dispatch test has those): -json after a
// query is text, and a flag of `voices add` before the file of `audio
// transcribe` gets the one hint that names its owner.
func Test_e2e_voices_flags_anywhere(t *testing.T) {
	e2etest.SetupMainTestConfigDir(t)
	wav := e2etest.WriteAudioFile(t)
	status, stdout, stderr := harness.RunAudio(t, "-r -cm test q hello -json")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, stdout, "hello -json\n")
	testboil.FailTestIfDiff(t, stderr, "")

	// A stray flag before the file on audio transcribe: the wording and the
	// hints of Wire.5 (maintainer, 2026-11-19, R1-11; A04).
	const placeIt = " — place it there, after that command."
	voicesAdd := "'audio voices add'"
	for _, row := range []struct {
		flag string
		hint func(string) bool
	}{
		{"-json", func(h string) bool {
			return h == "hint: -json selects the output format of the whole run: place it before the command (clai -json <command> ...) or among the command's flags before its first argument (clai <command> -json ...)."
		}},
		{"-from 1", func(h string) bool { return h == "hint: '-from' belongs to "+voicesAdd+placeIt }},
		{"-to 2", func(h string) bool { return h == "hint: '-to' belongs to "+voicesAdd+placeIt }},
		{"-name x", func(h string) bool {
			return strings.HasPrefix(h, "hint: '-name' belongs to ") && strings.Contains(h, voicesAdd) && strings.HasSuffix(h, placeIt)
		}},
	} {
		args := "audio transcribe -am test " + row.flag + " " + wav
		status, stdout, stderr := harness.RunAudio(t, args)
		testboil.FailTestIfDiff(t, status, 1)
		flag, _, _ := strings.Cut(row.flag, " ")
		testboil.AssertStringContains(t, stderr, "failed to parse flagset: flag provided but not defined: "+flag+"\n")
		var hints []string
		for line := range strings.Lines(stderr) {
			if h := strings.TrimSpace(line); strings.HasPrefix(h, "hint:") {
				hints = append(hints, h)
			}
		}
		if len(hints) != 1 || !row.hint(hints[0]) {
			t.Fatalf("%s: hints %q, want Wire.5's one hint", args, hints)
		}
		if strings.Contains(stdout, e2etest.MockTranscription) || strings.Contains(stderr, "transcribing ") {
			t.Fatalf("%s: transcribed; stdout %q, stderr %q", args, stdout, stderr)
		}
	}
}
