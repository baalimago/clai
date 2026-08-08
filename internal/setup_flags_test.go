package internal

import (
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

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
			name:     "Skills explicit all",
			args:     []string{"cmd", "-s=*"},
			defaults: Configurations{},
			want: Configurations{
				UseSkills: "*",
			},
		},
		{
			name:     "Skills explicit none",
			args:     []string{"cmd", "-skills=none"},
			defaults: Configurations{},
			want: Configurations{
				UseSkills: "none",
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
			name:     "Cmd ban flag parses comma-separated entries",
			args:     []string{"cmd", "-cmd-ban=rm,sudo"},
			defaults: Configurations{},
			want: Configurations{
				CmdBan: "rm,sudo",
			},
		},
		{
			name:     "Cmd ban flag with stray spaces is kept raw for later trimming",
			args:     []string{"cmd", "-cmd-ban=rm, sudo"},
			defaults: Configurations{},
			want: Configurations{
				CmdBan: "rm, sudo",
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
		{
			name:     "Response format path short flag",
			args:     []string{"cmd", "-rf", "/tmp/rf.json"},
			defaults: Configurations{},
			want: Configurations{
				ResponseFormatPath: "/tmp/rf.json",
			},
		},
		{
			name:     "Response format path long flag",
			args:     []string{"cmd", "-response-format", "/tmp/rf.json"},
			defaults: Configurations{},
			want: Configurations{
				ResponseFormatPath: "/tmp/rf.json",
			},
		},
		{
			name:     "Lookback short flag enables and marks explicit",
			args:     []string{"cmd", "-lb"},
			defaults: Configurations{},
			want: Configurations{
				UseLookback:    true,
				UseLookbackSet: true,
			},
		},
		{
			name:     "Lookback long flag enables and marks explicit",
			args:     []string{"cmd", "-lookback"},
			defaults: Configurations{},
			want: Configurations{
				UseLookback:    true,
				UseLookbackSet: true,
			},
		},
		{
			name:     "Lookback explicit disable is marked explicit so it can override profile",
			args:     []string{"cmd", "-lb=false"},
			defaults: Configurations{},
			want: Configurations{
				UseLookback:    false,
				UseLookbackSet: true,
			},
		},
		{
			name:     "Max tokens short flag",
			args:     []string{"cmd", "-mt", "5000"},
			defaults: Configurations{},
			want: Configurations{
				MaxTokens:    5000,
				MaxTokensSet: true,
			},
		},
		{
			name:     "Max tokens long flag",
			args:     []string{"cmd", "-max-tokens", "5000"},
			defaults: Configurations{},
			want: Configurations{
				MaxTokens:    5000,
				MaxTokensSet: true,
			},
		},
		{
			name:     "Max tokens explicit zero is detected as set",
			args:     []string{"cmd", "-mt=0"},
			defaults: Configurations{},
			want: Configurations{
				MaxTokens:    0,
				MaxTokensSet: true,
			},
		},
		{
			name:     "Max tokens both aliases equal",
			args:     []string{"cmd", "-mt=100", "-max-tokens=100"},
			defaults: Configurations{},
			want: Configurations{
				MaxTokens:    100,
				MaxTokensSet: true,
			},
		},
		{
			name:     "Max tokens both aliases equal explicit zero",
			args:     []string{"cmd", "-mt=0", "-max-tokens=0"},
			defaults: Configurations{},
			want: Configurations{
				MaxTokens:    0,
				MaxTokensSet: true,
			},
		},
		{
			name:            "Max tokens conflicting aliases rejected",
			args:            []string{"cmd", "-mt=0", "-max-tokens=5"},
			defaults:        Configurations{},
			wantErrContains: "mutually exclusive",
		},
		{
			name:            "Max tokens non-integer value rejected",
			args:            []string{"cmd", "-max-tokens=abc"},
			defaults:        Configurations{},
			wantErrContains: "invalid value",
		},
		{
			name:     "Max tool calls short flag",
			args:     []string{"cmd", "-mtc", "2"},
			defaults: Configurations{},
			want: Configurations{
				MaxToolCalls:    2,
				MaxToolCallsSet: true,
			},
		},
		{
			name:     "Max tool calls long flag",
			args:     []string{"cmd", "-max-tool-calls", "2"},
			defaults: Configurations{},
			want: Configurations{
				MaxToolCalls:    2,
				MaxToolCallsSet: true,
			},
		},
		{
			name:     "Max tool calls explicit zero is detected as set",
			args:     []string{"cmd", "-max-tool-calls=0"},
			defaults: Configurations{},
			want: Configurations{
				MaxToolCalls:    0,
				MaxToolCallsSet: true,
			},
		},
		{
			name:     "Max tool calls both aliases equal",
			args:     []string{"cmd", "-mtc=2", "-max-tool-calls=2"},
			defaults: Configurations{},
			want: Configurations{
				MaxToolCalls:    2,
				MaxToolCallsSet: true,
			},
		},
		{
			name:     "Max tool calls both aliases equal explicit zero",
			args:     []string{"cmd", "-mtc=0", "-max-tool-calls=0"},
			defaults: Configurations{},
			want: Configurations{
				MaxToolCalls:    0,
				MaxToolCallsSet: true,
			},
		},
		{
			name:            "Max tool calls conflicting aliases rejected",
			args:            []string{"cmd", "-mtc=1", "-max-tool-calls=2"},
			defaults:        Configurations{},
			wantErrContains: "mutually exclusive",
		},
		{
			name:     "No stoploss flags leaves both limits unset",
			args:     []string{"cmd", "q", "hello"},
			defaults: Configurations{},
			want: Configurations{
				ChatModel: "",
			},
			wantPostArgs: []string{"q", "hello"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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

// Test_setupLookback_ExplicitDisableOverridesProfile pins the documented
// precedence CLI > profile in the disabling direction: -lb=false must win
// over profile/file-enabled lookback.
func Test_setupLookback_ExplicitDisableOverridesProfile(t *testing.T) {
	tConf := text.Configurations{UseLookback: true}
	flagSet := Configurations{UseLookback: false, UseLookbackSet: true}
	if err := setupLookback(t.TempDir(), &tConf, flagSet); err != nil {
		t.Fatalf("setupLookback: %v", err)
	}
	if tConf.UseLookback {
		t.Fatal("expected explicit -lb=false to disable profile-enabled lookback")
	}
	if tConf.LookbackCWD == "" {
		t.Fatal("expected LookbackCWD to be captured even when lookback is disabled")
	}
}

// captureStdout redirects stdout for the duration of fn and returns what was
// written, so notice gating can be asserted without touching the process output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w
	readDone := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		readDone <- string(b)
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = orig
	return <-readDone
}

// Test_setupLookback_NoticeDebugGated pins the debug gate on the lookback setup
// notices: they print only when DEBUG_LOOKBACK (or plain DEBUG) is truthy, and
// never in structured-response mode. This covers the enabled-but-no-history
// branch; the with-history branch is covered by
// Test_e2e_dirscope_lookback_notice_debug_gated.
func Test_setupLookback_NoticeDebugGated(t *testing.T) {
	t.Run("silent without debug flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_LOOKBACK", "")
		out := captureStdout(t, func() {
			tConf := text.Configurations{UseLookback: true}
			flagSet := Configurations{UseLookbackSet: true, UseLookback: true}
			if err := setupLookback(t.TempDir(), &tConf, flagSet); err != nil {
				t.Fatalf("setupLookback: %v", err)
			}
		})
		if strings.Contains(out, "lookback:") {
			t.Fatalf("expected no lookback notice without DEBUG_LOOKBACK, got %q", out)
		}
	})
	t.Run("prints when any debug flag is set", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			env  map[string]string
		}{
			{name: "DEBUG_LOOKBACK", env: map[string]string{"DEBUG": "", "DEBUG_LOOKBACK": "1"}},
			{name: "plain DEBUG", env: map[string]string{"DEBUG": "1", "DEBUG_LOOKBACK": ""}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				for k, v := range tt.env {
					t.Setenv(k, v)
				}
				out := captureStdout(t, func() {
					tConf := text.Configurations{UseLookback: true}
					flagSet := Configurations{UseLookbackSet: true, UseLookback: true}
					if err := setupLookback(t.TempDir(), &tConf, flagSet); err != nil {
						t.Fatalf("setupLookback: %v", err)
					}
				})
				if !strings.Contains(out, "lookback: enabled (no recorded history") {
					t.Fatalf("expected no-history lookback notice with %s, got %q", tt.name, out)
				}
			})
		}
	})
	t.Run("silent in structured-response mode", func(t *testing.T) {
		t.Setenv("DEBUG", "1")
		t.Setenv("DEBUG_LOOKBACK", "1")
		out := captureStdout(t, func() {
			tConf := text.Configurations{UseLookback: true, ResponseFormat: &pub_models.ResponseFormat{}}
			flagSet := Configurations{UseLookbackSet: true, UseLookback: true}
			if err := setupLookback(t.TempDir(), &tConf, flagSet); err != nil {
				t.Fatalf("setupLookback: %v", err)
			}
		})
		if strings.Contains(out, "lookback:") {
			t.Fatalf("expected no lookback notice in structured-response mode, got %q", out)
		}
	})
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

