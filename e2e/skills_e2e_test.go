package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Covers AC1, AC17.
func Test_e2e_skills_bootstrap_creates_config_and_default_dir(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := e2etest.SetupMainTestConfigDir(t)
	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "bootstrap", "SKILL.md"), "---\ndescription: bootstrap\n---\nBody")
	repoDir := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("-r -cm test q bootstrap", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	if !strings.HasSuffix(stdout, "bootstrap\n") {
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
	readme, err := os.ReadFile(filepath.Join(e2etest.ModuleRoot(t), "architecture", "README.md"))
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
				"Body",
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
				"Body",
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
				"Body",
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
			confDir := e2etest.SetupMainTestConfigDir(t)
			_ = os.Remove(filepath.Join(cacheDir, "skills_trust.json"))
			repoDir := t.TempDir()
			if err := os.Chdir(repoDir); err != nil {
				t.Fatalf("Chdir(%q): %v", repoDir, err)
			}
			e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: Review pending changes\n---\nBody")
			enabled := false
			if tt.name == "skills_json_enables_skills" || tt.name == "profile_disable_overrides_enabled_skills_json" {
				enabled = true
			}
			e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
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
				e2etest.WriteJSONFileAny(t, filepath.Join(profilesDir, fmt.Sprintf("%s.json", tt.profileName)), tt.profile)
			}

			var gotStatus int
			stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
				gotStatus = harness.Run(strings.Split(tt.args, " "))
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

	confDir := e2etest.SetupMainTestConfigDir(t)
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
	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: default review\n---\nDefault body")
	e2etest.WriteSkillFile(t, filepath.Join(globalTwo, "review", "SKILL.md"), "---\ndescription: later global review\n---\nGlobal two body")
	e2etest.WriteSkillFile(t, filepath.Join(globalOne, "review", "SKILL.md"), "---\ndescription: earlier global review\n---\nGlobal one body")
	e2etest.WriteSkillFile(t, filepath.Join(repoDir, "agents", "skills", "review", "SKILL.md"), "---\ndescription: repo review\n---\nRepo body")
	e2etest.WriteSkillFile(t, filepath.Join(repoDir, "nested", "agents", "skills", "review", "SKILL.md"), "---\ndescription: nested review\n---\nNested body")
	e2etest.WriteSkillFile(t, filepath.Join(repoDir, "agents", "skills", "broken", "SKILL.md"), "---\ndescription broken\n---\nBody")
	e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
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
		gotStatus = harness.Run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	for _, want := range []string{"Nested body"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "skills: loaded=") {
		t.Fatalf("expected discovery information to stay hidden by default, got %q", stdout)
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

	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}

	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: Review pending changes\nallowed-tools: apply_patch,unknown_tool\ndisallowed-tools: write_file\n---\nBody")
	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "hidden", "SKILL.md"), "---\ndescription: hidden\ndisable-model-invocation: true\n---\nHidden body")
	e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})

	restoreInput := useReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	for _, want := range []string{
		"Call: 'load_skill'",
		"Untrusted skill detected!",
		"  Name: review",
		"  Description: Review pending changes",
		"done after tool for: please tool_load_skill",
		"Warnings:\n- skill requested unknown tool \"unknown_tool\"\n- skill enabled local tools: apply_patch",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "Loaded skill") {
		t.Fatalf("expected skill tool output without duplicate load notice, got %q", stdout)
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

	savedConversation := e2etest.FindSavedConversationFile(t, confDir)
	chatJSON := e2etest.ReadStringFile(t, savedConversation)
	if !strings.Contains(chatJSON, `\u003cavailable_skills\u003e`) || !strings.Contains(chatJSON, `\u003cname\u003ereview\u003c/name\u003e`) || !strings.Contains(chatJSON, `\u003cdescription\u003eReview pending changes\u003c/description\u003e`) {
		t.Fatalf("expected descriptor block in persisted conversation, got %s", chatJSON)
	}
	if strings.Contains(chatJSON, "<name>hidden</name>") || strings.Contains(chatJSON, "Hidden body") {
		t.Fatalf("expected hidden skill to stay absent from persisted transcript, got %s", chatJSON)
	}
	if !strings.Contains(chatJSON, `"name": "load_skill"`) || !strings.Contains(chatJSON, `Body`) {
		t.Fatalf("expected loaded skill transcript in conversation, got %s", chatJSON)
	}
	trustJSON := e2etest.ReadStringFile(t, filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	if !strings.Contains(trustJSON, `"hash"`) || !strings.Contains(trustJSON, `"path"`) {
		t.Fatalf("expected populated trust cache, got %s", trustJSON)
	}
}

func Test_e2e_skill_automatically_enables_local_tools(t *testing.T) {
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: Review pending changes\nallowed-tools: async_cmd,async_cmd_status,async_cmd_logs,async_cmd_await,async_cmd_cancel\n---\nBody")
	e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{},
		"trust_all_skills":   true,
		"maxActivatedSkills": 10,
	})

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("-cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	if strings.Contains(stdout, "skill requested unavailable tool") {
		t.Fatalf("local tools reported unavailable: %q", stdout)
	}
	compactStdout := strings.ReplaceAll(stdout, "\n  ", "")
	if !strings.Contains(compactStdout, "skill enabled local tools: async_cmd, async_cmd_await, async_cmd_cancel, async_cmd_logs, async_cmd_status") {
		t.Fatalf("expected automatic tool warning, got %q", stdout)
	}
	if count := strings.Count(stdout, "Body"); count != 1 {
		t.Fatalf("expected one complete raw skill result, got %d in %q", count, stdout)
	}
	if strings.Contains(stdout, "Loaded skill") {
		t.Fatalf("expected no duplicate activation notice, got %q", stdout)
	}
}

