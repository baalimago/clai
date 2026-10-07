package utils

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortenedOutput_TruncatesOnRuneBoundary(t *testing.T) {
	content := strings.Repeat("界", 101)

	got := ShortenedOutput(content, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("ShortenedOutput returned invalid UTF-8: %q", got)
	}
	if want := strings.Repeat("界", 100) + "\n...[and 1 more runes]"; got != want {
		t.Fatalf("ShortenedOutput() = %q, want %q", got, want)
	}
}

func TestTerminalRows_WrapsByDisplayCells(t *testing.T) {
	got := terminalRows("A界e\u0301🙂B", 4)
	want := []string{"A界e\u0301", "🙂B"}
	if len(got) != len(want) {
		t.Fatalf("terminalRows() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("terminalRows()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPrintMcpLogLine_AuthPromptWrapsWithoutLosingURL(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	line := "Please authorize at https://example.test/device?code=ABC123"
	var out bytes.Buffer
	if err := PrintMcpLogLine(&out, line, 16); err != nil {
		t.Fatalf("PrintMcpLogLine: %v", err)
	}

	rows := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	var joined strings.Builder
	for _, row := range rows {
		if !strings.HasPrefix(row, "  ") {
			t.Errorf("row lacks body indentation: %q", row)
		}
		joined.WriteString(strings.TrimPrefix(row, "  "))
		if utf8.RuneCountInString(row) > 16 {
			t.Errorf("row exceeds terminal width: %q", row)
		}
	}
	if got, want := joined.String(), "» "+line; got != want {
		t.Fatalf("wrapped auth text = %q, want %q", got, want)
	}
}

func TestIsMcpLogAuthChallengeLine_DistinguishesChallengeFromStatus(t *testing.T) {
	for _, line := range []string{
		"Please sign in to continue",
		"https://example.test/oauth/authorize",
	} {
		if !IsMcpLogAuthChallengeLine(line) {
			t.Errorf("IsMcpLogAuthChallengeLine(%q) = false, want true", line)
		}
	}
	for _, line := range []string{
		"HTTP 401",
		"HTTP 403",
		"OAuth coordination initialized",
		"server started",
	} {
		if IsMcpLogAuthChallengeLine(line) {
			t.Errorf("IsMcpLogAuthChallengeLine(%q) = true, want false", line)
		}
	}
}
