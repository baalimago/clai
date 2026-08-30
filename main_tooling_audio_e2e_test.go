package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func writeAudioToolConfig(t *testing.T, confDir, model string) {
	t.Helper()
	conf := `{"transcribe": {"model": "` + model + `", "output-format": "vtt", "parallelism": 3}}`
	if err := os.WriteFile(filepath.Join(confDir, "audioConfig.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("WriteFile(audioConfig.json): %v", err)
	}
}

func runAudioTool(t *testing.T, confDir, model, args string) (int, string, string) {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	writeAudioToolConfig(t, confDir, model)
	var status int
	stdout, stderr := captureStdoutStderr(t, func() {
		status = run(strings.Split(args, " "))
	})
	return status, stdout, stderr
}

func Test_e2e_audio_transcribe_tool_returns_transcript(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)

	status, stdout, stderr := runAudioTool(t, confDir, "test",
		"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

	if status != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	combined := stdout + stderr
	testboil.AssertStringContains(t, combined, "mock transcription")
	// Tool default is text, not the config file's vtt: no VTT framing in model context
	if strings.Contains(combined, "WEBVTT") {
		t.Errorf("expected text default for tool calls, got VTT: %q", combined)
	}
	if strings.HasPrefix(stdout, "WEBVTT") {
		t.Errorf("transcript must not be written raw to process stdout: %q", stdout)
	}
}

// Test_e2e_audio_transcribe_tool_flag_overrides pins that a normal query can
// configure the media tools it calls: -am picks the transcription model
// (audioConfig.json names a different one) and -af forces the transcript
// format over the tool call's own choice.
func Test_e2e_audio_transcribe_tool_flag_overrides(t *testing.T) {
	t.Run("-am overrides the configured model", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		audioFile := writeE2EAudioFile(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)
		t.Setenv("OPENAI_API_KEY", "")

		// The file names an OpenAI model that would need a key; the flag
		// routes to the mock instead.
		status, stdout, stderr := runAudioTool(t, confDir, "whisper-1",
			"-r -cm mock_test -am test -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		testboil.AssertStringContains(t, stdout+stderr, "mock transcription")
	})

	t.Run("-af overrides the tool's transcript format", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		audioFile := writeE2EAudioFile(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)

		status, stdout, stderr := runAudioTool(t, confDir, "test",
			"-r -cm mock_test -am test -af json -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		combined := stdout + stderr
		testboil.AssertStringContains(t, combined, `"start":`)
		if strings.Contains(combined, "WEBVTT") {
			t.Errorf("expected json transcript, got VTT: %q", combined)
		}
	})

	t.Run("an unknown -af value fails the run immediately", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		status, _, stderr := runAudioTool(t, confDir, "test",
			"-r -cm mock_test -af yaml -t=audio_transcribe q tool_audio_transcribe")

		testboil.FailTestIfDiff(t, status, 1)
		testboil.AssertStringContains(t, stderr, "yaml")
	})
}

func Test_e2e_audio_transcribe_tool_vtt_format(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FORMAT", "vtt")

	status, stdout, stderr := runAudioTool(t, confDir, "test",
		"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

	if status != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	testboil.AssertStringContains(t, stdout+stderr, "WEBVTT")
}

func Test_e2e_audio_transcribe_tool_diarized_text_keeps_speakers(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)

	status, stdout, stderr := runAudioTool(t, confDir, "test-diarize",
		"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

	if status != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	testboil.AssertStringContains(t, stdout+stderr, "A: mock transcription")
}

func Test_e2e_audio_transcribe_tool_listing(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	t.Run("tools listing includes audio_transcribe", func(t *testing.T) {
		status, stdout, _ := runAudio(t, "tools")
		testboil.FailTestIfDiff(t, status, 0)
		testboil.AssertStringContains(t, stdout, "audio_transcribe")
	})
	t.Run("detail view prints schema", func(t *testing.T) {
		status, stdout, _ := runAudio(t, "tools audio_transcribe")
		testboil.FailTestIfDiff(t, status, 0)
		testboil.AssertStringContains(t, stdout, "file_path")
		testboil.AssertStringContains(t, stdout, "output_format")
	})
}

func Test_e2e_audio_transcribe_tool_not_selected(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	audioFile := writeE2EAudioFile(t)
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)

	status, stdout, stderr := runAudioTool(t, confDir, "test",
		"-r -cm mock_test -t=website_text q tool_audio_transcribe")

	if status != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "mock transcription") {
		t.Errorf("tool must not be invocable when not selected, got: %q", stdout+stderr)
	}
}

func Test_e2e_audio_transcribe_tool_errors_keep_query_alive(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", filepath.Join(t.TempDir(), "gone.wav"))

		status, stdout, stderr := runAudioTool(t, confDir, "test",
			"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected query loop to continue, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		testboil.AssertStringContains(t, stdout+stderr, "gone.wav")
	})
	t.Run("invalid output format", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		audioFile := writeE2EAudioFile(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FORMAT", "yaml")

		status, stdout, stderr := runAudioTool(t, confDir, "test",
			"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected query loop to continue, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		testboil.AssertStringContains(t, stdout+stderr, "vtt")
	})
	t.Run("engine failure surfaces cause without process exit", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		audioFile := writeE2EAudioFile(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)
		// Unroutable model in config makes the engine fail inside the tool call
		status, stdout, stderr := runAudioTool(t, confDir, "unroutable9000",
			"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected query loop to continue, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		testboil.AssertStringContains(t, stdout+stderr, "unroutable9000")
	})
}
