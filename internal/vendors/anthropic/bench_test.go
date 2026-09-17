package anthropic

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/vendors/jsonltest"
)

// BenchmarkSourceReaderDiscover is the worklog's cold-discovery baseline. It
// runs against a generated corpus, never a developer's real ~/.claude, so
// two runs on different machines measure the same work. Benchmarks do not
// run under the repository test gate, so this costs it nothing.
//
// Run it the same way every time, or the figures are not comparable:
//
//	go test ./internal/vendors/anthropic/ -run '^$' \
//	  -bench BenchmarkSourceReaderDiscover -benchtime 10x
func BenchmarkSourceReaderDiscover(b *testing.B) {
	root := filepath.Join(b.TempDir(), "projects")
	corpus := jsonltest.WriteCorpus(b, root, jsonltest.CorpusOptions{
		Shape:           jsonltest.ShapeClaude,
		Sessions:        jsonltest.DefaultSessions,
		LinesPerSession: jsonltest.DefaultLinesPerSession,
	})
	reader := SourceReader{Root: corpus.Root}
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		rows, err := reader.Discover(ctx, nil)
		if err != nil {
			b.Fatalf("Discover: %v", err)
		}
		if len(rows) == 0 {
			b.Fatal("Discover returned no rows; the corpus did not reach the reader")
		}
	}
}
