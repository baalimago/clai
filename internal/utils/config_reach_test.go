package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// requireWritablePermissionModel skips cases that rely on chmod to deny writes:
// root ignores the permission bits, so the failure under test never happens.
func requireWritablePermissionModel(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not block root")
	}
}

func chmodForTest(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q, %o): %v", path, mode, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

func TestResolveConfigDirPath_SkipsEmptyComponents(t *testing.T) {
	got, err := ResolveConfigDirPath("/cfg", []string{"", "", ""})
	if err != nil {
		t.Fatalf("ResolveConfigDirPath: %v", err)
	}
	if got != "/cfg" {
		t.Fatalf("all-empty components should resolve to the config dir, got %q", got)
	}

	got, err = ResolveConfigDirPath("/cfg", []string{"", "skills", ""})
	if err != nil {
		t.Fatalf("ResolveConfigDirPath: %v", err)
	}
	if want := filepath.Join("/cfg", "skills"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCreateConfigDir_ReadOnlyParent(t *testing.T) {
	requireWritablePermissionModel(t)
	parent := t.TempDir()
	chmodForTest(t, parent, 0o500)

	err := CreateConfigDir(filepath.Join(parent, "cfg"))
	if err == nil {
		t.Fatal("expected an error creating the config dir under a read-only parent")
	}
	if !strings.Contains(err.Error(), "failed to setup config dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateConfigDir_PathIsFile(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "cfg")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := CreateConfigDir(notADir)
	if err == nil {
		t.Fatal("expected an error when the config path is a regular file")
	}
	if !strings.Contains(err.Error(), "stat shell context file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateConfigDir_ReadOnlyShellContexts(t *testing.T) {
	requireWritablePermissionModel(t)
	dir := t.TempDir()
	for _, d := range requiredConfigDirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", d, err)
		}
	}
	chmodForTest(t, filepath.Join(dir, "shellContexts"), 0o500)

	err := CreateConfigDir(dir)
	if err == nil {
		t.Fatal("expected an error when the shellContexts dir is read-only")
	}
	if !strings.Contains(err.Error(), "write shell context file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateDefaultConfigFile_ReadOnlyDir(t *testing.T) {
	requireWritablePermissionModel(t)
	dir := t.TempDir()
	chmodForTest(t, dir, 0o500)

	type cfg struct {
		A string `json:"a"`
	}
	err := createDefaultConfigFile(dir, "app.json", &cfg{A: "x"})
	if err == nil {
		t.Fatal("expected an error writing into a read-only dir")
	}
	if !strings.Contains(err.Error(), "failed to write config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloneConfigDefault(t *testing.T) {
	got, err := cloneConfigDefault[int](nil)
	if err != nil {
		t.Fatalf("cloneConfigDefault(nil): %v", err)
	}
	if got != 0 {
		t.Fatalf("cloneConfigDefault(nil) = %d, want the zero value", got)
	}

	unmarshalable := make(chan int)
	if _, err := cloneConfigDefault[chan int](&unmarshalable); err == nil {
		t.Fatal("expected an error cloning a value that cannot be marshaled")
	}
}

func TestLoadConfigFromFile_NoCreateReadError(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "cfg")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	NoCreateConfig = true
	t.Cleanup(func() { NoCreateConfig = false })

	type cfg struct {
		A string `json:"a"`
	}
	_, err := LoadConfigFromFile(notADir, "app.json", nil, &cfg{})
	if err == nil {
		t.Fatal("expected an error reading through a file path component")
	}
	if !strings.Contains(err.Error(), "failed to read config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigFromFile_NoCreateMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{"a":`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	NoCreateConfig = true
	t.Cleanup(func() { NoCreateConfig = false })

	type cfg struct {
		A string `json:"a"`
	}
	_, err := LoadConfigFromFile(dir, "app.json", nil, &cfg{})
	if err == nil {
		t.Fatal("expected an error for a malformed config")
	}
	if !strings.Contains(err.Error(), "failed to unmarshal config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigFromFile_NoCreateUnkeyable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(`[1,2]`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	NoCreateConfig = true
	t.Cleanup(func() { NoCreateConfig = false })

	// A document that parses as the target type but not as a keyed object must
	// be rejected with an error instead of reaching the reflection merge.
	_, err := LoadConfigFromFile(dir, "app.json", nil, new([]int))
	if err == nil {
		t.Fatal("expected an error for a config that is not a JSON object")
	}
	if !strings.Contains(err.Error(), "failed to parse config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigFromFile_DebugPrints(t *testing.T) {
	t.Setenv("DEBUG", "1")
	type cfg struct {
		A string `json:"a"`
	}

	t.Run("read-only existing config", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{"a":"x"}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		NoCreateConfig = true
		t.Cleanup(func() { NoCreateConfig = false })

		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			if _, err := LoadConfigFromFile(dir, "app.json", nil, &cfg{A: "d"}); err != nil {
				t.Fatalf("LoadConfigFromFile: %v", err)
			}
		})
		if !strings.Contains(stdout, "found config (read-only)") {
			t.Fatalf("expected the read-only debug line, got %q", stdout)
		}
	})

	t.Run("fresh writable config", func(t *testing.T) {
		dir := t.TempDir()
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			if _, err := LoadConfigFromFile(dir, "app.json", nil, &cfg{A: "d"}); err != nil {
				t.Fatalf("LoadConfigFromFile: %v", err)
			}
		})
		for _, want := range []string{"attempting to create file", "found config:"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("expected %q in debug output, got %q", want, stdout)
			}
		}
	})
}

