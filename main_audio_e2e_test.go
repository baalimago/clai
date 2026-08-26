package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

const (
	wantMockVTT = `WEBVTT

00:00:00.000 --> 00:00:01.500
mock transcription

00:00:01.500 --> 00:00:03.000
of an audio file
`
	wantMockText = "mock transcription\nof an audio file\n"
)

func writeE2EAudioFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.wav")
	if err := os.WriteFile(path, []byte("RIFF-fake-wav"), 0o644); err != nil {
		t.Fatalf("WriteFile(audio): %v", err)
	}
	return path
}

func runAudio(t *testing.T, args string) (int, string, string) {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	var status int
	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			status = run(strings.Split(args, " "))
		})
	})
	return status, stdout, stderr
}

func Test_goldenFile_AUDIO_transcribe_vtt(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)

	status, stdout, _ := runAudio(t, "-am test audio transcribe "+audioFile)

	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, stdout, wantMockVTT)
	if _, err := os.Stat(filepath.Join(confDir, "audioConfig.json")); err != nil {
		t.Errorf("expected audioConfig.json to be created from defaults: %v", err)
	}
}

func Test_goldenFile_AUDIO_alias_and_text_format(t *testing.T) {
	_ = setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)

	status, stdout, _ := runAudio(t, "-am test -af text a t "+audioFile)

	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, stdout, wantMockText)
	if strings.Contains(stdout, "WEBVTT") {
		t.Errorf("unexpected VTT header in text output: %q", stdout)
	}
}

func Test_goldenFile_AUDIO_stdin_dash(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if _, err := w.Write([]byte("RIFF\x24\x08\x00\x00WAVEfmt fake-wav-from-stdin")); err != nil {
		t.Fatalf("Write(stdin): %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close(stdin writer): %v", err)
	}
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	os.Stdin = r
	t.Cleanup(func() { _ = r.Close() })

	status, stdout, _ := runAudio(t, "-am test -af text a t -")

	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, stdout, wantMockText)
}

func Test_goldenFile_AUDIO_stdin_unrecognized_format(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if _, err := w.Write([]byte("definitely not audio bytes")); err != nil {
		t.Fatalf("Write(stdin): %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close(stdin writer): %v", err)
	}
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	os.Stdin = r
	t.Cleanup(func() { _ = r.Close() })

	status, stdout, stderr := runAudio(t, "-am test -af text a t -")

	testboil.FailTestIfDiff(t, status, 1)
	testboil.FailTestIfDiff(t, stdout, "")
	testboil.AssertStringContains(t, stderr, "wav")
}

func Test_goldenFile_AUDIO_namespace_help(t *testing.T) {
	t.Run("no verb errors with help on stderr", func(t *testing.T) {
		_ = setupMainTestConfigDir(t)
		status, stdout, stderr := runAudio(t, "audio")
		testboil.FailTestIfDiff(t, status, 1)
		testboil.AssertStringContains(t, stderr, "transcribe")
		testboil.FailTestIfDiff(t, stdout, "")
	})
	t.Run("unknown verb errors with help on stderr", func(t *testing.T) {
		_ = setupMainTestConfigDir(t)
		status, _, stderr := runAudio(t, "audio fly")
		testboil.FailTestIfDiff(t, status, 1)
		testboil.AssertStringContains(t, stderr, "transcribe")
	})
	t.Run("help verb lists verbs and exits 0", func(t *testing.T) {
		_ = setupMainTestConfigDir(t)
		status, stdout, _ := runAudio(t, "audio help")
		testboil.FailTestIfDiff(t, status, 0)
		testboil.AssertStringContains(t, stdout, "transcribe")
	})
}

func Test_goldenFile_AUDIO_missing_input_file(t *testing.T) {
	_ = setupMainTestConfigDir(t)
	missing := filepath.Join(t.TempDir(), "nope.wav")

	status, _, stderr := runAudio(t, "-am test a t "+missing)

	testboil.FailTestIfDiff(t, status, 1)
	testboil.AssertStringContains(t, stderr, "nope.wav")
}

func Test_goldenFile_AUDIO_invalid_format_flag(t *testing.T) {
	_ = setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)

	status, _, stderr := runAudio(t, "-am test -af yaml a t "+audioFile)

	testboil.FailTestIfDiff(t, status, 1)
	testboil.AssertStringContains(t, stderr, "vtt")
	testboil.AssertStringContains(t, stderr, "json")
}

func Test_goldenFile_AUDIO_unroutable_model(t *testing.T) {
	_ = setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)

	status, _, stderr := runAudio(t, "-am mystery9000 a t "+audioFile)

	testboil.FailTestIfDiff(t, status, 1)
	testboil.AssertStringContains(t, stderr, "or:")
	testboil.AssertStringContains(t, stderr, "whisper")
}

func Test_goldenFile_AUDIO_missing_api_key(t *testing.T) {
	_ = setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)
	t.Setenv("OPENAI_API_KEY", "")

	status, _, stderr := runAudio(t, "-am whisper-1 a t "+audioFile)

	testboil.FailTestIfDiff(t, status, 1)
	testboil.AssertStringContains(t, stderr, "OPENAI_API_KEY")
}

func Test_goldenFile_AUDIO_config_cascade(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)
	// File sets text format and an unroutable model; both should apply over defaults
	fileConf := `{"transcribe": {"model": "or:some/model", "output-format": "text", "parallelism": 2}}`
	if err := os.WriteFile(filepath.Join(confDir, "audioConfig.json"), []byte(fileConf), 0o644); err != nil {
		t.Fatalf("WriteFile(audioConfig.json): %v", err)
	}

	t.Run("file beats default", func(t *testing.T) {
		// -am test routes to mock; format text comes from the file (default vtt)
		status, stdout, _ := runAudio(t, "-am test a t "+audioFile)
		testboil.FailTestIfDiff(t, status, 0)
		testboil.FailTestIfDiff(t, stdout, wantMockText)
	})
	t.Run("flag beats file", func(t *testing.T) {
		status, stdout, _ := runAudio(t, "-am test -af json a t "+audioFile)
		testboil.FailTestIfDiff(t, status, 0)
		testboil.AssertStringContains(t, stdout, `"start":`)
		if strings.Contains(stdout, "WEBVTT") {
			t.Errorf("expected json output, got: %q", stdout)
		}
	})
}

func Test_goldenFile_AUDIO_corrupt_config(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)
	if err := os.WriteFile(filepath.Join(confDir, "audioConfig.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatalf("WriteFile(audioConfig.json): %v", err)
	}

	status, _, stderr := runAudio(t, "-am test a t "+audioFile)

	testboil.FailTestIfDiff(t, status, 1)
	testboil.AssertStringContains(t, stderr, "audioConfig.json")
}

func Test_goldenFile_AUDIO_help_includes_audio(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	status, stdout, _ := runAudio(t, "help")

	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, stdout, "a|audio")
	testboil.AssertStringContains(t, stdout, "-am")
	testboil.AssertStringContains(t, stdout, "-af")
	testboil.AssertStringContains(t, stdout, "-parallelism")
}
