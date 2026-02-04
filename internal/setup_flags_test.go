package internal

import (
	"flag"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// helper function to reset flags between tests
func resetFlags() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
}

func TestSetupFlags(t *testing.T) {
	testCases := []struct {
		name            string
		args            []string
		defaults        Configurations
		want            Configurations
		wantPostArgs    []string
		wantErrContains string
	}{
		{
			name: "Default Values",
			args: []string{"cmd"},
			defaults: Configurations{
				ChatModel:    "gpt-4-turbo-preview",
				PhotoModel:   "dall-e-3",
				PhotoPrefix:  "clai",
				PhotoDir:     "picDir",
				VideoModel:   "gpt-4o-mini",
				VideoPrefix:  "clai",
				VideoDir:     "vidDir",
				StdinReplace: "stdInReplace",
				PrintRaw:     false,
				ReplyMode:    false,
			},
			want: Configurations{
				ChatModel:    "gpt-4-turbo-preview",
				PhotoModel:   "dall-e-3",
				PhotoPrefix:  "clai",
				PhotoDir:     "picDir",
				VideoModel:   "gpt-4o-mini",
				VideoPrefix:  "clai",
				VideoDir:     "vidDir",
				StdinReplace: "stdInReplace",
				PrintRaw:     false,
				ReplyMode:    false,
			},
		},
		{
			name: "Short Flags",
			args: []string{
				"cmd", "-cm", "gpt-4", "-pm", "dall-e-2",
				"-pd", "/tmp", "-pp", "test-", "-I", "[stdin]",
				"-r", "-re", "-vm", "gpt-4o-mini",
				"-vd", "/videos", "-vp", "vid-",
			},
			defaults: Configurations{},
			want: Configurations{
				ChatModel:    "gpt-4",
				PhotoModel:   "dall-e-2",
				PhotoDir:     "/tmp",
				PhotoPrefix:  "test-",
				VideoModel:   "gpt-4o-mini",
				VideoDir:     "/videos",
				VideoPrefix:  "vid-",
				StdinReplace: "[stdin]",
				PrintRaw:     true,
				ReplyMode:    true,
			},
		},
		{
			name: "Long Flags",
			args: []string{
				"cmd", "-chat-model", "gpt-4",
				"-photo-model", "dall-e-2", "-photo-dir", "/tmp",
				"-photo-prefix", "test-", "-replace", "[stdin]",
				"-raw", "-reply", "-video-model", "gpt-4o-mini",
				"-video-dir", "/videos", "-video-prefix", "vid-",
			},
			defaults: Configurations{},
			want: Configurations{
				ChatModel:    "gpt-4",
				PhotoModel:   "dall-e-2",
				PhotoDir:     "/tmp",
				PhotoPrefix:  "test-",
				VideoModel:   "gpt-4o-mini",
				VideoDir:     "/videos",
				VideoPrefix:  "vid-",
				StdinReplace: "[stdin]",
				PrintRaw:     true,
				ReplyMode:    true,
			},
		},
		{
			name: "Precedence",
			args: []string{
				"cmd", "-cm", "gpt-4-short",
				"-pm", "dall-e-2-short", "-vm", "gpt-4o-mini-short",
			},
			defaults: Configurations{
				ChatModel:  "shouldBeReplaced",
				PhotoModel: "shouldBeReplaced",
				VideoModel: "shouldBeReplaced",
			},
			want: Configurations{
				ChatModel:  "gpt-4-short",
				PhotoModel: "dall-e-2-short",
				VideoModel: "gpt-4o-mini-short",
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
				VideoModel:    "gpt-4o-mini",
				VideoDir:      "/videos",
				VideoPrefix:   "vid-",
				StdinReplace:  "{}",
				PrintRaw:      true,
				ReplyMode:     true,
				ExpectReplace: false,
			},
			want: Configurations{
				ChatModel:     "gpt-4",
				PhotoModel:    "dall-e-2",
				PhotoDir:      "/tmp",
				PhotoPrefix:   "test-",
				VideoModel:    "gpt-4o-mini",
				VideoDir:      "/videos",
				VideoPrefix:   "vid-",
				StdinReplace:  "{}",
				PrintRaw:      true,
				ReplyMode:     true,
				ExpectReplace: true,
			},
		},
		{
			name:     "Profile path",
			args:     []string{"cmd", "-profile-path", "/tmp/p.json"},
			defaults: Configurations{},
			want: Configurations{
				ProfilePath: "/tmp/p.json",
			},
		},
		{
			name:     "Tools explicit all",
			args:     []string{"cmd", "-t=*"},
			defaults: Configurations{},
			want: Configurations{
				UseTools: "*",
			},
		},
		{
			name:     "Tools flag with comma-separated list => specific tools",
			args:     []string{"cmd", "-t=write_file,rg"},
			defaults: Configurations{},
			want: Configurations{
				UseTools: "write_file,rg",
			},
		},
		{
			name:     "Pass along only args after parsing",
			args:     []string{"cmd", "-cm", "test", "q", "hello"},
			defaults: Configurations{},
			want: Configurations{
				ChatModel: "test",
			},
			wantPostArgs: []string{"q", "hello"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resetFlags()
			os.Args = tc.args

			// parseFlags expects args WITHOUT argv[0].
			var parseArgs []string
			if len(tc.args) > 0 {
				parseArgs = tc.args[1:]
			}

			got, gotPostParseArgs, err := parseFlags(tc.defaults, parseArgs)
			testboil.FailTestIfDiff(t, debug.IndentedJsonFmt(got), debug.IndentedJsonFmt(tc.want))
			if tc.wantPostArgs != nil && !slices.Equal(tc.wantPostArgs, gotPostParseArgs) {
				t.Fatalf("post parse args doesnt match. Wanted: '%+v', got: '%+v'", tc.wantPostArgs, gotPostParseArgs)
			}
			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("expected err: '%v', to contain: '%v'", err, tc.wantErrContains)
				}
			}
			if tc.wantErrContains == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
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
			desc: "it should set stdinput config if flagged and " +
				"default is empty",
			given: text.Configurations{
				StdinReplace: "",
			},
			flagSet: Configurations{
				ExpectReplace: true,
				StdinReplace:  "{}",
			},
			// Use real defualtFlags here to check for regressions
			// if defaults change
			defaultFlags: defaultFlags,
			want: text.Configurations{
				StdinReplace: "{}",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			applyFlagOverridesForText(&tc.given, tc.flagSet,
				tc.defaultFlags)
			testboil.FailTestIfDiff(t, tc.given.StdinReplace,
				tc.want.StdinReplace)
		})
	}
}
