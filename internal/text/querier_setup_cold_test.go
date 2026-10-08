package text

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// TestSetupConfigFile_concurrentCold pins that N concurrent NewQuerier calls
// on a missing per-model config file all succeed (review 2, R2-01).
func TestSetupConfigFile_concurrentCold(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	const workers = 16
	for round := range 5 {
		confDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		conf := Configurations{
			Model:                 "test",
			ConfigDir:             confDir,
			SkipAmbientMcpServers: true,
			SaveReplyAsConv:       false,
			Raw:                   true,
			Out:                   &strings.Builder{},
		}
		errs := make([]error, workers)
		var wg sync.WaitGroup
		for i := range workers {
			wg.Go(func() {
				_, errs[i] = NewQuerier(t.Context(), conf, &vendors.Mock{})
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d worker %d: %v", round, i, err)
			}
		}
		b, err := os.ReadFile(filepath.Join(confDir, "mock_test_test.json"))
		if err != nil {
			t.Fatalf("model config missing after construction: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(b, &parsed); err != nil {
			t.Fatalf("model config unparseable: %v\n%s", err, b)
		}
		entries, err := os.ReadDir(confDir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp") {
				t.Fatalf("temp file left behind: %v", e.Name())
			}
		}
	}
}

// TestNewCostEnricher_defaultWarnf pins the CostWarnf seam and the debug gate:
// an enrich failure is a normal condition, so it is reported only under
// DEBUG_COST_MANAGER. A nil seam keeps ancli.Errf, an injected func receives
// the warning.
func TestNewCostEnricher_defaultWarnf(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	build := func(t *testing.T, warnf func(string, ...any)) Querier[*vendors.Mock] {
		t.Helper()
		confDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		// A model config without a price field: enrichment fails and warns.
		if err := os.WriteFile(filepath.Join(confDir, "mock_test_test.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		q, err := NewQuerier(t.Context(), Configurations{
			Model: "test", ConfigDir: confDir, SkipAmbientMcpServers: true, Raw: true, Out: &strings.Builder{}, CostWarnf: warnf,
		}, &vendors.Mock{})
		if err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		return q
	}
	// Cost diagnostics are stderr-only: the answer travels on stdout, so a price
	// miss must never enter the payload stream.
	t.Run("silent without the debug flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_COST_MANAGER", "")
		q := build(t, nil)
		var stderr string
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			stderr = testboil.CaptureStderr(t, func(t *testing.T) {
				q.costEnricher.enrich(mockChatWithUsage())
			})
		})
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q, want both empty: a price miss is debug-only", stdout, stderr)
		}
	})
	t.Run("nil keeps the default and warns on stderr", func(t *testing.T) {
		t.Setenv("DEBUG_COST_MANAGER", "1")
		q := build(t, nil)
		if q.costEnricher.warnf == nil {
			t.Fatal("warnf must default to ancli.Errf")
		}
		var stderr string
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			stderr = testboil.CaptureStderr(t, func(t *testing.T) {
				q.costEnricher.enrich(mockChatWithUsage())
			})
		})
		if strings.Contains(stdout, "failed to enrich chat with cost estimate") {
			t.Fatalf("stdout = %q, want the enrich warning off the payload stream", stdout)
		}
		if !strings.Contains(stderr, "failed to enrich chat with cost estimate") {
			t.Fatalf("stderr = %q, want the enrich warning", stderr)
		}
	})
	t.Run("injected func receives the warning", func(t *testing.T) {
		t.Setenv("DEBUG_COST_MANAGER", "1")
		var got []string
		q := build(t, func(format string, a ...any) { got = append(got, format) })
		var stderr string
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			stderr = testboil.CaptureStderr(t, func(t *testing.T) {
				q.costEnricher.enrich(mockChatWithUsage())
			})
		})
		if strings.Contains(stdout+stderr, "failed to enrich chat with cost estimate") {
			t.Fatalf("enrich warning must route through the seam, stdout = %q, stderr = %q", stdout, stderr)
		}
		if len(got) != 1 || !strings.Contains(got[0], "failed to enrich") {
			t.Fatalf("injected warnf got %q, want the enrich warning", got)
		}
	})
}

