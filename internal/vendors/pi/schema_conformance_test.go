package pi

import (
	"testing"

	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
)

// TestSchemaConformance_pi runs the shared prefilter guard over a generated pi
// corpus, and then pins the two lines that decide whether the filter is worth
// having: a model change contributes nothing and must be skipped, while a
// session line carries the file's whole identity and must never be.
func TestSchemaConformance_pi(t *testing.T) {
	root := t.TempDir()
	corpus := jsonltest.WriteCorpus(t, root, jsonltest.CorpusOptions{
		Shape:           jsonltest.ShapePi,
		Sessions:        jsonltest.MinSessions + 2,
		LinesPerSession: 12,
	})
	lines, err := corpus.Lines()
	if err != nil {
		t.Fatalf("Corpus.Lines: %v", err)
	}
	s := SourceReader{Root: root}.schema()
	if _, ok := any(s).(vendors.LinePrefilter); !ok {
		t.Fatal("the pi schema no longer implements vendors.LinePrefilter; the suite would pass vacuously")
	}
	jsonltest.RunSchemaConformance(t, s, lines)

	for _, tc := range []struct {
		name           string
		line           string
		wantContribute bool
	}{
		{"model change", `{"type":"model_change","model":"m1","timestamp":"2026-01-01T00:00:00Z"}`, false},
		{"thinking level", `{"type":"thinking_level_change","level":"high"}`, false},
		{"session", `{"type":"session","id":"s1","cwd":"/tmp","timestamp":"2026-01-01T00:00:00Z"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := []byte(tc.line)
			contributes := s.Fields(line) != (vendors.LineFields{})
			if contributes != tc.wantContribute {
				t.Fatalf("Fields contributes = %v, want %v", contributes, tc.wantContribute)
			}
			if got := s.MayContribute(line); got != tc.wantContribute {
				t.Fatalf("MayContribute = %v, want %v", got, tc.wantContribute)
			}
		})
	}
}
