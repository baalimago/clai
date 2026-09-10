package text

import (
	"context"
	"slices"
	"testing"
)

// Test_NewQuerier_AppliesCmdBanList pins D29: the effective list from
// Configurations.CmdBan lives on the querier as a snapshot, never in package
// state, so the executor can attach it to every tool call of this run only.
func Test_NewQuerier_AppliesCmdBanList(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	entries := []string{"rm"}
	conf := Configurations{
		Model:     "mock",
		ConfigDir: t.TempDir(),
		CmdBan:    entries,
	}

	q, err := NewQuerier(context.Background(), conf, &MockQuerier{})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	entries[0] = "sudo"
	if !slices.Equal(q.cmdBan, []string{"rm"}) {
		t.Fatalf("Querier.cmdBan = %v, want a snapshot [rm]", q.cmdBan)
	}
}

// Test_NewQuerier_NoCmdBanStaysPermissive pins the default: no configured
// bans leave the querier with an empty policy.
func Test_NewQuerier_NoCmdBanStaysPermissive(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	conf := Configurations{
		Model:     "mock",
		ConfigDir: t.TempDir(),
	}

	q, err := NewQuerier(context.Background(), conf, &MockQuerier{})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	if len(q.cmdBan) != 0 {
		t.Fatalf("Querier.cmdBan = %v, want empty", q.cmdBan)
	}
}
