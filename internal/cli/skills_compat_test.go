package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const skillsLoadedNotice = "skills: loaded=1 shadowed=0 invalid=0"

// TestSkillsDiagnosticCompatibility proves phase 10 criterion 2: skills
// discovery events follow the legacy and the new level controls as they did
// in phase 9, with the skills logger and its level parser removed.
func TestSkillsDiagnosticCompatibility(t *testing.T) {
	rows := []struct {
		name  string
		env   map[string]string
		args  string
		shown bool
	}{
		{"no controls", nil, "-r -cm test -s * q hello", false},
		{"DEBUG", map[string]string{"DEBUG": "1"}, "-r -cm test -s * q hello", true},
		{"DEBUG_SKILLS", map[string]string{"DEBUG_SKILLS": "1"}, "-r -cm test -s * q hello", true},
		{"LOG_LEVEL_SKILLS info", map[string]string{"DEBUG_SKILLS": "1", "LOG_LEVEL_SKILLS": "info"}, "-r -cm test -s * q hello", true},
		{"LOG_LEVEL_SKILLS warn", map[string]string{"DEBUG_SKILLS": "1", "LOG_LEVEL_SKILLS": "warn"}, "-r -cm test -s * q hello", false},
		{"LOG_LEVEL_SKILLS error", map[string]string{"DEBUG_SKILLS": "1", "LOG_LEVEL_SKILLS": "error"}, "-r -cm test -s * q hello", false},
		{"LOG_LEVEL skills selector hides", map[string]string{"DEBUG_SKILLS": "1", "LOG_LEVEL": "info,skills=error"}, "-r -cm test -s * q hello", false},
		{"LOG_LEVEL skills selector shows", map[string]string{"DEBUG_SKILLS": "1", "LOG_LEVEL": "warn,skills=info"}, "-r -cm test -s * q hello", true},
		{"LOG_LEVEL default keeps the DEBUG_SKILLS selector", map[string]string{"DEBUG_SKILLS": "1", "LOG_LEVEL": "warn"}, "-r -cm test -s * q hello", true},
		{"structured output", map[string]string{"DEBUG_SKILLS": "1"}, "-r -cm test -s * -rf {format} q hello", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			useConfigDir(t)
			for key, value := range row.env {
				t.Setenv(key, value)
			}
			confDir := os.Getenv("CLAI_CONFIG_DIR")
			skill := filepath.Join(confDir, "skills", "review", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
				t.Fatalf("create skill dir: %v", err)
			}
			if err := os.WriteFile(skill, []byte("---\ndescription: review\n---\nBody"), 0o644); err != nil {
				t.Fatalf("write skill: %v", err)
			}
			format := filepath.Join(confDir, "format.json")
			if err := os.WriteFile(format, []byte(`{"type":"json_schema","json_schema":{"name":"r","schema":{"type":"object"}}}`), 0o644); err != nil {
				t.Fatalf("write response format: %v", err)
			}
			got := runCLI(t, strings.ReplaceAll(row.args, "{format}", format), cliOpts{})
			if got.status != 0 {
				t.Fatalf("got %+v, want exit 0", got)
			}
			assertNoDiagnosticsIn(t, got.stdout)
			if shown := strings.Contains(got.stderr, skillsLoadedNotice); shown != row.shown {
				t.Fatalf("notice shown = %v, want %v; stderr %q", shown, row.shown, got.stderr)
			}
		})
	}
}
