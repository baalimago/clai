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

// TestConfigurations_StoplossAbsentLoadsCleanly pins acceptance criterion 4:
// old configs lacking the stoploss key unmarshal without error and leave the
// pointer nil.
func TestConfigurations_StoplossAbsentLoadsCleanly(t *testing.T) {
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
	if conf.Stoploss != nil {
		t.Fatalf("expected nil Stoploss, got %+v", conf.Stoploss)
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
