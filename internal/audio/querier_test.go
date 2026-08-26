package audio

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMockTranscriber(t *testing.T) {
	t.Run("returns deterministic segments for existing file", func(t *testing.T) {
		segs, err := MockTranscriber{}.Transcribe(context.Background(), sparseFile(t, 8))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(segs) != 2 {
			t.Fatalf("expected 2 segments, got %v", len(segs))
		}
		if segs[0].Text != "mock transcription" {
			t.Errorf("unexpected first segment: %+v", segs[0])
		}
	})
	t.Run("errors on missing file", func(t *testing.T) {
		_, err := MockTranscriber{}.Transcribe(context.Background(), filepath.Join(t.TempDir(), "gone.wav"))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestTranscribeQuerier_Query(t *testing.T) {
	newQuerier := func(t *testing.T, format OutputFormat) (*TranscribeQuerier, *strings.Builder) {
		t.Helper()
		out := &strings.Builder{}
		return &TranscribeQuerier{
			Splitter: NewSplitter(MockTranscriber{}, &fakeRunner{}),
			FilePath: sparseFile(t, 8),
			Format:   format,
			Out:      out,
		}, out
	}
	t.Run("renders vtt to out", func(t *testing.T) {
		q, out := newQuerier(t, FormatVTT)
		if err := q.Query(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(out.String(), "WEBVTT\n") {
			t.Errorf("expected VTT output, got: %q", out.String())
		}
	})
	t.Run("json output ends with newline", func(t *testing.T) {
		q, out := newQuerier(t, FormatJSON)
		if err := q.Query(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := out.String()
		if !strings.HasSuffix(got, "]\n") {
			t.Errorf("expected newline-terminated json, got: %q", got)
		}
	})
	t.Run("cleanup runs after query", func(t *testing.T) {
		q, _ := newQuerier(t, FormatText)
		cleaned := false
		q.Cleanup = func() { cleaned = true }
		if err := q.Query(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cleaned {
			t.Error("expected cleanup to run")
		}
	})
	t.Run("transcription error propagates and cleanup still runs", func(t *testing.T) {
		q, _ := newQuerier(t, FormatText)
		q.FilePath = filepath.Join(t.TempDir(), "gone.wav")
		cleaned := false
		q.Cleanup = func() { cleaned = true }
		if err := q.Query(context.Background()); err == nil {
			t.Fatal("expected error, got nil")
		}
		if !cleaned {
			t.Error("expected cleanup to run on error")
		}
	})
	t.Run("suppresses completion bell to keep stdout clean", func(t *testing.T) {
		q, _ := newQuerier(t, FormatText)
		if !q.SuppressCompletionNotification() {
			t.Error("expected completion notification suppression")
		}
	})
}
