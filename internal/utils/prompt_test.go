package utils

import (
	"os"
	"testing"
)

func TestPrompt(t *testing.T) {
	testCases := []struct {
		name           string
		stdinReplace   string
		args           []string
		stdin          string
		expectedPrompt string
		expectedError  bool
	}{
		{
			name:           "No arguments and no stdin",
			stdinReplace:   "",
			args:           []string{""},
			stdin:          "",
			expectedPrompt: "",
			expectedError:  true,
		},
		{
			name:           "Arguments only",
			stdinReplace:   "",
			args:           []string{"cmd", "arg1", "arg2"},
			stdin:          "",
			expectedPrompt: "arg1 arg2",
			expectedError:  false,
		},
		{
			name:           "Stdin only",
			stdinReplace:   "",
			args:           []string{"cmd"},
			stdin:          "input from stdin",
			expectedPrompt: "input from stdin",
			expectedError:  false,
		},
		{
			name:           "Arguments and stdin",
			stdinReplace:   "{}",
			args:           []string{"cmd", "arg1", "arg2", "{}"},
			stdin:          "input from stdin",
			expectedPrompt: "arg1 arg2 input from stdin",
			expectedError:  false,
		},
		{
			name:           "Arguments with stdinReplace",
			stdinReplace:   "<stdin>",
			args:           []string{"cmd", "prefix", "<stdin>", "suffix"},
			stdin:          "input from stdin",
			expectedPrompt: "prefix input from stdin suffix",
			expectedError:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stdin != "" {
				// Set up stdin
				oldStdin := os.Stdin
				t.Cleanup(func() { os.Stdin = oldStdin })
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				os.Stdin = r
				_, err = w.WriteString(tc.stdin)
				if err != nil {
					t.Fatal(err)
				}
				w.Close()
			}

			// Call the function
			prompt, err := Prompt(tc.stdinReplace, tc.args)

			// Check the error
			if tc.expectedError && err == nil {
				t.Error("Expected an error, but got nil")
			} else if !tc.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Check the prompt
			if prompt != tc.expectedPrompt {
				t.Errorf("Prompt mismatch. Expected: %q, Got: %q", tc.expectedPrompt, prompt)
			}
		})
	}
}
