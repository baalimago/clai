package jsonltest

import (
	"fmt"
	"testing"

	"github.com/baalimago/clai/internal/vendors"
)

// conformance.go guards vendors.LinePrefilter, whose only failure mode is
// hiding a line the schema would have used. The check is per line, so a corpus
// needs a line per marker to police the marker set. It cannot catch a
// non-canonically spelled JSON key (D26): the prefilters match bytes while
// Fields decodes case-insensitively.

// CheckSchemaConformance reports the first line that schema's prefilter
// rejects although Fields would have contributed something from it. A schema
// without a prefilter conforms trivially.
func CheckSchemaConformance(schema vendors.JSONLSchema, lines [][]byte) error {
	prefilter, ok := schema.(vendors.LinePrefilter)
	if !ok {
		return nil
	}
	var zero vendors.LineFields
	for i, line := range lines {
		fields := schema.Fields(line)
		if fields == zero || prefilter.MayContribute(line) {
			continue
		}
		return fmt.Errorf("jsonltest: %v schema line %d: MayContribute rejected a line whose Fields is %+v: %s",
			schema.SourceName(), i+1, fields, line)
	}
	return nil
}

// RunSchemaConformance fails the test when CheckSchemaConformance does, and
// refuses an empty corpus: a suite that checks nothing passes for nothing.
func RunSchemaConformance(t *testing.T, schema vendors.JSONLSchema, lines [][]byte) {
	t.Helper()
	if len(lines) == 0 {
		t.Fatal("jsonltest.RunSchemaConformance: no lines to check")
	}
	if err := CheckSchemaConformance(schema, lines); err != nil {
		t.Fatal(err)
	}
}
