package jsonltest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/vendors"
)

// fakeSchema is a minimal JSONLSchema whose prefilter a test dictates. It
// exists so the conformance runner's own failure is observable: a helper that
// only calls t.Fatal has no failure a test can see (D21).
type fakeSchema struct {
	prefilter func(line []byte) bool
}

func (fakeSchema) SourceName() string { return "fake" }
func (fakeSchema) Root() string       { return "" }
func (fakeSchema) SkipDirs() []string { return nil }

// Fields treats any line containing "keep" as a user message, so the fixture
// lines below say plainly which ones contribute.
func (fakeSchema) Fields(line []byte) vendors.LineFields {
	if !bytes.Contains(line, []byte("keep")) {
		return vendors.LineFields{}
	}
	return vendors.LineFields{SessionID: "s1", Role: vendors.LineRoleUser}
}

// prefilteredSchema adds the optional upgrade; fakeSchema alone does not
// implement it, which is the "not implemented is always correct" case.
type prefilteredSchema struct {
	fakeSchema
}

func (p prefilteredSchema) MayContribute(line []byte) bool { return p.prefilter(line) }

func conformanceLines() [][]byte {
	return [][]byte{
		[]byte(`{"text":"keep this one"}`),
		[]byte(`{"text":"drop this one"}`),
		[]byte(`{"text":"keep this one too"}`),
	}
}

// TestCheckSchemaConformance_detectsUnderInclusivePrefilter is the error row
// the whole suite exists for: a prefilter that hides a contributing line is
// reported, and the report names the line.
func TestCheckSchemaConformance_detectsUnderInclusivePrefilter(t *testing.T) {
	schema := prefilteredSchema{fakeSchema{prefilter: func(line []byte) bool {
		return !bytes.Contains(line, []byte("too"))
	}}}
	err := CheckSchemaConformance(schema, conformanceLines())
	if err == nil {
		t.Fatal("an under-inclusive prefilter conformed; the suite cannot fail")
	}
	for _, want := range []string{"fake", "line 3", "keep this one too"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestCheckSchemaConformance_acceptsOverInclusivePrefilter accepts the safe
// direction: a prefilter that passes everything costs time, never a count.
func TestCheckSchemaConformance_acceptsOverInclusivePrefilter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema vendors.JSONLSchema
	}{
		{"accepts everything", prefilteredSchema{fakeSchema{prefilter: func([]byte) bool { return true }}}},
		{"exact", prefilteredSchema{fakeSchema{prefilter: func(line []byte) bool {
			return bytes.Contains(line, []byte("keep"))
		}}}},
		{"no prefilter at all", fakeSchema{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckSchemaConformance(tc.schema, conformanceLines()); err != nil {
				t.Fatalf("CheckSchemaConformance: %v", err)
			}
		})
	}
}

// TestRunSchemaConformance_rejectsEmptyCorpus keeps the fatal-ing wrapper from
// passing for the wrong reason. It is checked through the core, because the
// wrapper's own failure cannot be intercepted.
func TestRunSchemaConformance_rejectsEmptyCorpus(t *testing.T) {
	if err := CheckSchemaConformance(fakeSchema{}, nil); err != nil {
		t.Fatalf("the core accepts an empty line set: %v", err)
	}
	// The guard lives in the wrapper, so the corpus is what must be non-empty.
	root := t.TempDir()
	corpus := WriteCorpus(t, root, CorpusOptions{Shape: ShapeClaude, Sessions: 1, LinesPerSession: 3})
	lines, err := corpus.Lines()
	if err != nil {
		t.Fatalf("Corpus.Lines: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("a one-session corpus yielded no lines")
	}
	RunSchemaConformance(t, fakeSchema{}, lines)
}
