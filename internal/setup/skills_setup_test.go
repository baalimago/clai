package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/skills"
)

func TestGeneralConfigLoadIncludesSkillsConfig(t *testing.T) {
	dir := t.TempDir()
	category := setupCategory{
		name: "general config",
		load: func(dir string) ([]config, error) {
			cfgs, err := getConfigs(filepath.Join(dir, "*Config.json"), []string{})
			if err != nil {
				return nil, err
			}
			if _, err := skills.LoadConfig(dir); err != nil {
				return nil, err
			}
			cfgs = append(cfgs, config{name: "skills.json", filePath: filepath.Join(dir, "skills.json")})
			return cfgs, nil
		},
	}

	if err := os.WriteFile(filepath.Join(dir, "textConfig.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}
	cfgs, err := category.load(dir)
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	found := false
	for _, cfg := range cfgs {
		if cfg.name == "skills.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected skills.json in %+v", cfgs)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills.json")); err != nil {
		t.Fatalf("expected skills.json to exist: %v", err)
	}
}
