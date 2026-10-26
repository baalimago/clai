package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if len(cfg.GlobalSkillDirs) != 1 || cfg.GlobalSkillDirs[0] != "~/.agents/skills" {
		t.Fatalf("unexpected global dirs: %#v", cfg.GlobalSkillDirs)
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

// TestLoadConfig_leavesANormalizedFileUntouched pins the per-query cost: a
// config that already carries every default is read, never rewritten.
func TestLoadConfig_leavesANormalizedFileUntouched(t *testing.T) {
	cfgDir := t.TempDir()
	if _, err := LoadConfig(cfgDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	path := filepath.Join(cfgDir, "skills.json")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if _, err := LoadConfig(cfgDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.ModTime().Equal(old) {
		t.Fatalf("skills.json rewritten: mtime %v, want %v", info.ModTime(), old)
	}
}

func TestLoadConfig_fillsMissingDefaultsOnDisk(t *testing.T) {
	cfgDir := t.TempDir()
	path := filepath.Join(cfgDir, "skills.json")
	if err := os.WriteFile(path, []byte(`{"enabled": true}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadConfig(cfgDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(b), `"maxActivatedSkills": 10`) {
		t.Fatalf("skills.json = %s, want the defaults written back", b)
	}
}
