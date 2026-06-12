package skills

import (
	"strings"
	"unicode"
)

func renderSkill(skill Skill, req ActivationRequest) (string, error) {
	var b strings.Builder
	body := skill.Parsed.NormalizedBody
	for i := 0; i < len(body); {
		switch {
		case strings.HasPrefix(body[i:], "${CLAUDE_SKILL_DIR}"):
			b.WriteString(skill.Dir)
			i += len("${CLAUDE_SKILL_DIR}")
		case strings.HasPrefix(body[i:], "$ARGUMENTS"):
			consumed, replacement, ok := parseArgumentsToken(body[i:], req)
			if ok {
				b.WriteString(replacement)
				i += consumed
				continue
			}
			b.WriteByte(body[i])
			i++
		case body[i] == '$':
			consumed, replacement, ok := parseVariableToken(body[i:], skill, req)
			if ok {
				b.WriteString(replacement)
				i += consumed
				continue
			}
			b.WriteByte(body[i])
			i++
		default:
			b.WriteByte(body[i])
			i++
		}
	}
	rendered := b.String()
	if req.RawArgs != "" && !strings.Contains(skill.Parsed.NormalizedBody, "$ARGUMENTS") {
		rendered += "\nARGUMENTS: " + req.RawArgs
	}
	return rendered, nil
}

func parseArgumentsToken(input string, req ActivationRequest) (int, string, bool) {
	if !strings.HasPrefix(input, "$ARGUMENTS") {
		return 0, "", false
	}
	if len(input) > len("$ARGUMENTS") && input[len("$ARGUMENTS")] == '[' {
		end := strings.IndexByte(input, ']')
		if end == -1 {
			return 0, "", false
		}
		idx := atoiDefault(input[len("$ARGUMENTS["):end], -1)
		if idx < 0 || idx >= len(req.Args) {
			return end + 1, "", true
		}
		return end + 1, req.Args[idx], true
	}
	return len("$ARGUMENTS"), req.RawArgs, true
}

func parseVariableToken(input string, skill Skill, req ActivationRequest) (int, string, bool) {
	j := 1
	for j < len(input) && (unicode.IsLetter(rune(input[j])) || unicode.IsDigit(rune(input[j])) || input[j] == '_') {
		j++
	}
	if j == 1 {
		return 0, "", false
	}
	token := input[1:j]
	if isDigits(token) {
		idx := atoiDefault(token, -1)
		if idx < 0 || idx >= len(req.Args) {
			return j, "", true
		}
		return j, req.Args[idx], true
	}
	for idx, name := range skill.Parsed.Metadata.Arguments {
		if name == token {
			if idx >= len(req.Args) {
				return j, "", true
			}
			return j, req.Args[idx], true
		}
	}
	return 0, "", false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
