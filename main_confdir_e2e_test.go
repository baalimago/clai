package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_CONFDIR_prints_config_dir(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("confdir", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.FailTestIfDiff(t, stdout, confDir+"\n")
}

func Test_goldenFile_CONFDIR_prints_registered_subdir(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("confdir mcpServers", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.FailTestIfDiff(t, stdout, filepath.Join(confDir, "mcpServers")+"\n")
}

func Test_goldenFile_CONFDIR_unknown_subpath_errors(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("confdir definitely-not-a-real-path", " "))
	})

	if gotStatus == 0 {
		t.Fatalf("expected non-zero status code")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got: %q", stdout)
	}
}

func Test_goldenFile_HELP_mentions_confdir_command(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("help", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "confdir [subpath ...]        Print clai config dir or a registered config subpath")
}

// Test_e2e_confdir_migrates_mode_configs proves the united config migration:
// every command upgrades the mode configs before dispatch, so a downgraded
// textConfig.json is repaired and announced even by commands that never load
// the mode configs themselves (config migration design, Q5).
func Test_e2e_confdir_migrates_mode_configs(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{
		"model": "test",
	})

	stdout, status := runOne(t, confDir, "confdir")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "added new field(s) to textConfig.json:") {
		t.Fatalf("expected the config upgrade announcement for a non-setup command, got:\n%s", stdout)
	}
	regenerated, err := os.ReadFile(filepath.Join(confDir, "textConfig.json"))
	if err != nil {
		t.Fatalf("ReadFile(textConfig.json): %v", err)
	}
	if !strings.Contains(string(regenerated), `"stoploss"`) {
		t.Fatalf("expected stoploss appended to textConfig.json:\n%s", regenerated)
	}
}

// Test_e2e_raw_run_does_not_migrate_configs pins the raw-mode contract: raw
// (machine-readable) runs fill missing fields in memory but never rewrite the
// mode configs and never pollute the machine output with upgrade
// announcements. This is what keeps shell hooks such as
// `clai -r chat dirv2` (run by a zsh precmd) from silently migrating the
// user's configs before their own commands run.
func Test_e2e_raw_run_does_not_migrate_configs(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{
		"model": "test",
	})

	stdout, status := runOne(t, confDir, "-r confdir")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if strings.Contains(stdout, "added new field(s)") {
		t.Fatalf("raw run must not announce, got:\n%s", stdout)
	}
	regenerated, err := os.ReadFile(filepath.Join(confDir, "textConfig.json"))
	if err != nil {
		t.Fatalf("ReadFile(textConfig.json): %v", err)
	}
	if strings.Contains(string(regenerated), `"stoploss"`) {
		t.Fatalf("raw run must not migrate textConfig.json:\n%s", regenerated)
	}
}

// Test_e2e_confdir_migrates_profiles proves the united config migration also
// covers every profiles/*.json: a profile that predates the current schema is
// upgraded in place and announced even when it is never selected via
// -p/-profile-path (config migration design, Q5 extension).
func Test_e2e_confdir_migrates_profiles(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "profiles", "john.json"), map[string]any{
		"name":  "john",
		"model": "test",
	})

	stdout, status := runOne(t, confDir, "confdir")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "added new field(s) to john.json:") {
		t.Fatalf("expected the profile upgrade announcement, got:\n%s", stdout)
	}
	regenerated, err := os.ReadFile(filepath.Join(confDir, "profiles", "john.json"))
	if err != nil {
		t.Fatalf("ReadFile(john.json): %v", err)
	}
	for _, want := range []string{`"use_tools"`, `"save-reply-as-conv"`, `"prompt"`} {
		if !strings.Contains(string(regenerated), want) {
			t.Fatalf("expected %s appended to john.json:\n%s", want, regenerated)
		}
	}
}

// Test_e2e_raw_run_does_not_migrate_profiles pins the raw-mode contract for
// profiles: a raw (machine-readable) run fills missing profile fields in
// memory but never rewrites profiles/*.json and never announces.
func Test_e2e_raw_run_does_not_migrate_profiles(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "profiles", "john.json"), map[string]any{
		"name":  "john",
		"model": "test",
	})

	stdout, status := runOne(t, confDir, "-r confdir")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if strings.Contains(stdout, "added new field(s)") {
		t.Fatalf("raw run must not announce, got:\n%s", stdout)
	}
	regenerated, err := os.ReadFile(filepath.Join(confDir, "profiles", "john.json"))
	if err != nil {
		t.Fatalf("ReadFile(john.json): %v", err)
	}
	if strings.Contains(string(regenerated), `"use_tools"`) {
		t.Fatalf("raw run must not migrate john.json:\n%s", regenerated)
	}
}

// Test_e2e_confdir_migrates_profiles_skips_broken_and_non_json_files pins the
// broken-profile policy: a malformed profile warns and is skipped (same policy
// as the mode configs), non-JSON files are never touched, and one broken file
// does not block the migration of the others.
func Test_e2e_confdir_migrates_profiles_skips_broken_and_non_json_files(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "profiles", "john.json"), map[string]any{
		"name":  "john",
		"model": "test",
	})
	if err := os.WriteFile(filepath.Join(confDir, "profiles", "broken.json"), []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("WriteFile(broken.json): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "profiles", "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile(notes.txt): %v", err)
	}

	stdout, status := runOne(t, confDir, "confdir")
	if status != 0 {
		t.Fatalf("expected zero status, got %d. stdout=%q", status, stdout)
	}
	if !strings.Contains(stdout, "failed to upgrade profile broken.json") {
		t.Fatalf("expected a warning for the broken profile, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "added new field(s) to john.json:") {
		t.Fatalf("expected the profile upgrade announcement, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "notes.txt") {
		t.Fatalf("non-JSON files must not be migrated or announced, got:\n%s", stdout)
	}
}
