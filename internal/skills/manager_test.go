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
	if !strings.Contains(strings.Join(loaded.Warnings, "\n"), "unavailable tool \"ls\"") {
		t.Fatalf("expected unavailable ls warning, got %#v", loaded.Warnings)
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
