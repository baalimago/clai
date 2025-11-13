package gemini

import (
	"testing"

	"github.com/baalimago/clai/internal/photo"
)

func TestGetFirstB64Blob(t *testing.T) {
	resp := GeminiResponse{
		Candidates: []Candidate{
			{Content: Content{Parts: []Part{{Text: strPtr("nope")}}}},
			{Content: Content{Parts: []Part{{InlineData: &InlineData{MimeType: "image/png", Data: "b64data"}}}}},
		},
	}
	if got := resp.GetFirstB64Blob(); got != "b64data" {
		t.Fatalf("expected first b64 blob 'b64data', got %q", got)
	}

	resp = GeminiResponse{Candidates: []Candidate{{Content: Content{Parts: []Part{{Text: strPtr("no b64 here")}}}}}}
	if got := resp.GetFirstB64Blob(); got != "" {
		t.Fatalf("expected empty when no inline data, got %q", got)
	}
}

func TestNewPhotoQuerierEnv(t *testing.T) {
	// missing env -> error
	t.Setenv("GEMINI_API_KEY", "")
	if _, err := NewPhotoQuerier(photo.Configurations{}); err == nil {
		t.Fatalf("expected error when GEMINI_API_KEY missing")
	}

	// present env -> ok
	t.Setenv("GEMINI_API_KEY", "secret")
	if _, err := NewPhotoQuerier(photo.Configurations{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// helpers
func strPtr(s string) *string { return &s }
