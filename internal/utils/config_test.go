package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestRunMigrationCallback(t *testing.T) {
	// Create a test migration callback
	var migrationCalled bool
	migrationCb := func(configDirPath string) error {
		migrationCalled = true
		return nil
	}

	// Test running the migration callback
	configDirPath := "/path/to/config"
	err := runMigrationCallback(migrationCb, configDirPath)
	if err != nil {
		t.Errorf("Unexpected error running migration callback: %v", err)
	}
	if !migrationCalled {
		t.Error("Expected migration callback to be called")
	}

	// Test running the migration callback with nil callback
	migrationCalled = false
	err = runMigrationCallback(nil, configDirPath)
	if err != nil {
		t.Errorf("Unexpected error running nil migration callback: %v", err)
	}
	if migrationCalled {
		t.Error("Expected migration callback not to be called")
	}
}

func TestCreateConfigDir(t *testing.T) {
	// Create a temporary directory for testing
	configDirPath := filepath.Join(t.TempDir(), ".clai")

	// Test creating a new config directory
	err := CreateConfigDir(configDirPath)
	if err != nil {
		t.Errorf("Unexpected error creating config directory: %v", err)
	}
	if _, err := os.Stat(configDirPath); os.IsNotExist(err) {
		t.Error("Expected config directory to exist")
	}
	for _, d := range requiredConfigDirs {
		if _, err := os.Stat(filepath.Join(configDirPath, d)); os.IsNotExist(err) {
			t.Fatalf("Expected required config dir to exist: %v", d)
		}
	}

	shellContextPath := filepath.Join(configDirPath, "shellContexts", "default.json")
	gotShellContextBytes, err := os.ReadFile(shellContextPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", shellContextPath, err)
	}
	gotShellContext := string(gotShellContextBytes)
	for _, wantFragment := range []string{
		`"template":`,
		`{{if .hostname}}hostname: {{.hostname}}\n`,
		`{{end}}{{if .shell}}shell: {{.shell}}\n`,
		`"cwd": "pwd"`,
		`"date": "date`,
		`"user": "id -un`,
		`"python_venv":`,
		`"k8s_context":`,
		`"go_version":`,
		`"git_branch":`,
		`"docker_context":`,
		`"hostname":`,
	} {
		if !strings.Contains(gotShellContext, wantFragment) {
			t.Fatalf("default shell context missing fragment %q in:\n%s", wantFragment, gotShellContext)
		}
	}

	if _, err := os.Stat(filepath.Join(configDirPath, "shellContexts", "neat.json")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy shell context file neat.json to not exist, got err=%v", err)
	}

	// Test creating an existing config directory
	err = CreateConfigDir(configDirPath)
	if err != nil {
		t.Errorf("Unexpected error creating existing config directory: %v", err)
	}
}

func TestCreateDefaultConfigFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	os.MkdirAll(filepath.Join(tempDir, ".clai"), 0o755)

	configDirPath := filepath.Join(tempDir, ".clai")
	configFileName := "config.json"

	// Test creating a new default config file
	dflt := &struct {
		Name string `json:"name"`
	}{Name: "John"}
	err := createDefaultConfigFile(configDirPath, configFileName, dflt)
	if err != nil {
		t.Errorf("Unexpected error creating default config file: %v", err)
	}
	configFilePath := filepath.Join(configDirPath, configFileName)
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		t.Error("Expected default config file to exist")
	}

	// Test creating an existing default config file
	err = createDefaultConfigFile(configDirPath, configFileName, dflt)
	if err != nil {
		t.Errorf("Unexpected error creating existing default config file: %v", err)
	}
}

type migrationFixture struct {
	Model   string         `json:"model"`
	Limit   int            `json:"limit"`
	Ratio   float64        `json:"ratio"`
	Enabled bool           `json:"enabled"`
	Nested  *migrationNest `json:"nested"`
	Flat    migrationNest  `json:"flat"`
	SkipMe  string         `json:"-"`
	NoTag   string
}

type migrationNest struct {
	A int    `json:"a"`
	B string `json:"b"`
}

