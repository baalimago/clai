package records

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/tools"
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
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split(args, " "))
	})
	return status, stdout, stderr
}

func Test_e2e_audio_transcribe_tool_returns_transcript(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	audioFile := e2etest.WriteAudioFile(t)
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
		confDir := e2etest.SetupMainTestConfigDir(t)
		audioFile := e2etest.WriteAudioFile(t)
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
		confDir := e2etest.SetupMainTestConfigDir(t)
		audioFile := e2etest.WriteAudioFile(t)
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
		confDir := e2etest.SetupMainTestConfigDir(t)
		status, _, stderr := runAudioTool(t, confDir, "test",
			"-r -cm mock_test -af yaml -t=audio_transcribe q tool_audio_transcribe")

		testboil.FailTestIfDiff(t, status, 1)
		testboil.AssertStringContains(t, stderr, "yaml")
	})
}

// Test_e2e_audio_transcribe_tool_profile_model pins that a profile may pin the
// model the media tool it calls uses: "audio": {"model": …} reaches
// audio_transcribe for that run, and -am still outranks it.
func Test_e2e_audio_transcribe_tool_profile_model(t *testing.T) {
	writeProfile := func(t *testing.T, confDir, audioModel string) {
		t.Helper()
		e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "profiles", "meeting.json"), map[string]any{
			"name":  "meeting",
			"model": "mock_test",
			"audio": map[string]any{"model": audioModel},
		})
	}

	t.Run("the profile pins the transcribe model", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", e2etest.WriteAudioFile(t))
		t.Setenv("OPENAI_API_KEY", "")
		writeProfile(t, confDir, "test")

		// audioConfig.json names an OpenAI model that needs a key; the
		// profile routes the tool to the mock instead.
		status, stdout, stderr := runAudioTool(t, confDir, "whisper-1",
			"-r -cm mock_test -p meeting -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		testboil.AssertStringContains(t, stdout+stderr, "mock transcription")
	})
	t.Run("-am beats the profile", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", e2etest.WriteAudioFile(t))
		t.Setenv("OPENAI_API_KEY", "")
		writeProfile(t, confDir, "whisper-1")

		status, stdout, stderr := runAudioTool(t, confDir, "test",
			"-r -cm mock_test -am test -p meeting -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		testboil.AssertStringContains(t, stdout+stderr, "mock transcription")
	})
}

// Test_e2e_audio_transcribe_tool_model_alias pins alias fallback in the tool (architecture/model-aliases.md).
func Test_e2e_audio_transcribe_tool_model_alias(t *testing.T) {
	setup := func(t *testing.T) string {
		t.Helper()
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", e2etest.WriteAudioFile(t))
		t.Setenv("OPENAI_API_KEY", "")
		e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "audioConfig.json"), map[string]any{
			"transcribe":    map[string]any{"model": "whisper-1", "output-format": "vtt"},
			"model-aliases": map[string][]string{"fast": {"gpt-4o-transcribe-diarize", "test-diarize"}},
		})
		return confDir
	}
	run := func(t *testing.T, args string) string {
		t.Helper()
		oldArgs := os.Args
		t.Cleanup(func() { os.Args = oldArgs })
		var status int
		stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
			status = harness.Run(strings.Split(args, " "))
		})
		if status != 0 {
			t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		return stdout + stderr
	}
	assertFellBack := func(t *testing.T, out string) {
		t.Helper()
		testboil.AssertStringContains(t, out, "A: mock transcription")
		testboil.AssertStringContains(t, out, "▸ model gpt-4o-transcribe-diarize unavailable · ")
		testboil.AssertStringContains(t, out, "trying test-diarize")
	}

	t.Run("a profile names the alias", func(t *testing.T) {
		confDir := setup(t)
		e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "profiles", "meeting.json"), map[string]any{
			"name":  "meeting",
			"model": "mock_test",
			"audio": map[string]any{"model": "fast"},
		})
		assertFellBack(t, run(t, "-r -cm mock_test -p meeting -t=audio_transcribe q tool_audio_transcribe"))
	})
	t.Run("-am names the alias", func(t *testing.T) {
		setup(t)
		assertFellBack(t, run(t, "-r -cm mock_test -am fast -t=audio_transcribe q tool_audio_transcribe"))
	})
}

func Test_e2e_audio_transcribe_tool_vtt_format(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	audioFile := e2etest.WriteAudioFile(t)
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
	confDir := e2etest.SetupMainTestConfigDir(t)
	audioFile := e2etest.WriteAudioFile(t)
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", audioFile)

	status, stdout, stderr := runAudioTool(t, confDir, "test-diarize",
		"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

	if status != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", status, stdout, stderr)
	}
	testboil.AssertStringContains(t, stdout+stderr, "A: mock transcription")
}

func Test_e2e_audio_transcribe_tool_listing(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)

	t.Run("tools listing includes audio_transcribe", func(t *testing.T) {
		status, stdout, _ := harness.RunAudio(t, "tools")
		testboil.FailTestIfDiff(t, status, 0)
		testboil.AssertStringContains(t, stdout, "audio_transcribe")
	})
	t.Run("detail view prints schema", func(t *testing.T) {
		status, stdout, _ := harness.RunAudio(t, "tools audio_transcribe")
		testboil.FailTestIfDiff(t, status, 0)
		testboil.AssertStringContains(t, stdout, "file_path")
		testboil.AssertStringContains(t, stdout, "output_format")
	})
}

