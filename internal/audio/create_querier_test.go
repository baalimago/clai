package audio

import (
	"errors"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/pkg/claierr"
)

func audioConfWithModel(model string) Configurations {
	conf := Default
	conf.Transcribe.Model = model
	return conf
}

func mustCreateQuerier(t *testing.T, model string) *TranscribeQuerier {
	t.Helper()
	q, err := CreateQuerier(audioConfWithModel(model), "f.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tq, ok := q.(*TranscribeQuerier)
	if !ok {
		t.Fatalf("expected *TranscribeQuerier, got %T", q)
	}
	return tq
}

// TestCreateAudioQuerier_UnknownModelNamesItsRoutes pins that a name no route
// claims keeps the supported routes in its error and offers one phrase to a
// fallback notice (architecture/model-aliases.md).
func TestCreateAudioQuerier_UnknownModelNamesItsRoutes(t *testing.T) {
	_, err := CreateQuerier(audioConfWithModel("no-such-model"), "f.wav")
	if err == nil {
		t.Fatal("CreateQuerier: no error, want the routing failure")
	}
	for _, want := range []string{"no-such-model", "supported routes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
	if !errors.Is(err, claierr.ErrModelNotFound) || !claierr.ModelUnavailable(err) {
		t.Errorf("err = %v, want errors.Is(err, claierr.ErrModelNotFound) and ModelUnavailable(err)", err)
	}
}

func TestCreateAudioQuerier_Routing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENROUTER_API_KEY", "test-key")

	t.Run("or prefix routes to openrouter", func(t *testing.T) {
		tq := mustCreateQuerier(t, "or:openai/whisper-1")
		if _, ok := tq.Splitter.Transcriber.(*openrouter.Transcriber); !ok {
			t.Errorf("expected openrouter transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("whisper routes to openai", func(t *testing.T) {
		tq := mustCreateQuerier(t, "whisper-1")
		if _, ok := tq.Splitter.Transcriber.(*openai.Transcriber); !ok {
			t.Errorf("expected openai transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("transcribe substring routes to openai", func(t *testing.T) {
		tq := mustCreateQuerier(t, "gpt-4o-transcribe-diarize")
		if _, ok := tq.Splitter.Transcriber.(*openai.Transcriber); !ok {
			t.Errorf("expected openai transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("test routes to mock", func(t *testing.T) {
		tq := mustCreateQuerier(t, "test")
		if _, ok := tq.Splitter.Transcriber.(MockTranscriber); !ok {
			t.Errorf("expected mock transcriber, got %T", tq.Splitter.Transcriber)
		}
	})
	t.Run("parallelism and model propagate to splitter", func(t *testing.T) {
		conf := audioConfWithModel("test")
		conf.Transcribe.Parallelism = 5
		q, err := CreateQuerier(conf, "f.wav")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tq := q.(*TranscribeQuerier)
		if tq.Splitter.Parallelism != 5 {
			t.Errorf("expected parallelism 5, got %v", tq.Splitter.Parallelism)
		}
		if tq.Splitter.Model != "test" {
			t.Errorf("expected model on splitter, got %v", tq.Splitter.Model)
		}
	})
	t.Run("unroutable model lists supported routes", func(t *testing.T) {
		_, err := CreateQuerier(audioConfWithModel("mystery9000"), "f.wav")
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
		_, err := CreateQuerier(conf, "f.wav")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "vtt") {
			t.Errorf("expected valid formats listed, got: %v", err)
		}
	})
}
