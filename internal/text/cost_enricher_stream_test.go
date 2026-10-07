package text

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type stubCostManager struct{ err error }

func (s stubCostManager) Start(context.Context) (<-chan struct{}, <-chan error) {
	return make(chan struct{}), make(chan error)
}

func (s stubCostManager) Enrich(pub_models.Chat) (pub_models.Chat, error) {
	return pub_models.Chat{}, s.err
}

// Test_costEnricher_diagnosticsAvoidStdout pins that cost diagnostics stay off
// stdout. A missing price is a normal condition, and clai pipes its answers, so a
// warning on stdout corrupts the payload.
func Test_costEnricher_diagnosticsAvoidStdout(t *testing.T) {
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	// A never-ready catalog forces the timeout path, which is the default warnf.
	enricher := newCostEnricher(stubCostManager{err: errors.New("find price for model \"x\": model not found")}, make(chan struct{}))
	enricher.waitFor = time.Millisecond
	_ = enricher.enrich(pub_models.Chat{})

	if err := outW.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	if err := errW.Close(); err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}
	stdout, err := io.ReadAll(outR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := io.ReadAll(errR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}

	if strings.Contains(string(stdout), "cost manager") {
		t.Errorf("cost diagnostic reached stdout: %q", string(stdout))
	}
	if !strings.Contains(string(stderr), "cost manager") {
		t.Errorf("stderr does not carry the cost diagnostic: %q", string(stderr))
	}
}
