package skills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscover_profileSkillsWinAndNeedNoSecondTrust(t *testing.T) {
	cfgDir := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "repo")
	mustMkdirAll(t, cwd)
	profileRoot := filepath.Join(t.TempDir(), "abc")
	writeSkill(t, filepath.Join(cwd, "agents", "skills", "review", "SKILL.md"), "---\ndescription: project review\n---\nProject body")
	writeSkill(t, filepath.Join(profileRoot, "review", "SKILL.md"), "---\ndescription: the agent's review\n---\nAgent body")
	mustWriteSkillsConfig(t, cfgDir, Config{ProjectSkillDirs: []string{"./agents/skills"}})

	asked := 0
	mgr, err := Discover(Options{
		ConfigDir:    cfgDir,
		CacheDir:     t.TempDir(),
		WorkingDir:   cwd,
		ProfileRoots: []string{profileRoot},
		TrustPrompter: func(context.Context, TrustPrompt) (bool, error) {
			asked++
			return false, nil
		},
		LogLevel: LogLevelError,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	review := mgr.Skills["review"]
	if review.SourceClass != SourceProfile || !strings.Contains(review.Parsed.NormalizedBody, "Agent body") {
		t.Fatalf("review = %s from %s, want the profile's", review.Parsed.NormalizedBody, review.SourceClass)
	}
	loaded, err := mgr.LoadSkill(t.Context(), "review", "", nil)
	if err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}
	if asked != 0 || !strings.Contains(loaded.RenderedBody, "Agent body") {
		t.Fatalf("asked %d times, body %q: a profile skill was trusted with its profile", asked, loaded.RenderedBody)
	}
}

func TestCheckSkill(t *testing.T) {
	meta, err := CheckSkill([]byte("---\nname: pdf\ndescription: Read PDFs.\n---\nBody"))
	if err != nil || meta.Name != "pdf" || meta.Description != "Read PDFs." {
		t.Fatalf("CheckSkill = %+v, %v", meta, err)
	}
	for _, bad := range []string{"no frontmatter, no description", "---\nname: x\n---\nBody", "---\nbroken\n---\n"} {
		if _, err := CheckSkill([]byte(bad)); err == nil {
			t.Fatalf("CheckSkill(%q): want an error", bad)
		}
	}
}
