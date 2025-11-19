package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/photo"
)

func TestGetFirstB64BlobOK(t *testing.T) {
	s := "hi"
	gr := GeminiResponse{
		Candidates: []Candidate{
			{Content: Content{
				Parts: []Part{
					{Text: &s},
				},
			}},
			{Content: Content{
				Parts: []Part{
					{InlineData: &InlineData{
						MimeType: "image/png",
						Data:     "abc",
					}},
				},
			}},
		},
	}
	got, err := gr.GetFirstB64Blob()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "abc" {
		t.Fatalf("got %q want %q", got, "abc")
	}
}

func TestGetFirstB64BlobNoImage(t *testing.T) {
	gr := GeminiResponse{
		Candidates: []Candidate{
			{Content: Content{
				Parts: []Part{},
			}},
		},
	}
	got, err := gr.GetFirstB64Blob()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if got != "" {
		t.Fatalf("got %q want empty", got)
	}
	if !strings.Contains(err.Error(),
		"failed to find any image in response") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func setDefaultClient(c *http.Client) func() {
	old := http.DefaultClient
	http.DefaultClient = c
	return func() { http.DefaultClient = old }
}

func TestQueryBuildsReqAndSavesImage(t *testing.T) {
	tmp := t.TempDir()
	b64 := base64.StdEncoding.EncodeToString([]byte("abc"))
	resp := GeminiResponse{
		Candidates: []Candidate{
			{Content: Content{
				Parts: []Part{
					{InlineData: &InlineData{
						MimeType: "image/png",
						Data:     b64,
					}},
				},
			}},
		},
	}
	b, _ := json.Marshal(resp)

	called := false
	restore := setDefaultClient(&http.Client{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			if r.Method != http.MethodPost {
				t.Fatalf("method %s", r.Method)
			}
			if r.URL.Host != "generativelanguage.googleapis.com" {
				t.Fatalf("host %s", r.URL.Host)
			}
			expPath := "/v1beta/models/test:generateContent"
			if r.URL.Path != expPath {
				t.Fatalf("path %s", r.URL.Path)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("ct %s", ct)
			}
			if k := r.Header.Get("x-goog-api-key"); k != "key" {
				t.Fatalf("key %s", k)
			}
			body, _ := io.ReadAll(r.Body)
			var got GeminiRequest
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal req: %v", err)
			}
			if len(got.Contents) != 1 {
				t.Fatalf("contents %d", len(got.Contents))
			}
			ps := got.Contents[0].Parts
			if len(ps) == 0 || ps[0].Text == nil {
				t.Fatalf("missing text part")
			}
			if *ps[0].Text != "p" {
				t.Fatalf("text %q", *ps[0].Text)
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader(b)),
			}, nil
		}),
	})
	defer restore()

	q := &GeminiFlashImage{
		Configurations: photo.Configurations{
			Model:  "test",
			Raw:    true,
			Prompt: "p",
			Output: photo.Output{
				Type:   photo.LOCAL,
				Dir:    tmp,
				Prefix: "x",
			},
		},
		apiKey: "key",
	}
	if err := q.Query(context.Background()); err != nil {
		t.Fatalf("query err: %v", err)
	}
	if !called {
		t.Fatalf("http not called")
	}
	ents, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	found := false
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".png") {
			_, err := os.ReadFile(filepath.Join(tmp, e.Name()))
			if err != nil {
				t.Fatalf("read saved: %v", err)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no .png saved")
	}
}

func TestQueryHandlesNonOK(t *testing.T) {
	restore := setDefaultClient(&http.Client{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(bytes.NewBufferString("nope")),
			}, nil
		}),
	})
	defer restore()

	q := &GeminiFlashImage{
		Configurations: photo.Configurations{
			Model:  "test",
			Raw:    true,
			Prompt: "p",
			Output: photo.Output{
				Type:   photo.LOCAL,
				Dir:    t.TempDir(),
				Prefix: "x",
			},
		},
		apiKey: "key",
	}
	err := q.Query(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "gemini: status 500") {
		t.Fatalf("err %v", err)
	}
}

func TestQueryInvalidJSON(t *testing.T) {
	restore := setDefaultClient(&http.Client{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString("nope")),
			}, nil
		}),
	})
	defer restore()

	q := &GeminiFlashImage{
		Configurations: photo.Configurations{
			Model:  "test",
			Raw:    true,
			Prompt: "p",
			Output: photo.Output{
				Type:   photo.LOCAL,
				Dir:    t.TempDir(),
				Prefix: "x",
			},
		},
		apiKey: "key",
	}
	err := q.Query(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "failed to decode JSON") {
		t.Fatalf("err %v", err)
	}
}

func TestQueryNoImageInResponse(t *testing.T) {
	resp := GeminiResponse{
		Candidates: []Candidate{
			{Content: Content{
				Parts: []Part{
					{Text: ptr("hi")},
				},
			}},
		},
	}
	b, _ := json.Marshal(resp)

	restore := setDefaultClient(&http.Client{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader(b)),
			}, nil
		}),
	})
	defer restore()

	q := &GeminiFlashImage{
		Configurations: photo.Configurations{
			Model:  "test",
			Raw:    true,
			Prompt: "p",
			Output: photo.Output{
				Type:   photo.LOCAL,
				Dir:    t.TempDir(),
				Prefix: "x",
			},
		},
		apiKey: "key",
	}
	err := q.Query(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "failed to get first b64 blob") {
		t.Fatalf("err %v", err)
	}
}

func ptr(s string) *string { return &s }
