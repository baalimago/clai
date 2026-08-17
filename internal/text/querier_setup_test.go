package text

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

func TestVendorType_OpenRouter(t *testing.T) {
	vendor, model, modelVersion, err := vendorType("or:openai/gpt-5.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "openrouter" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "openrouter")
	}
	if model != "chat" {
		t.Fatalf("model mismatch: got %q want %q", model, "chat")
	}
	if modelVersion != "openai/gpt-5.2" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "openai/gpt-5.2")
	}
}

func TestVendorType_Berget(t *testing.T) {
	vendor, model, modelVersion, err := vendorType("berget:zai-org/GLM-4.7-FP8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "berget" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "berget")
	}
	if model != "zai-org" {
		t.Fatalf("model mismatch: got %q want %q", model, "zai-org")
	}
	if modelVersion != "GLM-4.7-FP8" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "GLM-4.7-FP8")
	}
}

func TestVendorType_Berget_NoOrg(t *testing.T) {
	vendor, model, modelVersion, err := vendorType("berget:gemma-4-31B-it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "berget" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "berget")
	}
	if model != "berget" {
		t.Fatalf("model mismatch: got %q want %q", model, "berget")
	}
	if modelVersion != "gemma-4-31B-it" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "gemma-4-31B-it")
	}
}

func TestCanonicalModelString_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		model string
	}{
		{"openai gpt", "gpt-5.2"},
		{"anthropic claude", "claude-sonnet-4"},
		{"openrouter", "or:gpt-4.1"},
		{"openrouter with slash", "or:openai/gpt-5.2"},
		{"berget with org", "berget:zai-org/GLM-4.7-FP8"},
		{"berget without org", "berget:gemma-4-31B-it"},
		{"ollama with prefix", "ollama:llama3"},
		{"ollama bare", "ollama"},
		{"novita with org", "novita:gryphe/some-model"},
		{"novita bare", "novita"},
		{"huggingface", "hf:model:provider"},
		{"deepseek", "deepseek-chat"},
		{"mistral", "mistral-large"},
		{"gemini", "gemini-2.0-flash"},
		{"grok", "grok-3"},
		{"mercury", "mercury-coder"},
		{"mock", "mock"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vendor, family, modelVersion, err := vendorType(tt.model)
			if err != nil {
				t.Fatalf("vendorType(%q): %v", tt.model, err)
			}
			got := CanonicalModelString(vendor, family, modelVersion)
			if got != tt.model {
				t.Fatalf("round-trip broken: %q → vendorType → CanonicalModelString → %q", tt.model, got)
			}

			v2, f2, mv2, err2 := vendorType(got)
			if err2 != nil {
				t.Fatalf("vendorType(%q) after round-trip: %v", got, err2)
			}
			if v2 != vendor || f2 != family || mv2 != modelVersion {
				t.Fatalf("second pass mismatch: (%q, %q, %q) != (%q, %q, %q)",
					v2, f2, mv2, vendor, family, modelVersion)
			}
		})
	}
}

func TestCanonicalModelString_FromConfigFilename(t *testing.T) {
	tests := []struct {
		vendor, family, modelVersion string
		want                         string
	}{
		{"openai", "gpt", "gpt-4.1", "gpt-4.1"},
		{"anthropic", "claude", "sonnet-4", "sonnet-4"},
		{"openrouter", "chat", "gpt-4.1", "or:gpt-4.1"},
		{"berget", "zai-org", "GLM-4.7-FP8", "berget:zai-org/GLM-4.7-FP8"},
		{"berget", "berget", "gemma-4-31B-it", "berget:gemma-4-31B-it"},
		{"ollama", "llama3", "ollama:llama3", "ollama:llama3"},
		{"ollama", "llama3", "ollama", "ollama"},
		{"novita", "gryphe", "some-model", "novita:gryphe/some-model"},
		{"novita", "", "novita", "novita"},
		{"hf", "provider", "model", "hf:model:provider"},
	}

	for _, tt := range tests {
		got := CanonicalModelString(tt.vendor, tt.family, tt.modelVersion)
		if got != tt.want {
			t.Errorf("CanonicalModelString(%q, %q, %q) = %q, want %q", tt.vendor, tt.family, tt.modelVersion, got, tt.want)
		}
	}
}

