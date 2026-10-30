package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
)

func TestResponseFormatFor(t *testing.T) {
	testCases := []struct {
		model string
		want  generic.ResponseFormat
	}{
		{"whisper-1", generic.ResponseFormatVerboseJSON},
		{"gpt-4o-transcribe-diarize", generic.ResponseFormatDiarizedJSON},
		{"gpt-transcribe", generic.ResponseFormatJSON},
		{"gpt-4o-transcribe", generic.ResponseFormatJSON},
		{"gpt-4o-mini-transcribe", generic.ResponseFormatJSON},
		{"gpt-4o-mini-transcribe-2025-12-15", generic.ResponseFormatJSON},
	}
	for _, tc := range testCases {
		t.Run(tc.model, func(t *testing.T) {
			if got := ResponseFormatFor(tc.model); got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestTranscriber_Transcribe_GPTTranscribe(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "audio", "testdata", "openai_gpt_transcribe_json.json"))
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	var gotModel, gotFormat, gotChunking string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}
		gotModel = r.FormValue("model")
		gotFormat = r.FormValue("response_format")
		gotChunking = r.FormValue("chunking_strategy")
		w.Write(fixture)
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "oai-test-key")
	trans := TranscriberDefault
	trans.Model = "gpt-transcribe"
	if err := trans.Setup(); err != nil {
		t.Fatalf("failed to setup: %v", err)
	}
	trans.Transcriber.URL = server.URL
	audioFile := filepath.Join(t.TempDir(), "test.wav")
	if err := os.WriteFile(audioFile, []byte("fake-wav"), 0o644); err != nil {
		t.Fatalf("failed to write audio file: %v", err)
	}

	segs, err := trans.Transcribe(context.Background(), audioFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotModel != "gpt-transcribe" {
		t.Errorf("expected model gpt-transcribe, got: %q", gotModel)
	}
	if gotFormat != "json" {
		t.Errorf("expected json, got: %q", gotFormat)
	}
	if gotChunking != "" {
		t.Errorf("expected no chunking_strategy, got: %q", gotChunking)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %v", len(segs))
	}
	if segs[0].End != 10*time.Second {
		t.Errorf("expected end from usage.seconds, got: %v", segs[0].End)
	}
}

func TestTranscriber_Setup_FormatPerModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "oai-test-key")
	for model, want := range map[string]generic.ResponseFormat{
		"whisper-1":                 generic.ResponseFormatVerboseJSON,
		"gpt-4o-transcribe-diarize": generic.ResponseFormatDiarizedJSON,
		"gpt-transcribe":            generic.ResponseFormatJSON,
	} {
		trans := TranscriberDefault
		trans.Model = model
		if err := trans.Setup(); err != nil {
			t.Fatalf("%v: failed to setup: %v", model, err)
		}
		if trans.Transcriber.Format != want {
			t.Errorf("%v: expected format %v, got %v", model, want, trans.Transcriber.Format)
		}
	}
}
