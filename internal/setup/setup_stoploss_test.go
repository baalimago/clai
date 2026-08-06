package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// stoplossFixture returns a textConfig.json-shaped object carrying the
// Phase 2 stoploss block with both keys. Numbers are ints so the fixture
// reads naturally; jsonRoundTrip converts them to the float64 shape the
// wizard sees after unmarshal.
func stoplossFixture() map[string]any {
	return map[string]any{
		"max-tool-calls": 5,
		"model":          "mock_test_test",
		"stoploss": map[string]any{
			"max-tokens":                       100000,
			"max-tokens-handover-instructions": "Wrap up.",
		},
		"system-prompt": "You are a helpful assistant.",
	}
}

// stoplossFieldIndex returns the sorted top-level index of the stoploss
// key in jzon. Indices are fixture-derived, never hardcoded (R7-02).
func stoplossFieldIndex(t *testing.T, jzon map[string]any) int {
	t.Helper()
	idx := slices.Index(sortedKeys(jzon), "stoploss")
	if idx < 0 {
		t.Fatalf("fixture has no 'stoploss' key, keys: %v", sortedKeys(jzon))
	}
	return idx
}

// jsonRoundTrip marshals and unmarshals v, yielding the float64-typed
// shape the wizard produces for JSON numbers after writeConfig.
func jsonRoundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return out
}

// runInterractiveReconfigure writes orig to cfgPath, drives the
// field-by-field wizard with the given input lines, and returns the
// resulting file content unmarshaled into a map.
func runInterractiveReconfigure(t *testing.T, cfgPath string, orig map[string]any, inputs []string) map[string]any {
	t.Helper()
	origBytes, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal fixture: %v", err)
	}
	if err := os.WriteFile(cfgPath, origBytes, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	inputIdx := 0
	restore := useReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", io.EOF
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	defer restore()

	if err := interractiveReconfigure(config{name: "textConfig.json", filePath: cfgPath}, origBytes); err != nil {
		t.Fatalf("interractiveReconfigure: %v", err)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var jzon map[string]any
	if err := json.Unmarshal(got, &jzon); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	return jzon
}

// ============================================================
// Acceptance criterion 1 — editMap on a stoploss-shaped object
// ============================================================

// TestEditMap_StoplossUpdate pins that updating each stoploss key keeps
// its JSON type: an int stays an int, a string stays a string.
func TestEditMap_StoplossUpdate(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		input string
		want  any
	}{
		{"max-tokens stays int", "max-tokens", "5000", 5000},
		{"instructions stays string", "max-tokens-handover-instructions", "Wrap up now. Summarize your work for handover.", "Wrap up now. Summarize your work for handover."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := []string{"u", tt.key, tt.input, "d"}
			inputIdx := 0
			restore := useReadUserInputForTests(func() (string, error) {
				if inputIdx >= len(inputs) {
					return "", io.EOF
				}
				ret := inputs[inputIdx]
				inputIdx++
				return ret, nil
			})
			defer restore()

			m := map[string]any{
				"max-tokens":                       100000,
				"max-tokens-handover-instructions": "Wrap up.",
			}
			got, err := editMap("stoploss", m, "")
			if err != nil {
				t.Fatalf("editMap(update %s): %v", tt.key, err)
			}

			// DeepEqual also pins the type: 5000 (int) != "5000" (string).
			if !reflect.DeepEqual(got[tt.key], tt.want) {
				t.Fatalf("%s = %#v (%T), want %#v", tt.key, got[tt.key], got[tt.key], tt.want)
			}

			sibling := "max-tokens"
			if tt.key == "max-tokens" {
				sibling = "max-tokens-handover-instructions"
			}
			if !reflect.DeepEqual(got[sibling], m[sibling]) {
				t.Fatalf("%s = %#v, want unchanged %#v", sibling, got[sibling], m[sibling])
			}
		})
	}
}