// Test_Querier_NewQuerier_dimsBoundToOutputWriter proves the phase-3 snapshot
// wiring: NewQuerier resolves one dimensions snapshot from the session output
// writer's fd (R2-02). A non-terminal writer must not fail the querier setup;
// it deterministically yields dimensions.Fallback, so every width-aware render
// path of the querier reads one usable value.
func Test_Querier_NewQuerier_dimsBoundToOutputWriter(t *testing.T) {
	// Avoid races with the cost manager error logger goroutine in NewQuerier.
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	model := "mock"
	tmpDir := t.TempDir()
	if err := os.Mkdir(path.Join(tmpDir, ".clai"), os.FileMode(0o755)); err != nil {
		t.Fatalf("mkdir .clai: %v", err)
	}
	saved, err := json.Marshal(MockQuerier{Somefield: "somevalue"})
	if err != nil {
		t.Fatalf("marshal mock: %v", err)
	}
	if err := os.WriteFile(path.Join(tmpDir, ".clai", "mock_mock_mock.json"), saved, os.FileMode(0o755)); err != nil {
		t.Fatalf("write mock config: %v", err)
	}

	conf := Configurations{
		Model:     model,
		ConfigDir: path.Join(tmpDir, ".clai"),
		Out:       &strings.Builder{},
	}
	q, err := NewQuerier(context.Background(), conf, &MockQuerier{})
	if err != nil {
		t.Fatalf("NewQuerier with non-terminal output: %v", err)
	}
	if q.dims != dimensions.Fallback {
		t.Fatalf("dims = %+v, want fallback %+v for a non-terminal session writer", q.dims, dimensions.Fallback)
	}
	if q.out == nil {
		t.Fatal("querier output writer must be set")
	}
}

// recordingToolCallRecorder is a fake ToolCallRecorder that records every
// invocation. It pairs with recordingCallUsageRecorder (session_runner_test.go)
// for the AgentSettings plumbing tests.
type recordingToolCallRecorder struct {
	calls []ToolCall
}

func (r *recordingToolCallRecorder) RecordToolCall(_ context.Context, call ToolCall) error {
	r.calls = append(r.calls, call)
	return nil
}

// TestNewQuerier_AgentSettings proves NewQuerier copies the whole AgentSettings
// pointer and sources both recorder hooks from it (worklog 2026-08-15-agent-slog-output, D7). A nil AgentSettings
// (the CLI and pkg/text paths) keeps the querier recorders nil and logging
// disabled.
func TestNewQuerier_AgentSettings(t *testing.T) {
	// Avoid races with the cost manager error logger goroutine in NewQuerier.
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	model := "mock"
	tmpDir := t.TempDir()
	confDir := path.Join(tmpDir, ".clai")
	if err := os.Mkdir(confDir, os.FileMode(0o755)); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	saved, err := json.Marshal(MockQuerier{Somefield: "somevalue"})
	if err != nil {
		t.Fatalf("marshal mock: %v", err)
	}
	if err := os.WriteFile(path.Join(confDir, "mock_mock_mock.json"), saved, os.FileMode(0o755)); err != nil {
		t.Fatalf("write mock config: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	usageRec := &recordingCallUsageRecorder{}
	toolRec := &recordingToolCallRecorder{}
	agentSettings := &AgentSettings{
		Logger:           logger,
		Level:            slog.LevelWarn,
		RuneLimit:        42,
		UsageRecorder:    usageRec,
		ToolCallRecorder: toolRec,
	}
	conf := Configurations{
		Model:         model,
		ConfigDir:     confDir,
		Out:           &strings.Builder{},
		AgentSettings: agentSettings,
	}
	q, err := NewQuerier(context.Background(), conf, &MockQuerier{})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	if q.agentSettings != agentSettings {
		t.Fatalf("agentSettings: got %v, want the configured pointer", q.agentSettings)
	}
	if q.callUsageRecorder != usageRec {
		t.Fatalf("callUsageRecorder: got %v, want the AgentSettings recorder", q.callUsageRecorder)
	}
	if q.tooling.callRecorder != toolRec {
		t.Fatalf("toolCallRecorder: got %v, want the AgentSettings recorder", q.tooling.callRecorder)
	}

	// A nil AgentSettings keeps every channel disabled.
	plain, err := NewQuerier(context.Background(), Configurations{
		Model:     model,
		ConfigDir: confDir,
		Out:       &strings.Builder{},
	}, &MockQuerier{})
	if err != nil {
		t.Fatalf("NewQuerier (nil AgentSettings): %v", err)
	}
	if plain.agentSettings != nil {
		t.Fatalf("expected nil agentSettings, got %v", plain.agentSettings)
	}
	if plain.callUsageRecorder != nil {
		t.Fatalf("expected nil callUsageRecorder, got %v", plain.callUsageRecorder)
	}
	if plain.tooling.callRecorder != nil {
		t.Fatalf("expected nil toolCallRecorder, got %v", plain.tooling.callRecorder)
	}
}
