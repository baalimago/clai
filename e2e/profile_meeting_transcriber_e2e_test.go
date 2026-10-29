package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/tools"
)

// Test_e2e_meeting_transcriber_profile drives the shipped profile through a
// mock model: it must inspect the inputs, record them and transcribe the
// result, in that order (worklog 2026-10-28-audio-record-tool, profile.9).
func Test_e2e_meeting_transcriber_profile(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	recordingDir := t.TempDir()
	e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "audioConfig.json"), map[string]any{
		"transcribe": map[string]any{"model": "test"},
		"capture": map[string]any{
			"dir":             recordingDir,
			"max-seconds":     30,
			"silence-timeout": 1,
		},
	})
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", e2etest.WriteAudioFile(t))

	// The registry fills once per process, so a fake ffmpeg that arrives later
	// would never register the audio tools. Swap in a fresh registry, put the
	// fixture on PATH and let Init observe both.
	fixture, err := os.ReadFile(filepath.Join(e2etest.ModuleRoot(t), "internal", "capture", "testdata", "fake_ffmpeg.sh"))
	if err != nil {
		t.Fatalf("ReadFile(fake_ffmpeg.sh): %v", err)
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "ffmpeg"), fixture, 0o755); err != nil {
		t.Fatalf("WriteFile(ffmpeg): %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A non-interactive run gets /dev/null, which is a character device and so
	// looks like a terminal to the record tool. End of input must not be read
	// as an Enter press, or the recording ends before ffmpeg writes a WAV
	// (worklog 2026-10-28-audio-record-tool, R3-01). clai itself never reads a
	// character-device stdin as piped input.
	oldStdin := os.Stdin
	stdinFile, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("Open(stdin): %v", err)
	}
	os.Stdin = stdinFile
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = stdinFile.Close()
	})

	profilePath := filepath.Join(e2etest.ModuleRoot(t), "examples", "profiles", "meeting-transcriber.json")
	tools.WithTestRegistry(t, func() {
		tools.Init()
		for _, name := range []string{"audio_sources", "audio_record"} {
			if _, ok := tools.Registry.Get(name); !ok {
				t.Fatalf("%s is not registered with ffmpeg on PATH", name)
			}
		}
		var status int
		stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
			status = harness.Run([]string{
				"-cm", "mock_test",
				"-prp", profilePath,
				"q", "tool_audio_sources tool_audio_record tool_audio_transcribe",
			})
		})
		if status != 0 {
			t.Fatalf("status = %d, want 0: stdout=%q stderr=%q", status, stdout, stderr)
		}
		assertInOrder(t, stdout,
			"system_audio: available",
			"recorded: "+recordingDir,
			"mock transcription",
		)
	})

	entries, err := os.ReadDir(recordingDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", recordingDir, err)
	}
	var wavs []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".wav") {
			wavs = append(wavs, entry.Name())
		}
	}
	if len(wavs) != 1 {
		t.Fatalf("recordings in %q = %v, want exactly one wav", recordingDir, wavs)
	}
}

func assertInOrder(t *testing.T, body string, parts ...string) {
	t.Helper()
	at := 0
	for _, part := range parts {
		found := strings.Index(body[at:], part)
		if found < 0 {
			t.Fatalf("output does not contain %q after byte %d: %q", part, at, body)
		}
		at += found + len(part)
	}
}

// Test_meeting_transcriber_documented keeps the profile discoverable where
// users look for profiles and worked examples (profile.6, F10).
func Test_meeting_transcriber_documented(t *testing.T) {
	root := e2etest.ModuleRoot(t)
	for _, path := range []string{
		filepath.Join("examples", "profiles", "README.md"),
		"examples.md",
	} {
		body := e2etest.ReadStringFile(t, filepath.Join(root, path))
		for _, want := range []string{"meeting-transcriber", "audio_sources", "audio_record", "audio_transcribe"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not name %q", path, want)
			}
		}
	}
}
