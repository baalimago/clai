package openrouter

import (
	"context"
	"net/http"
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
	var got diarizeRequest
	server := recordingServer(t, &got, http.StatusOK, []byte(verboseFixture))
	trans := setupAt(t, "or:openai/whisper-1", server.URL)

	segs, err := trans.Transcribe(context.Background(), audioFile(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.model != "openai/whisper-1" {
		t.Errorf("expected or: prefix trimmed, got: %q", got.model)
	}
	if v := got.headers.Get("HTTP-Referer"); v != "clai" {
		t.Errorf("expected HTTP-Referer clai, got: %q", v)
	}
	if v := got.headers.Get("X-OpenRouter-Title"); v != "clai" {
		t.Errorf("expected X-OpenRouter-Title clai, got: %q", v)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %v", len(segs))
	}
	if segs[0].Text != "Testing OpenRouter transcription." {
		t.Errorf("unexpected segment text: %q", segs[0].Text)
	}
}
