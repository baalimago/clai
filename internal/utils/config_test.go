package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestReturnNonDefault(t *testing.T) {
	tests := []struct {
		name       string
		a          any
		b          any
		defaultVal any
		want       any
		wantErr    bool
	}{
		{
			name:       "Both defaults",
			a:          "default",
			b:          "default",
			defaultVal: "default",
			want:       "default",
			wantErr:    false,
		},
		{
			name:       "A non-default",
			a:          "non-default",
			b:          "default",
			defaultVal: "default",
			want:       "non-default",
			wantErr:    false,
		},
		{
			name:       "B non-default",
			a:          "default",
			b:          "non-default",
			defaultVal: "default",
			want:       "non-default",
			wantErr:    false,
		},
		{
			name:       "Both non-default",
			a:          "non-default-a",
			b:          "non-default-b",
			defaultVal: "default",
			want:       "default",
			wantErr:    true,
		},
		{
			name:       "Both non-default same value",
			a:          "non-default",
			b:          "non-default",
			defaultVal: "default",
			want:       "default",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReturnNonDefault(tt.a, tt.b, tt.defaultVal)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReturnNonDefault() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ReturnNonDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

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

type testStruct struct {
	A string
	B string
}

func Test_appendNewFieldsFromDefault(t *testing.T) {
	testCases := []struct {
		desc  string
		given testStruct
		when  testStruct
		want  testStruct
	}{
		{
			desc: "it should append new fields from default if they are zero value in want",
			given: testStruct{
				A: "filled",
			},
			when: testStruct{
				A: "filled",
				B: "new",
			},
			want: testStruct{
				A: "filled",
				B: "new",
			},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			setNonZeroValueFields(&tC.given, &tC.when)
			got := tC.given
			testboil.FailTestIfDiff(t, got.A, tC.want.A)
			testboil.FailTestIfDiff(t, got.B, tC.want.B)
		})
	}
}
