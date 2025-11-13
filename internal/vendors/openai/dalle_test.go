package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/baalimago/clai/internal/photo"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestClient(fn roundTripperFunc) *http.Client { return &http.Client{Transport: fn} }

func TestNewPhotoQuerierRequiresAPIKey(t *testing.T) {
	// ensure missing
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewPhotoQuerier(photo.Configurations{Model: "gpt-image-1", Output: photo.Output{Type: photo.URL}})
	if err == nil {
		t.Fatalf("expected error when OPENAI_API_KEY missing")
	}
}

func TestCreateRequestBuildsExpectedPayloadAndHeaders(t *testing.T) {
	q := &DallE{
		Model:          "gpt-image-1",
		N:              2,
		Size:           "256x256",
		Quality:        "hd",
		Style:          "vivid",
		ResponseFormat: "url",
		Prompt:         "draw a cat",
		apiKey:         "key",
	}
	req, err := q.createRequest(context.Background())
	if err != nil {
		t.Fatalf("createRequest failed: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", req.Method)
	}
	if req.URL.String() != PhotoURL {
		t.Errorf("unexpected url: %s", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer key" {
		t.Errorf("unexpected auth header: %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("unexpected content-type: %q", got)
	}
	b, _ := io.ReadAll(req.Body)
	var gotReq DallERequest
	if err := json.Unmarshal(b, &gotReq); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	exp := DallERequest{Model: "gpt-image-1", N: 2, Size: "256x256", Quality: "hd", Style: "vivid", ResponseFormat: "url", Prompt: "draw a cat"}
	if gotReq != exp {
		t.Errorf("unexpected payload: %#v", gotReq)
	}
}

func TestDoHandlesNonOK(t *testing.T) {
	q := &DallE{client: newTestClient(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Status: "500", Body: io.NopCloser(bytes.NewBufferString("nope"))}, nil
	})}
	q.raw = true
	req, _ := http.NewRequest("POST", PhotoURL, nil)
	if err := q.do(req); err == nil {
		t.Fatalf("expected error for non-OK status")
	}
}

func TestDoParsesURLResponse(t *testing.T) {
	img := ImageResponses{Created: 1, Data: []ImageResponse{{URL: "http://x/y.jpg", RevisedPrompt: "rp"}}}
	b, _ := json.Marshal(img)
	q := &DallE{
		client: newTestClient(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
		}),
		Output: photo.Output{Type: photo.URL},
	}
	req, _ := http.NewRequest("POST", PhotoURL, nil)
	if err := q.do(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQuerySuccessFlow(t *testing.T) {
	img := ImageResponses{Created: 1, Data: []ImageResponse{{B64JSON: base64.StdEncoding.EncodeToString([]byte("abc")), RevisedPrompt: "rp"}}}
	b, _ := json.Marshal(img)
	q := &DallE{
		Model:          "gpt-image-1",
		N:              1,
		Size:           "1024x1024",
		Quality:        "auto",
		ResponseFormat: "b64_json",
		Prompt:         "p",
		apiKey:         "key",
		client: newTestClient(func(r *http.Request) (*http.Response, error) {
			// Return success for both createRequest POST body consumption and do
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
		}),
		Output: photo.Output{Type: photo.URL},
	}
	if err := q.Query(context.Background()); err != nil {
		t.Fatalf("unexpected error in Query: %v", err)
	}
}
