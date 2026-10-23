package video

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// soraRecorder answers every request with 500 after keeping the input_reference part.
type soraRecorder struct {
	references [][]byte
	prompts    []string
}

func (r *soraRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	reader := multipart.NewReader(req.Body, params["boundary"])
	var reference []byte
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		switch part.FormName() {
		case "input_reference":
			reference = data
		case "prompt":
			r.prompts = append(r.prompts, string(data))
		}
	}
	r.references = append(r.references, reference)
	return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 stop", Body: io.NopCloser(strings.NewReader("stop")), Request: req}, nil
}

func setStdin(t *testing.T, f *os.File) {
	t.Helper()
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		_ = f.Close()
	})
}

func pipedStdin(t *testing.T, data []byte) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	return r
}

func TestVideo_StdinImage(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "key")
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64)...)
	recorder := &soraRecorder{}
	run := func(args ...string) {
		t.Helper()
		c := Command(CommandDeps{
			ConfigPrep: func() (string, error) { return confDir, nil },
			Client:     &http.Client{Transport: recorder},
		})
		if err := c.Flagset().Parse(append([]string{"-vm", "sora-2", "-vd", t.TempDir()}, args...)); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if err := c.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "500 stop") {
			t.Fatalf("Run: %v, want the recorder's stop", err)
		}
	}

	setStdin(t, pipedStdin(t, []byte(base64.StdEncoding.EncodeToString(png)+"\n")))
	run("animate", "this")

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	setStdin(t, devNull)
	path := filepath.Join(t.TempDir(), "x.png")
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run("-img", path, "animate", "this")

	if len(recorder.references) != 2 {
		t.Fatalf("Sora received %d requests, want 2", len(recorder.references))
	}
	for i, ref := range recorder.references {
		if !bytes.Equal(ref, png) {
			t.Fatalf("request %d input_reference = %q, want the png", i, ref)
		}
		if recorder.prompts[i] != "animate this" {
			t.Fatalf("request %d prompt = %q, want the prompt text without image data", i, recorder.prompts[i])
		}
	}
}
