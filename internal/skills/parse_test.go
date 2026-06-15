package skills

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMarkdownWithFrontmatterSupportsIndentedLists(t *testing.T) {
	parsed, err := parseMarkdownWithFrontmatter("---\ndescription: review\narguments:\n  - target\n  - extra\nallowed-tools:\n  - rg\npaths: [a, b]\n---\nBody")
	if err != nil {
		t.Fatalf("parseMarkdownWithFrontmatter() error = %v", err)
	}
	if parsed.Metadata.Arguments[0] != "target" || parsed.Metadata.Arguments[1] != "extra" {
		t.Fatalf("unexpected arguments: %#v", parsed.Metadata.Arguments)
	}
	if parsed.Metadata.AllowedTools[0] != "rg" || parsed.Metadata.Paths[1] != "b" {
		t.Fatalf("unexpected metadata: %#v", parsed.Metadata)
	}
	if parsed.NormalizedBody != "Body" {
		t.Fatalf("unexpected normalized body: %q", parsed.NormalizedBody)
	}
}

func TestParseSkillPreservesInvalidReason(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "broken")
	writeSkill(t, filepath.Join(dir, "SKILL.md"), "---\ndescription broken\n---\nBody")
	_, invalid := parseSkill("default", root, dir)
	if invalid == nil || !strings.Contains(invalid.Err.Error(), "invalid frontmatter line") {
		t.Fatalf("expected invalid reason, got %#v", invalid)
	}
}
