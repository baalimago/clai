package audio

import (
	"strings"
	"testing"
)

func Test_SetTranscribeOverrides(t *testing.T) {
	t.Run("valid overrides are readable and resettable", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		if err := SetTranscribeOverrides("gpt-4o-transcribe-diarize", "json"); err != nil {
			t.Fatalf("SetTranscribeOverrides: %v", err)
		}
		model, format := transcribeOverrides()
		if model != "gpt-4o-transcribe-diarize" || format != "json" {
			t.Fatalf("overrides: got %q/%q", model, format)
		}
		ResetTranscribeOverrides()
		if model, format := transcribeOverrides(); model != "" || format != "" {
			t.Fatalf("expected cleared overrides, got %q/%q", model, format)
		}
	})

	t.Run("an unknown format is rejected before the run", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		err := SetTranscribeOverrides("", "yaml")
		if err == nil {
			t.Fatal("expected an error for an unknown format")
		}
		for _, want := range []string{"yaml", "json"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
		if _, format := transcribeOverrides(); format != "" {
			t.Fatalf("a rejected format must not be stored, got %q", format)
		}
	})

	t.Run("empty overrides leave the config in charge", func(t *testing.T) {
		t.Cleanup(ResetTranscribeOverrides)
		if err := SetTranscribeOverrides("", ""); err != nil {
			t.Fatalf("SetTranscribeOverrides: %v", err)
		}
		if model, format := transcribeOverrides(); model != "" || format != "" {
			t.Fatalf("expected no overrides, got %q/%q", model, format)
		}
	})
}
