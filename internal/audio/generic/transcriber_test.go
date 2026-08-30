package generic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestAudioFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.wav")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("failed to write test audio file: %v", err)
	}
	return path
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("failed to read fixture %v: %v", name, err)
	}
	return b
}

func setupTranscriber(t *testing.T, model, url string) *Transcriber {
	t.Helper()
	t.Setenv("TEST_AUDIO_API_KEY", "test-key")
	trans := &Transcriber{Model: model}
	if err := trans.Setup("TEST_AUDIO_API_KEY", url, "DEBUG_TEST_AUDIO"); err != nil {
		t.Fatalf("failed to setup transcriber: %v", err)
	}
	return trans
}

func TestSetup_MissingAPIKey(t *testing.T) {
	t.Setenv("TEST_AUDIO_API_KEY", "")
	trans := &Transcriber{}
	err := trans.Setup("TEST_AUDIO_API_KEY", "http://localhost", "DEBUG_TEST_AUDIO")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "TEST_AUDIO_API_KEY") {
		t.Errorf("expected error naming the env var, got: %v", err)
	}
}

type recordedRequest struct {
	auth             string
	model            string
	responseFormat   string
	chunkingStrategy string
	fileBytes        []byte
	contentLength    int64
	headers          http.Header
}

func multipartRecorder(t *testing.T, rec *recordedRequest, respBody []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.auth = r.Header.Get("Authorization")
		rec.contentLength = r.ContentLength
		rec.headers = r.Header.Clone()
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rec.model = r.FormValue("model")
		rec.responseFormat = r.FormValue("response_format")
		rec.chunkingStrategy = r.FormValue("chunking_strategy")
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("failed to get file part: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		rec.fileBytes, err = io.ReadAll(file)
		if err != nil {
			t.Errorf("failed to read file part: %v", err)
		}
		w.Write(respBody)
	}))
}

func TestTranscribe_VerboseJSON(t *testing.T) {
	var rec recordedRequest
	server := multipartRecorder(t, &rec, loadFixture(t, "openai_whisper1_verbose.json"))
	defer server.Close()
	trans := setupTranscriber(t, "whisper-1", server.URL)
	audioContent := []byte("RIFF-fake-wav-content")

	got, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, audioContent))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.auth != "Bearer test-key" {
		t.Errorf("expected bearer auth, got: %q", rec.auth)
	}
	if rec.model != "whisper-1" {
		t.Errorf("expected model whisper-1, got: %q", rec.model)
	}
	if rec.responseFormat != "verbose_json" {
		t.Errorf("expected response_format verbose_json, got: %q", rec.responseFormat)
	}
	if rec.chunkingStrategy != "" {
		t.Errorf("expected no chunking_strategy for non-diarize models, got: %q", rec.chunkingStrategy)
	}
	if string(rec.fileBytes) != string(audioContent) {
		t.Errorf("file content mismatch, got: %q", rec.fileBytes)
	}
	want := []Segment{
		{Start: 0, End: 5280 * time.Millisecond, Text: "Hello and welcome to the meeting."},
		{Start: 5280 * time.Millisecond, End: 9040 * time.Millisecond, Text: "Let's get started with the agenda."},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %v segments, got %v", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %v: expected %+v, got %+v", i, want[i], got[i])
		}
	}
}

func TestTranscribe_DiarizedModel(t *testing.T) {
	var rec recordedRequest
	server := multipartRecorder(t, &rec, loadFixture(t, "openai_diarized.json"))
	defer server.Close()
	trans := setupTranscriber(t, "gpt-4o-transcribe-diarize", server.URL)

	got, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("x")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.responseFormat != "diarized_json" {
		t.Errorf("expected response_format diarized_json, got: %q", rec.responseFormat)
	}
	// OpenAI 400s diarize requests without it: "chunking_strategy is required
	// for diarization models"
	if rec.chunkingStrategy != "auto" {
		t.Errorf("expected chunking_strategy auto for diarize models, got: %q", rec.chunkingStrategy)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 segments, got %v", len(got))
	}
	if got[0].Speaker != "A" || got[1].Speaker != "B" {
		t.Errorf("expected speakers A and B, got: %q, %q", got[0].Speaker, got[1].Speaker)
	}
}

func TestTranscribe_ExtraHeaders(t *testing.T) {
	var rec recordedRequest
	server := multipartRecorder(t, &rec, loadFixture(t, "openrouter_verbose.json"))
	defer server.Close()
	trans := setupTranscriber(t, "whisper-1", server.URL)
	trans.ExtraHeaders = map[string]string{"HTTP-Referer": "clai"}

	if _, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("x"))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rec.headers.Get("HTTP-Referer"); got != "clai" {
		t.Errorf("expected extra header to be sent, got: %q", got)
	}
}

func TestTranscribe_StreamsLargeFile(t *testing.T) {
	var rec recordedRequest
	server := multipartRecorder(t, &rec, loadFixture(t, "openai_whisper1_verbose.json"))
	defer server.Close()
	trans := setupTranscriber(t, "whisper-1", server.URL)
	largeContent := make([]byte, 3<<20)
	for i := range largeContent {
		largeContent[i] = byte(i % 251)
	}

	if _, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, largeContent)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Streamed pipe bodies have unknown length: chunked transfer, no Content-Length
	if rec.contentLength > 0 {
		t.Errorf("expected streamed (chunked) body without Content-Length, got: %v", rec.contentLength)
	}
	if len(rec.fileBytes) != len(largeContent) {
		t.Fatalf("expected %v file bytes, got %v", len(largeContent), len(rec.fileBytes))
	}
}

func TestTranscribe_FileMissing(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	trans := setupTranscriber(t, "whisper-1", server.URL)

	_, err := trans.Transcribe(context.Background(), filepath.Join(t.TempDir(), "nonexistent.wav"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if requests != 0 {
		t.Errorf("expected no HTTP request for missing file, got %v requests", requests)
	}
}

func TestTranscribe_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "unsupported response_format"}`))
	}))
	defer server.Close()
	trans := setupTranscriber(t, "whisper-1", server.URL)

	_, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("x")))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected status in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "unsupported response_format") {
		t.Errorf("expected body in error, got: %v", err)
	}
}

func TestTranscribe_MalformedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{not json"))
	}))
	defer server.Close()
	trans := setupTranscriber(t, "whisper-1", server.URL)

	_, err := trans.Transcribe(context.Background(), writeTestAudioFile(t, []byte("x")))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "verbose_json") {
		t.Errorf("expected wrapped parse error naming format, got: %v", err)
	}
}

func TestTranscribe_ContextCancelled(t *testing.T) {
	blockUntilClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockUntilClosed
	}))
	defer func() {
		close(blockUntilClosed)
		server.Close()
	}()
	trans := setupTranscriber(t, "whisper-1", server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := trans.Transcribe(ctx, writeTestAudioFile(t, []byte("x")))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in error chain, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("expected prompt abort, took: %v", elapsed)
	}
}