func mockChatWithUsage() pub_models.Chat {
	return pub_models.Chat{
		ID:         "usage",
		Messages:   []pub_models.Message{{Role: "user", Content: "hi"}},
		TokenUsage: &pub_models.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
}

func TestCostReadinessWaitsForDiagnosticDelivery(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mock_test_test.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	q, err := NewQuerier(t.Context(), Configurations{
		Model: "test", ConfigDir: dir, Out: io.Discard, SkipAmbientMcpServers: true,
		CostWarnf: func(string, ...any) { close(entered); <-release },
	}, &vendors.Mock{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cost diagnostic was dropped")
	}
	select {
	case <-q.costEnricher.ready:
		t.Fatal("cost readiness closed while diagnostics were still in flight")
	default:
	}
	finish()
	select {
	case <-q.costEnricher.ready:
	case <-time.After(time.Second):
		t.Fatal("cost readiness did not close after diagnostic delivery")
	}
}

// TestNewQuerier_costManagerErrorUsesCostWarnf pins that the cost manager's
// asynchronous error log also goes through CostWarnf, so a discarded-output
// querier never interleaves a warning into the host's stdout (R2-03).
func TestNewQuerier_costManagerErrorUsesCostWarnf(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// No price and no fetcher: the manager reports "missing model catalog
	// fetcher" on its error channel.
	if err := os.WriteFile(filepath.Join(confDir, "mock_test_test.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := make(chan string, 4)
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := NewQuerier(t.Context(), Configurations{
			Model: "test", ConfigDir: confDir, SkipAmbientMcpServers: true, Raw: true, Out: &strings.Builder{},
			CostWarnf: func(format string, a ...any) { got <- fmt.Sprintf(format, a...) },
		}, &vendors.Mock{}); err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		select {
		case msg := <-got:
			if !strings.Contains(msg, "cost manager error") || !strings.Contains(msg, "missing model catalog fetcher") {
				t.Fatalf("CostWarnf got %q, want the manager's fetcher error", msg)
			}
		case <-time.After(time.Second):
			t.Fatal("cost manager error never reached CostWarnf")
		}
	})
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty: the manager error must not use ancli", stdout)
	}
}

// TestNewQuerier_errOut pins the ErrOut seam: nil keeps the process stderr
// for the MCP log sink, a writer replaces it and the sink's dimension
// probes fall back without touching os.Stderr (phase 8, R2-09).
func TestNewQuerier_errOut(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	build := func(t *testing.T, errOut io.Writer) Querier[*vendors.Mock] {
		t.Helper()
		confDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		q, err := NewQuerier(t.Context(), Configurations{
			Model: "test", ConfigDir: confDir, SkipAmbientMcpServers: true, Raw: true, Out: &strings.Builder{}, ErrOut: errOut,
		}, &vendors.Mock{})
		if err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		return q
	}
	t.Run("nil keeps os.Stderr", func(t *testing.T) {
		q := build(t, nil)
		if q.mcpSink.errOut != os.Stderr {
			t.Fatalf("sink errOut = %T, want os.Stderr", q.mcpSink.errOut)
		}
	})
	t.Run("writer replaces the stream", func(t *testing.T) {
		q := build(t, io.Discard)
		if q.mcpSink.errOut != io.Discard {
			t.Fatalf("sink errOut = %T, want io.Discard", q.mcpSink.errOut)
		}
		if w := q.mcpSink.termWidth(); w != dimensions.Fallback.Width {
			t.Fatalf("termWidth = %d, want the fallback width for a non-file writer", w)
		}
	})
}

// TestNewQuerier_costWarnfRoutesManagerWarnings pins that NewQuerier hands
// CostWarnf to the cost manager itself: a manager warning raised after
// construction never reaches a process stream when the seam is set, and
// the manager's own stream is kept when it is not (review 3, R3-03).
func TestNewQuerier_costWarnfRoutesManagerWarnings(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("DEBUG", "")
	t.Setenv("DEBUG_COST_MANAGER", "")
	build := func(t *testing.T, warnf func(string, ...any)) Querier[*vendors.Mock] {
		t.Helper()
		confDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeMockPriceFile(t, confDir)
		q, err := NewQuerier(t.Context(), Configurations{
			Model: "test", ConfigDir: confDir, SkipAmbientMcpServers: true, Raw: true, Out: &strings.Builder{}, CostWarnf: warnf,
		}, &vendors.Mock{})
		if err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		select {
		case <-q.costEnricher.ready:
		case <-time.After(2 * time.Second):
			t.Fatal("cost manager never became ready")
		}
		return q
	}
	noUserRole := pub_models.Chat{
		ID:         "no-user",
		Messages:   []pub_models.Message{{Role: "assistant", Content: "hi"}},
		TokenUsage: &pub_models.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
	t.Run("nil keeps the manager's stream", func(t *testing.T) {
		q := build(t, nil)
		var err error
		stdout, stderr := captureStdoutStderrText(t, func() { _, err = q.costEnricher.manager.Enrich(noUserRole) })
		if err != nil {
			t.Fatalf("Enrich: %v", err)
		}
		if !strings.Contains(stdout+stderr, "failed to find user role") {
			t.Fatalf("stdout = %q, stderr = %q, want the manager's own warning", stdout, stderr)
		}
	})
	t.Run("seam set leaves both streams empty", func(t *testing.T) {
		got := make(chan string, 4)
		q := build(t, func(format string, a ...any) { got <- fmt.Sprintf(format, a...) })
		var err error
		stdout, stderr := captureStdoutStderrText(t, func() { _, err = q.costEnricher.manager.Enrich(noUserRole) })
		if err != nil {
			t.Fatalf("Enrich: %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
		}
		select {
		case msg := <-got:
			if !strings.Contains(msg, "failed to find user role") {
				t.Fatalf("CostWarnf got %q, want the manager's role warning", msg)
			}
		default:
			t.Fatal("CostWarnf never received the manager's warning")
		}
	})
}

// TestShouldReportCostError pins the manager-error triage: a priced-catalog
// failure (DebugError) is a normal cold condition, so it is reported only
// under the cost debug flag, while every other error stays a warning.
func TestShouldReportCostError(t *testing.T) {
	t.Run("debug error is silent without the flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_COST_MANAGER", "")
		if shouldReportCostError(cost.NewDebugError(errors.New("failed to fetch model"))) {
			t.Fatal("DebugError must be debug-only")
		}
	})
	t.Run("debug error is reported under the flag", func(t *testing.T) {
		t.Setenv("DEBUG_COST_MANAGER", "1")
		if !shouldReportCostError(cost.NewDebugError(errors.New("failed to fetch model"))) {
			t.Fatal("DebugError must be reported under DEBUG_COST_MANAGER")
		}
	})
	t.Run("other errors stay warnings", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_COST_MANAGER", "")
		if !shouldReportCostError(errors.New("missing model catalog fetcher")) {
			t.Fatal("an ordinary cost failure must stay a warning")
		}
	})
}
