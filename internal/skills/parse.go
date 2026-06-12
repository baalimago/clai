package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func parseSkill(class, root, dir string) (Skill, *InvalidSkill) {
	path := filepath.Join(dir, "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, &InvalidSkill{Class: class, Root: root, Dir: dir, Path: path, Err: err}
	}
	parsed, err := parseMarkdownWithFrontmatter(string(b))
	if err != nil {
		return Skill{}, &InvalidSkill{Class: class, Root: root, Dir: dir, Path: path, Err: err, Diagnostics: parsed.Diagnostics}
	}
	if strings.TrimSpace(parsed.NormalizedBody) == "" {
		return Skill{}, &InvalidSkill{Class: class, Root: root, Dir: dir, Path: path, Err: fmt.Errorf("empty skill body"), Diagnostics: parsed.Diagnostics}
	}
	if strings.TrimSpace(parsed.Metadata.Description) == "" {
		return Skill{}, &InvalidSkill{Class: class, Root: root, Dir: dir, Path: path, Err: fmt.Errorf("missing required description"), Diagnostics: parsed.Diagnostics}
	}
	hash := sha256.Sum256(b)
	name := filepath.Base(dir)
	return Skill{
		Name:        name,
		DisplayName: firstNonEmpty(parsed.Metadata.Name, name),
		SourceClass: class,
		SourceRoot:  root,
		Dir:         mustAbsClean(dir),
		Path:        mustAbsClean(path),
		Parsed:      parsed,
		Hash:        hex.EncodeToString(hash[:]),
	}, nil
}

func parseMarkdownWithFrontmatter(content string) (ParsedSkill, error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	parsed := ParsedSkill{
		RawContent: normalized,
		Metadata:   Metadata{Unknown: map[string]string{}},
	}
	if !strings.HasPrefix(normalized, "---\n") {
		parsed.RawBody = normalized
		parsed.NormalizedBody = strings.TrimSpace(normalized)
		return parsed, nil
	}
	lines := strings.Split(normalized, "\n")
	idx := 1
	for idx < len(lines) {
		line := lines[idx]
		if strings.TrimSpace(line) == "---" {
			idx++
			break
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			idx++
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return parsed, fmt.Errorf("invalid frontmatter line %q", line)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "" {
			items, next := parseIndentedList(lines, idx+1)
			if len(items) > 0 {
				assignMetadata(&parsed, key, items, idx+1)
				idx = next
				continue
			}
		}
		assignMetadata(&parsed, key, val, idx+1)
		idx++
	}
	if idx == len(lines) && (len(lines) == 1 || strings.TrimSpace(lines[idx-1]) != "---") {
		return parsed, fmt.Errorf("unterminated frontmatter")
	}
	body := strings.Join(lines[idx:], "\n")
	parsed.RawBody = body
	parsed.NormalizedBody = strings.TrimSpace(body)
	return parsed, nil
}

func parseIndentedList(lines []string, start int) ([]string, int) {
	items := []string{}
	i := start
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}
		trimmedLeft := strings.TrimLeft(line, " 	")
		if !strings.HasPrefix(trimmedLeft, "- ") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmedLeft, "- "))
		item = trimQuotes(item)
		if item != "" {
			items = append(items, item)
		}
		i++
	}
	return items, i
}

func assignMetadata(parsed *ParsedSkill, key string, raw any, line int) {
	switch v := raw.(type) {
	case []string:
		assignListMetadata(parsed, key, v)
	case string:
		assignScalarMetadata(parsed, key, v)
	}
	parsed.Diagnostics = append(parsed.Diagnostics, Diagnostic{Level: "info", Field: key, Line: line})
}

func assignListMetadata(parsed *ParsedSkill, key string, vals []string) {
	meta := &parsed.Metadata
	switch key {
	case "arguments":
		meta.Arguments = append([]string{}, vals...)
	case "allowed-tools":
		meta.AllowedTools = append([]string{}, vals...)
	case "disallowed-tools":
		meta.DisallowedTools = append([]string{}, vals...)
	case "paths":
		meta.Paths = append([]string{}, vals...)
	default:
		meta.Unknown[key] = strings.Join(vals, ",")
	}
}

func assignScalarMetadata(parsed *ParsedSkill, key, val string) {
	meta := &parsed.Metadata
	switch key {
	case "name":
		meta.Name = trimQuotes(val)
	case "description":
		meta.Description = trimQuotes(val)
	case "when_to_use":
		meta.WhenToUse = trimQuotes(val)
	case "argument-hint":
		meta.ArgumentHint = trimQuotes(val)
	case "arguments":
		meta.Arguments = parseInlineList(val)
	case "disable-model-invocation":
		meta.DisableModelInvocation = parseBool(val)
	case "user-invocable":
		meta.UserInvocable = parseBool(val)
	case "allowed-tools":
		meta.AllowedTools = parseInlineList(val)
	case "disallowed-tools":
		meta.DisallowedTools = parseInlineList(val)
	case "model":
		meta.Model = trimQuotes(val)
	case "effort":
		meta.Effort = trimQuotes(val)
	case "context":
		meta.Context = trimQuotes(val)
	case "agent":
		meta.Agent = trimQuotes(val)
	case "paths":
		meta.Paths = parseInlineList(val)
	case "shell":
		meta.Shell = trimQuotes(val)
	default:
		meta.Unknown[key] = trimQuotes(val)
	}
}

func parseInlineList(val string) []string {
	val = strings.TrimSpace(val)
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	ret := make([]string, 0, len(parts))
	for _, part := range parts {
		part = trimQuotes(strings.TrimSpace(part))
		if part != "" {
			ret = append(ret, part)
		}
	}
	return ret
}

func parseBool(val string) bool {
	val = strings.TrimSpace(strings.ToLower(val))
	return val == "true" || val == "yes"
}

func trimQuotes(val string) string {
	return strings.Trim(strings.TrimSpace(val), "\"'")
}

func mustAbsClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
