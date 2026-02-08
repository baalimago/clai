package utils

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var ansiEscapeSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleRuneCount(s string) int {
	// Strip common ANSI SGR sequences then count runes. This keeps our width
	// calculations stable when strings already contain ANSI escapes.
	clean := ansiEscapeSeq.ReplaceAllString(s, "")
	return utf8.RuneCountInString(clean)
}

// WidthAppropriateStringTruncColored is like WidthAppropriateStringTrunc but allows
// coloring of the prefix and the truncation infix.
//
// prefixColor and truncColor are raw ANSI sequences (or empty). Colors are disabled
// when NO_COLOR is truthy.
func WidthAppropriateStringTruncColored(toShorten, prefix, prefixColor, truncColor string, padding int) (string, error) {
	toShorten = strings.ReplaceAll(toShorten, "\n", "\\n")
	toShorten = strings.ReplaceAll(toShorten, "\t", "\\t")

	termWidth, err := TermWidth()
	if err != nil {
		return "", fmt.Errorf("get term width: %w", err)
	}

	return fillRemainderOfTermWidthColored(prefix, toShorten, prefixColor, truncColor, termWidth, padding), nil
}

func fillRemainderOfTermWidthColored(prefix, remainder, prefixColor, truncColor string, termWidth, padding int) string {
	infix := " ... "
	infixLen := visibleRuneCount(infix)

	// NOTE: prefix may already contain ANSI sequences (callers might pre-colorize).
	// Do not let these escape sequences count towards the terminal width.
	remainingWidth := termWidth - visibleRuneCount(prefix) - padding
	if remainingWidth < 0 {
		remainingWidth = 0
	}
	widthAdjustedRemainder := ""
	r := []rune(remainder)
	if remainingWidth == 0 {
		widthAdjustedRemainder = ""
	} else if len(r) <= remainingWidth {
		widthAdjustedRemainder = remainder
	} else if remainingWidth <= infixLen {
		widthAdjustedRemainder = string(r[:remainingWidth])
	} else {
		avail := remainingWidth - infixLen
		startLen := avail / 2
		endLen := avail - startLen
		if endLen < 0 {
			endLen = 0
		}
		if startLen < 0 {
			startLen = 0
		}
		if startLen > len(r) {
			startLen = len(r)
		}
		if endLen > len(r)-startLen {
			endLen = len(r) - startLen
		}
		endStart := len(r) - endLen
		if endStart < 0 {
			endStart = 0
		}

		widthAdjustedRemainder = string(r[:startLen]) +
			Colorize(truncColor, infix) +
			string(r[endStart:])
	}

	return Colorize(prefixColor, prefix) + widthAdjustedRemainder
}