func Test_fillMissingFromDefaults(t *testing.T) {
	defaults := &migrationFixture{
		Model:   "gpt-5.2",
		Limit:   21600,
		Ratio:   0.5,
		Enabled: true,
		Nested:  &migrationNest{A: 1, B: "b"},
		Flat:    migrationNest{A: 2, B: "flat"},
		SkipMe:  "secret",
		NoTag:   "untagged",
	}

	// loadFixture mirrors LoadConfigFromFile: loaded and present are both
	// derived from the same on-disk JSON so they always agree.
	loadFixture := func(t *testing.T, jsonStr string) (*migrationFixture, map[string]json.RawMessage) {
		t.Helper()
		var loaded migrationFixture
		if err := json.Unmarshal([]byte(jsonStr), &loaded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		var present map[string]json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &present); err != nil {
			t.Fatalf("Unmarshal present: %v", err)
		}
		return &loaded, present
	}

	t.Run("absent keys are filled from non-zero defaults", func(t *testing.T) {
		loaded, present := loadFixture(t, `{"model":"test"}`)
		added := fillMissingFromDefaults(loaded, defaults, present, "")
		testboil.FailTestIfDiff(t, strings.Join(added, ","), "limit,ratio,enabled,nested,flat,NoTag")
		testboil.FailTestIfDiff(t, loaded.Limit, 21600)
		testboil.FailTestIfDiff(t, loaded.Enabled, true)
		if loaded.Nested == nil || loaded.Nested.A != 1 || loaded.Nested.B != "b" {
			t.Fatalf("expected nested copied wholesale, got %+v", loaded.Nested)
		}
		if loaded.Flat != (migrationNest{A: 2, B: "flat"}) {
			t.Fatalf("expected flat copied, got %+v", loaded.Flat)
		}
		if loaded.SkipMe != "" {
			t.Fatalf("json:\"-\" field must not be filled, got %q", loaded.SkipMe)
		}
	})

	t.Run("present keys are preserved even when explicitly zero", func(t *testing.T) {
		loaded, present := loadFixture(t, `{"model":"","limit":0,"ratio":0,"enabled":false}`)
		added := fillMissingFromDefaults(loaded, defaults, present, "")
		testboil.FailTestIfDiff(t, strings.Join(added, ","), "nested,flat,NoTag")
		if loaded.Model != "" || loaded.Limit != 0 || loaded.Ratio != 0 || loaded.Enabled {
			t.Fatalf("present zero values must survive, got %+v", loaded)
		}
	})

	t.Run("missing subkeys inside a present nested object are filled", func(t *testing.T) {
		loaded, present := loadFixture(t, `{"model":"test","limit":100,"ratio":0.25,"enabled":true,"nested":{"a":7},"flat":{"a":2,"b":"flat"},"NoTag":"untagged"}`)
		added := fillMissingFromDefaults(loaded, defaults, present, "")
		testboil.FailTestIfDiff(t, strings.Join(added, ","), "nested.b")
		testboil.FailTestIfDiff(t, loaded.Nested.A, 7)
		testboil.FailTestIfDiff(t, loaded.Nested.B, "b")
	})

	t.Run("present nested subkeys are never overwritten", func(t *testing.T) {
		loaded, present := loadFixture(t, `{"model":"test","limit":100,"ratio":0.25,"enabled":true,"nested":{"a":0,"b":"user"},"flat":{"a":2,"b":"flat"},"NoTag":"untagged"}`)
		added := fillMissingFromDefaults(loaded, defaults, present, "")
		if len(added) != 0 {
			t.Fatalf("expected no additions, got %v", added)
		}
		if loaded.Nested.A != 0 || loaded.Nested.B != "user" {
			t.Fatalf("present subkeys must survive, got %+v", loaded.Nested)
		}
	})
}

// migrateZeroFixture distinguishes migrate-tagged fields (surfaced by the
// migration even at a zero default) from plain zero-default fields (skipped,
// status quo) and non-zero defaults (standard fill).
type migrateZeroFixture struct {
	Tagged   int    `json:"tagged" migrate:"true"`
	Untagged int    `json:"untagged"`
	Active   string `json:"active"`
}

