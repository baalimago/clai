package text

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

// TestConfigurations_StoplossLoadsFromTextConfig pins the nested stoploss
// object contract (Phase 2): max-tokens and
// max-tokens-handover-instructions load through utils.LoadConfigFromFile.
func TestConfigurations_StoplossLoadsFromTextConfig(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","stoploss":{"max-tokens":100,"max-tokens-handover-instructions":"wrap up"}}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	conf, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	if conf.Stoploss == nil {
		t.Fatal("expected Stoploss to be non-nil")
	}
	if conf.Stoploss.MaxTokens != 100 {
		t.Fatalf("expected MaxTokens 100, got %d", conf.Stoploss.MaxTokens)
	}
	if conf.Stoploss.MaxTokensHandoverMsg != "wrap up" {
		t.Fatalf("expected handover message 'wrap up', got %q", conf.Stoploss.MaxTokensHandoverMsg)
	}
}

// TestConfigurations_StoplossZeroMaxTokensDisabled pins that max-tokens: 0
// disables the stoploss (effective semantics: active iff MaxTokens > 0).
func TestConfigurations_StoplossZeroMaxTokensDisabled(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","stoploss":{"max-tokens":0}}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	conf, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	if conf.Stoploss == nil {
		t.Fatal("expected Stoploss to be non-nil")
	}
	if conf.Stoploss.MaxTokens != 0 {
		t.Fatalf("expected MaxTokens 0, got %d", conf.Stoploss.MaxTokens)
	}
}

// TestConfigurations_StoplossAbsentGetsAppendedWithDefaults pins the config
// migration contract: a textConfig.json lacking the stoploss key loads
// cleanly and is upgraded in place — the nested stoploss object is appended
// with the disabled default (max-tokens: 0) and the default handover message
// (config migration design, Q1 option B).
func TestConfigurations_StoplossAbsentGetsAppendedWithDefaults(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test"}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	conf, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	if conf.Stoploss == nil {
		t.Fatal("expected Stoploss to be appended from defaults")
	}
	if conf.Stoploss.MaxTokens != 0 {
		t.Fatalf("expected disabled max-tokens 0, got %d", conf.Stoploss.MaxTokens)
	}
	if conf.Stoploss.MaxTokensHandoverMsg != DefaultHandoverInstructions {
		t.Fatalf("expected the default handover message, got %q", conf.Stoploss.MaxTokensHandoverMsg)
	}
	regenerated, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("ReadFile(regenerated): %v", err)
	}
	if !strings.Contains(string(regenerated), `"stoploss"`) {
		t.Fatalf("expected the stoploss object appended to the file:\n%s", regenerated)
	}
}

// TestConfigurations_StoplossPartialObjectFillsMissingSubfield pins the
// recursive merge: a stoploss object that exists but lacks the handover
// message subfield gets that subfield filled from the default while the
// present max-tokens value survives untouched.
func TestConfigurations_StoplossPartialObjectFillsMissingSubfield(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","stoploss":{"max-tokens":0}}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	conf, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	if conf.Stoploss == nil || conf.Stoploss.MaxTokens != 0 {
		t.Fatalf("present max-tokens must survive, got %+v", conf.Stoploss)
	}
	if conf.Stoploss.MaxTokensHandoverMsg != DefaultHandoverInstructions {
		t.Fatalf("expected the missing subfield filled from default, got %q", conf.Stoploss.MaxTokensHandoverMsg)
	}
}

// TestConfigurations_StoplossNotObject pins the error coverage: a non-object
// stoploss value is a json.Unmarshal type error propagated by
// LoadConfigFromFile.
func TestConfigurations_StoplossNotObject(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","stoploss":42}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	_, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err == nil {
		t.Fatal("expected error for non-object stoploss")
	}
}

// TestConfigurations_StoplossMarshalsNestedObject pins acceptance criterion 1:
// the pointer marshals to the nested stoploss object with both keys, and an
// absent pointer marshals to nothing (omitempty).
func TestConfigurations_StoplossMarshalsNestedObject(t *testing.T) {
	with := Configurations{
		Model:    "test",
		Stoploss: &Stoploss{MaxTokens: 200000, MaxTokensHandoverMsg: "wrap up"},
	}
	data, err := json.Marshal(with)
	if err != nil {
		t.Fatalf("Marshal(with stoploss): %v", err)
	}
	want := `"stoploss":{"max-tokens":200000,"max-tokens-handover-instructions":"wrap up"}`
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected marshaled config to contain %q, got %s", want, data)
	}

	without := Configurations{Model: "test"}
	data, err = json.Marshal(without)
	if err != nil {
		t.Fatalf("Marshal(without stoploss): %v", err)
	}
	if strings.Contains(string(data), "stoploss") {
		t.Fatalf("expected absent stoploss to marshal to nothing, got %s", data)
	}
}

// TestStoploss_HandoverInstructions pins the effective message semantics
// (Phase 2): a non-empty configured message wins, empty falls back to
// DefaultHandoverInstructions, and a nil receiver returns the default.
func TestStoploss_HandoverInstructions(t *testing.T) {
	if got := (&Stoploss{MaxTokensHandoverMsg: "wrap up"}).HandoverInstructions(); got != "wrap up" {
		t.Fatalf("expected configured message, got %q", got)
	}
	if got := (&Stoploss{}).HandoverInstructions(); got != DefaultHandoverInstructions {
		t.Fatalf("expected default message, got %q", got)
	}
	if got := (*Stoploss)(nil).HandoverInstructions(); got != DefaultHandoverInstructions {
		t.Fatalf("expected default message for nil receiver, got %q", got)
	}
}

// Test_NewQuerier_CarriesStoploss pins the Phase 2 wiring: the stoploss
// policy is carried from Configurations onto the Querier for the Phase 3
// controller.
func Test_NewQuerier_CarriesStoploss(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	conf := Configurations{
		Model:     "mock",
		ConfigDir: t.TempDir(),
		Stoploss: &Stoploss{
			MaxTokens:            100,
			MaxTokensHandoverMsg: "wrap up",
		},
	}

	q, err := NewQuerier(context.Background(), conf, &MockQuerier{})
	if err != nil {
		t.Fatalf("NewQuerier: %v", err)
	}
	if q.stoploss == nil {
		t.Fatal("expected stoploss to be carried onto the querier")
	}
	if q.stoploss.MaxTokens != 100 || q.stoploss.MaxTokensHandoverMsg != "wrap up" {
		t.Fatalf("unexpected stoploss carried onto querier: %+v", q.stoploss)
	}
}
