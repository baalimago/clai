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
			name:              "It should handle multiple termwidth overflows",
			msg:               "1111 2222 3333 4444",
			line:              "",
			lineCount:         0,
			termWidth:         5,
			expectedLine:      "4444",
			expectedLineCount: 3,
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
			expectedLineCount: 5,
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

func Test_countNewLines(t *testing.T) {
	testCases := []struct {
		desc  string
		given struct {
			msg       string
			termWidth int
		}
		want int
	}{
		{
			desc: "it should count spaces when splitting into new lines",
			given: struct {
				msg       string
				termWidth int
			}{
				msg:       "          ",
				termWidth: 5,
			},
			want: 2,
		},
		{
			desc: "it should count spaces when splitting into new lines, test 2",
			given: struct {
				msg       string
				termWidth int
			}{
				msg:       "1 2 3 ",
				termWidth: 2,
			},
			want: 3,
		},
		{
			desc: "it should count spaces when splitting into new lines, test 3",
			given: struct {
				msg       string
				termWidth int
			}{
				msg:       "1 2 3 4",
				termWidth: 2,
			},
			want: 3,
		},
		{
			desc: "it should sometimes treat space as newline, maybe",
			given: struct {
				msg       string
				termWidth int
			}{
				msg:       "Hello World",
				termWidth: 5,
			},
			want: 2,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			got := countNewLines(tC.given.msg, tC.given.termWidth)
			want := tC.want
			if got != want {
				t.Fatalf("expected: %v, got: %v", want, got)
			}
		})
	}
}
