//go:build acceptance

package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors/openai"
)

// Reference recording (README, "Reference recording").
const referenceSHA256 = "5c28ff2c021f526644b8995f533f39476305baed7e8863af8f53be9f5b89e635"

// TestReferenceMeeting runs the real Splitter against the real provider on
// the reference recording and evaluates the annotation. Human required:
//
//	CLAI_ACCEPTANCE_AUDIO=/path/to/recording.wav \
//	  go test -tags acceptance -run TestReferenceMeeting -v ./internal/audio/
func TestReferenceMeeting(t *testing.T) {
	path := os.Getenv("CLAI_ACCEPTANCE_AUDIO")
	if path == "" {
		t.Fatal("CLAI_ACCEPTANCE_AUDIO must point at the reference recording")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := hex.EncodeToString(h.Sum(nil)); got != referenceSHA256 {
		t.Fatalf("recording hash mismatch: expected %v, got %v", referenceSHA256, got)
	}
	annFile, err := os.Open(filepath.Join("testdata", "reference-annotation.tsv"))
	if err != nil {
		t.Fatalf("annotation missing (human required): %v", err)
	}
	ann, err := ParseAnnotation(annFile)
	annFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	conf := Default
	conf.Transcribe.Model = "gpt-4o-transcribe-diarize"
	vendor := openai.TranscriberDefault
	vendor.Model = conf.Transcribe.Model
	if err := vendor.Setup(); err != nil {
		t.Fatal(err)
	}
	budgets, err := ResolveBudgets(conf.Transcribe)
	if err != nil {
		t.Fatal(err)
	}
	splitter := NewSplitter(&vendor, ExecRunner{})
	splitter.Model = conf.Transcribe.Model
	splitter.Budgets = budgets
	splitter.MaxBytes = budgets.MaxRequestBytes
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	segs, err := splitter.Transcribe(ctx, path)
	if err != nil {
		t.Fatalf("transcription failed: %v", err)
	}
	report := Evaluate(segs, ann)
	t.Logf("\n%v", report)
	if report.Merges != 0 || report.Extra != 0 {
		t.Errorf("identity safety gate failed: merges %v, extra material IDs %v", report.Merges, report.Extra)
	}
	if report.Attribution() < 0.95 {
		t.Errorf("attribution gate failed: %.1f%%", 100*report.Attribution())
	}
	if report.UnknownShare() >= 0.02 {
		t.Errorf("unknown gate failed: %.1f%%", 100*report.UnknownShare())
	}
}