// Test_resolveIntAlias pins the four-state short/long alias resolver for the
// int limit flags: neither set, exactly one set, both set and equal, both set
// and conflicting. Explicit zero and explicit values equal to the default must
// still resolve as set (R5-02).
func Test_resolveIntAlias(t *testing.T) {
	testCases := []struct {
		name       string
		short      int
		long       int
		defaultVal int
		shortSet   bool
		longSet    bool
		wantValue  int
		wantSet    bool
		wantErr    bool
	}{
		{
			name:       "neither alias set uses default and stays unset",
			long:       7,
			defaultVal: 3,
			wantValue:  3,
			wantSet:    false,
		},
		{
			name:      "short alias only",
			short:     5,
			shortSet:  true,
			wantValue: 5,
			wantSet:   true,
		},
		{
			name:      "long alias only",
			long:      6,
			longSet:   true,
			wantValue: 6,
			wantSet:   true,
		},
		{
			name:      "short alias explicit zero",
			short:     0,
			shortSet:  true,
			wantValue: 0,
			wantSet:   true,
		},
		{
			name:      "long alias explicit zero",
			long:      0,
			longSet:   true,
			wantValue: 0,
			wantSet:   true,
		},
		{
			name:      "both aliases set and equal",
			short:     4,
			long:      4,
			shortSet:  true,
			longSet:   true,
			wantValue: 4,
			wantSet:   true,
		},
		{
			name:      "both aliases set and equal explicit zero",
			short:     0,
			long:      0,
			shortSet:  true,
			longSet:   true,
			wantValue: 0,
			wantSet:   true,
		},
		{
			name:     "both aliases set and conflicting",
			short:    1,
			long:     2,
			shortSet: true,
			longSet:  true,
			wantErr:  true,
		},
		{
			name:     "both aliases set and conflicting with zero",
			short:    0,
			long:     5,
			shortSet: true,
			longSet:  true,
			wantErr:  true,
		},
		{
			name:       "explicit value equal to default is still set",
			short:      3,
			defaultVal: 3,
			shortSet:   true,
			wantValue:  3,
			wantSet:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotValue, gotSet, err := resolveIntAlias(tc.short, tc.long, tc.defaultVal, tc.shortSet, tc.longSet)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got value %d set %v", gotValue, gotSet)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testboil.FailTestIfDiff(t, gotValue, tc.wantValue)
			testboil.FailTestIfDiff(t, gotSet, tc.wantSet)
		})
	}
}