func Test_e2e_audio_transcribe_tool_not_selected(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	audioFile := e2etest.WriteAudioFile(t)
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
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", filepath.Join(t.TempDir(), "gone.wav"))

		status, stdout, stderr := runAudioTool(t, confDir, "test",
			"-r -cm mock_test -t=audio_transcribe q tool_audio_transcribe")

		if status != 0 {
			t.Fatalf("expected query loop to continue, got %d stdout=%q stderr=%q", status, stdout, stderr)
		}
		testboil.AssertStringContains(t, stdout+stderr, "gone.wav")
	})
	t.Run("invalid output format", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		audioFile := e2etest.WriteAudioFile(t)
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
		confDir := e2etest.SetupMainTestConfigDir(t)
		audioFile := e2etest.WriteAudioFile(t)
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

// withSplittingFFmpeg puts an ffprobe that reports 6 s and an ffmpeg that
// writes three chunks on PATH, and registers the audio tools against them.
func withSplittingFFmpeg(t *testing.T, fn func()) {
	t.Helper()
	binDir := t.TempDir()
	e2etest.WriteExecutable(t, binDir, "ffprobe", []byte("#!/bin/sh\necho 6.0\n"))
	e2etest.WriteExecutable(t, binDir, "ffmpeg", []byte("#!/bin/sh\nfor a; do last=$a; done\ndir=$(dirname \"$last\")\nfor i in 000 001 002; do echo chunk > \"$dir/chunk_$i.wav\"; done\n"))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tools.WithTestRegistry(t, func() {
		tools.Init(context.Background())
		fn()
	})
}

// runSplittingTranscribeTool runs a query whose model calls audio_transcribe
// on a file over the configured request size, from a pre-upgrade audioConfig.
func runSplittingTranscribeTool(t *testing.T, args string) streamRun {
	t.Helper()
	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_MOCK_AUDIO_TRANSCRIBE_FILE", e2etest.WriteAudioFile(t))
	if err := os.WriteFile(filepath.Join(confDir, "audioConfig.json"), []byte(`{"transcribe": {"model": "test", "max-request-bytes": 8}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(audioConfig.json): %v", err)
	}
	var run streamRun
	withSplittingFFmpeg(t, func() { run = runOnStreams(strings.Fields(args), streamOpts{}) })
	return run
}

// TestAudioTranscribeToolProgress is R2-02: the audio_transcribe tool runs
// on the run's context, so a split transcription draws its board and the
// audioConfig upgrade notice on stderr in text, and writes one progress
// record per state change under -json.
func TestAudioTranscribeToolProgress(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		run := runSplittingTranscribeTool(t, "-cm mock_test -t=audio_transcribe q tool_audio_transcribe")
		if run.status != 0 {
			t.Fatalf("status %d, stderr %q", run.status, run.stderr)
		}
		for _, want := range []string{"splitting via ffmpeg", "3 chunks", "transcribed", "added new field(s) to audioConfig.json"} {
			if !strings.Contains(run.stderr, want) {
				t.Errorf("stderr lacks %q: %q", want, run.stderr)
			}
		}
		if strings.Contains(run.stdout, "splitting via ffmpeg") {
			t.Errorf("the board is on stdout: %q", run.stdout)
		}
	})
	t.Run("json", func(t *testing.T) {
		run := runSplittingTranscribeTool(t, "-json -cm mock_test -t=audio_transcribe q tool_audio_transcribe")
		if run.status != 0 {
			t.Fatalf("status %d, stderr %q", run.status, run.stderr)
		}
		stdout := parseRecords(t, "stdout", run.stdout)
		stderr := parseRecords(t, "stderr", run.stderr)
		if p := ofType(stdout, "progress"); len(p) != 0 {
			t.Fatalf("progress records on stdout: %v", p)
		}
		progress := ofType(stderr, "progress")
		if len(progress) == 0 {
			t.Fatalf("no progress records on stderr: %v", stderr)
		}
		rowsDone := map[string]bool{}
		last := map[string]string{}
		for _, p := range progress {
			if p.str("title") != "audio_transcribe" {
				t.Fatalf("progress record %v, want title audio_transcribe", p)
			}
			row := p.str("row")
			if state := p.str("state") + "|" + p.str("detail"); last[row] == state {
				t.Fatalf("progress record repeated without a state change: %v", p)
			} else {
				last[row] = state
			}
			if row != "" && p.str("state") == "done" {
				rowsDone[row] = true
			}
		}
		if first := progress[0]; first.str("state") != "start" || first.str("row") != "" || !strings.Contains(first.str("detail"), "splitting via ffmpeg") {
			t.Fatalf("first progress record %v, want the board's start", first)
		}
		if final := progress[len(progress)-1]; final.str("state") != "done" || final.str("row") != "" {
			t.Fatalf("last progress record %v, want the board's done", final)
		}
		if len(rowsDone) != 3 {
			t.Fatalf("rows done %v, want three chunks", rowsDone)
		}
		for _, r := range ofType(stderr, "output") {
			if strings.Contains(r.str("text"), "splitting via ffmpeg") {
				t.Fatalf("the board is drawn under -json: %v", r)
			}
		}
		notice := false
		for _, r := range stderr {
			if strings.Contains(r.str("message")+r.str("text"), "added new field(s) to audioConfig.json") {
				notice = true
			}
		}
		if !notice {
			t.Fatalf("no audioConfig upgrade notice on stderr: %v", stderr)
		}
	})
}
