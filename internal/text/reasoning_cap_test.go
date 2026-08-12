package text

import (
	"strings"
	"testing"
)

// TestAppendReasoning_CapsBuffer verifies the OOM guard for the querier's
// reasoning accumulator: an endless reasoning stream (a looping model) must
// not grow reasoningBuf without bound. The buffer keeps only the last
// maxReasoningBuf bytes — the tail — so tool-call context survives while
// memory stays flat. Regression for the kinoview production OOM of 2026-08-11
// (2.53 GB heap from unbounded strings.Builder growth).
func TestAppendReasoning_CapsBuffer(t *testing.T) {
	q := &Querier[*MockQuerier]{}
	chunk := strings.Repeat("r", 4096)
	for range 1024 {
		q.appendReasoning(chunk)
	}
	if q.reasoningBuf.Len() != maxReasoningBuf {
		t.Fatalf("reasoningBuf length %d, want cap %d", q.reasoningBuf.Len(), maxReasoningBuf)
	}
	if got := q.reasoningBuf.String(); !strings.HasSuffix(got, chunk) {
		t.Fatal("expected reasoningBuf to keep the tail (last chunk)")
	}
}
