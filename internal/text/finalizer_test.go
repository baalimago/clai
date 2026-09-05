package text

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Test_Finalizer_PersistFailure_JoinsRunError pins the S8 repair: a failed
// reply persist at run end joins the run's returned error, so a later
// -re/-dre cannot silently read stale state (worklog
// 2026-09-05-error-propagation, phase 8).
func Test_Finalizer_PersistFailure_JoinsRunError(t *testing.T) {
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 2)
		out <- "the final answer"
		close(out)
		return out, nil
	}

	// configDir points at a regular file, so the persist's MkdirAll for
	// <configDir>/conversations fails deterministically and the cause stays a
	// *os.PathError through every %w wrap.
	blocker := filepath.Join(t.TempDir(), "config-dir-blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	q := &Querier[*MockQuerier]{
		out:       &strings.Builder{},
		Model:     model,
		configDir: blocker,
	}
	session := &QuerySession{
		Chat: pub_models.Chat{
			Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
		},
		ShouldSaveReply: true,
	}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		finalizer:    sessionFinalizer[*MockQuerier]{querier: q},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err == nil {
		t.Fatal("expected the persist failure to surface from Run, got nil")
	}
	if !strings.Contains(err.Error(), "failed to save previous query") {
		t.Errorf("err = %v, want it to name the failed reply persist", err)
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("err = %v, want the persist's *os.PathError cause reachable", err)
	}
	if !session.Failed {
		t.Error("session must be marked failed when the persist failure joins the run error")
	}
}
