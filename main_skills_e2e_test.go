package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Covers AC1, AC17.
func Test_e2e_skills_bootstrap_creates_config_and_default_dir(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := setupMainTestConfigDir(t)
	writeSkillFile(t, filepath.Join(confDir, "skills", "bootstrap", "SKILL.md"), "---\ndescription: bootstrap\n---\nBody")
	repoDir := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	rootWd := oldWd
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm test q bootstrap", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	if !strings.HasSuffix(stdout, "bootstrap\n\a") {
		t.Fatalf("unexpected bootstrap output: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(confDir, "skills")); err != nil {
		t.Fatalf("expected skills dir to exist: %v", err)
	}
	cfgBytes, err := os.ReadFile(filepath.Join(confDir, "skills.json"))
	if err != nil {
		t.Fatalf("ReadFile(skills.json): %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatalf("Unmarshal(skills.json): %v", err)
	}
	if cfg["maxActivatedSkills"] != float64(10) || cfg["trust_all_skills"] != false {
		t.Fatalf("unexpected skills config: %s", string(cfgBytes))
	}
	if cfg["enabled"] != false {
		t.Fatalf("expected enabled=false default in bootstrap config, got %s", string(cfgBytes))
	}
	if cfg["projectSkillDirs"] == nil {
		t.Fatalf("expected projectSkillDirs defaults, got %s", string(cfgBytes))
	}
	readme, err := os.ReadFile(filepath.Join(rootWd, "architecture", "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(architecture/README.md): %v", err)
	}
	if !strings.Contains(string(readme), "./skills.md") {
		t.Fatalf("expected architecture index to reference skills.md")
	}
}

// Covers AC1(enablement), AC7a.
func Test_e2e_skills_opt_in_enablement_and_precedence(t *testing.T) {
	oldArgs := os.Args
	oldWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
	})

	type tc struct {
		name           string
		profile        map[string]any
		profileName    string
		args           string
		wantStatus     int
		wantContains   []string
		wantNotContain []string
	}

	tests := []tc{
		{
			name:       "default_disabled_is_silent",
			args:       "-r -cm mock_test q please tool_load_skill",
			wantStatus: 1,
			wantNotContain: []string{
				"skills default:",
				"loaded skill review",
				"Untrusted skill detected!",
			},
		},
		{
			name:       "skills_json_enables_skills",
			args:       "-r -cm mock_test q please tool_load_skill",
			wantStatus: 0,
			wantContains: []string{
				"loaded skill review [default]",
			},
		},
		{
			name:        "profile_disable_overrides_enabled_skills_json",
			profileName: "skills-off",
			profile: map[string]any{
				"name":       "skills-off",
				"model":      "mock_test",
				"prompt":     "profile prompt",
				"use_skills": false,
			},
			args:       "-r -p skills-off q please tool_load_skill",
			wantStatus: 1,
			wantContains: []string{
				"load_skill requested but skills are unavailable",
			},
			wantNotContain: []string{
				"skills default:",
				"loaded skill review",
			},
		},
		{
			name:        "profile_enables_skills",
			profileName: "skills-on",
			profile: map[string]any{
				"name":       "skills-on",
				"model":      "mock_test",
				"prompt":     "profile prompt",
				"use_skills": true,
			},
			args:       "-r -p skills-on q please tool_load_skill",
			wantStatus: 0,
			wantContains: []string{
				"loaded skill review [default]",
			},
		},
		{
			name:        "cli_enable_overrides_disabled_profile_and_text_config",
			profileName: "skills-off",
			profile: map[string]any{
				"name":       "skills-off",
				"model":      "mock_test",
				"prompt":     "profile prompt",
				"use_skills": false,
			},
			args:       "-r -p skills-off -s=* q please tool_load_skill",
			wantStatus: 0,
			wantContains: []string{
				"loaded skill review [default]",
			},
		},
		{
			name:        "cli_disable_overrides_enabled_profile_and_text_config",
			profileName: "skills-on",
			profile: map[string]any{
				"name":       "skills-on",
				"model":      "mock_test",
				"prompt":     "profile prompt",
				"use_skills": true,
			},
			args:       "-r -p skills-on -s=none q please tool_load_skill",
			wantStatus: 1,
			wantContains: []string{
				"load_skill requested but skills are unavailable",
			},
			wantNotContain: []string{
				"skills default:",
				"loaded skill review",
			},
		},
		{
			name:       "invalid_cli_value_errors",
			args:       "-r -cm mock_test -s=bogus q please tool_load_skill",
			wantStatus: 1,
			wantContains: []string{
				"invalid skills flag value",
			},
			wantNotContain: []string{
				"skills default:",
				"loaded skill review",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cacheDir := filepath.Join(t.TempDir(), "cache")
			t.Setenv("CLAI_CACHE_DIR", cacheDir)
			confDir := setupMainTestConfigDir(t)
			_ = os.Remove(filepath.Join(cacheDir, "skills_trust.json"))
			repoDir := t.TempDir()
			if err := os.Chdir(repoDir); err != nil {
				t.Fatalf("Chdir(%q): %v", repoDir, err)
			}
			writeSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: Review pending changes\n---\nBody")
			enabled := false
			if tt.name == "skills_json_enables_skills" || tt.name == "profile_disable_overrides_enabled_skills_json" {
				enabled = true
			}
			writeSkillsConfigJSON(t, confDir, map[string]any{
				"enabled":            enabled,
				"globalSkillDirs":    []string{},
				"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
				"trust_all_skills":   true,
				"maxActivatedSkills": 10,
			})
			if tt.profile != nil {
				profilesDir := filepath.Join(confDir, "profiles")
				if err := os.MkdirAll(profilesDir, 0o755); err != nil {
					t.Fatalf("MkdirAll(%q): %v", profilesDir, err)
				}
				writeJSONFileAny(t, filepath.Join(profilesDir, fmt.Sprintf("%s.json", tt.profileName)), tt.profile)
			}

			var gotStatus int
			stdout, stderr := captureStdoutStderr(t, func() {
				gotStatus = run(strings.Split(tt.args, " "))
			})
			if gotStatus != tt.wantStatus {
				t.Fatalf("expected status %d, got %d, stdout=%q stderr=%q", tt.wantStatus, gotStatus, stdout, stderr)
			}
			combined := stdout + stderr
			for _, want := range tt.wantContains {
				if !strings.Contains(combined, want) {
					t.Fatalf("expected %q in output, got %q", want, combined)
				}
			}
			for _, unwanted := range tt.wantNotContain {
				if strings.Contains(combined, unwanted) {
					t.Fatalf("expected %q to be absent from output, got %q", unwanted, combined)
				}
			}
		})
	}
}

