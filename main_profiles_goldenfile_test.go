package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_PROFILES_list_prints_summary_for_valid_profiles_and_skips_invalid(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	profilesDir := filepath.Join(confDir, "profiles")

	// Valid profile with explicit name.
	valid1 := map[string]any{
		"name":   "cody",
		"model":  "test",
		"tools":  []string{"bash", "rg"},
		"prompt": "You are Cody. Be helpful. Second sentence.",
	}
	b, err := json.Marshal(valid1)
	if err != nil {
		t.Fatalf("Marshal(valid1): %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "cody.json"), b, 0o644); err != nil {
		t.Fatalf("WriteFile(cody.json): %v", err)
	}

	// Valid profile without name; should fall back to filename.
	valid2 := map[string]any{
		"model":  "test",
		"tools":  []string{},
		"prompt": "First line only\nsecond line",
	}
	b, err = json.Marshal(valid2)
	if err != nil {
		t.Fatalf("Marshal(valid2): %v", err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, "gopher.json"), b, 0o644); err != nil {
		t.Fatalf("WriteFile(gopher.json): %v", err)
	}

	// Invalid JSON; must be skipped.
	if err := os.WriteFile(filepath.Join(profilesDir, "broken.json"), []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("WriteFile(broken.json): %v", err)
	}

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("profiles", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)

	// runProfilesList iterates directory entries; order is not guaranteed.
	// Assert presence of key blocks rather than exact full output.
	testboil.AssertStringContains(t, stdout, "Name: cody\n")
	testboil.AssertStringContains(t, stdout, "Model: test\n")
	testboil.AssertStringContains(t, stdout, fmt.Sprintf("Tools: %v\n", []string{"bash", "rg"}))
	testboil.AssertStringContains(t, stdout, "First sentence prompt: You are Cody.\n---\n")

	testboil.AssertStringContains(t, stdout, "Name: gopher\n")
	testboil.AssertStringContains(t, stdout, fmt.Sprintf("Tools: %v\n", []string{}))
	// Note: getFirstSentence includes the newline terminator when splitting on \n.
	testboil.AssertStringContains(t, stdout, "First sentence prompt: First line only\n\n---\n")

	if strings.Contains(stdout, "broken") {
		t.Fatalf("output must not include invalid profile file name; got output: %q", stdout)
	}
}

func Test_goldenFile_PROFILES_list_warns_when_no_profiles_found(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	required := []string{
		"conversations",
		"profiles", // created, but empty
		"mcpServers",
		"conversations/dirs",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("profiles", " "))
	})

	// profiles list exits via ErrUserInitiatedExit which main.run maps to status code 0.
	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "warning")
	testboil.AssertStringContains(t, stdout, "no profiles found in ")
	testboil.AssertStringContains(t, stdout, filepath.Join(confDir, "profiles"))
}
