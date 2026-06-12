package skills

import (
	"os"
	"path/filepath"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type staticTool struct{ name string }

func (s staticTool) Call(pub_models.Input) (string, error) { return "", nil }
func (s staticTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: s.name}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
}

func writeSkill(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func mustWriteSkillsConfig(t *testing.T, cfgDir string, cfg Config) {
	t.Helper()
	if err := writeJSONFile(filepath.Join(cfgDir, "skills.json"), cfg); err != nil {
		t.Fatalf("writeJSONFile(skills.json): %v", err)
	}
}
