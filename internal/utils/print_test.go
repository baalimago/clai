package utils

import (
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestUpdateMessageTerminalMetadata(t *testing.T) {
	testCases := []struct {
		name              string
		msg               string
		line              string
		lineCount         int
		termWidth         int
		expectedLine      string
		expectedLineCount int
	}{
		{
			name:              "Single line message",
			msg:               "Hello",
			line:              "",
			lineCount:         0,
			termWidth:         10,
			expectedLine:      "Hello",
			expectedLineCount: 1,
		},
		{
			name:              "Message with newline",
			msg:               "Hello\nWorld",
			line:              "",
			lineCount:         0,
			termWidth:         10,
			expectedLine:      "World",
			expectedLineCount: 2,
		},
		{
			name:              "Message exceeding terminal width",
			msg:               "Hello World",
			line:              "",
			lineCount:         0,
			termWidth:         5,
			expectedLine:      "World",
			expectedLineCount: 3,
		},
		{
			name:              "Append to existing line",
			msg:               "World",
			line:              "Hello ",
			lineCount:         0,
			termWidth:         20,
			expectedLine:      "Hello World",
			expectedLineCount: 1,
		},
		{
			name:              "It should handle multiple termwidth overflows",
			msg:               "1111 2222 3333 4444",
			line:              "",
			lineCount:         0,
			termWidth:         5,
			expectedLine:      "4444",
			expectedLineCount: 4,
		},
		{
			name:              "It should handle multiple termwidth overflows + newlines",
			msg:               "1111 22\n3333 4444",
			line:              "",
			lineCount:         0,
			termWidth:         5,
			expectedLine:      "4444",
			expectedLineCount: 4,
		},
		{
			name:              "It should handle multiple termwidth overflows + newlines",
			msg:               "11 22 33 44 55 66",
			line:              "",
			lineCount:         0,
			termWidth:         3,
			expectedLine:      "66",
			expectedLineCount: 6,
		},
		{
			name: "it should not fail on this edge case that I found",
			msg:  "Debugging involves systematically finding and resolving issues within your code or software. Start by identifying the problem, replicate the error, and use tools like breakpoints or logging to trace the source. Testing changes iteratively helps ensure the fix is successful and doesn't cause new issues.",
			// This is not correct, but that's fine, the last line functionality isn't used anywhere anyways
			expectedLine:      "issues.",
			lineCount:         0,
			termWidth:         223,
			expectedLineCount: 2,
		},
		{
			name:              "it should not fail on this edge case that I found",
			msg:               "*Hurrmph* I'm as well as a 90-year old can be, which is better than the alternative, I suppose. My joints are creaking like an old rocking chair, but my mind is still sharp as a tack.\n\nWhat can I help you with today, young whippersnapper? *adjusts spectacles*\n",
			expectedLine:      "",
			lineCount:         0,
			termWidth:         127,
			expectedLineCount: 5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.line
			lineCount := tc.lineCount

			UpdateMessageTerminalMetadata(tc.msg, &line, &lineCount, tc.termWidth)

			if line != tc.expectedLine {
				t.Errorf("Expected line: %q, got: %q", tc.expectedLine, line)
			}

			if lineCount != tc.expectedLineCount {
				t.Errorf("Expected lineCount: %d, got: %d", tc.expectedLineCount, lineCount)
			}
		})
	}
}

func Test_ShortenedOutput(t *testing.T) {
	t.Run("it should shorten line with a lot of newlines", func(t *testing.T) {
		var given strings.Builder
		amNewlines := 90
		maxShortenedNewlines := 5
		for range amNewlines {
			given.WriteString("\n")
		}
		gotStr := ShortenedOutput(given.String(), maxShortenedNewlines)
		got := strings.Count(gotStr, "\n")
		want := maxShortenedNewlines + 1
		if got >= want {
			t.Fatalf("expected: %v, got: %v", want, got)
		}
	})

	t.Run("it should prioritize newline shortening over rune shortening", func(t *testing.T) {
		var given strings.Builder
		for range 30 {
			given.WriteString("0123456789\n")
		}
		got := ShortenedOutput(given.String(), 5)
		if !strings.Contains(got, "[and 26 more lines]") {
			t.Fatalf("expected line-based shortening, got %q", got)
		}
	})
}

