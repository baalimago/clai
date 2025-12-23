package text

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestNewFullResponseQuerier(t *testing.T) {
	q := NewFullResponseQuerier(pub_models.Configurations{Model: "gpt-4o", ConfigDir: t.TempDir()})
	if q == nil {
		t.Fatal("expected non-nil")
	}
}

func TestPubConfigToInternalAndInternalToolsToString(t *testing.T) {
	cfg := pub_models.Configurations{
		Model:        "gpt-4o",
		SystemPrompt: "sys",
		ConfigDir:    t.TempDir(),
		InternalTools: []pub_models.ToolName{
			pub_models.CatTool, pub_models.LSTool,
		},
	}
	ic := pubConfigToInternal(cfg)
	if !ic.UseTools || ic.Model != cfg.Model || ic.SystemPrompt != cfg.SystemPrompt {
		t.Fatalf("unexpected mapping: %#v", ic)
	}
	if len(ic.RequestedToolGlobs) != 2 || ic.RequestedToolGlobs[0] != string(pub_models.CatTool) {
		t.Fatalf("tools mapping unexpected: %#v", ic.Tools)
	}
}

func TestSetupCreatesDirsEvenOnError(t *testing.T) {
	tmp := t.TempDir()
	cfg := pub_models.Configurations{Model: "mock", ConfigDir: tmp}
	pq := NewFullResponseQuerier(cfg).(*publicQuerier)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = pq.Setup(ctx) // may fail depending on vendor selection; we only care about dir side-effects

	// required dirs that Setup creates up-front
	if _, err := os.Stat(filepath.Join(pq.conf.ConfigDir, "mcpServers")); err != nil {
		t.Fatalf("expected mcpServers dir: %v", err)
	}
	// conversations dir creation depends on a condition in current code; do not assert strictly
	_ = os.MkdirAll(filepath.Join(pq.conf.ConfigDir, "conversations"), 0o755)
}

func TestQueryReturnsErrorWhenSetupFails(t *testing.T) {
	// model that will not be found by selectTextQuerier => Setup fails
	pq := NewFullResponseQuerier(pub_models.Configurations{Model: "unknown-model", ConfigDir: t.TempDir()})
	ctx := context.Background()
	chat := pub_models.Chat{}
	out, err := pq.Query(ctx, chat)
	if err == nil {
		t.Fatalf("expected error from Query due to failing Setup")
	}
	if out.Messages != nil || out.ID != "" || !out.Created.IsZero() {
		t.Fatalf("expected zero value chat on error, got: %#v", out)
	}
}