// Covers AC1, AC2(partial), AC6, AC7.
func Test_e2e_skills_discovery_precedence_and_logging(t *testing.T) {
	oldArgs := os.Args
	oldWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
	})

	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	globalOne := filepath.Join(t.TempDir(), "global-one")
	globalTwo := filepath.Join(t.TempDir(), "global-two")
	repoDir := filepath.Join(t.TempDir(), "repo")
	nestedDir := filepath.Join(repoDir, "nested", "deeper")
	for _, dir := range []string{globalOne, globalTwo, nestedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}
	writeSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: default review\n---\nDefault body")
	writeSkillFile(t, filepath.Join(globalTwo, "review", "SKILL.md"), "---\ndescription: later global review\n---\nGlobal two body")
	writeSkillFile(t, filepath.Join(globalOne, "review", "SKILL.md"), "---\ndescription: earlier global review\n---\nGlobal one body")
	writeSkillFile(t, filepath.Join(repoDir, "agents", "skills", "review", "SKILL.md"), "---\ndescription: repo review\n---\nRepo body")
	writeSkillFile(t, filepath.Join(repoDir, "nested", "agents", "skills", "review", "SKILL.md"), "---\ndescription: nested review\n---\nNested body")
	writeSkillFile(t, filepath.Join(repoDir, "agents", "skills", "broken", "SKILL.md"), "---\ndescription broken\n---\nBody")
	writeSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{globalOne, globalTwo},
		"projectSkillDirs":   []string{"./agents/skills"},
		"trust_all_skills":   true,
		"maxActivatedSkills": 10,
	})

	if err := os.Chdir(nestedDir); err != nil {
		t.Fatalf("Chdir(%q): %v", nestedDir, err)
	}
	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	for _, want := range []string{
		"skills project: " + filepath.Join(repoDir, "nested", "agents", "skills") + " [loaded=1]",
		"skills: loaded=1 shadowed=4 invalid=1",
		"loaded skill review [project]",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}
}

