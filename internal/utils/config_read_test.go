package utils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

type readConfigT struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

func TestReadConfigFile(t *testing.T) {
	dflt := &readConfigT{Model: "default-model", Prompt: "default-prompt"}

	t.Run("fills missing fields in memory and writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "c.json")
		if err := os.WriteFile(p, []byte(`{"model":"m"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ReadConfigFile(p, dflt)
		if err != nil {
			t.Fatalf("ReadConfigFile: %v", err)
		}
		if got.Model != "m" || got.Prompt != "default-prompt" {
			t.Fatalf("got %+v", got)
		}
		after, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != `{"model":"m"}` {
			t.Fatalf("file rewritten to %s", after)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("dir has %d entries, want only the config file", len(entries))
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, err := ReadConfigFile(filepath.Join(t.TempDir(), "absent.json"), dflt); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want fs.ErrNotExist", err)
		}
	})
	t.Run("malformed file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(p, []byte(`{`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadConfigFile(p, dflt); err == nil {
			t.Fatal("ReadConfigFile accepted malformed JSON")
		}
	})
}
