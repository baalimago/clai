package utils

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"
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
		{
			name:           "Arguments with stdinReplace",
			stdinReplace:   "",
			args:           []string{"cmd", "prefix", "suffix"},
			stdin:          "input from stdin",
			expectedPrompt: "prefix suffix input from stdin",
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

func TestPrompt_FileRedirect(t *testing.T) {
	// Simulate "clai q Here is file: < file" — stdin is a regular file, not a pipe.
	// This is the case ModeNamedPipe misses but ModeCharDevice==0 catches.
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })

	tmp, err := os.CreateTemp("", "clai-prompt-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	content := "file content here"
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	os.Stdin = tmp

	prompt, err := Prompt("", []string{"cmd", "prefix"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// With no stdinReplace token, stdin is appended after args.
	if prompt != "prefix file content here" {
		t.Errorf("Expected 'prefix file content here', got: %q", prompt)
	}
}

func TestPromptFrom_NoArgsIsNoPrompt(t *testing.T) {
	for _, stdin := range []io.Reader{nil, strings.NewReader("piped")} {
		_, err := PromptFrom("", nil, stdin)
		if !errors.Is(err, ErrNoPrompt) {
			t.Fatalf("PromptFrom(no args, stdin %v) error = %v, want ErrNoPrompt", stdin != nil, err)
		}
	}
}

func TestPromptFrom_ReadError(t *testing.T) {
	boom := errors.New("boom")
	_, err := PromptFrom("", []string{"cmd", "x"}, iotest.ErrReader(boom))
	if !errors.Is(err, boom) {
		t.Fatalf("PromptFrom error = %v, want the read error", err)
	}
}

func TestPrompt_StdinStatErrorIsReturned(t *testing.T) {
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = f
	if _, err := Prompt("", []string{"cmd", "x"}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Prompt error = %v, want the stdin stat error", err)
	}
}

func TestPipedStdin(t *testing.T) {
	oldStdin := os.Stdin
	t.Cleanup(func() { os.Stdin = oldStdin })

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devNull.Close() })
	os.Stdin = devNull
	if in, err := PipedStdin(); err != nil || in != nil {
		t.Fatalf("PipedStdin() on a character device = %v, %v; want nil, nil", in, err)
	}

	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	os.Stdin = file
	if in, err := PipedStdin(); err != nil || in != io.Reader(file) {
		t.Fatalf("PipedStdin() on a file = %v, %v; want the file", in, err)
	}
}
