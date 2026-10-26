package utils

import (
	"os"
	"path/filepath"
	"testing"
)

type readOnlyConf struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Tools  bool   `json:"use_tools"`
}

func TestReadConfigFile_fillsDefaultsInMemoryAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	body := []byte(`{"model":"m","use_tools":false}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadConfigFile(path, &readOnlyConf{Prompt: "default", Tools: true})
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}
	if got != (readOnlyConf{Model: "m", Prompt: "default", Tools: false}) {
		t.Fatalf("got %+v", got)
	}
	after, _ := os.ReadFile(path)
	entries, _ := os.ReadDir(dir)
	if string(after) != string(body) || len(entries) != 1 {
		t.Fatalf("file now %q, dir holds %d entries; want both untouched", after, len(entries))
	}
}

func TestReadConfigFile_errors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadConfigFile(filepath.Join(dir, "missing.json"), &readOnlyConf{}); err == nil {
		t.Fatal("missing file: want error")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfigFile(bad, &readOnlyConf{}); err == nil {
		t.Fatal("bad json: want error")
	}
}