func Test_e2e_skills_raw_mode_shows_full_output(t *testing.T) {
	oldArgs := os.Args
	oldWd, _ := os.Getwd()
	t.Cleanup(func() {
		os.Args = oldArgs
		_ = os.Chdir(oldWd)
	})

	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: Review pending changes\n---\n# Review\nDescription: Review pending changes\nUse ${CLAUDE_SKILL_DIR}\nARG:$ARGUMENTS")
	e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})
	t.Setenv("CLAI_MOCK_LOAD_SKILL_ARGS", "raw-value")

	restoreInput := useReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("-r -raw -cm mock_test q please tool_load_skill", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stdout=%q", gotStatus, stdout)
	}
	for _, want := range []string{"Use ", "ARG:raw-value"} {
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

	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	skillPath := filepath.Join(confDir, "skills", "review", "SKILL.md")
	e2etest.WriteSkillFile(t, skillPath, "---\ndescription: review trust\n---\nBody v1")
	e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})
	restoreInput := useReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	runPrompt := func() string {
		var gotStatus int
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			gotStatus = harness.Run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
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
	e2etest.WriteSkillFile(t, skillPath, "---\ndescription: review trust\n---\nBody v2")
	third := runPrompt()
	if !strings.Contains(third, "Untrusted skill detected!") {
		t.Fatalf("expected hash change to re-prompt, got %q", third)
	}

	cachePath := filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json")
	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(%q): %v", cachePath, err)
	}
	e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
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
	trustJSON := e2etest.ReadStringFile(t, cachePath)
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

	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
	repoDir := t.TempDir()
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	restoreInput := useReadUserInputForTests(func() (string, error) { return "y", nil })
	t.Cleanup(restoreInput)

	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: render review\narguments: [target,extra]\n---\nALL:$ARGUMENTS\nIDX0:$ARGUMENTS[0]\nPOS0:$0\nPOS1:$1\nNAMED:$target|$extra\nDIR:${CLAUDE_SKILL_DIR}\nLITERAL:!`echo nope`")
	e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
		"enabled":            true,
		"globalSkillDirs":    []string{},
		"projectSkillDirs":   []string{"./agents/skills", ".claude/skills"},
		"trust_all_skills":   false,
		"maxActivatedSkills": 10,
	})
	t.Setenv("CLAI_MOCK_LOAD_SKILL_ARGS", "src/main.go extra-value")

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
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
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected %q in output, got %q", want, stdout)
		}
	}

	e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "review", "SKILL.md"), "---\ndescription: missing args\n---\nNeed $2")
	errStdout, errStderr := e2etest.CaptureStdoutStderr(t, func() {
		gotStatus = harness.Run(strings.Split("-r -cm mock_test q please tool_load_skill", " "))
	})
	errOut := errStdout + errStderr
	if gotStatus != 0 {
		t.Fatalf("expected missing argument soft success, got %d, output=%q", gotStatus, errOut)
	}
	if !strings.Contains(errOut, "Need ") {
		t.Fatalf("expected rendered output with empty substitution, got %q", errOut)
	}

	t.Run("activation_cap", func(t *testing.T) {
		confDir := e2etest.SetupMainTestConfigDir(t)
		t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
		_ = os.Remove(filepath.Join(os.Getenv("CLAI_CACHE_DIR"), "skills_trust.json"))
		repoDir := t.TempDir()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("Chdir(%q): %v", repoDir, err)
		}
		e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "one", "SKILL.md"), "---\ndescription: one\n---\nOne")
		e2etest.WriteSkillFile(t, filepath.Join(confDir, "skills", "two", "SKILL.md"), "---\ndescription: two\n---\nTwo")
		e2etest.WriteSkillsConfigJSON(t, confDir, map[string]any{
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
			gotStatus = harness.Run(strings.Split("-r -cm mock_test q please tool_load_skill tool_load_skill", " "))
		})
		if gotStatus != 0 {
			t.Fatalf("expected success status with cap error in context, got %d, stdout=%q", gotStatus, capOut)
		}
		if !strings.Contains(capOut, "Two") || !strings.Contains(capOut, "ERROR: skill activation cap exceeded: maxActivatedSkills=1") {
			t.Fatalf("expected first load plus cap error, got %q", capOut)
		}
		chatJSON := e2etest.ReadStringFile(t, e2etest.FindSavedConversationFile(t, confDir))
		if !strings.Contains(chatJSON, `ERROR: skill activation cap exceeded`) || !strings.Contains(chatJSON, `"skill": "two"`) {
			t.Fatalf("expected cap error persisted in conversation, got %s", chatJSON)
		}
	})
}
