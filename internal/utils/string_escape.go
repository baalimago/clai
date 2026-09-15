package utils

import "strings"

// UnescapeConfigString rehydrates literal "\n" and "\t" sequences into real
// control characters. Config values written by older clai setup editors carry
// those literals instead of real newlines
// (architecture/shell-context.md, "Newline encoding").
func UnescapeConfigString(toEdit string) string {
	unescapedStr := strings.ReplaceAll(toEdit, "\\t", "\t")
	unescapedStr = strings.ReplaceAll(unescapedStr, "\\n", "\n")
	return unescapedStr
}

// RehydrateEscapedConfigString cleans up a value corrupted by an older clai
// setup editor and leaves every other value byte-identical.
//
// The old editor escaped every real newline and tab, so a corrupted value holds
// literal "\n"/"\t" and no real newline or tab character. A value that already
// holds a real newline or tab was written by a human or by a current editor, so
// the guard keeps it. This protects prompts that quote a literal "\n" or "\t"
// as text, which is why RehydrateEscapedConfigString is not used for the shell
// context template (architecture/shell-context.md, "Newline encoding").
func RehydrateEscapedConfigString(s string) string {
	if !strings.Contains(s, `\n`) && !strings.Contains(s, `\t`) {
		return s
	}
	if strings.ContainsAny(s, "\n\t") {
		return s
	}
	return UnescapeConfigString(s)
}
