package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const verboseFixture = `{
  "task": "transcribe",
  "duration": 9.04,
  "segments": [
    {"id": 0, "start": 0.0, "end": 5.28, "text": " Hello and welcome to the meeting."},
    {"id": 1, "start": 5.28, "end": 9.04, "text": " Let's get started with the agenda."}
  ]
}`

func TestTranscriber_Setup_MissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	trans := TranscriberDefault
	err := trans.Setup()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("expected error naming OPENAI_API_KEY, got: %v", err)
	}
}

func TestTranscriber_Transcribe(t *testing.T) {
	var gotModel, gotFormat, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
		}
		gotModel = r.FormValue("model")
		gotFormat = r.FormValue("response_format")
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(verboseFixture))
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "oai-test-key")
	trans := TranscriberDefault
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

	if gotModel != "whisper-1" {
		t.Errorf("expected default model whisper-1, got: %q", gotModel)
	}
	if gotFormat != "verbose_json" {
		t.Errorf("expected verbose_json, got: %q", gotFormat)
	}
	if gotAuth != "Bearer oai-test-key" {
		t.Errorf("expected bearer auth, got: %q", gotAuth)
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %v", len(segs))
	}
	if segs[1].Text != "Let's get started with the agenda." {
		t.Errorf("unexpected segment text: %q", segs[1].Text)
	}
	if segs[1].End != 9040*time.Millisecond {
		t.Errorf("unexpected segment end: %v", segs[1].End)
	}
}

func TestTranscriberDefault_URL(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "oai-test-key")
	trans := TranscriberDefault
	if err := trans.Setup(); err != nil {
		t.Fatalf("failed to setup: %v", err)
	}
	if trans.Transcriber.URL != TranscribeURL {
		t.Errorf("expected default URL %v, got: %v", TranscribeURL, trans.Transcriber.URL)
	}
}
