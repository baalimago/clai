package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/audio/generic"
)

func TestResolveAudioInput(t *testing.T) {
	t.Run("missing argument errors", func(t *testing.T) {
		_, _, err := resolveAudioInput(nil)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("nonexistent path errors with path in message", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "gone.wav")
		_, _, err := resolveAudioInput([]string{missing})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "gone.wav") {
			t.Errorf("expected path in error, got: %v", err)
		}
	})
	t.Run("existing path passes through with noop cleanup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "f.wav")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, cleanup, err := resolveAudioInput([]string{path})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != path {
			t.Errorf("expected %v, got %v", path, got)
		}
		cleanup()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("cleanup must not remove user files: %v", err)
		}
	})
	t.Run("dash reads stdin into temp file with sniffed extension, cleanup removes it", func(t *testing.T) {
		stdinBytes := fakeWAVBytes("stdin-audio-bytes")
		setStdin(t, stdinBytes)

		got, cleanup, err := resolveAudioInput([]string{"-"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if filepath.Ext(got) != ".wav" {
			t.Errorf("expected sniffed .wav extension on temp file, got: %v", got)
		}
		content, err := os.ReadFile(got)
		if err != nil {
			t.Fatalf("failed to read temp file: %v", err)
		}
		if string(content) != string(stdinBytes) {
			t.Errorf("unexpected temp file content: %q", content)
		}
		cleanup()
		if _, err := os.Stat(got); !os.IsNotExist(err) {
			t.Errorf("expected temp file removed by cleanup, stat err: %v", err)
		}
	})
	t.Run("dash with unrecognized bytes errors before creating a file", func(t *testing.T) {
		setStdin(t, []byte("definitely not audio bytes"))
		_, _, err := resolveAudioInput([]string{"-"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "wav") {
			t.Errorf("expected recognized formats in error, got: %v", err)
		}
	})
}

// fakeWAVBytes prefixes content with a valid RIFF/WAVE header so the
// container sniffer accepts it
func fakeWAVBytes(content string) []byte {
	return append([]byte("RIFF\x24\x08\x00\x00WAVEfmt "), content...)
}

func setStdin(t *testing.T, content []byte) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("Write: %v", err)
	}
	w.Close()
	oldStdin := os.Stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		r.Close()
	})
	os.Stdin = r
}

// TestStdinMultipartFilename proves the seam R1-01 severed: the multipart
// filename a vendor sees for stdin input must carry a recognized audio
// extension, since vendors infer the upload format from it
func TestStdinMultipartFilename(t *testing.T) {
	setStdin(t, fakeWAVBytes("stdin-audio-bytes"))
	filePath, cleanup, err := resolveAudioInput([]string{"-"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(cleanup)

	var gotFilename string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("failed to parse multipart form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("failed to get file part: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotFilename = header.Filename
		fmt.Fprint(w, `{"segments":[{"start":0,"end":1,"text":"hi"}]}`)
	}))
	t.Cleanup(server.Close)

	t.Setenv("TEST_AUDIO_API_KEY", "test-key")
	trans := &generic.Transcriber{Model: "whisper-1"}
	if err := trans.Setup("TEST_AUDIO_API_KEY", server.URL, "DEBUG_TEST_AUDIO"); err != nil {
		t.Fatalf("failed to setup transcriber: %v", err)
	}
	if _, err := trans.Transcribe(context.Background(), filePath); err != nil {
		t.Fatalf("unexpected transcribe error: %v", err)
	}
	if !strings.HasPrefix(gotFilename, "clai-audio-stdin-") || !strings.HasSuffix(gotFilename, ".wav") {
		t.Errorf("expected multipart filename 'clai-audio-stdin-*.wav', got: %q", gotFilename)
	}
}

func TestApplyFlagOverridesForAudio(t *testing.T) {
	fileConf := audio.Configurations{
		Transcribe: audio.TranscribeConfig{
			Model:        "from-file",
			OutputFormat: "text",
			Parallelism:  2,
		},
	}
	t.Run("default flags leave file values", func(t *testing.T) {
		conf := fileConf
		applyFlagOverridesForAudio(&conf, defaultFlags, defaultFlags)
		if conf != fileConf {
			t.Errorf("expected file config untouched, got: %+v", conf)
		}
	})
	t.Run("set flags beat file values", func(t *testing.T) {
		conf := fileConf
		flagSet := defaultFlags
		flagSet.AudioModel = "from-flag"
		flagSet.AudioFormat = "json"
		flagSet.Parallelism = 7
		applyFlagOverridesForAudio(&conf, flagSet, defaultFlags)
		if conf.Transcribe.Model != "from-flag" {
			t.Errorf("expected flag model, got: %v", conf.Transcribe.Model)
		}
		if conf.Transcribe.OutputFormat != "json" {
			t.Errorf("expected flag format, got: %v", conf.Transcribe.OutputFormat)
		}
		if conf.Transcribe.Parallelism != 7 {
			t.Errorf("expected flag parallelism, got: %v", conf.Transcribe.Parallelism)
		}
	})
}
