package openai

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

	"github.com/baalimago/clai/internal/utils"
	video "github.com/baalimago/clai/internal/video/generic"
)

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestSoraCreateRequestIncludesImageReference(t *testing.T) {
	q := &Sora{
		Model:          "sora-2",
		Prompt:         "p",
		apiKey:         "key",
		promptImageB64: base64.StdEncoding.EncodeToString([]byte("png-bytes")),
	}

	req, err := q.createRequest(context.Background())
	if err != nil {
		t.Fatalf("createRequest: %v", err)
	}

	_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	mr := multipart.NewReader(req.Body, params["boundary"])
	found := false
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		if p.FormName() == "input_reference" {
			found = true
			if ct := p.Header.Get("Content-Type"); ct != "image/png" {
				t.Fatalf("input_reference content type = %q", ct)
			}
			if !strings.HasSuffix(p.FileName(), ".png") {
				t.Fatalf("input_reference filename = %q", p.FileName())
			}
		}
	}
	if !found {
		t.Fatal("expected an input_reference part")
	}
}

func TestSoraCreateRequestImageDecodeError(t *testing.T) {
	q := &Sora{Model: "sora-2", Prompt: "p", apiKey: "key", promptImageB64: "not-base64!!!"}
	if _, err := q.createRequest(context.Background()); err == nil {
		t.Fatal("expected error for undecodable prompt image")
	}
}

func TestSoraCreateRequestDebugPrint(t *testing.T) {
	const apiKey = "key12-secret"
	q := &Sora{Model: "sora-2", Prompt: "p", apiKey: apiKey, debug: true}
	var req *http.Request
	output := captureOpenAIStdout(t, func() {
		var err error
		req, err = q.createRequest(context.Background())
		if err != nil {
			t.Errorf("createRequest: %v", err)
		}
	})
	if req == nil {
		t.Fatal("createRequest returned no request")
	}
	if !strings.Contains(output, "Sora request:") || !strings.Contains(output, "apiKey:key12...") {
		t.Fatalf("debug output does not include the request summary and masked key: %s", output)
	}
	if strings.Contains(output, apiKey) {
		t.Fatalf("debug output exposed the full API key: %s", output)
	}
}

func TestSoraQueryErrorPaths(t *testing.T) {
	badImage := &Sora{Model: "sora-2", apiKey: "key", promptImageB64: "%", client: newTestClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected http call")
		return nil, nil
	})}
	if err := badImage.Query(context.Background()); err == nil {
		t.Fatal("expected createRequest error to surface from Query")
	}

	tests := []struct {
		name   string
		client *http.Client
	}{
		{"transport error", newTestClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		})},
		{"body read error", newTestClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(errReader{errors.New("boom")})}, nil
		})},
		{"non-200", newTestClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Status: "500 Internal Server Error", Body: io.NopCloser(strings.NewReader("nope"))}, nil
		})},
		{"bad json", newTestClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader("not-json"))}, nil
		})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &Sora{Model: "sora-2", apiKey: "key", client: tc.client}
			if err := q.Query(context.Background()); err == nil {
				t.Fatalf("expected error from Query for %s", tc.name)
			}
		})
	}
}

func TestSoraDownloadErrorPaths(t *testing.T) {
	payload := []byte("mp4")

	tests := []struct {
		name   string
		client *http.Client
	}{
		{"transport error", newTestClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		})},
		{"non-200", newTestClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 404, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("missing"))}, nil
		})},
		{"body read error", newTestClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(errReader{errors.New("boom")})}, nil
		})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &Sora{apiKey: "key", Output: video.Output{Type: video.LOCAL, Dir: t.TempDir()}, client: tc.client}
			if err := q.download(context.Background(), "id"); err == nil {
				t.Fatalf("expected download error for %s", tc.name)
			}
		})
	}

	t.Run("write fallback to tmp", func(t *testing.T) {
		const prefix = "sora-fallback-test"
		matches, _ := filepath.Glob("/tmp/" + prefix + "_*.mp4")
		for _, m := range matches {
			_ = os.Remove(m)
		}
		t.Cleanup(func() {
			rem, _ := filepath.Glob("/tmp/" + prefix + "_*.mp4")
			for _, m := range rem {
				_ = os.Remove(m)
			}
		})

		q := &Sora{
			apiKey: "key",
			Output: video.Output{Type: video.LOCAL, Dir: filepath.Join(t.TempDir(), "does-not-exist"), Prefix: prefix},
			client: newTestClient(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(payload))}, nil
			}),
		}
		if err := q.download(context.Background(), "id"); err != nil {
			t.Fatalf("download: %v", err)
		}
		written, _ := filepath.Glob("/tmp/" + prefix + "_*.mp4")
		if len(written) != 1 {
			t.Fatalf("expected one /tmp fallback file, got %v", written)
		}
	})
}

func TestSoraPollContextCanceled(t *testing.T) {
	q := &Sora{apiKey: "key", client: newTestClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected http call")
		return nil, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.poll(ctx, "id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll err = %v, want context.Canceled", err)
	}
}

func TestNewVideoQuerierWarnsOnBadConfigFile(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("DEBUG", "1")

	model := "testmodel"
	if err := os.WriteFile(filepath.Join(confDir, "openai_sora_"+model+".json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A broken config is a warning, not a hard failure: the querier is still
	// built so the user can be told and retry.
	q, err := NewVideoQuerier(video.Configurations{Model: model, Output: video.Output{Type: video.LOCAL}})
	if err != nil {
		t.Fatalf("NewVideoQuerier: %v", err)
	}
	if _, ok := q.(*Sora); !ok {
		t.Fatalf("expected *Sora, got %T", q)
	}
}
