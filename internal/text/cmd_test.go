package text

import (
	"testing"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// tfWith builds a TextFlags and applies mods, simulating parsed flags via
// the flag.Value Set path so Explicit/Changed behave as in production.
func tfWith(t *testing.T, mods func(tf *internal.TextFlags)) internal.TextFlags {
	t.Helper()
	tf := internal.NewTextFlags()
	if mods != nil {
		mods(&tf)
	}
	return tf
}

func mustSet(t *testing.T, v interface{ Set(string) error }, val string) {
	t.Helper()
	if err := v.Set(val); err != nil {
		t.Fatalf("Set(%q): %v", val, err)
	}
}

func Test_ApplyFlagOverrides_stdin(t *testing.T) {
	t.Run("it should set stdinput config if flagged and default is empty", func(t *testing.T) {
		given := Configurations{StdinReplace: ""}
		tf := tfWith(t, func(tf *internal.TextFlags) {
			mustSet(t, &tf.ReplyStdin.ExpectReplace, "true")
		})
		ApplyFlagOverrides(&given, tf)
		testboil.FailTestIfDiff(t, given.StdinReplace, "{}")
	})
}

// Test_ApplyFlagOverrides_Stoploss pins the flag > file override cascade
// for both stoploss limits: flag beats file, explicit zero disables a file
// limit, and an omitted flag leaves the file value (and nil Stoploss)
// untouched (Phase 4 acceptance criterion 2).
func Test_ApplyFlagOverrides_Stoploss(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	testCases := []struct {
		desc  string
		given Configurations
		mods  func(tf *internal.TextFlags)
		want  Configurations
	}{
		{
			desc:  "max-tokens flag creates the stoploss object",
			given: Configurations{},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxTokens, "5000") },
			want:  Configurations{Stoploss: &Stoploss{MaxTokens: 5000}},
		},
		{
			desc: "max-tokens flag overrides file limit and keeps the configured message",
			given: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
			mods: func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxTokens, "5000") },
			want: Configurations{
				Stoploss: &Stoploss{MaxTokens: 5000, MaxTokensHandoverMsg: "wrap up"},
			},
		},
		{
			desc: "explicit zero max-tokens disables a file limit",
			given: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
			mods: func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxTokens, "0") },
			want: Configurations{
				Stoploss: &Stoploss{MaxTokens: 0, MaxTokensHandoverMsg: "wrap up"},
			},
		},
		{
			desc: "omitted max-tokens flag leaves the file stoploss untouched",
			given: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
			mods: nil,
			want: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
			},
		},
		{
			desc:  "omitted max-tokens flag leaves nil stoploss untouched",
			given: Configurations{},
			mods:  nil,
			want:  Configurations{},
		},
		{
			desc:  "max-tool-calls flag overrides file limit",
			given: Configurations{MaxToolCalls: intPtr(5)},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxToolCalls, "2") },
			want:  Configurations{MaxToolCalls: intPtr(2)},
		},
		{
			desc:  "explicit zero max-tool-calls disables a file limit",
			given: Configurations{MaxToolCalls: intPtr(5)},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxToolCalls, "0") },
			want:  Configurations{MaxToolCalls: intPtr(0)},
		},
		{
			desc:  "omitted max-tool-calls flag leaves the file limit untouched",
			given: Configurations{MaxToolCalls: intPtr(5)},
			mods:  nil,
			want:  Configurations{MaxToolCalls: intPtr(5)},
		},
		{
			desc:  "max-tool-calls-after-handover flag creates the stoploss object",
			given: Configurations{},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxToolCallsAfterHandover, "3") },
			want:  Configurations{Stoploss: &Stoploss{MaxToolCallsAfterHandover: 3}},
		},
		{
			desc: "max-tool-calls-after-handover flag overrides the file value and keeps the rest",
			given: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up", MaxToolCallsAfterHandover: 5},
			},
			mods: func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxToolCallsAfterHandover, "2") },
			want: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up", MaxToolCallsAfterHandover: 2},
			},
		},
		{
			desc: "explicit zero max-tool-calls-after-handover disables a file budget",
			given: Configurations{
				Stoploss: &Stoploss{MaxToolCallsAfterHandover: 5},
			},
			mods: func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxToolCallsAfterHandover, "0") },
			want: Configurations{
				Stoploss: &Stoploss{MaxToolCallsAfterHandover: 0},
			},
		},
		{
			desc: "omitted max-tool-calls-after-handover flag leaves the file value untouched",
			given: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxToolCallsAfterHandover: 5},
			},
			mods: nil,
			want: Configurations{
				Stoploss: &Stoploss{MaxTokens: 100, MaxToolCallsAfterHandover: 5},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ApplyFlagOverrides(&tc.given, tfWith(t, tc.mods))
			testboil.FailTestIfDiff(t, debug.IndentedJsonFmt(tc.given), debug.IndentedJsonFmt(tc.want))
		})
	}
}
