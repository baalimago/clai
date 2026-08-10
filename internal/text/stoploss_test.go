package text

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// TestMigrateTagContract pins the migrate:"true" contract: a field tagged
// migrate:"true" must not carry the omitempty json option, or the config
// rewrite would drop the filled zero value again and the field would silently
// vanish from upgraded files (config migration design, phase 8). The guard
// walks every struct reachable from the loaded config types, so future
// migrate-tagged fields are covered automatically.
func TestMigrateTagContract(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(typ reflect.Type)
	walk = func(typ reflect.Type) {
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for sf := range typ.Fields() {
			if sf.Tag.Get("migrate") == "true" && strings.Contains(sf.Tag.Get("json"), "omitempty") {
				t.Fatalf("field %s.%s carries migrate:\"true\" and omitempty; the filled zero would be dropped by the rewrite", typ.Name(), sf.Name)
			}
			walk(sf.Type)
		}
	}
	walk(reflect.TypeFor[Configurations]())
	walk(reflect.TypeFor[Profile]())
}

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
	want := `"stoploss":{"max-tokens":200000,"max-tokens-handover-instructions":"wrap up","max-tool-calls-after-handover":0}`
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

// TestConfigurations_StoplossMaxToolCallsAfterHandoverLoads pins that the
// post-handover tool budget key loads through utils.LoadConfigFromFile.
func TestConfigurations_StoplossMaxToolCallsAfterHandoverLoads(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","stoploss":{"max-tokens":100,"max-tool-calls-after-handover":3}}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	conf, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	if conf.Stoploss == nil || conf.Stoploss.MaxTokens != 100 {
		t.Fatalf("expected stoploss with max-tokens 100, got %+v", conf.Stoploss)
	}
	if conf.Stoploss.MaxToolCallsAfterHandover != 3 {
		t.Fatalf("expected MaxToolCallsAfterHandover 3, got %d", conf.Stoploss.MaxToolCallsAfterHandover)
	}
}

// TestConfigurations_StoplossMaxToolCallsAfterHandoverAbsentGetsMaterialized
// pins the migrate:"true" highlight for the new key: a pre-existing stoploss
// object without the key gains it on the next load as the explicit zero
// (0 = unlimited), the file is rewritten once, the upgrade announcement lists
// the path, and a second load is silent.
func TestConfigurations_StoplossMaxToolCallsAfterHandoverAbsentGetsMaterialized(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","stoploss":{"max-tokens":100,"max-tokens-handover-instructions":"wrap up"}}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	var conf Configurations
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		var err error
		conf, err = utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
		if err != nil {
			t.Fatalf("LoadConfigFromFile: %v", err)
		}
	})
	if conf.Stoploss == nil || conf.Stoploss.MaxToolCallsAfterHandover != 0 {
		t.Fatalf("expected the absent key materialized as 0 (unlimited), got %+v", conf.Stoploss)
	}
	if !strings.Contains(stdout, "stoploss.max-tool-calls-after-handover") {
		t.Fatalf("expected the upgrade announcement to list the new key, got %q", stdout)
	}
	regenerated, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("ReadFile(regenerated): %v", err)
	}
	if !strings.Contains(string(regenerated), `"max-tool-calls-after-handover": 0`) {
		t.Fatalf("expected the materialized zero key in the rewritten file:\n%s", regenerated)
	}
	before := string(regenerated)
	stdout2 := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default); err != nil {
			t.Fatalf("second LoadConfigFromFile: %v", err)
		}
	})
	if stdout2 != "" {
		t.Fatalf("expected the second load to be silent, got %q", stdout2)
	}
	after, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("ReadFile(after): %v", err)
	}
	if string(after) != before {
		t.Fatalf("second load must not rewrite the file:\n%s", after)
	}
}

// TestConfigurations_StoplossMarshalsPostHandoverBudget pins the marshal
// contract for the new key: a positive budget marshals into the nested
// stoploss object, and the zero value marshals explicitly (no omitempty) so
// the migrate highlight survives the rewrite.
func TestConfigurations_StoplossMarshalsPostHandoverBudget(t *testing.T) {
	with := Configurations{
		Model:    "test",
		Stoploss: &Stoploss{MaxTokens: 200000, MaxToolCallsAfterHandover: 5},
	}
	data, err := json.Marshal(with)
	if err != nil {
		t.Fatalf("Marshal(with budget): %v", err)
	}
	want := `"stoploss":{"max-tokens":200000,"max-tokens-handover-instructions":"","max-tool-calls-after-handover":5}`
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected marshaled config to contain %q, got %s", want, data)
	}

	without := Configurations{Model: "test", Stoploss: &Stoploss{MaxTokens: 200000}}
	data, err = json.Marshal(without)
	if err != nil {
		t.Fatalf("Marshal(without budget): %v", err)
	}
	if !strings.Contains(string(data), `"max-tool-calls-after-handover":0`) {
		t.Fatalf("expected the zero budget to marshal explicitly, got %s", data)
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
