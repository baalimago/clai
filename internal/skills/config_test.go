package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfgDir := t.TempDir()
	cfg, err := LoadConfig(cfgDir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.ProjectSkillDirs) != 2 || cfg.ProjectSkillDirs[0] != "./agents/skills" || cfg.ProjectSkillDirs[1] != ".claude/skills" {
		t.Fatalf("unexpected project dirs: %#v", cfg.ProjectSkillDirs)
	}
	if cfg.MaxActivatedSkills != 10 {
		t.Fatalf("unexpected max activated skills: %d", cfg.MaxActivatedSkills)
	}
	if cfg.Enabled {
		t.Fatalf("expected skills disabled by default")
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "skills.json")); err != nil {
		t.Fatalf("expected skills.json to exist: %v", err)
	}
}
