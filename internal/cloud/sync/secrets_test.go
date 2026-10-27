package sync

import (
	"encoding/json"
	"os"
	"testing"
)

// The vectors are shared with clai-cloud's cloud/site/secrets.ts, so a
// file the CLI commits is a file the server accepts.
func TestFindSecret_sharedVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/secrets.vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Vectors []struct {
			File   string `json:"file"`
			Body   string `json:"body"`
			Secret bool   `json:"secret"`
			Field  string `json:"field"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Vectors) < 10 {
		t.Fatalf("only %d vectors", len(doc.Vectors))
	}
	for _, v := range doc.Vectors {
		got, found := FindSecret(v.File, []byte(v.Body))
		if found != v.Secret || (found && got.Field != v.Field) {
			t.Errorf("FindSecret(%s, %s) = %+v %v, want %v %q", v.File, v.Body, got, found, v.Secret, v.Field)
		}
		if found && got.String() == "" {
			t.Errorf("empty description for %s", v.Body)
		}
	}
}
