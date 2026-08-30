package photo

import (
	"os"
	"strings"
	"testing"
)

func TestCreateQuerier(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("OPENAI_API_KEY", "key")
	defer os.Unsetenv("OPENAI_API_KEY")
	conf := Configurations{Model: "dall-e-3", Output: Output{Type: URL, Dir: tmp}}
	q, err := CreateQuerier(conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q == nil {
		t.Fatal("expected querier")
	}
}

func TestCreateQuerier_Routing(t *testing.T) {
	t.Run("unknown model errors", func(t *testing.T) {
		_, err := CreateQuerier(Configurations{Model: "mystery9000", Output: Output{Type: UNSET}})
		if err == nil || !strings.Contains(err.Error(), "failed to find photo querier") {
			t.Fatalf("expected routing error, got: %v", err)
		}
	})
	t.Run("invalid output type errors", func(t *testing.T) {
		_, err := CreateQuerier(Configurations{Model: "dall-e-3", Output: Output{Type: "bogus"}})
		if err == nil || !strings.Contains(err.Error(), "invalid output type") {
			t.Fatalf("expected output-type error, got: %v", err)
		}
	})
	t.Run("missing local output dir errors", func(t *testing.T) {
		_, err := CreateQuerier(Configurations{Model: "dall-e-3", Output: Output{Type: LOCAL, Dir: "/does/not/exist"}})
		if err == nil || !strings.Contains(err.Error(), "photo output directory") {
			t.Fatalf("expected output-dir error, got: %v", err)
		}
	})
	t.Run("gemini model routes to the gemini querier", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "key")
		q, err := CreateQuerier(Configurations{Model: "gemini-2.0-flash", Output: Output{Type: UNSET}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q == nil {
			t.Fatal("expected a querier")
		}
	})
}
