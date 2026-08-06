package skills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestLoadSkillMergesPoliciesAcrossMultipleActivations(t *testing.T) {
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	writeSkill(t, filepath.Join(cfgDir, "skills", "one", "SKILL.md"), "---\ndescription: one\nallowed-tools: rg\n---\nOne")
	writeSkill(t, filepath.Join(cfgDir, "skills", "two", "SKILL.md"), "---\ndescription: two\ndisallowed-tools: cat\nallowed-tools: ls\n---\nTwo")
	mgr, err := Discover(Options{
		ConfigDir:      cfgDir,
		CacheDir:       cacheDir,
		WorkingDir:     t.TempDir(),
		KnownToolNames: []string{"rg", "cat", "ls"},
		LocalTools: map[string]pub_models.LLMTool{
			"rg":  staticTool{name: "rg"},
			"cat": staticTool{name: "cat"},
			"ls":  staticTool{name: "ls"},
		},
		TrustPrompter: func(context.Context, TrustPrompt) (bool, error) {
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	base := map[string]pub_models.LLMTool{"rg": staticTool{name: "rg"}, "cat": staticTool{name: "cat"}}
	if _, err := mgr.LoadSkill(context.Background(), "one", "", base); err != nil {
		t.Fatalf("LoadSkill(one): %v", err)
	}
	loaded, err := mgr.LoadSkill(context.Background(), "two", "", base)
	if err != nil {
		t.Fatalf("LoadSkill(two): %v", err)
	}
	if _, ok := loaded.ActiveTools["cat"]; ok {
		t.Fatalf("expected merged disallow to remove cat")
	}
	if _, ok := loaded.ActiveTools["ls"]; !ok {
		t.Fatalf("expected allowed local tool to be enabled, got %#v", loaded.ActiveTools)
	}
	if len(loaded.Warnings) != 0 {
		t.Fatalf("expected no warnings for known local tools, got %#v", loaded.Warnings)
	}
}

func TestLoadSkillDoesNotEnableMCPTools(t *testing.T) {
	cfgDir := t.TempDir()
	writeSkill(t, filepath.Join(cfgDir, "skills", "one", "SKILL.md"), "---\ndescription: one\nallowed-tools: mcp_known_tool,mcp_unknown_tool\n---\nOne")
	mgr, err := Discover(Options{
		ConfigDir:      cfgDir,
		CacheDir:       t.TempDir(),
		WorkingDir:     t.TempDir(),
		KnownToolNames: []string{"rg"},
		TrustPrompter: func(context.Context, TrustPrompt) (bool, error) {
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	loaded, err := mgr.LoadSkill(context.Background(), "one", "", map[string]pub_models.LLMTool{})
	if err != nil {
		t.Fatalf("LoadSkill(one): %v", err)
	}
	if len(loaded.ActiveTools) != 0 {
		t.Fatalf("expected no MCP tools to be enabled, got %#v", loaded.ActiveTools)
	}
	for _, toolName := range []string{"mcp_known_tool", "mcp_unknown_tool"} {
		if !strings.Contains(strings.Join(loaded.Warnings, "\n"), toolName) {
			t.Fatalf("expected warning for %q, got %#v", toolName, loaded.Warnings)
		}
	}
}

func TestLoadSkillActivationCap(t *testing.T) {
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	writeSkill(t, filepath.Join(cfgDir, "skills", "one", "SKILL.md"), "---\ndescription: one\n---\nBody")
	writeSkill(t, filepath.Join(cfgDir, "skills", "two", "SKILL.md"), "---\ndescription: two\n---\nBody")
	mustWriteSkillsConfig(t, cfgDir, Config{ProjectSkillDirs: []string{"./agents/skills", ".claude/skills"}, MaxActivatedSkills: 1})
	mgr, err := Discover(Options{
		ConfigDir:  cfgDir,
		CacheDir:   cacheDir,
		WorkingDir: t.TempDir(),
		TrustPrompter: func(context.Context, TrustPrompt) (bool, error) {
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if _, err := mgr.LoadSkill(context.Background(), "one", "", nil); err != nil {
		t.Fatalf("first load error = %v", err)
	}
	loaded, err := mgr.LoadSkill(context.Background(), "two", "", nil)
	if err != nil {
		t.Fatalf("expected nil error on cap exceed, got %v", err)
	}
	if !strings.Contains(loaded.ActivationErr, "activation cap") {
		t.Fatalf("expected activation error, got %#v", loaded)
	}
}

func TestLoadSkillUnknownName(t *testing.T) {
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	writeSkill(t, filepath.Join(cfgDir, "skills", "known", "SKILL.md"), "---\ndescription: known\n---\nBody")
	mgr, err := Discover(Options{
		ConfigDir:  cfgDir,
		CacheDir:   cacheDir,
		WorkingDir: t.TempDir(),
		TrustPrompter: func(context.Context, TrustPrompt) (bool, error) {
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	loaded, err := mgr.LoadSkill(context.Background(), "nonexistent", "", nil)
	if err != nil {
		t.Fatalf("expected nil error for unknown skill, got %v", err)
	}
	if !strings.Contains(loaded.ActivationErr, "unknown skill") {
		t.Fatalf("expected activation error for unknown skill, got %#v", loaded)
	}
}
