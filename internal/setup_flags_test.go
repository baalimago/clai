package internal

import (
	"flag"
	"os"
	"testing"

	"github.com/baalimago/clai/internal/text"
)

// helper function to reset flags between tests
func resetFlags() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
}

func TestSetupFlags(t *testing.T) {
	testCases := []struct {
		name     string
		args     []string
		defaults Configurations
		expected Configurations
	}{
		{
			name: "Default Values",
			args: []string{"cmd"},
			defaults: Configurations{
				ChatModel:    "gpt-4-turbo-preview",
				PhotoModel:   "dall-e-3",
				PhotoPrefix:  "clai",
				PhotoDir:     "picDir",
				StdinReplace: "stdInReplace",
				PrintRaw:     false,
				ReplyMode:    false,
			},
			expected: Configurations{
				ChatModel:    "gpt-4-turbo-preview",
				PhotoModel:   "dall-e-3",
				PhotoPrefix:  "clai",
				PhotoDir:     "picDir",
				StdinReplace: "stdInReplace",
				PrintRaw:     false,
				ReplyMode:    false,
			},
		},
		{
			name:     "Short Flags",
			args:     []string{"cmd", "-cm", "gpt-4", "-pm", "dall-e-2", "-pd", "/tmp", "-pp", "test-", "-I", "[stdin]", "-r", "-re"},
			defaults: Configurations{},
			expected: Configurations{
				ChatModel:    "gpt-4",
				PhotoModel:   "dall-e-2",
				PhotoDir:     "/tmp",
				PhotoPrefix:  "test-",
				StdinReplace: "[stdin]",
				PrintRaw:     true,
				ReplyMode:    true,
			},
		},
		{
			name:     "Long Flags",
			args:     []string{"cmd", "-chat-model", "gpt-4", "-photo-model", "dall-e-2", "-photo-dir", "/tmp", "-photo-prefix", "test-", "-replace", "[stdin]", "-raw", "-reply"},
			defaults: Configurations{},
			expected: Configurations{
				ChatModel:    "gpt-4",
				PhotoModel:   "dall-e-2",
				PhotoDir:     "/tmp",
				PhotoPrefix:  "test-",
				StdinReplace: "[stdin]",
				PrintRaw:     true,
				ReplyMode:    true,
			},
		},
		{
			name: "Precedence",
			args: []string{"cmd", "-cm", "gpt-4-short", "-pm", "dall-e-2-short"},
			defaults: Configurations{
				ChatModel:  "shouldBeReplaced",
				PhotoModel: "shouldBeReplaced",
			},
			expected: Configurations{
				ChatModel:  "gpt-4-short",
				PhotoModel: "dall-e-2-short",
			},
		},
		{
			name: "-i should cause stdin replace",
			args: []string{"cmd", "-i"},
			defaults: Configurations{
				ChatModel:     "gpt-4",
				PhotoModel:    "dall-e-2",
				PhotoDir:      "/tmp",
				PhotoPrefix:   "test-",
				StdinReplace:  "{}",
				PrintRaw:      true,
				ReplyMode:     true,
				ExpectReplace: false,
			},
			expected: Configurations{
				ChatModel:     "gpt-4",
				PhotoModel:    "dall-e-2",
				PhotoDir:      "/tmp",
				PhotoPrefix:   "test-",
				StdinReplace:  "{}",
				PrintRaw:      true,
				ReplyMode:     true,
				ExpectReplace: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resetFlags()
			os.Args = tc.args
			result := setupFlags(tc.defaults)
			if result != tc.expected {
				t.Errorf("Expected %+v, but got %+v", tc.expected, result)
			}
		})
	}
}

func Test_applyFlagOverridesForTest(t *testing.T) {
	testCases := []struct {
		desc         string
		given        text.Configurations
		flagSet      Configurations
		defaultFlags Configurations
		want         text.Configurations
	}{
		{

			desc: "it should set stdinput config if flagged and default is empty",
			given: text.Configurations{
				StdinReplace: "",
			},
			flagSet: Configurations{
				ExpectReplace: true,
				StdinReplace:  "{}",
			},
			// Use real defualtFlags here to check for regressions if defaults change
			defaultFlags: defaultFlags,
			want: text.Configurations{
				StdinReplace: "{}",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			applyFlagOverridesForText(&tc.given, tc.flagSet, tc.defaultFlags)
			failTestIfDiff(t, tc.given.StdinReplace, tc.want.StdinReplace)
		})
	}
}

func failTestIfDiff[C comparable](t *testing.T, got, expected C) {
	t.Helper()
	if got != expected {
		t.Errorf("Expected %v, but got %v", expected, got)
	}
}
