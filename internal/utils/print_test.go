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
			expectedLineCount: 2,
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
			name:              "It should handle very large blocks of text lines",
			msg:               "One oneone two twotwo threethree. Four four five five six\nSeven",
			line:              "",
			lineCount:         0,
			termWidth:         10,
			expectedLine:      "Seven",
			expectedLineCount: 6,
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
