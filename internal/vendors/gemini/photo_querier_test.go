package gemini

import (
	"strings"
	"testing"

	photo "github.com/baalimago/clai/internal/photo/generic"
)

func TestNewPhotoQuerier(t *testing.T) {
	t.Run("names the api key in the failure", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		_, err := NewPhotoQuerier(photo.Configurations{})
		if err == nil {
			t.Fatalf("NewPhotoQuerier succeeded without an api key")
		}
		if !strings.Contains(err.Error(), "GEMINI_API_KEY") {
			t.Errorf("err = %v, want it to name GEMINI_API_KEY", err)
		}
	})

	t.Run("builds a querier", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "key")
		querier, err := NewPhotoQuerier(photo.Configurations{Model: "gemini-3.6-flash"})
		if err != nil {
			t.Fatalf("NewPhotoQuerier: %v", err)
		}
		if querier == nil {
			t.Fatalf("querier is nil")
		}
		flash, ok := querier.(*GeminiFlashImage)
		if !ok {
			t.Fatalf("querier = %T, want *GeminiFlashImage", querier)
		}
		if flash.apiKey != "key" || flash.Model != "gemini-3.6-flash" {
			t.Errorf("querier = %+v, want the key and model carried through", flash)
		}
	})
}
