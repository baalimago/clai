package openrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const verboseFixture = `{
  "task": "transcribe",
  "duration": 3.84,
  "segments": [
    {"id": 0, "start": 0.0, "end": 3.84, "text": " Testing OpenRouter transcription."}
  ]
}`

func TestTranscriber_Setup_MissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	trans := TranscriberDefault
	err := trans.Setup()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Errorf("expected error naming OPENROUTER_API_KEY, got: %v", err)
	}
}

func TestTranscriber_Transcribe_TrimsPrefixAndSendsHeaders(t *testing.T) {
	var gotModel, gotReferer, gotTitle string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}
		gotModel = r.FormValue("model")
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-OpenRouter-Title")
		w.Write([]byte(verboseFixture))
	}))
	defer server.Close()
	t.Setenv("OPENROUTER_API_KEY", "or-test-key")
	trans := Transcriber{Model: "or:openai/whisper-1"}
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

	if gotModel != "openai/whisper-1" {
		t.Errorf("expected or: prefix trimmed, got: %q", gotModel)
	}
	if gotReferer != "clai" {
		t.Errorf("expected HTTP-Referer clai, got: %q", gotReferer)
	}
	if gotTitle != "clai" {
		t.Errorf("expected X-OpenRouter-Title clai, got: %q", gotTitle)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %v", len(segs))
	}
	if segs[0].Text != "Testing OpenRouter transcription." {
		t.Errorf("unexpected segment text: %q", segs[0].Text)
	}
}
