package anthropic

import (
	"testing"

	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
)

// TestSchemaConformance_anthropic holds this vendor's byte prefilter to the
// only rule it has: it may not hide a line Fields would have used. Every line
// of a generated Claude corpus — sidechains, tool results, the body that
// quotes a role marker — goes through the shared runner.
func TestSchemaConformance_anthropic(t *testing.T) {
	root := t.TempDir()
	corpus := jsonltest.WriteCorpus(t, root, jsonltest.CorpusOptions{
		Shape:           jsonltest.ShapeClaude,
		Sessions:        jsonltest.MinSessions + 2,
		LinesPerSession: 12,
	})
	lines, err := corpus.Lines()
	if err != nil {
		t.Fatalf("Corpus.Lines: %v", err)
	}
	s := SourceReader{Root: root}.schema()
	if _, ok := any(s).(vendors.LinePrefilter); !ok {
		t.Fatal("the claude-code schema no longer implements vendors.LinePrefilter; the suite would pass vacuously")
	}
	jsonltest.RunSchemaConformance(t, s, lines)

	// A prefilter that accepts everything conforms but buys nothing. A
	// Claude summary line carries no field this schema reads, so it is the
	// proof that the filter rejects something.
	summary := []byte(`{"type":"summary","summary":"a title","leafUuid":"abc"}`)
	if got := s.Fields(summary); got != (vendors.LineFields{}) {
		t.Fatalf("Fields(summary line) = %+v, want the zero contribution", got)
	}
	if s.MayContribute(summary) {
		t.Fatal("MayContribute accepted a summary line; the prefilter rejects nothing")
	}
}
