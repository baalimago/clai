package text

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Test_findProfileByPath_writesNothingNextToProfile pins that -prp only reads
// its file: loading a profile from, e.g., a cache folder must not create
// shellContexts/, profiles/ or any other config scaffolding beside it.
func Test_findProfileByPath_writesNothingNextToProfile(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "remote.json")
	content := []byte(`{"model":"gpt-x"}`)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	got, err := findProfileByPath(p)
	if err != nil {
		t.Fatalf("findProfileByPath: %v", err)
	}
	if got.Model != "gpt-x" || got.Prompt != DefaultProfile.Prompt || !got.UseTools {
		t.Fatalf("profile = %+v, want gpt-x with defaults filled in", got)
	}
	if names := dirEntries(t, dir); !slices.Equal(names, []string{"remote.json"}) {
		t.Fatalf("profile dir = %v, want only remote.json", names)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != string(content) {
		t.Fatalf("profile file rewritten to %s, want it untouched", after)
	}
}

func Test_findProfileByPath_missingFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	_, err := findProfileByPath(filepath.Join(dir, "absent.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("findProfileByPath err = %v, want fs.ErrNotExist", err)
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Fatalf("dir = %v, want nothing created for a missing profile", names)
	}
}

func Test_findProfile_writesNothingInProfilesDir(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	profiles := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "john.json"), []byte(`{"model":"test"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := findProfile("john"); err != nil {
		t.Fatalf("findProfile: %v", err)
	}
	if names := dirEntries(t, profiles); !slices.Equal(names, []string{"john.json"}) {
		t.Fatalf("profiles dir = %v, want only john.json", names)
	}
}