// Test_applyFlagOverridesForText_Stoploss pins the flag > file override
// cascade for both stoploss limits: flag beats file, explicit zero disables a
// file limit, and an omitted flag leaves the file value (and nil Stoploss)
// untouched (Phase 4 acceptance criterion 2).
func Test_applyFlagOverridesForText_Stoploss(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	testCases := []struct {
		desc         string
		given        text.Configurations
		flagSet      Configurations
		defaultFlags Configurations
		want         text.Configurations
	}{
		{
			desc:    "max-tokens flag creates the stoploss object",
			given:   text.Configurations{},
			flagSet: Configurations{MaxTokens: 5000, MaxTokensSet: true},
			want:    text.Configurations{Stoploss: &text.Stoploss{MaxTokens: 5000}},
		},
		{
			desc: "max-tokens flag overrides file limit and keeps the configured message",
			given: text.Configurations{
				Stoploss: &text.Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
			flagSet: Configurations{MaxTokens: 5000, MaxTokensSet: true},
			want: text.Configurations{
				Stoploss: &text.Stoploss{MaxTokens: 5000, MaxTokensHandoverMsg: "wrap up"},
			},
		},
		{
			desc: "explicit zero max-tokens disables a file limit",
			given: text.Configurations{
				Stoploss: &text.Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
			flagSet: Configurations{MaxTokens: 0, MaxTokensSet: true},
			want: text.Configurations{
				Stoploss: &text.Stoploss{MaxTokens: 0, MaxTokensHandoverMsg: "wrap up"},
			},
		},
		{
			desc: "omitted max-tokens flag leaves the file stoploss untouched",
			given: text.Configurations{
				Stoploss: &text.Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
			flagSet: Configurations{},
			want: text.Configurations{
				Stoploss: &text.Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
		},
		{
			desc:    "omitted max-tokens flag leaves nil stoploss untouched",
			given:   text.Configurations{},
			flagSet: Configurations{},
			want:    text.Configurations{},
		},
		{
			desc:    "max-tool-calls flag overrides file limit",
			given:   text.Configurations{MaxToolCalls: intPtr(5)},
			flagSet: Configurations{MaxToolCalls: 2, MaxToolCallsSet: true},
			want:    text.Configurations{MaxToolCalls: intPtr(2)},
		},
		{
			desc:    "explicit zero max-tool-calls disables a file limit",
			given:   text.Configurations{MaxToolCalls: intPtr(5)},
			flagSet: Configurations{MaxToolCalls: 0, MaxToolCallsSet: true},
			want:    text.Configurations{MaxToolCalls: intPtr(0)},
		},
		{
			desc:    "omitted max-tool-calls flag leaves the file limit untouched",
			given:   text.Configurations{MaxToolCalls: intPtr(5)},
			flagSet: Configurations{},
			want:    text.Configurations{MaxToolCalls: intPtr(5)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			applyFlagOverridesForText(&tc.given, tc.flagSet, tc.defaultFlags)
			testboil.FailTestIfDiff(t, debug.IndentedJsonFmt(tc.given), debug.IndentedJsonFmt(tc.want))
		})
	}
}
