package utils

import (
	"regexp"
	"testing"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleWidth(s string) int {
	stripped := ansiPattern.ReplaceAllString(s, "")
	return len([]rune(stripped))
}

func Test_fillRemainderOfTermWidthColored_PrefixWithANSICodesDoesNotAffectWidth(t *testing.T) {
	termWidth := 10
	padding := 0

	// Simulate being passed an already-colored prefix (this happens when callers
	// pre-colorize or compose prefixes).
	prefix := "\x1b[31mPr\x1b[0m" // visible width 2
	remainder := "abcdefghijklmnopqrstuvwxyz"

	out := fillRemainderOfTermWidthColored(prefix, remainder, "", "\x1b[32m", termWidth, padding)

	if got := visibleWidth(out); got != termWidth {
		t.Fatalf("expected visible width %d, got %d (out=%q)", termWidth, got, out)
	}

	if !regexp.MustCompile(`^\x1b\[31mPr\x1b\[0m`).MatchString(out) {
		t.Fatalf("expected colored prefix at beginning, got %q", out)
	}
	if !regexp.MustCompile(`\x1b\[32m \.\.\. \x1b\[0m`).MatchString(out) {
		t.Fatalf("expected colored truncation infix, got %q", out)
	}
}
