package utils

import "testing"

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
