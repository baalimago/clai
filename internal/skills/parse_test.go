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

func TestParseMarkdownWithFrontmatterSupportsLiteralBlockScalar(t *testing.T) {
	parsed, err := parseMarkdownWithFrontmatter("---\ndescription: |\n  First line.\n  Second line.\n---\nBody")
	if err != nil {
		t.Fatalf("parseMarkdownWithFrontmatter() error = %v", err)
	}
	if got, want := parsed.Metadata.Description, "First line.\nSecond line."; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestParseSkillAcceptsFrontmatterOnlySkill(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "frontmatter-only")
	writeSkill(t, filepath.Join(dir, "SKILL.md"), "---\ndescription: A descriptor-only skill.\n---\n")

	skill, invalid := parseSkill("default", root, dir)
	if invalid != nil {
		t.Fatalf("parseSkill() invalid = %#v", invalid)
	}
	if skill.Parsed.NormalizedBody != "" {
		t.Fatalf("NormalizedBody = %q, want empty", skill.Parsed.NormalizedBody)
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
