package skills

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestParseMarkdownWithFrontmatter_AllScalarKeys walks every metadata key the
// scalar branch knows about, so a new key that silently falls into Unknown is
// caught.
func TestParseMarkdownWithFrontmatter_AllScalarKeys(t *testing.T) {
	content := strings.Join([]string{
		"---",
		"name: my-skill",
		"description: d",
		"when_to_use: now",
		"argument-hint: hint",
		"arguments: [a, b]",
		"disable-model-invocation: true",
		"user-invocable: yes",
		"allowed-tools: [rg, cat]",
		"disallowed-tools: [rm]",
		"model: sonnet",
		"effort: high",
		"context: ctx",
		"agent: agent-a",
		"paths: [x, y]",
		"shell: bash",
		"mystery: value",
		"---",
		"Body",
	}, "\n")

	parsed, err := parseMarkdownWithFrontmatter(content)
	if err != nil {
		t.Fatalf("parseMarkdownWithFrontmatter() error = %v", err)
	}
	meta := parsed.Metadata
	if meta.WhenToUse != "now" || meta.ArgumentHint != "hint" {
		t.Fatalf("unexpected when_to_use/argument-hint: %#v", meta)
	}
	if !meta.DisableModelInvocation || !meta.UserInvocable {
		t.Fatalf("unexpected bool metadata: %#v", meta)
	}
	if meta.Model != "sonnet" || meta.Effort != "high" || meta.Context != "ctx" || meta.Agent != "agent-a" || meta.Shell != "bash" {
		t.Fatalf("unexpected scalar metadata: %#v", meta)
	}
	if got := strings.Join(meta.DisallowedTools, ","); got != "rm" {
		t.Fatalf("disallowed-tools = %q", got)
	}
	if got := strings.Join(meta.Paths, ","); got != "x,y" {
		t.Fatalf("paths = %q", got)
	}
	if meta.Unknown["mystery"] != "value" {
		t.Fatalf("unknown key not captured: %#v", meta.Unknown)
	}
}

func TestAssignListMetadata_Keys(t *testing.T) {
	var parsed ParsedSkill
	parsed.Metadata.Unknown = map[string]string{}
	assignMetadata(&parsed, "disallowed-tools", []string{"rm", "ls"}, 1)
	assignMetadata(&parsed, "paths", []string{"a/b"}, 2)
	assignMetadata(&parsed, "custom-list", []string{"x", "y"}, 3)

	if got := strings.Join(parsed.Metadata.DisallowedTools, ","); got != "rm,ls" {
		t.Fatalf("disallowed-tools = %q", got)
	}
	if got := strings.Join(parsed.Metadata.Paths, ","); got != "a/b" {
		t.Fatalf("paths = %q", got)
	}
	if parsed.Metadata.Unknown["custom-list"] != "x,y" {
		t.Fatalf("unknown list key = %q", parsed.Metadata.Unknown["custom-list"])
	}
	if len(parsed.Diagnostics) != 3 {
		t.Fatalf("expected one diagnostic per assignment, got %#v", parsed.Diagnostics)
	}
}

func TestParseMarkdownWithFrontmatter_NoFrontmatter(t *testing.T) {
	parsed, err := parseMarkdownWithFrontmatter("just a body\r\nwith CRLF\n")
	if err != nil {
		t.Fatalf("parseMarkdownWithFrontmatter() error = %v", err)
	}
	if parsed.RawBody != "just a body\nwith CRLF\n" {
		t.Fatalf("RawBody = %q", parsed.RawBody)
	}
	if parsed.NormalizedBody != "just a body\nwith CRLF" {
		t.Fatalf("NormalizedBody = %q", parsed.NormalizedBody)
	}
}

func TestParseMarkdownWithFrontmatter_SkipsBlankAndCommentLines(t *testing.T) {
	parsed, err := parseMarkdownWithFrontmatter("---\n\n# a comment\ndescription: d\n---\nBody")
	if err != nil {
		t.Fatalf("parseMarkdownWithFrontmatter() error = %v", err)
	}
	if parsed.Metadata.Description != "d" {
		t.Fatalf("description = %q", parsed.Metadata.Description)
	}
}

func TestParseMarkdownWithFrontmatter_Unterminated(t *testing.T) {
	_, err := parseMarkdownWithFrontmatter("---\ndescription: d\n")
	if err == nil || !strings.Contains(err.Error(), "unterminated frontmatter") {
		t.Fatalf("err = %v, want unterminated frontmatter", err)
	}
}

func TestParseBlockScalar_FoldedStyle(t *testing.T) {
	got, next := parseBlockScalar([]string{"  first", "  second", "---"}, 0, ">")
	if got != "first second" {
		t.Fatalf("folded block = %q", got)
	}
	if next != 2 {
		t.Fatalf("next = %d, want 2", next)
	}
}

func TestParseIndentedList_SkipsBlankLines(t *testing.T) {
	items, next := parseIndentedList([]string{"", "- a", "  - b", "stop"}, 0)
	if len(items) != 2 || items[0] != "a" || items[1] != "b" {
		t.Fatalf("items = %#v", items)
	}
	if next != 3 {
		t.Fatalf("next = %d, want 3", next)
	}
}

func TestParseSkill_MissingDescription(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "no-description")
	writeSkill(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\nBody")

	_, invalid := parseSkill("default", root, dir)
	if invalid == nil || !strings.Contains(invalid.Err.Error(), "missing required description") {
		t.Fatalf("expected a missing-description error, got %#v", invalid)
	}
}

func TestSmallHelpers(t *testing.T) {
	if got := parseInlineList("[]"); got != nil {
		t.Fatalf("parseInlineList([]) = %#v, want nil", got)
	}
	if got := parseInlineList(" [ a , , b ] "); strings.Join(got, ",") != "a,b" {
		t.Fatalf("parseInlineList = %#v", got)
	}
	if got := firstNonEmpty("", "   "); got != "" {
		t.Fatalf("firstNonEmpty = %q, want empty", got)
	}
	if got := firstNonEmpty("", "second"); got != "second" {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if cmpString("a", "b") != -1 || cmpString("b", "a") != 1 || cmpString("a", "a") != 0 {
		t.Fatal("cmpString ordering is wrong")
	}
	if got := atoiDefault("nope", 7); got != 7 {
		t.Fatalf("atoiDefault = %d, want the default", got)
	}
	if got := atoiDefault("7", 0); got != 7 {
		t.Fatalf("atoiDefault = %d", got)
	}
}
