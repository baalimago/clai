package diag

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const redactedMarker = "[REDACTED]"

type redaction struct {
	pattern     *regexp.Regexp
	replacement string
	// hints are lower-case literals one of which every match contains; text
	// without any skips the pattern, so long diagnostics stay cheap.
	hints []string
}

// redactor applies best-effort redaction of known secret shapes. It does not
// guarantee the removal of unknown secrets (F7).
type redactor struct {
	redactions []redaction
}

func newRedactor() redactor {
	return redactor{redactions: []redaction{
		{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} " + redactedMarker, []string{"bearer", "basic"}},
		{regexp.MustCompile(`(?i)((?:x-)?(?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|auth[_-]?token|token|client[_-]?secret|secret|password|passwd)["']?\s*[:=]\s*["']?)[^\s"'&,;]+`), "${1}" + redactedMarker, []string{"key", "token", "secret", "password", "passwd"}},
		{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), redactedMarker, []string{"sk-"}},
		{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), redactedMarker, []string{"akia"}},
		{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), redactedMarker, []string{"gh"}},
		{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{6,}`), redactedMarker, []string{"xox"}},
		{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), redactedMarker, []string{"aiza"}},
		{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`), redactedMarker, []string{"eyj"}},
	}}
}

func (r redactor) text(s string) string {
	lower := strings.ToLower(s)
	for _, redaction := range r.redactions {
		if !slices.ContainsFunc(redaction.hints, func(h string) bool { return strings.Contains(lower, h) }) {
			continue
		}
		s = redaction.pattern.ReplaceAllString(s, redaction.replacement)
		lower = strings.ToLower(s)
	}
	return s
}

func (r redactor) attrs(attrs []slog.Attr) []slog.Attr {
	if len(attrs) == 0 {
		return attrs
	}
	redacted := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		redacted[i] = r.attr(attr)
	}
	return redacted
}

func (r redactor) attr(attr slog.Attr) slog.Attr {
	value := attr.Value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, r.text(value.String()))
	case slog.KindGroup:
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(r.attrs(value.Group())...)}
	case slog.KindAny:
		switch held := value.Any().(type) {
		case error:
			if held != nil {
				return slog.String(attr.Key, r.text(held.Error()))
			}
		case []string:
			redacted := make([]string, len(held))
			for i, s := range held {
				redacted[i] = r.text(s)
			}
			return slog.Any(attr.Key, redacted)
		}
	}
	return attr
}

// Sanitizer prepares text from outside clai, such as a child process's
// output, for the run's streams as the Presenters prepare a diagnostic's.
type Sanitizer struct {
	redactor redactor
}

func NewSanitizer() Sanitizer {
	return Sanitizer{redactor: newRedactor()}
}

// Text is text with the known secret shapes redacted, best effort (F7): the
// form a JSON record carries, whose string encoding escapes the controls.
func (s Sanitizer) Text(text string) string {
	return s.redactor.text(text)
}

// Line is Text(text) with each control or format character, a newline
// included, and each invalid byte as a visible escape: the form a text
// stream carries.
func (s Sanitizer) Line(text string) string {
	return s.redactor.line(text)
}

// line is r.text(s) with every terminal control escaped, a newline included,
// so external text stays on one line.
func (r redactor) line(s string) string {
	return escapeControls(r.text(s))
}

// escapeControls writes each control or format character, and each invalid
// byte, as a visible escape.
func escapeControls(s string) string {
	var escaped strings.Builder
	for i := 0; i < len(s); {
		character, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case character == utf8.RuneError && size == 1:
			escaped.WriteRune(utf8.RuneError)
		case character <= 0xff && unicode.IsControl(character):
			fmt.Fprintf(&escaped, `\x%02x`, character)
		case unicode.IsControl(character) || unicode.Is(unicode.Cf, character):
			fmt.Fprintf(&escaped, `\u%04x`, character)
		default:
			escaped.WriteRune(character)
		}
	}
	return escaped.String()
}
