package utils

import "strings"

func UnescapeEditorString(toEdit string) string {
	unescapedStr := strings.ReplaceAll(toEdit, "\\t", "\t")
	unescapedStr = strings.ReplaceAll(unescapedStr, "\\n", "\n")
	return unescapedStr
}

func EscapeEditorString(edited string) string {
	escapedStr := strings.ReplaceAll(edited, "\t", "\\t")
	escapedStr = strings.ReplaceAll(escapedStr, "\n", "\\n")
	return escapedStr
}