func TestEditMap_StoplossAddKey(t *testing.T) {
	inputs := []string{"a", "max-tokens", "200000", "d"}
	inputIdx := 0
	restore := useReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", io.EOF
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	defer restore()

	m := map[string]any{} // present but empty (R7-01)
	got, err := editMap("stoploss", m, "")
	if err != nil {
		t.Fatalf("editMap(add): %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("editMap(add) len = %d, want 1", len(got))
	}
	v, ok := got["max-tokens"].(int)
	if !ok {
		t.Fatalf("max-tokens type = %T, want int", got["max-tokens"])
	}
	if v != 200000 {
		t.Fatalf("max-tokens = %v, want 200000", v)
	}
}

func TestEditMap_StoplossRemoveKey(t *testing.T) {
	inputs := []string{"r", "max-tokens-handover-instructions", "d"}
	inputIdx := 0
	restore := useReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", io.EOF
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	defer restore()

	m := map[string]any{
		"max-tokens":                       100000,
		"max-tokens-handover-instructions": "Wrap up.",
	}
	got, err := editMap("stoploss", m, "")
	if err != nil {
		t.Fatalf("editMap(remove): %v", err)
	}

	if _, exists := got["max-tokens-handover-instructions"]; exists {
		t.Fatal("editMap(remove) instructions key still present")
	}
	if got["max-tokens"] != 100000 {
		t.Fatalf("editMap(remove) max-tokens = %v, want 100000", got["max-tokens"])
	}
	if len(got) != 1 {
		t.Fatalf("editMap(remove) len = %d, want 1", len(got))
	}
}

func TestEditMap_StoplossDone_Unchanged(t *testing.T) {
	restore := useReadUserInputForTests(func() (string, error) {
		return "d", nil
	})
	defer restore()

	m := map[string]any{
		"max-tokens":                       100000,
		"max-tokens-handover-instructions": "Wrap up.",
	}
	got, err := editMap("stoploss", m, "")
	if err != nil {
		t.Fatalf("editMap(done): %v", err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("editMap(done) = %v, want %v (unchanged)", got, m)
	}
}

// TestEditMap_StoplossInvalidInt_StaysString pins the error-coverage row:
// castPrimitive keeps a non-integer entry as a string.
func TestEditMap_StoplossInvalidInt_StaysString(t *testing.T) {
	inputs := []string{"u", "max-tokens", "abc", "d"}
	inputIdx := 0
	restore := useReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", io.EOF
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	defer restore()

	m := map[string]any{
		"max-tokens":                       100000,
		"max-tokens-handover-instructions": "Wrap up.",
	}
	got, err := editMap("stoploss", m, "")
	if err != nil {
		t.Fatalf("editMap(invalid int): %v", err)
	}

	v, ok := got["max-tokens"].(string)
	if !ok {
		t.Fatalf("max-tokens type = %T, want string", got["max-tokens"])
	}
	if v != "abc" {
		t.Fatalf("max-tokens = %q, want \"abc\"", v)
	}
}

// ============================================================
// Acceptance criterion 2 + integration contract — full wizard flow
// ============================================================

// TestInterractiveReconfigure_StoplossRoundTrip_Unchanged pins acceptance
// criterion 2: with no key touched, the whole config survives the wizard
// with no key loss and no type corruption.
func TestInterractiveReconfigure_StoplossRoundTrip_Unchanged(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "textConfig.json")
	orig := stoplossFixture()
	idx := stoplossFieldIndex(t, orig)

	got := runInterractiveReconfigure(t, cfgPath, orig, []string{fmt.Sprintf("%d", idx), "d", "d"})

	if !reflect.DeepEqual(got, jsonRoundTrip(t, orig)) {
		t.Fatalf("round-trip changed the config:\n got: %#v\nwant: %#v", got, jsonRoundTrip(t, orig))
	}
}

// TestInterractiveReconfigure_StoplossUpdate_EndToEnd pins integration
// rows 1 and 2: updating max-tokens writes a JSON number, updating the
// instructions writes the new string, and untouched keys stay intact.
func TestInterractiveReconfigure_StoplossUpdate_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "textConfig.json")
	orig := stoplossFixture()
	idx := stoplossFieldIndex(t, orig)

	inputs := []string{
		fmt.Sprintf("%d", idx),
		"u", "max-tokens", "5000",
		"u", "max-tokens-handover-instructions", "Wrap up now. Summarize your work for handover.",
		"d", "d",
	}
	got := runInterractiveReconfigure(t, cfgPath, orig, inputs)

	sl, ok := got["stoploss"].(map[string]any)
	if !ok {
		t.Fatalf("stoploss type = %T, want map", got["stoploss"])
	}
	if sl["max-tokens"] != float64(5000) {
		t.Fatalf("max-tokens = %#v (%T), want JSON number 5000", sl["max-tokens"], sl["max-tokens"])
	}
	if sl["max-tokens-handover-instructions"] != "Wrap up now. Summarize your work for handover." {
		t.Fatalf("instructions = %q, want the new sentence", sl["max-tokens-handover-instructions"])
	}
	if got["max-tool-calls"] != float64(5) || got["model"] != "mock_test_test" {
		t.Fatalf("untouched keys changed: %#v", got)
	}
}