// Covers AC2(partial), AC3, AC4, AC5(partial), AC8(partial), AC11, AC12(partial).
func Test_e2e_skills_descriptor_activation_and_persistence(t *testing.T) {
	oldArgs := os.Args
	oldWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
	})

	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}

	writeSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: Review pending changes\nallowed-tools: rg,unknown_tool\ndisallowed-tools: ls\n---\nBody")
	writeSkillFile(t, filepath.Join(confDir, "skills", "hidden", "SKILL.md"), "---\ndescription: hidden\ndisable-model-invocation: true\n---\nHidden body")
	writeSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})

	restoreInput := utils.UseReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	for _, want := range []string{
		"Call: 'load_skill'",
		"Untrusted skill detected!",
		"Loaded skill",
		"  Name: review",
		"  Source: default",
		"  Description: Review pending changes",
		"  Length: 4 chars",
		"  Estimated tokens: ~1",
		"done after tool for: please tool_load_skill",
		"Warnings:\n- skill requested unavailable tool \"rg\"\n- skill requested unknown tool \"unknown_tool\"",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}
	for _, notWant := range []string{"ARG:"} {
		if strings.Contains(stdout, notWant) {
			t.Fatalf("expected non-raw user-visible skill output to omit %q, got %q", notWant, stdout)
		}
	}
	for _, want := range []string{"\nBody\n\nWarnings:\n- "} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}

	savedConversation := findSavedConversationFile(t, confDir)
	chatJSON := readStringFile(t, savedConversation)
	if !strings.Contains(chatJSON, `\u003cavailable_skills\u003e`) || !strings.Contains(chatJSON, `\u003cname\u003ereview\u003c/name\u003e`) || !strings.Contains(chatJSON, `\u003cdescription\u003eReview pending changes\u003c/description\u003e`) {
		t.Fatalf("expected descriptor block in persisted conversation, got %s", chatJSON)
	}
	if strings.Contains(chatJSON, "<name>hidden</name>") || strings.Contains(chatJSON, "Hidden body") {
		t.Fatalf("expected hidden skill to stay absent from persisted transcript, got %s", chatJSON)
	}
	if !strings.Contains(chatJSON, `"name":"load_skill"`) || !strings.Contains(chatJSON, `Body`) {
		t.Fatalf("expected loaded skill transcript in conversation, got %s", chatJSON)
	}
	trustJSON := readStringFile(t, filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	if !strings.Contains(trustJSON, `"hash"`) || !strings.Contains(trustJSON, `"path"`) {
		t.Fatalf("expected populated trust cache, got %s", trustJSON)
	}
}

func Test_e2e_skills_raw_mode_shows_full_output(t *testing.T) {
	oldArgs := os.Args
	oldWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
	})

	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	writeSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: Review pending changes\n---\n# Review\nDescription: Review pending changes\nUse ${CLAUDE_SKILL_DIR}\nARG:$ARGUMENTS")
	writeSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})
	t.Setenv("CLAI_MOCK_LOAD_SKILL_ARGS", "raw-value")

	restoreInput := utils.UseReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -raw -cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	for _, want := range []string{"Use ", "ARG:raw-value", "loaded skill review [default] args=\"raw-value\""} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}
}

// Covers AC5, AC14, AC15.
func Test_e2e_skills_trust_reprompt_hash_invalidation_and_trust_all(t *testing.T) {
	oldArgs := os.Args
	oldWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
	})

	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	skillPath := filepath.Join(confDir, "skills", "review", "SKILL.md")
	writeSkillFile(t, skillPath, "---\ndescription: review trust\n---\nBody v1")
	writeSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})
	restoreInput := utils.UseReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	runPrompt := func() string {
		var gotStatus int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			gotStatus = run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
		})
		if gotStatus != 0 {
			t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
		}
		return stdout
	}

	first := runPrompt()
	if !strings.Contains(first, "Untrusted skill detected!") {
		t.Fatalf("expected initial trust prompt, got %q", first)
	}
	second := runPrompt()
	if strings.Contains(second, "Untrusted skill detected!") {
		t.Fatalf("expected cached trust to skip prompt, got %q", second)
	}
	writeSkillFile(t, skillPath, "---\ndescription: review trust\n---\nBody v2")
	third := runPrompt()
	if !strings.Contains(third, "Untrusted skill detected!") {
		t.Fatalf("expected hash change to re-prompt, got %q", third)
	}

	cachePath := filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json")
	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(%q): %v", cachePath, err)
	}
	writeSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   true,
		"maxActivatedSkills": 10,
	})
	fourth := runPrompt()
	if strings.Contains(fourth, "Untrusted skill detected!") {
		t.Fatalf("expected trust_all_skills to suppress prompt, got %q", fourth)
	}
	trustJSON := readStringFile(t, cachePath)
	if !strings.Contains(trustJSON, `"path"`) || !strings.Contains(trustJSON, `"hash"`) {
		t.Fatalf("expected trust cache to be written in trust-all mode, got %s", trustJSON)
	}
}

