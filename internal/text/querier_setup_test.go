package text

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
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
	vendor, model, modelVersion, err := vendorType("berget:orgx/fixture-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "berget" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "berget")
	}
	if model != "orgx" {
		t.Fatalf("model mismatch: got %q want %q", model, "orgx")
	}
	if modelVersion != "fixture-model" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "fixture-model")
	}
}

func TestVendorType_Berget_NoOrg(t *testing.T) {
	vendor, model, modelVersion, err := vendorType("berget:fixture-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "berget" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "berget")
	}
	if model != "berget" {
		t.Fatalf("model mismatch: got %q want %q", model, "berget")
	}
	if modelVersion != "fixture-model" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "fixture-model")
	}
}

// Model ids in these tables are synthetic on purpose. vendorType and
// CanonicalModelString parse a naming *shape* (bare, org-qualified,
// prefixed) and never consult a provider catalogue, so a real model id here
// would rot the moment a vendor retires it and would wrongly suggest that
// the parser or the package defaults are pinned to it. Keep them fictional.
//
// One constraint: vendorType routes anything containing "test" to the mock
// vendor, so fixture names use "fixture" instead.
func TestCanonicalModelString_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		model string
	}{
		{"openai gpt", "gpt-fixture"},
		{"anthropic claude", "claude-fixture"},
		{"openrouter", "or:fixture-model"},
		{"openrouter with slash", "or:orgx/fixture-model"},
		{"berget with org", "berget:orgx/fixture-model"},
		{"berget without org", "berget:fixture-model"},
		{"ollama with prefix", "ollama:fixture-model"},
		{"ollama bare", "ollama"},
		{"novita with org", "novita:orgx/fixture-model"},
		{"novita bare", "novita"},
		{"huggingface", "hf:fixture-model:providerx"},
		{"deepseek", "deepseek-fixture"},
		{"mistral", "mistral-fixture"},
		{"gemini", "gemini-fixture"},
		{"grok", "grok-fixture"},
		{"mercury", "mercury-fixture"},
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
		{"openai", "gpt", "gpt-fixture", "gpt-fixture"},
		{"anthropic", "claude", "claude-fixture", "claude-fixture"},
		{"openrouter", "chat", "fixture-model", "or:fixture-model"},
		{"berget", "orgx", "fixture-model", "berget:orgx/fixture-model"},
		{"berget", "berget", "fixture-model", "berget:fixture-model"},
		{"ollama", "fixture-model", "ollama:fixture-model", "ollama:fixture-model"},
		{"ollama", "fixture-model", "ollama", "ollama"},
		{"novita", "orgx", "fixture-model", "novita:orgx/fixture-model"},
		{"novita", "", "novita", "novita"},
		{"hf", "providerx", "fixture-model", "hf:fixture-model:providerx"},
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

// TestNewQuerier_oneOffQuerier_isSideEffectFree is the integration proof for
// a summarizer-shaped querier (D18, D19, D23): built with the mock vendor,
// SkipAmbientMcpServers and one injected tool it registers exactly that
// tool, spawns no config-dir server, and a non-persisting TextQuery returns
// a cost row while writing nothing under the config dir.
func TestNewQuerier_oneOffQuerier_isSideEffectFree(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	confDir := t.TempDir()
	marker := writeAmbientMarkerServer(t, filepath.Join(confDir, "mcpServers"))
	writeMockPriceFile(t, confDir)
	conf := Configurations{
		Model:                 "test",
		ConfigDir:             confDir,
		UseTools:              true,
		Tools:                 []pub_models.LLMTool{setupToolsTestTool{name: "injected"}},
		SkipAmbientMcpServers: true,
		SaveReplyAsConv:       false,
		Raw:                   true,
		Out:                   &strings.Builder{},
	}

	var q Querier[*vendors.Mock]
	var err error
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		q, err = NewQuerier(t.Context(), conf, &vendors.Mock{})
	})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	if _, ok := q.tooling.registered["injected"]; !ok || len(q.tooling.registered) != 1 {
		t.Fatalf("registered tools = %v, want only the injected tool", q.tooling.registered)
	}
	assertMarkerAbsent(t, marker)
	if strings.Contains(stderr, "failed to setup") {
		t.Fatalf("ambient server must not be started, stderr: %q", stderr)
	}

	chat, err := q.TextQuery(t.Context(), pub_models.Chat{ID: "one-off", Messages: []pub_models.Message{{Role: "user", Content: "summarize this conversation"}}})
	if err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	if len(chat.Queries) != 1 {
		t.Fatalf("Queries = %+v, want exactly one row", chat.Queries)
	}
	if row := chat.Queries[0]; row.Usage.TotalTokens == 0 || row.CostUSD <= 0 {
		t.Fatalf("Queries[0] = %+v, want usage and cost", row)
	}
	want := []string{"/mcpServers/ambient.json", "/mock_test_test.json"}
	if got := configDirEntries(t, confDir); !slices.Equal(got, want) {
		t.Fatalf("config dir entries = %v, want only the fixtures %v", got, want)
	}
}

// TestNewQuerier_summaryDefaults pins that NewQuerier copies the two summary
// config fields and defaults a zero join timeout to the README value.
func TestNewQuerier_summaryDefaults(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Run("zero join timeout takes the default", func(t *testing.T) {
		conf := Configurations{Model: "mock", ConfigDir: t.TempDir(), SummarizeConversations: true, SummaryModel: "summary-model"}
		q, err := NewQuerier(t.Context(), conf, &MockQuerier{})
		if err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		if !q.summarizeConversations || q.summaryModel != "summary-model" || q.runModel != "mock" {
			t.Fatalf("copied summarize=%t model=%q run=%q", q.summarizeConversations, q.summaryModel, q.runModel)
		}
		if q.summaryJoinTimeout != defaultSummaryJoinTimeout || defaultSummaryJoinTimeout != 5*time.Second {
			t.Fatalf("join timeout = %v, want the 5s default", q.summaryJoinTimeout)
		}
		if q.summarizer != nil || q.summaryRun != nil {
			t.Fatal("NewQuerier must attach no summarizer")
		}
	})
	t.Run("explicit join timeout is copied", func(t *testing.T) {
		conf := Configurations{Model: "mock", ConfigDir: t.TempDir(), SummaryJoinTimeout: 7 * time.Millisecond}
		q, err := NewQuerier(t.Context(), conf, &MockQuerier{})
		if err != nil {
			t.Fatalf("NewQuerier: %v", err)
		}
		if q.summaryJoinTimeout != 7*time.Millisecond || q.summarizeConversations {
			t.Fatalf("join timeout = %v summarize=%t", q.summaryJoinTimeout, q.summarizeConversations)
		}
	})
}
