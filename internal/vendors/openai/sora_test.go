package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/video"
)

func TestNewVideoQuerierRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewVideoQuerier(video.Configurations{Model: "sora-2", Output: video.Output{Type: video.URL}})
	if err == nil {
		t.Fatalf("expected error when OPENAI_API_KEY missing")
	}
}

func TestSoraCreateRequestBuildsMultipartAndHeaders(t *testing.T) {
	q := &Sora{
		Model:   "sora-2",
		Size:    "720x1280",
		Seconds: "4",
		Prompt:  "make a video",
		apiKey:  "key",
	}

	req, err := q.createRequest(context.Background())
	if err != nil {
		t.Fatalf("createRequest failed: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("expected POST, got %s", req.Method)
	}
	if req.URL.String() != VideoURL {
		t.Fatalf("unexpected url: %s", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer key" {
		t.Fatalf("unexpected auth header: %q", got)
	}
	ct := req.Header.Get("Content-Type")
	mediatype, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("ParseMediaType(Content-Type=%q): %v", ct, err)
	}
	if mediatype != "multipart/form-data" {
		t.Fatalf("expected multipart/form-data, got %q", mediatype)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatalf("missing boundary param in content-type: %q", ct)
	}

	b, _ := io.ReadAll(req.Body)
	mr := multipart.NewReader(bytes.NewReader(b), boundary)

	fields := map[string]string{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		name := p.FormName()
		data, _ := io.ReadAll(p)
		fields[name] = string(data)
	}

	if fields["prompt"] != "make a video" {
		t.Fatalf("unexpected prompt field: %q", fields["prompt"])
	}
	if fields["model"] != "sora-2" {
		t.Fatalf("unexpected model field: %q", fields["model"])
	}
	if fields["size"] != "720x1280" {
		t.Fatalf("unexpected size field: %q", fields["size"])
	}
	if fields["seconds"] != "4" {
		t.Fatalf("unexpected seconds field: %q", fields["seconds"])
	}
}

func TestSoraDownloadURLModeDoesNotHTTP(t *testing.T) {
	q := &Sora{
		apiKey:  "key",
		client:  newTestClient(func(r *http.Request) (*http.Response, error) { t.Fatalf("unexpected http call"); return nil, nil }),
		Output:  video.Output{Type: video.URL},
		Model:   "sora-2",
		Seconds: "4",
	}
	if err := q.download(context.Background(), "id123"); err != nil {
		t.Fatalf("download: %v", err)
	}
}

func TestSoraDownloadWritesFileToOutputDir(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("fake-mp4")
	q := &Sora{
		apiKey: "key",
		Output: video.Output{Type: video.LOCAL, Dir: dir, Prefix: "pref"},
		client: newTestClient(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET, got %s", r.Method)
			}
			if !strings.HasSuffix(r.URL.String(), "/content") {
				t.Fatalf("expected content url, got %s", r.URL.String())
			}
			if got := r.Header.Get("Authorization"); got != "Bearer key" {
				t.Fatalf("unexpected auth header: %q", got)
			}
			return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(payload))}, nil
		}),
	}

	if err := q.download(context.Background(), "id123"); err != nil {
		t.Fatalf("download: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected 1 file written, got %d: %v", len(entries), names)
	}
	if !strings.HasPrefix(entries[0].Name(), "pref_") || !strings.HasSuffix(entries[0].Name(), ".mp4") {
		t.Fatalf("unexpected output filename: %q", entries[0].Name())
	}
	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(b, payload) {
		t.Fatalf("written payload mismatch: %q", string(b))
	}
}

func TestSoraPollCompleted(t *testing.T) {
	q := &Sora{
		apiKey: "key",
		client: newTestClient(func(r *http.Request) (*http.Response, error) {
			job := VideoJob{ID: "id", Status: "completed", Progress: 100}
			b, _ := json.Marshal(job)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := q.poll(ctx, "id"); err != nil {
		t.Fatalf("poll: %v", err)
	}
}

func TestSoraQuerySuccessFlow(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	payload := []byte("mp4")

	q := &Sora{
		Model:   "sora-2",
		Size:    "720x1280",
		Seconds: "4",
		Prompt:  "p",
		apiKey:  "key",
		Output:  video.Output{Type: video.LOCAL, Dir: dir, Prefix: "pref"},
		client: newTestClient(func(r *http.Request) (*http.Response, error) {
			calls++
			switch {
			case r.Method == http.MethodPost && r.URL.String() == VideoURL:
				job := VideoJob{ID: "id123", Status: "queued", Progress: 0}
				b, _ := json.Marshal(job)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
			case r.Method == http.MethodGet && r.URL.String() == VideoURL+"/id123":
				job := VideoJob{ID: "id123", Status: "completed", Progress: 100}
				b, _ := json.Marshal(job)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
			case r.Method == http.MethodGet && r.URL.String() == VideoURL+"/id123/content":
				return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(payload))}, nil
			default:
				return &http.Response{StatusCode: 500, Status: "500", Body: io.NopCloser(strings.NewReader("unexpected request"))}, nil
			}
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := q.Query(ctx); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 http calls, got %d", calls)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 output file, got %d", len(entries))
	}
}
