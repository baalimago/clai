package summary

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// TestAgentSummarizer_concurrentColdModelConfig pins that concurrent
// Summarize calls on a missing per-model config all return a label
// (review 2, R2-01).
func TestAgentSummarizer_concurrentColdModelConfig(t *testing.T) {
	const workers = 16
	for round := range 5 {
		confDir := seedConfigDir(t)
		if err := os.Remove(filepath.Join(confDir, "mock_test_test.json")); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		s := newTestSummarizer(t, confDir)
		errs := make([]error, workers)
		titles := make([]string, workers)
		var wg sync.WaitGroup
		for i := range workers {
			wg.Go(func() {
				got, err := s.Summarize(context.Background(), mockRequest(1))
				errs[i], titles[i] = err, got.Title
			})
		}
		wg.Wait()
		for i := range workers {
			if errs[i] != nil || titles[i] != "Mock title" {
				t.Fatalf("round %d job %d: err=%v title=%q", round, i, errs[i], titles[i])
			}
		}
	}
}

// TestAgentSummarizer_coldPriceIsSilent is the cold-price row of the
// side-effect contract: with no price in the model config and no catalog
// key, the summarizer's success path still writes nothing to stderr
// (review 2, R2-03).
func TestAgentSummarizer_coldPriceIsSilent(t *testing.T) {
	t.Setenv("DEBUG_SUMMARY", "")
	t.Setenv("DEBUG", "")
	confDir := seedConfigDir(t)
	if err := os.WriteFile(filepath.Join(confDir, "mock_test_test.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := newTestSummarizer(t, confDir)
	var stderr string
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		stderr = testboil.CaptureStderr(t, func(t *testing.T) {
			got, err := s.Summarize(context.Background(), mockRequest(1))
			if err != nil || got.Title != "Mock title" {
				t.Fatalf("Summarize = %+v, %v", got, err)
			}
			if len(got.Queries) != 1 || got.Queries[0].Usage.TotalTokens == 0 {
				t.Fatalf("Queries = %+v, want one usage row with zero cost", got.Queries)
			}
		})
	})
	if stdout != "" || stderr != "" {
		t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
	}
}

// TestNewAgentSummarizer_announcesUpgrade pins that a config upgrade the
// constructor's load performs is announced, and an up-to-date file stays
// silent (review 2, R2-04).
func TestNewAgentSummarizer_announcesUpgrade(t *testing.T) {
	t.Setenv("DEBUG", "")
	t.Run("stale file announces the added field", func(t *testing.T) {
		confDir := seedConfigDir(t)
		stale := `{"model":"test","system-prompt":"x","use-tools":false}`
		if err := os.WriteFile(filepath.Join(confDir, "textConfig.json"), []byte(stale), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			if _, err := NewAgentSummarizer(confDir); err != nil {
				t.Fatalf("NewAgentSummarizer: %v", err)
			}
		})
		if !strings.Contains(stdout, "textConfig.json") || !strings.Contains(stdout, "summary-model") {
			t.Fatalf("stdout = %q, want the upgrade announcement naming summary-model", stdout)
		}
		b, err := os.ReadFile(filepath.Join(confDir, "textConfig.json"))
		if err != nil || !strings.Contains(string(b), `"summary-model"`) {
			t.Fatalf("file not upgraded: %v\n%s", err, b)
		}
	})
	t.Run("up-to-date file is silent", func(t *testing.T) {
		confDir := seedConfigDir(t)
		if _, err := NewAgentSummarizer(confDir); err != nil {
			t.Fatalf("first load: %v", err)
		}
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			if _, err := NewAgentSummarizer(confDir); err != nil {
				t.Fatalf("NewAgentSummarizer: %v", err)
			}
		})
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})
}

// TestAgentSummarizer_querierConfig pins the querier configuration the
// summarizer hands to text.CreateQuerier: every output stream discarded,
// cost warnings routed to the trace, no persistence, no ambient servers,
// exactly the submit tool (phase 2 contract plus phase 8 R2-03, R2-09).
func TestAgentSummarizer_querierConfig(t *testing.T) {
	confDir := seedConfigDir(t)
	s := newTestSummarizer(t, confDir)
	var got text.Configurations
	s.newQuerier = func(_ context.Context, conf text.Configurations) (models.ChatQuerier, error) {
		got = conf
		return nil, errors.New("captured")
	}
	if _, err := s.Summarize(context.Background(), mockRequest(1)); err == nil || !strings.Contains(err.Error(), "captured") {
		t.Fatalf("Summarize err = %v, want the capturing constructor's error", err)
	}
	if got.Out != io.Discard || got.ErrOut != io.Discard {
		t.Fatalf("Out = %T, ErrOut = %T, want io.Discard for both", got.Out, got.ErrOut)
	}
	if got.CostWarnf == nil {
		t.Fatal("CostWarnf must route cost warnings to the trace")
	}
	if got.SaveReplyAsConv || !got.SkipAmbientMcpServers || !got.UseTools || len(got.Tools) != 1 || got.Tools[0].Specification().Name != ToolName {
		t.Fatalf("config = %+v, want no persistence, no ambient servers, exactly submit_summary", got)
	}
	if got.ConfigDir != confDir || got.Model != "test" {
		t.Fatalf("ConfigDir = %q Model = %q", got.ConfigDir, got.Model)
	}
}