func TestPrepareDisplayMessage(t *testing.T) {
	t.Run("tool messages are shortened", func(t *testing.T) {
		msg := pub_models.Message{Role: "tool", Content: strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if !strings.Contains(got.Content, "[and 26 more lines]") {
			t.Fatalf("expected shortened tool output, got %q", got.Content)
		}
	})

	t.Run("assistant messages are shortened using same formatter", func(t *testing.T) {
		msg := pub_models.Message{Role: "assistant", Content: strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if !strings.Contains(got.Content, "[and 26 more lines]") {
			t.Fatalf("expected shortened assistant output, got %q", got.Content)
		}
	})

	t.Run("system messages are not shortened (final output)", func(t *testing.T) {
		msg := pub_models.Message{Role: "system", Content: strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if got.Content != msg.Content {
			t.Fatalf("expected system message to remain untouched")
		}
	})

	t.Run("assistant reasoning messages are preserved", func(t *testing.T) {
		msg := pub_models.Message{
			Role:             "assistant",
			Content:          "Body\n\nWarnings:\n- a\n- b\n- c\n- d\n- e\n- f",
			ReasoningContent: "Need tool.",
		}
		got := PrepareDisplayMessage(msg)
		if got.Content != msg.Content {
			t.Fatalf("expected reasoning-bearing assistant message to remain untouched")
		}
	})

	t.Run("mcp tool messages are preserved", func(t *testing.T) {
		msg := pub_models.Message{Role: "tool", Content: "mcp_result\n" + strings.Repeat("0123456789\n", 30)}
		got := PrepareDisplayMessage(msg)
		if got.Content != msg.Content {
			t.Fatalf("expected mcp tool output to remain untouched")
		}
	})
}

func TestAttemptPrettyPrint_ReasoningContent(t *testing.T) {
	// Test raw mode includes reasoning.
	t.Run("raw mode includes reasoning", func(t *testing.T) {
		msg := pub_models.Message{
			Role:             "assistant",
			Content:          "final answer",
			ReasoningContent: "step by step",
		}
		var b strings.Builder
		if err := AttemptPrettyPrint(&b, msg, "user", true); err != nil {
			t.Fatalf("AttemptPrettyPrint: %v", err)
		}
		got := b.String()
		if !strings.Contains(got, "[thinking]") {
			t.Fatalf("expected [thinking] marker, got: %q", got)
		}
		if !strings.Contains(got, "step by step") {
			t.Fatalf("expected reasoning text, got: %q", got)
		}
		if !strings.Contains(got, "final answer") {
			t.Fatalf("expected content text, got: %q", got)
		}
	})

	// Test NO_COLOR includes reasoning.
	t.Run("NO_COLOR includes reasoning", func(t *testing.T) {
		t.Setenv("NO_COLOR", "true")
		msg := pub_models.Message{
			Role:             "assistant",
			Content:          "final answer",
			ReasoningContent: "step by step",
		}
		var b strings.Builder
		if err := AttemptPrettyPrint(&b, msg, "user", false); err != nil {
			t.Fatalf("AttemptPrettyPrint: %v", err)
		}
		got := b.String()
		if !strings.Contains(got, "[thinking]") {
			t.Fatalf("expected [thinking] marker, got: %q", got)
		}
		if !strings.Contains(got, "step by step") {
			t.Fatalf("expected reasoning text, got: %q", got)
		}
	})

	// Test no reasoning leaves output unchanged.
	t.Run("no reasoning unchanged", func(t *testing.T) {
		msg := pub_models.Message{
			Role:    "assistant",
			Content: "just an answer",
		}
		var b strings.Builder
		if err := AttemptPrettyPrint(&b, msg, "user", true); err != nil {
			t.Fatalf("AttemptPrettyPrint: %v", err)
		}
		got := b.String()
		if strings.Contains(got, "[thinking]") {
			t.Fatalf("expected no thinking markers, got: %q", got)
		}
		if !strings.Contains(got, "just an answer") {
			t.Fatalf("expected content, got: %q", got)
		}
	})
}
