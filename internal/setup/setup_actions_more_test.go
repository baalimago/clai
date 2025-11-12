package setup

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func feedInput(t *testing.T, s string) func() {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	_, _ = io.WriteString(w, s)
	_ = w.Close()
	return func() { os.Stdin = old }
}

func TestRemove_ConfirmAndAbort(t *testing.T) {
	// create file
	dir := t.TempDir()
	fp := filepath.Join(dir, "x.json")
	if err := os.WriteFile(fp, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// abort
	cleanup := feedInput(t, "n\n")
	defer cleanup()
	if err := remove(config{name: "x", filePath: fp}); err == nil {
		t.Fatal("expected abort error")
	}

	// confirm
	if err := os.WriteFile(fp, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanup2 := feedInput(t, "y\n")
	defer cleanup2()
	if err := remove(config{name: "x", filePath: fp}); err != nil {
		t.Fatalf("remove err: %v", err)
	}
	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}
}

func TestBuildNewConfig_AndEditors(t *testing.T) {
	initial := map[string]any{
		"a": 1,
		"b": []any{1, 2},
		"c": map[string]any{"x": true},
	}

	// Prepare sequence: update a->3, done; slice update index 1->5; map update key x->false
	inputs := strings.Join([]string{
		// handleValue(a)
		"3\n",
		// editSlice(b)
		"u\n", // choose update
		"1\n", // index
		"5\n", // new val
		"d\n", // done
		// editMap(c)
		"u\n",     // update
		"x\n",     // key
		"false\n", // new val
		"d\n",     // done
	}, "")
	cleanup := feedInput(t, inputs)
	defer cleanup()

	got, err := buildNewConfig(initial)
	if err != nil {
		t.Fatalf("buildNewConfig: %v", err)
	}
	want := map[string]any{
		"a": 3,
		"b": []any{1, 5},
		"c": map[string]any{"x": false},
	}
	if !reflect.DeepEqual(got, want) {
		b1, _ := json.Marshal(got)
		b2, _ := json.Marshal(want)
		t.Fatalf("mismatch got=%s want=%s", b1, b2)
	}
}

func TestConfigure_SelectIndexAndAction(t *testing.T) {
	// prepare two files
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.json")
	f2 := filepath.Join(dir, "b.json")
	_ = os.WriteFile(f1, []byte("{}"), 0o644)
	_ = os.WriteFile(f2, []byte("{}"), 0o644)

	cfgs := []config{{name: "a.json", filePath: f1}, {name: "b.json", filePath: f2}}

	// choose index 1 then expect error because file empty and reconfigure tries to parse JSON
	cleanup := feedInput(t, "1\n")
	defer cleanup()
	if err := configure(cfgs, conf); err == nil {
		t.Fatal("expected error due to interactive reconfigure reading empty JSON")
	}
}

func TestGetNewValue_ToolsBypassWhenEmpty(t *testing.T) {
	// when k=="tools", getToolsValue expects []any but if user presses enter
	// it should return the original slice, which in our call path is casted to []string
	inputs := strings.Join([]string{"\n"}, "")
	cleanup := feedInput(t, inputs)
	defer cleanup()
	v, err := getNewValue("tools", []string{"a", "b"})
	if err != nil {
		t.Fatalf("getNewValue: %v", err)
	}
	if !reflect.DeepEqual(v, []string{"a", "b"}) {
		t.Fatalf("unexpected: %#v", v)
	}
}

func TestCastPrimitiveTruthiness(t *testing.T) {
	// ensure true/false strings
	if castPrimitive("true") != true {
		t.Fatal("expected true")
	}
	if castPrimitive("false") != false {
		t.Fatal("expected false")
	}
}
