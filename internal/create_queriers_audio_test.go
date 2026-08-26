package internal

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
)

func audioConfWithModel(model string) audio.Configurations {
	conf := audio.Default
	conf.Transcribe.Model = model
	return conf
}

func mustCreateAudioQuerier(t *testing.T, model string) *audio.TranscribeQuerier {
	t.Helper()
	q, err := CreateAudioQuerier(audioConfWithModel(model), "f.wav", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tq, ok := q.(*audio.TranscribeQuerier)
	if !ok {
		t.Fatalf("expected *audio.TranscribeQuerier, got %T", q)
	}
	return tq
}

func TestCreateAudioQuerier_Routing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENROUTER_API_KEY", "test-key")

	t.Run("or prefix routes to openrouter", func(t *testing.T) {
		tq := mustCreateAudioQuerier(t, "or:openai/whisper-1")
		if _, ok := tq.Splitter.Transcriber.(*openrouter.Transcriber); !ok {
			t.Errorf("expected openrouter transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("whisper routes to openai", func(t *testing.T) {
		tq := mustCreateAudioQuerier(t, "whisper-1")
		if _, ok := tq.Splitter.Transcriber.(*openai.Transcriber); !ok {
			t.Errorf("expected openai transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("transcribe substring routes to openai", func(t *testing.T) {
		tq := mustCreateAudioQuerier(t, "gpt-4o-transcribe-diarize")
		if _, ok := tq.Splitter.Transcriber.(*openai.Transcriber); !ok {
			t.Errorf("expected openai transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("test routes to mock", func(t *testing.T) {
		tq := mustCreateAudioQuerier(t, "test")
		if _, ok := tq.Splitter.Transcriber.(audio.MockTranscriber); !ok {
			t.Errorf("expected mock transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("parallelism and model propagate to splitter", func(t *testing.T) {
		conf := audioConfWithModel("test")
		conf.Transcribe.Parallelism = 5
		q, err := CreateAudioQuerier(conf, "f.wav", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tq := q.(*audio.TranscribeQuerier)
		if tq.Splitter.Parallelism != 5 {
			t.Errorf("expected parallelism 5, got %v", tq.Splitter.Parallelism)
		}
		if tq.Splitter.Model != "test" {
			t.Errorf("expected model on splitter, got %v", tq.Splitter.Model)
		}
	})
	t.Run("unroutable model lists supported routes", func(t *testing.T) {
		_, err := CreateAudioQuerier(audioConfWithModel("mystery9000"), "f.wav", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		for _, hint := range []string{"or:", "whisper", "transcribe"} {
			if !strings.Contains(err.Error(), hint) {
				t.Errorf("expected error to mention %q, got: %v", hint, err)
			}
		}
	})
	t.Run("invalid output format errors listing valid values", func(t *testing.T) {
		conf := audioConfWithModel("test")
		conf.Transcribe.OutputFormat = "yaml"
		_, err := CreateAudioQuerier(conf, "f.wav", nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "vtt") {
			t.Errorf("expected valid formats listed, got: %v", err)
		}
	})
}
