package audio

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_SetTranscribeOverrides(t *testing.T) {
	t.Run("valid overrides are readable and resettable", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		want := ToolOverrides{Model: "cchub:ada/diarize@1", Models: []string{"gpt-4o-transcribe-diarize", "or:elevenlabs/scribe-v2+diarize"}, Format: "json"}
		if err := SetTranscribeOverrides(want); err != nil {
			t.Fatalf("SetTranscribeOverrides: %v", err)
		}
		got := transcribeOverrides()
		if got.Model != want.Model || got.Format != want.Format || !slices.Equal(got.Models, want.Models) {
			t.Fatalf("overrides = %+v, want %+v", got, want)
		}
		ResetTranscribeOverrides()
		if got := transcribeOverrides(); got.Model != "" || got.Format != "" || got.Models != nil {
			t.Fatalf("expected cleared overrides, got %+v", got)
		}
	})

	t.Run("an unknown format is rejected before the run", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		err := SetTranscribeOverrides(ToolOverrides{Format: "yaml"})
		if err == nil {
			t.Fatal("expected an error for an unknown format")
		}
		for _, want := range []string{"yaml", "json"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
		if got := transcribeOverrides(); got.Format != "" {
			t.Fatalf("a rejected format must not be stored, got %q", got.Format)
		}
	})

	t.Run("a model list needs the model it resolves", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		err := SetTranscribeOverrides(ToolOverrides{Models: []string{"whisper-1"}})
		if err == nil || !strings.Contains(err.Error(), "whisper-1") {
			t.Fatalf("err = %v, want a refusal naming the orphan list", err)
		}
	})

	t.Run("empty overrides leave the config in charge", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		if err := SetTranscribeOverrides(ToolOverrides{}); err != nil {
			t.Fatalf("SetTranscribeOverrides: %v", err)
		}
		if got := transcribeOverrides(); got.Model != "" || got.Format != "" {
			t.Fatalf("expected no overrides, got %+v", got)
		}
	})

	t.Run("the stored list is a copy", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		models := []string{"a", "b"}
		if err := SetTranscribeOverrides(ToolOverrides{Model: "cchub:ada/x@1", Models: models}); err != nil {
			t.Fatalf("SetTranscribeOverrides: %v", err)
		}
		models[0] = "changed"
		if got := transcribeOverrides(); got.Models[0] != "a" {
			t.Fatalf("stored list = %v, want the caller's later writes ignored", got.Models)
		}
	})
}

// runEngine returns the engine's result and stderr over confDir.
func runEngine(t *testing.T, confDir, format string) (result, stderr string, err error) {
	t.Helper()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	input := sparseFile(t, 8)
	stderr = testboil.CaptureStderr(t, func(t *testing.T) {
		result, err = transcribeEngine(procenv.Context(context.Background()), input, format)
	})
	return result, stderr, err
}

// TestTranscribeEngine_ResolvesModelAliases pins alias fallback in the tool (architecture/model-aliases.md).
func TestTranscribeEngine_ResolvesModelAliases(t *testing.T) {
	ladder := modelalias.Table{"meeting": {"gpt-4o-transcribe-diarize", "test-diarize"}}

	t.Run("the config names the alias", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		confDir := seedAudioAliases(t, "meeting", ladder)
		got, stderr, err := runEngine(t, confDir, "text")
		if err != nil {
			t.Fatalf("engine: %v (stderr %q)", err, stderr)
		}
		if !strings.Contains(got, "A: mock transcription") {
			t.Fatalf("result = %q, want the diarized transcript of the model that answered", got)
		}
		if !strings.Contains(stderr, "▸ model gpt-4o-transcribe-diarize unavailable · ") || !strings.Contains(stderr, "trying test-diarize") {
			t.Fatalf("stderr = %q, want the fallback notice", stderr)
		}
	})

	t.Run("the run's model override names the alias", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		t.Cleanup(ResetTranscribeOverrides)
		confDir := seedAudioAliases(t, "whisper-1", ladder)
		if err := SetTranscribeOverrides(ToolOverrides{Model: "meeting"}); err != nil {
			t.Fatalf("SetTranscribeOverrides: %v", err)
		}
		got, stderr, err := runEngine(t, confDir, "text")
		if err != nil {
			t.Fatalf("engine: %v (stderr %q)", err, stderr)
		}
		if !strings.Contains(got, "A: mock transcription") {
			t.Fatalf("result = %q, want the alias resolved", got)
		}
	})

	t.Run("a resolved remote list is the ladder", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		t.Cleanup(ResetTranscribeOverrides)
		confDir := seedAudioAliases(t, "whisper-1", nil)
		err := SetTranscribeOverrides(ToolOverrides{Model: "cchub:ada/diarize@1", Models: []string{"gpt-4o-transcribe-diarize", "test-diarize"}})
		if err != nil {
			t.Fatalf("SetTranscribeOverrides: %v", err)
		}
		got, stderr, err := runEngine(t, confDir, "text")
		if err != nil {
			t.Fatalf("engine: %v (stderr %q)", err, stderr)
		}
		if !strings.Contains(got, "A: mock transcription") || !strings.Contains(stderr, "trying test-diarize") {
			t.Fatalf("result = %q, stderr = %q, want the remote list walked", got, stderr)
		}
	})

	t.Run("the first model that composes answers silently", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		confDir := seedAudioAliases(t, "meeting", modelalias.Table{"meeting": {"test-diarize", "gpt-4o-transcribe-diarize"}})
		got, stderr, err := runEngine(t, confDir, "json")
		if err != nil {
			t.Fatalf("engine: %v", err)
		}
		if !strings.Contains(got, `"speaker": "A"`) || strings.Contains(stderr, "▸ model ") {
			t.Fatalf("result = %q, stderr = %q, want the first model's json and no notice", got, stderr)
		}
	})

	t.Run("an exhausted ladder names every model", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		t.Setenv("OPENROUTER_API_KEY", "")
		confDir := seedAudioAliases(t, "meeting", modelalias.Table{"meeting": {"gpt-4o-transcribe-diarize", "or:elevenlabs/scribe-v2+diarize"}})
		_, _, err := runEngine(t, confDir, "text")
		if err == nil {
			t.Fatal("engine: no error, want every candidate reported")
		}
		for _, want := range []string{"gpt-4o-transcribe-diarize", "OPENAI_API_KEY", "or:elevenlabs/scribe-v2+diarize", "OPENROUTER_API_KEY"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want it to name %q", err, want)
			}
		}
	})
}

// TestTranscribeEngine_ResolvesARemoteConfigModel pins that audioConfig.json's remote list is resolved.
func TestTranscribeEngine_ResolvesARemoteConfigModel(t *testing.T) {
	t.Setenv("CLAI_CLOUD", "off")
	t.Setenv("CLAI_CACHE_DIR", t.TempDir())
	confDir := seedAudioAliases(t, "cchub:ada/diarize@1", nil)
	_, _, err := runEngine(t, confDir, "text")
	if err == nil || !strings.Contains(err.Error(), "cchub:ada/diarize@1") {
		t.Fatalf("err = %v, want the remote target resolved (offline here) and named", err)
	}
	if strings.Contains(err.Error(), "supported routes") {
		t.Fatalf("err = %v, want resolution, not routing of the target as a model", err)
	}
}
