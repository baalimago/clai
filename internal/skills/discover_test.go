package skills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestDiscoverPrecedenceAndDescriptor(t *testing.T) {
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "repo", "nested")
	mustMkdirAll(t, cwd)
	globalRoot := filepath.Join(t.TempDir(), "global")
	projectRoot := filepath.Join(filepath.Dir(cwd), "agents", "skills")
	defaultRoot := filepath.Join(cfgDir, "skills")

	writeSkill(t, filepath.Join(defaultRoot, "review", "SKILL.md"), "---\ndescription: default review\n---\nDefault body")
	writeSkill(t, filepath.Join(globalRoot, "review", "SKILL.md"), "---\ndescription: global review\n---\nGlobal body")
	writeSkill(t, filepath.Join(projectRoot, "review", "SKILL.md"), "---\ndescription: project review\nallowed-tools: rg,cat\ndisallowed-tools: cat\n---\nProject body")
	writeSkill(t, filepath.Join(projectRoot, "hidden", "SKILL.md"), "---\ndescription: should stay hidden\ndisable-model-invocation: true\n---\nHidden body")
	writeSkill(t, filepath.Join(projectRoot, "broken", "SKILL.md"), "---\ndescription broken\n---\nBody")

	mustWriteSkillsConfig(t, cfgDir, Config{
		GlobalSkillDirs:    []string{globalRoot},
		ProjectSkillDirs:   []string{"./agents/skills"},
		TrustAllSkills:     false,
		MaxActivatedSkills: 10,
	})

	mgr, err := Discover(Options{
		ConfigDir:  cfgDir,
		CacheDir:   cacheDir,
		WorkingDir: cwd,
		TrustPrompter: func(context.Context, TrustPrompt) (bool, error) {
			return true, nil
		},
		LogLevel: LogLevelError,
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if mgr.Summary.Loaded != 2 || mgr.Summary.Shadowed != 2 || mgr.Summary.Invalid != 1 {
		t.Fatalf("unexpected summary: %#v", mgr.Summary)
	}
	if len(mgr.Summary.Invalids) != 1 || !strings.Contains(mgr.Summary.Invalids[0].Err.Error(), "invalid frontmatter line") {
		t.Fatalf("expected invalid reason, got %#v", mgr.Summary.Invalids)
	}
	desc := mgr.DescriptorBlock()
	if !strings.Contains(desc, "<name>review</name>") || strings.Contains(desc, "<name>hidden</name>") {
		t.Fatalf("unexpected descriptor block: %s", desc)
	}
}

func TestDiscover_LogsPostResolutionRootCounts(t *testing.T) {
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "repo", "nested")
	mustMkdirAll(t, cwd)
	globalRoot := filepath.Join(t.TempDir(), "global")
	projectRoot := filepath.Join(filepath.Dir(cwd), "agents", "skills")
	defaultRoot := filepath.Join(cfgDir, "skills")
	writeSkill(t, filepath.Join(defaultRoot, "review", "SKILL.md"), "---\ndescription: default review\n---\nDefault body")
	writeSkill(t, filepath.Join(globalRoot, "review", "SKILL.md"), "---\ndescription: global review\n---\nGlobal body")
	writeSkill(t, filepath.Join(projectRoot, "review", "SKILL.md"), "---\ndescription: project review\n---\nProject body")
	mustWriteSkillsConfig(t, cfgDir, Config{
		Enabled:            true,
		GlobalSkillDirs:    []string{globalRoot},
		ProjectSkillDirs:   []string{"./agents/skills"},
		MaxActivatedSkills: 10,
	})

	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		_, err := Discover(Options{
			ConfigDir:  cfgDir,
			CacheDir:   cacheDir,
			WorkingDir: cwd,
			TrustPrompter: func(context.Context, TrustPrompt) (bool, error) {
				return true, nil
			},
			LogLevel: LogLevelInfo,
		})
		if err != nil {
			t.Fatalf("Discover() error = %v", err)
		}
	})
	if strings.Contains(stdout, "skills default: "+defaultRoot+" [loaded=1]") {
		t.Fatalf("expected shadowed default root to be omitted, got %q", stdout)
	}
	if strings.Contains(stdout, "skills global: "+globalRoot+" [loaded=1]") {
		t.Fatalf("expected shadowed global root to be omitted, got %q", stdout)
	}
	if !strings.Contains(stdout, "skills project: "+projectRoot+" [loaded=1]") {
		t.Fatalf("expected winning project root log, got %q", stdout)
	}
}
