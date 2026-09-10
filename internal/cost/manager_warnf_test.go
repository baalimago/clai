package cost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func captureStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	stdout = testboil.CaptureStdout(t, func(t *testing.T) {
		stderr = testboil.CaptureStderr(t, func(*testing.T) { fn() })
	})
	return stdout, stderr
}

// unwritableConfig is the failed-store fixture of TestStorePriceScheme_concurrent:
// a per-model config in a directory that refuses new files.
func unwritableConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "vendor_model_v.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"m"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return configPath
}

func noUserRoleChat() pub_models.Chat {
	return pub_models.Chat{
		Messages:   []pub_models.Message{{Role: "assistant", Content: "hi"}},
		TokenUsage: &pub_models.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}
}

// TestManager_SetWarnf pins that every process-stream writer of the manager
// goes through the warn seam when it is set, and keeps its current stream
// when it is not (review 3, R3-03).
func TestManager_SetWarnf(t *testing.T) {
	t.Setenv("DEBUG", "")
	t.Setenv("DEBUG_COST_MANAGER", "")
	price := ModelPriceScheme{InputUSDPerToken: 0.3, OutputUSDPerToken: 0.1}

	t.Run("failed store without the seam is an error on stderr", func(t *testing.T) {
		mgr := Manager{model: "m", configFilePath: unwritableConfig(t), fetcher: fakeFetcher{price: price}}
		var err error
		stdout, stderr := captureStreams(t, func() { _, err = mgr.resolveModelPrice(context.Background()) })
		if err != nil {
			t.Fatalf("resolveModelPrice: %v", err)
		}
		if stdout != "" || !strings.Contains(stderr, "failed to store price scheme") {
			t.Fatalf("stdout = %q, stderr = %q, want the store error on stderr only", stdout, stderr)
		}
	})
	t.Run("failed store with the seam is silent and reaches the seam", func(t *testing.T) {
		var got []string
		mgr := Manager{model: "m", configFilePath: unwritableConfig(t), fetcher: fakeFetcher{price: price}}
		mgr.SetWarnf(func(format string, a ...any) { got = append(got, fmt.Sprintf(format, a...)) })
		var err error
		stdout, stderr := captureStreams(t, func() { _, err = mgr.resolveModelPrice(context.Background()) })
		if err != nil {
			t.Fatalf("resolveModelPrice: %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
		}
		if len(got) != 1 || !strings.Contains(got[0], "failed to store price scheme") {
			t.Fatalf("seam got %q, want the store error", got)
		}
	})
	t.Run("user-role warning without the seam is a warning on stdout", func(t *testing.T) {
		mgr := Manager{model: "m", price: &price}
		var err error
		stdout, stderr := captureStreams(t, func() { _, err = mgr.Enrich(noUserRoleChat()) })
		if err != nil {
			t.Fatalf("Enrich: %v", err)
		}
		if stderr != "" || !strings.Contains(stdout, "failed to find user role") {
			t.Fatalf("stdout = %q, stderr = %q, want the role warning on stdout only", stdout, stderr)
		}
	})
	t.Run("user-role warning with the seam is silent and reaches the seam", func(t *testing.T) {
		var got []string
		mgr := Manager{model: "m", price: &price}
		mgr.SetWarnf(func(format string, a ...any) { got = append(got, fmt.Sprintf(format, a...)) })
		var enriched pub_models.Chat
		var err error
		stdout, stderr := captureStreams(t, func() { enriched, err = mgr.Enrich(noUserRoleChat()) })
		if err != nil {
			t.Fatalf("Enrich: %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
		}
		if len(got) != 1 || !strings.Contains(got[0], "failed to find user role") {
			t.Fatalf("seam got %q, want the role warning", got)
		}
		if len(enriched.Queries) != 1 || enriched.Queries[0].MessageTrigger != -1 {
			t.Fatalf("Queries = %+v, want one row with trigger -1", enriched.Queries)
		}
	})
	t.Run("nil keeps the default", func(t *testing.T) {
		mgr := NewManager(nil, "m", "")
		mgr.SetWarnf(nil)
		if mgr.warnf != nil {
			t.Fatal("SetWarnf(nil) must keep the default writer")
		}
	})
}