// TestInterractiveReconfigure_StoplossRemove_EndToEnd pins integration
// row 3: removing the instructions key deletes only that key; the
// stoploss object itself survives.
func TestInterractiveReconfigure_StoplossRemove_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "textConfig.json")
	orig := stoplossFixture()
	idx := stoplossFieldIndex(t, orig)

	got := runInterractiveReconfigure(t, cfgPath, orig, []string{fmt.Sprintf("%d", idx), "r", "max-tokens-handover-instructions", "d", "d"})

	sl, ok := got["stoploss"].(map[string]any)
	if !ok {
		t.Fatalf("stoploss type = %T, want map (object must survive)", got["stoploss"])
	}
	if _, exists := sl["max-tokens-handover-instructions"]; exists {
		t.Fatal("instructions key still present after remove")
	}
	if sl["max-tokens"] != float64(100000) {
		t.Fatalf("max-tokens = %#v, want unchanged 100000", sl["max-tokens"])
	}
}

// TestInterractiveReconfigure_StoplossAddToEmpty_EndToEnd pins integration
// row 4: an empty stoploss object accepts [a]dd and writes the new key
// with a cast int.
func TestInterractiveReconfigure_StoplossAddToEmpty_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "textConfig.json")
	orig := map[string]any{
		"model":    "mock_test_test",
		"stoploss": map[string]any{},
	}
	idx := stoplossFieldIndex(t, orig)

	got := runInterractiveReconfigure(t, cfgPath, orig, []string{fmt.Sprintf("%d", idx), "a", "max-tokens", "200000", "d", "d"})

	sl, ok := got["stoploss"].(map[string]any)
	if !ok {
		t.Fatalf("stoploss type = %T, want map", got["stoploss"])
	}
	if len(sl) != 1 {
		t.Fatalf("stoploss len = %d, want 1", len(sl))
	}
	if sl["max-tokens"] != float64(200000) {
		t.Fatalf("max-tokens = %#v (%T), want JSON number 200000", sl["max-tokens"], sl["max-tokens"])
	}
}

// TestInterractiveReconfigure_StoplossInvalidInt_WritesString pins the
// error-coverage row end to end: an invalid int is written as a JSON
// string, not a number.
func TestInterractiveReconfigure_StoplossInvalidInt_WritesString(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "textConfig.json")
	orig := stoplossFixture()
	idx := stoplossFieldIndex(t, orig)

	got := runInterractiveReconfigure(t, cfgPath, orig, []string{fmt.Sprintf("%d", idx), "u", "max-tokens", "abc", "d", "d"})

	sl, ok := got["stoploss"].(map[string]any)
	if !ok {
		t.Fatalf("stoploss type = %T, want map", got["stoploss"])
	}
	if sl["max-tokens"] != "abc" {
		t.Fatalf("max-tokens = %#v (%T), want JSON string \"abc\"", sl["max-tokens"], sl["max-tokens"])
	}
}