func TestLoadConfigFromFile_CreateConfigDirError(t *testing.T) {
	requireWritablePermissionModel(t)
	parent := t.TempDir()
	chmodForTest(t, parent, 0o500)

	type cfg struct {
		A string `json:"a"`
	}
	_, err := LoadConfigFromFile(filepath.Join(parent, "cfg"), "app.json", nil, &cfg{})
	if err == nil {
		t.Fatal("expected an error creating the config dir")
	}
	if !strings.Contains(err.Error(), "failed to setup config dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// seedConfigDir pre-creates every required subdir and the default shell context
// so CreateConfigDir becomes a no-op and a later chmod can target the load steps.
func seedConfigDir(t *testing.T, dir string) {
	t.Helper()
	for _, d := range requiredConfigDirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "shellContexts", "default.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile(default shell context): %v", err)
	}
}

func TestLoadConfigFromFile_CreateDefaultConfigFileError(t *testing.T) {
	requireWritablePermissionModel(t)
	dir := t.TempDir()
	seedConfigDir(t, dir)
	chmodForTest(t, dir, 0o500)

	type cfg struct {
		A string `json:"a"`
	}
	_, err := LoadConfigFromFile(dir, "app.json", nil, &cfg{A: "a"})
	if err == nil {
		t.Fatal("expected an error creating the default config file")
	}
	if !strings.Contains(err.Error(), "failed to write config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigFromFile_PostAppendageWriteError(t *testing.T) {
	requireWritablePermissionModel(t)
	dir := t.TempDir()
	seedConfigDir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{"a":"user"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	chmodForTest(t, dir, 0o500)

	type cfg struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	_, err := LoadConfigFromFile(dir, "app.json", nil, &cfg{A: "a", B: "b"})
	if err == nil {
		t.Fatal("expected an error rewriting the upgraded config")
	}
	if !strings.Contains(err.Error(), "post missing-field appendage") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func Test_fillMissingFromDefaults_NilPointers(t *testing.T) {
	type sub struct {
		B string `json:"b"`
	}
	type cfg struct {
		A *sub `json:"a"`
	}

	t.Run("present object materializes a nil pointer and fills subkeys", func(t *testing.T) {
		loaded := &cfg{}
		dflt := &cfg{A: &sub{B: "b"}}
		present := map[string]json.RawMessage{"a": json.RawMessage(`{}`)}

		added := fillMissingFromDefaults(loaded, dflt, present, "")
		if len(added) != 1 || added[0] != "a.b" {
			t.Fatalf("expected [a.b], got %v", added)
		}
		if loaded.A == nil || loaded.A.B != "b" {
			t.Fatalf("expected the pointer materialized with b filled, got %+v", loaded.A)
		}
	})

	t.Run("nil default pointer is skipped", func(t *testing.T) {
		loaded := &cfg{}
		present := map[string]json.RawMessage{"a": json.RawMessage(`{}`)}

		added := fillMissingFromDefaults(loaded, &cfg{}, present, "")
		if len(added) != 0 {
			t.Fatalf("expected no additions for a nil default pointer, got %v", added)
		}
		if loaded.A == nil || loaded.A.B != "" {
			t.Fatalf("expected the present object materialized but unfilled, got %+v", loaded.A)
		}
	})
}

func Test_fillMissingFromDefaults_ClonesContainerDefaults(t *testing.T) {
	type cfg struct {
		NilPtr   *int           `json:"nil_ptr" migrate:"true"`
		NilSlice []int          `json:"nil_slice" migrate:"true"`
		NilMap   map[string]int `json:"nil_map" migrate:"true"`
		Slice    []int          `json:"slice" migrate:"true"`
		Map      map[string]int `json:"map" migrate:"true"`
	}
	dflt := &cfg{
		Slice: []int{1, 2, 3},
		Map:   map[string]int{"a": 1},
	}
	loaded := &cfg{}

	added := fillMissingFromDefaults(loaded, dflt, map[string]json.RawMessage{}, "")
	if len(added) != 5 {
		t.Fatalf("expected all five fields filled, got %v", added)
	}
	if loaded.NilPtr != nil || loaded.NilSlice != nil || loaded.NilMap != nil {
		t.Fatalf("nil defaults must stay nil, got %+v", loaded)
	}
	// The merged config must not alias the package-level default.
	loaded.Slice[0] = 99
	loaded.Map["a"] = 99
	if dflt.Slice[0] != 1 || dflt.Map["a"] != 1 {
		t.Fatalf("merged config aliases the default: %+v", dflt)
	}
}