// Covers AC8(partial), AC9, AC10, AC13, AC16.
func Test_e2e_skills_argument_rendering_and_activation_cap(t *testing.T) {
	oldArgs := os.Args
	oldWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
		_ = os.Unsetenv("CLAI_MOCK_LOAD_SKILL_ARGS")
		_ = os.Unsetenv("CLAI_MOCK_LOAD_SKILL_NAME")
	})

	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	restoreInput := utils.UseReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	writeSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: render review\narguments: [target,extra]\n---\nALL:$ARGUMENTS\nIDX0:$ARGUMENTS[0]\nPOS0:$0\nPOS1:$1\nNAMED:$target|$extra\nDIR:${CLAUDE_SKILL_DIR}\nLITERAL:!`echo nope`")
	writeSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})
	t.Setenv("CLAI_MOCK_LOAD_SKILL_ARGS", "src/main.go extra-value")

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	for _, want := range []string{
		`ALL:src/main.go extra-value`,
		`IDX0:src/main.go`,
		`POS0:src/main.go`,
		`POS1:extra-value`,
		`NAMED:src/main.go|extra-value`,
		`LITERAL:!` + "`echo nope`",
		`loaded skill review [default] args="src/main.go extra-value"`,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}

	writeSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: missing args\n---\nNeed $2")
	errStdout, errStderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
	})
	errOut := errStdout + errStderr
	if gotStatus != 0 {
		t.Fatalf("expected missing argument soft success, got %d, output=%q", gotStatus, errOut)
	}
	if !strings.Contains(errOut, "Need ") {
		t.Fatalf("expected rendered output with empty substitution, got %q", errOut)
	}

	t.Run("activation_cap", func(t *testing.T) {
		confDir := setupMainTestConfigDir(t)
		t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
		_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
		repoDir := t.TempDir()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("Chdir(%q): %v", repoDir, err)
		}
		writeSkillFile(t, filepath.Join(confDir, "skills", "one", "SKILL.md"), "---\ndescription: one\n---\nOne")
		writeSkillFile(t, filepath.Join(confDir, "skills", "two", "SKILL.md"), "---\ndescription: two\n---\nTwo")
		writeSkillsConfigJSON(t, confDir, map[string]any{
			"enabled":            true,
			"globalSkillDirs":    []string{},
			"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
			"trust_all_skills":   true,
			"maxActivatedSkills": 1,
		})
		t.Setenv("CLAI_MOCK_LOAD_SKILL_ARGS", "")
		t.Setenv("CLAI_MOCK_LOAD_SKILL_NAME", "two")
		var gotStatus int
		capOut := testboil.CaptureStdout(t, func(t *testing.T) {
			gotStatus = run(strings.Split("-r -cm mock_test q please tool_load_skill tool_load_skill", " "))
		})
		if gotStatus != 0 {
			t.Fatalf("expected success status with cap error in context, got %d, stdout=%q", gotStatus, capOut)
		}
		if !strings.Contains(capOut, "loaded skill two [default]") || !strings.Contains(capOut, "ERROR: skill activation cap exceeded: maxActivatedSkills=1") {
			t.Fatalf("expected first load plus cap error, got %q", capOut)
		}
		chatJSON := readStringFile(t, findSavedConversationFile(t, confDir))
		if !strings.Contains(chatJSON, `ERROR: skill activation cap exceeded`) || !strings.Contains(chatJSON, `"skill":"two"`) {
			t.Fatalf("expected cap error persisted in conversation, got %s", chatJSON)
		}
	})
}

func writeSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func writeSkillsConfigJSON(t *testing.T, confDir string, cfg map[string]any) {
	t.Helper()
	writeJSONFileAny(t, filepath.Join(confDir, "skills.json"), cfg)
}

func writeJSONFileAny(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("Marshal(%q): %v", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func findSavedConversationFile(t *testing.T, confDir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "globalScope.json" || entry.Name() == "seed.json" {
			continue
		}
		return filepath.Join(confDir, "conversations", entry.Name())
	}
	t.Fatalf("expected a saved conversation file")
	return ""
}

func readStringFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(b)
}

func captureStdoutStderr(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe(stdout): %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe(stderr): %v", err)
	}
	os.Stdout = outW
	os.Stderr = errW
	doneOut := make(chan string, 1)
	doneErr := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		doneOut <- string(b)
	}()
	go func() {
		b, _ := io.ReadAll(errR)
		doneErr <- string(b)
	}()
	fn()
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	return <-doneOut, <-doneErr
}