func Test_fillMissingFromDefaults_MigrateTag(t *testing.T) {
	defaults := &migrateZeroFixture{Active: "on"}
	loadFixture := func(jsonStr string) (*migrateZeroFixture, map[string]json.RawMessage) {
		var loaded migrateZeroFixture
		if err := json.Unmarshal([]byte(jsonStr), &loaded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		var present map[string]json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &present); err != nil {
			t.Fatalf("Unmarshal present: %v", err)
		}
		return &loaded, present
	}

	t.Run("migrate-tagged zero-default fields are filled when absent", func(t *testing.T) {
		loaded, present := loadFixture(`{"active":"user"}`)
		added := fillMissingFromDefaults(loaded, defaults, present, "")
		got := strings.Join(added, ",")
		if !strings.Contains(got, "tagged") {
			t.Fatalf("expected the migrate-tagged field added, got %q", got)
		}
		if strings.Contains(got, "untagged") {
			t.Fatalf("untagged zero-default field must stay skipped, got %q", got)
		}
		if loaded.Tagged != 0 {
			t.Fatalf("expected the migrate-tagged field at its zero default, got %d", loaded.Tagged)
		}
	})

	t.Run("migrate-tagged fields present in the file are never touched", func(t *testing.T) {
		loaded, present := loadFixture(`{"tagged":7,"active":"user"}`)
		added := fillMissingFromDefaults(loaded, defaults, present, "")
		if strings.Contains(strings.Join(added, ","), "tagged") {
			t.Fatalf("present migrate-tagged field must not be re-added, got %v", added)
		}
		if loaded.Tagged != 7 {
			t.Fatalf("present value must survive, got %d", loaded.Tagged)
		}
	})

	t.Run("migrate-tagged zero fills surface through LoadConfigFromFile once", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "app.json")
		if err := os.WriteFile(configPath, []byte(`{"active":"user"}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		conf, added, err := LoadConfigFromFileCollect(dir, "app.json", nil, defaults)
		if err != nil {
			t.Fatalf("LoadConfigFromFileCollect: %v", err)
		}
		got := strings.Join(added, ",")
		if !strings.Contains(got, "tagged") || strings.Contains(got, "untagged") {
			t.Fatalf("expected only the migrate-tagged field added, got %v", added)
		}
		if conf.Tagged != 0 || conf.Untagged != 0 || conf.Active != "user" {
			t.Fatalf("unexpected merge result: %+v", conf)
		}
		regenerated, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !strings.Contains(string(regenerated), `"tagged": 0`) {
			t.Fatalf("expected the zero default materialized in the file:\n%s", regenerated)
		}
		// Idempotent: a second load adds nothing and does not rewrite.
		before := string(regenerated)
		_, added, err = LoadConfigFromFileCollect(dir, "app.json", nil, defaults)
		if err != nil {
			t.Fatalf("second LoadConfigFromFileCollect: %v", err)
		}
		if len(added) != 0 {
			t.Fatalf("expected the second load to be silent, got %v", added)
		}
		after, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("ReadFile(after): %v", err)
		}
		if string(after) != before {
			t.Fatalf("second load must not rewrite the file:\n%s", after)
		}
	})
}

func TestLoadConfigFromFile_AppendsMissingFieldsAndAnnounces(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "app.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"test"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dflt := &migrationFixture{
		Model:  "gpt-5.2",
		Limit:  21600,
		Nested: &migrationNest{A: 1, B: "b"},
	}

	var conf migrationFixture
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		var err error
		conf, err = LoadConfigFromFile(dir, "app.json", nil, dflt)
		if err != nil {
			t.Fatalf("LoadConfigFromFile: %v", err)
		}
	})
	if conf.Model != "test" {
		t.Fatalf("present model must survive, got %q", conf.Model)
	}
	if conf.Limit != 21600 {
		t.Fatalf("expected limit filled, got %d", conf.Limit)
	}
	if !strings.Contains(stdout, "added new field(s) to app.json:") {
		t.Fatalf("expected the upgrade announcement, got %q", stdout)
	}
	regenerated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, want := range []string{`"model": "test"`, `"limit": 21600`, `"nested"`} {
		if !strings.Contains(string(regenerated), want) {
			t.Fatalf("regenerated config missing %q:\n%s", want, regenerated)
		}
	}
}

func TestLoadConfigFromFile_ReadonlyDoesNotRewriteOrAnnounce(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "app.json")
	orig := `{"model":"test"}`
	if err := os.WriteFile(configPath, []byte(orig), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dflt := &migrationFixture{
		Model:  "gpt-5.2",
		Limit:  21600,
		Nested: &migrationNest{A: 1, B: "b"},
	}

	ReadonlyConfig = true
	t.Cleanup(func() { ReadonlyConfig = false })

	var conf migrationFixture
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		var err error
		conf, err = LoadConfigFromFile(dir, "app.json", nil, dflt)
		if err != nil {
			t.Fatalf("LoadConfigFromFile: %v", err)
		}
	})
	// The in-memory load still sees the current schema.
	if conf.Model != "test" {
		t.Fatalf("present model must survive, got %q", conf.Model)
	}
	if conf.Limit != 21600 {
		t.Fatalf("expected limit filled in memory, got %d", conf.Limit)
	}
	// No announcement and no file rewrite: raw runs are machine-readable
	// and must not mutate the user's configs as a side effect.
	if strings.Contains(stdout, "added new field(s)") {
		t.Fatalf("readonly load must not announce, got %q", stdout)
	}
	regenerated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(regenerated) != orig {
		t.Fatalf("readonly load must not rewrite the file, got %q want %q", regenerated, orig)
	}
}

func TestLoadConfigFromFile_NoCreateReturnsDefaultsWithoutCreating(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "app.json")
	dflt := &migrationFixture{
		Model:  "gpt-5.2",
		Limit:  21600,
		Nested: &migrationNest{A: 1, B: "b"},
	}

	NoCreateConfig = true
	t.Cleanup(func() { NoCreateConfig = false })

	migrationRan := false
	var conf migrationFixture
	var err error
	conf, err = LoadConfigFromFile(dir, "app.json", func(string) error {
		migrationRan = true
		return nil
	}, dflt)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	if conf.Model != dflt.Model || conf.Limit != dflt.Limit {
		t.Fatalf("expected defaults in memory, got %+v", conf)
	}
	if migrationRan {
		t.Fatal("migration callback must not run when config creation is disabled")
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no config file created, stat err=%v", statErr)
	}
	for _, sub := range ConfigDirPaths() {
		if _, statErr := os.Stat(filepath.Join(dir, sub)); !os.IsNotExist(statErr) {
			t.Fatalf("expected no config subdir %q created, stat err=%v", sub, statErr)
		}
	}
}

func TestLoadConfigFromFile_FreshCreationIsSilent(t *testing.T) {
	dir := t.TempDir()
	dflt := &migrationFixture{
		Model:  "gpt-5.2",
		Limit:  21600,
		Nested: &migrationNest{A: 1, B: "b"},
	}

	var conf migrationFixture
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		var err error
		conf, err = LoadConfigFromFile(dir, "app.json", nil, dflt)
		if err != nil {
			t.Fatalf("LoadConfigFromFile: %v", err)
		}
	})
	if strings.Contains(stdout, "added new field(s)") {
		t.Fatalf("fresh creation must not announce, got %q", stdout)
	}
	if conf.Limit != 21600 {
		t.Fatalf("expected default limit, got %d", conf.Limit)
	}
}

func TestLoadConfigFromFile_UpgradeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "app.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"test"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dflt := &migrationFixture{Model: "gpt-5.2", Limit: 21600}
	if _, err := LoadConfigFromFile(dir, "app.json", nil, dflt); err != nil {
		t.Fatalf("first load: %v", err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := LoadConfigFromFile(dir, "app.json", nil, dflt); err != nil {
			t.Fatalf("second load: %v", err)
		}
	})
	if strings.Contains(stdout, "added new field(s)") {
		t.Fatalf("second load must not announce, got %q", stdout)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	testboil.FailTestIfDiff(t, string(second), string(first))
}

func TestLoadConfigFromFile_CallbackCreatedFileDoesNotAnnounce(t *testing.T) {
	dir := t.TempDir()
	cb := func(configDirPath string) error {
		return os.WriteFile(filepath.Join(configDirPath, "app.json"), []byte(`{"model":"test"}`), 0o644)
	}
	dflt := &migrationFixture{Model: "gpt-5.2", Limit: 21600}

	var conf migrationFixture
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		var err error
		conf, err = LoadConfigFromFile(dir, "app.json", cb, dflt)
		if err != nil {
			t.Fatalf("LoadConfigFromFile: %v", err)
		}
	})
	if strings.Contains(stdout, "added new field(s)") {
		t.Fatalf("callback-created file must not announce, got %q", stdout)
	}
	if conf.Limit != 21600 {
		t.Fatalf("expected limit filled, got %d", conf.Limit)
	}
	regenerated, err := os.ReadFile(filepath.Join(dir, "app.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(regenerated), `"limit": 21600`) {
		t.Fatalf("expected the merged file persisted:\n%s", regenerated)
	}
}
