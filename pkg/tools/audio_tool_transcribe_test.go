package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func withFakeEngine(t *testing.T, engine func(ctx context.Context, filePath, outputFormat string) (string, error)) {
	t.Helper()
	orig := AudioTranscribeEngine
	AudioTranscribeEngine = engine
	t.Cleanup(func() { AudioTranscribeEngine = orig })
}

func TestAudioTranscribe_Specification(t *testing.T) {
	spec := AudioTranscribe.Specification()
	if spec.Name != "audio_transcribe" {
		t.Errorf("expected name audio_transcribe, got: %v", spec.Name)
	}
	if len(spec.Inputs.Required) != 1 || spec.Inputs.Required[0] != "file_path" {
		t.Errorf("expected file_path required, got: %v", spec.Inputs.Required)
	}
	format, ok := spec.Inputs.Properties["output_format"]
	if !ok {
		t.Fatal("expected output_format property")
	}
	if format.Enum == nil || len(*format.Enum) != 4 {
		t.Errorf("expected 4-value output_format enum, got: %v", format.Enum)
	}
}

func TestAudioTranscribe_Call(t *testing.T) {
	t.Run("passes file path and defaults format to text", func(t *testing.T) {
		var gotPath, gotFormat string
		withFakeEngine(t, func(_ context.Context, filePath, outputFormat string) (string, error) {
			gotPath, gotFormat = filePath, outputFormat
			return "the transcript", nil
		})
		got, err := AudioTranscribe.Call(pub_models.Input{"file_path": "f.wav"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "the transcript" {
			t.Errorf("expected engine result returned, got: %q", got)
		}
		if gotPath != "f.wav" || gotFormat != "text" {
			t.Errorf("expected f.wav/text, got: %q/%q", gotPath, gotFormat)
		}
	})
	t.Run("forwards explicit output_format", func(t *testing.T) {
		var gotFormat string
		withFakeEngine(t, func(_ context.Context, _, outputFormat string) (string, error) {
			gotFormat = outputFormat
			return "", nil
		})
		if _, err := AudioTranscribe.Call(pub_models.Input{"file_path": "f.wav", "output_format": "vtt"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotFormat != "vtt" {
			t.Errorf("expected vtt, got: %q", gotFormat)
		}
	})
	t.Run("missing file_path errors", func(t *testing.T) {
		withFakeEngine(t, func(context.Context, string, string) (string, error) { return "", nil })
		_, err := AudioTranscribe.Call(pub_models.Input{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "file_path") {
			t.Errorf("expected file_path in error, got: %v", err)
		}
	})
	t.Run("non-string output_format errors", func(t *testing.T) {
		withFakeEngine(t, func(context.Context, string, string) (string, error) { return "", nil })
		_, err := AudioTranscribe.Call(pub_models.Input{"file_path": "f.wav", "output_format": 7})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("engine errors propagate as tool errors", func(t *testing.T) {
		withFakeEngine(t, func(context.Context, string, string) (string, error) {
			return "", errors.New("vendor exploded")
		})
		_, err := AudioTranscribe.Call(pub_models.Input{"file_path": "f.wav"})
		if err == nil || !strings.Contains(err.Error(), "vendor exploded") {
			t.Errorf("expected engine error propagated, got: %v", err)
		}
	})
	t.Run("unwired engine yields clear error", func(t *testing.T) {
		withFakeEngine(t, nil)
		_, err := AudioTranscribe.Call(pub_models.Input{"file_path": "f.wav"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

type ctxKey struct{}

// TestAudioTranscribe_CallWithContext is R2-02: the engine runs on the
// call's context, so its progress and notices reach the run's streams.
func TestAudioTranscribe_CallWithContext(t *testing.T) {
	var got any
	withFakeEngine(t, func(ctx context.Context, _, _ string) (string, error) {
		got = ctx.Value(ctxKey{})
		return "", nil
	})
	ctx := context.WithValue(context.Background(), ctxKey{}, "the run")
	if _, err := AudioTranscribe.CallWithContext(ctx, pub_models.Input{"file_path": "f.wav"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "the run" {
		t.Fatalf("engine context value = %v, want the call's context", got)
	}
}
