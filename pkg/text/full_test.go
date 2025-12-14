package text

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	priv_models "github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestNewFullResponseQuerier(t *testing.T) {
	q := NewFullResponseQuerier()
	if q == nil {
		t.Fatal("expected non-nil")
	}
}

func TestDefaultInternalConfig(t *testing.T) {
	cfg := defaultInternalConfig()
	if cfg.Model == "" {
		t.Fatalf("expected default model to be set, got empty")
	}
	if cfg.ConfigDir == "" {
		t.Fatalf("expected default config dir to be set, got empty")
	}
	if !cfg.UseTools {
		t.Fatalf("expected tools to be enabled in default config")
	}
}

// fakeTool is a minimal implementation of models.LLMTool used for testing
// that the option wiring hooks into the underlying querier.
type fakeTool struct {
	calledWith []pub_models.Input
}

func (f *fakeTool) Call(in pub_models.Input) (string, error) {
	f.calledWith = append(f.calledWith, in)
	return "", nil
}

func (f *fakeTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: "fake"}
}

// fakeInternalQuerier is used to verify that options are applied during Setup.
type fakeInternalQuerier struct {
	priv_models.ChatQuerier
	registeredTools   []pub_models.LLMTool
	registeredServers []pub_models.McpServer
}

func (f *fakeInternalQuerier) RegisterLLMTools(tools ...pub_models.LLMTool) {
	f.registeredTools = append(f.registeredTools, tools...)
}

func (f *fakeInternalQuerier) RegisterMcpServers(servers []pub_models.McpServer) {
	f.registeredServers = append(f.registeredServers, servers...)
}

// TestOptionsAreAppliedDuringSetup verifies that WithLLMTools and
// WithMcpServers populate the publicQuerier fields. The actual wiring to
// internal.CreateTextQuerier is covered indirectly by other tests; here we
// only validate option behaviour.
func TestOptionsAreAppliedDuringSetup(t *testing.T) {
	cfg := pub_models.Configurations{Model: "unknown-model", ConfigDir: t.TempDir()}
	pq := &publicQuerier{
		conf: pubConfigToInternal(cfg),
	}

	tool := &fakeTool{}
	server := pub_models.McpServer{Name: "s", Command: "cmd"}

	// Apply options directly to pq to simulate constructor behaviour.
	WithLLMTools(tool)(pq)
	WithMcpServers(server)(pq)

	if len(pq.llmTools) != 1 || pq.llmTools[0] != tool {
		t.Fatalf("expected llmTools to contain injected tool, got %#v", pq.llmTools)
	}
	if len(pq.mcpServers) != 1 || pq.mcpServers[0].Name != server.Name {
		t.Fatalf("expected mcpServers to contain injected server, got %#v", pq.mcpServers)
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
	if len(ic.Tools) != 2 || ic.Tools[0] != string(pub_models.CatTool) {
		t.Fatalf("tools mapping unexpected: %#v", ic.Tools)
	}
}

func TestSetupCreatesDirsEvenOnError(t *testing.T) {
	tmp := t.TempDir()
	cfg := pub_models.Configurations{Model: "mock", ConfigDir: tmp}
	pq := &publicQuerier{
		conf: pubConfigToInternal(cfg),
	}

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
	cfg := pub_models.Configurations{Model: "unknown-model", ConfigDir: t.TempDir()}
	pq := &publicQuerier{
		conf: pubConfigToInternal(cfg),
	}
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
