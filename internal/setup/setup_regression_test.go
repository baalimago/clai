package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGetConfigs_EmptyMatchReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := getConfigs(filepath.Join(dir, "*.json"), nil)
	if err != nil {
		t.Fatalf("getConfigs(empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("getConfigs(empty) len = %d, want 0", len(got))
	}
}

func TestGetConfigs_ExcludeUsesBaseNameOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	keepPath := filepath.Join(dir, "keep.json")
	skipPath := filepath.Join(dir, "textConfig.json")
	for _, p := range []string{keepPath, skipPath} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", p, err)
		}
	}

	got, err := getConfigs(filepath.Join(dir, "*.json"), []string{"textConfig"})
	if err != nil {
		t.Fatalf("getConfigs(exclude): %v", err)
	}

	want := []config{{name: "keep.json", filePath: keepPath}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("getConfigs(exclude) = %#v, want %#v", got, want)
	}
}

func FuzzGetConfigs_DoesNotErrorForNormalPatterns(f *testing.F) {
	f.Add("*.json", "")
	f.Add("*.txt", "textConfig")
	f.Add("*Config.json", "photoConfig")

	f.Fuzz(func(t *testing.T, pattern, exclude string) {
		dir := t.TempDir()
		for _, name := range []string{"a.json", "textConfig.json", "photoConfig.json", "note.txt"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", name, err)
			}
		}

		_, err := getConfigs(filepath.Join(dir, pattern), []string{exclude})
		if err != nil {
			t.Fatalf("getConfigs(pattern=%q, exclude=%q): %v", pattern, exclude, err)
		}
	})
}
