package text

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func Test_newLoopRunner_rejectsQuerierWithoutLoopSupport(t *testing.T) {
	_, err := newLoopRunner(queryOnlyQuerier{}, NewCompletionRecorder(""), LoopConfig{})
	if err == nil {
		t.Fatal("want an error for a querier without RegisterExtraTool")
	}
	testboil.AssertStringContains(t, err.Error(), "text.queryOnlyQuerier")
}

func TestQueryCommand_registersTextAndLoopFlags(t *testing.T) {
	c := QueryCommand(QueryCommandDeps{})
	fs := c.Flagset()
	for _, name := range []string{"r", "cm", "t", "dre", "l", "loop", "mi", "max-iterations", "check"} {
		if fs.Lookup(name) == nil {
			t.Fatalf("flag %q missing from the query command", name)
		}
	}
}

// TestQueryCommand_loopFlagsRequireLoopMode pins the command boundary of
// LoopFlags.Validate: a loop-only flag without -l fails setup before any
// querier is built.
func TestQueryCommand_loopFlagsRequireLoopMode(t *testing.T) {
	for _, args := range [][]string{
		{"-mi", "3", "hello"},
		{"-check", "true", "hello"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c := QueryCommand(QueryCommandDeps{})
			if err := c.Flagset().Parse(args); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			err := c.Setup(context.Background())
			if !errors.Is(err, internal.ErrLoopFlagWithoutMode) {
				t.Fatalf("Setup(%v) = %v, want ErrLoopFlagWithoutMode", args, err)
			}
		})
	}
}

type queryOnlyQuerier struct{}

func (queryOnlyQuerier) Query(context.Context) error { return nil }

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
			desc:  "max-tokens flag accepts a scaled value",
			given: Configurations{},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.AgentText.MaxTokens, "300k") },
			want:  Configurations{Stoploss: &Stoploss{MaxTokens: 300000}},
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

// TestApplyFlagOverrides_summary pins the summary flag cascade: -summarize
// applies on Explicit in both directions, -sm applies on Changed, and an
// omitted flag leaves the file value (worklog
// 2026-09-09-conversation-summaries, phase 4).
func TestApplyFlagOverrides_summary(t *testing.T) {
	testCases := []struct {
		desc  string
		given Configurations
		mods  func(tf *internal.TextFlags)
		want  Configurations
	}{
		{
			desc:  "-summarize=false over a true config",
			given: Configurations{SummarizeConversations: true},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.QueryText.Summarize, "false") },
			want:  Configurations{SummarizeConversations: false},
		},
		{
			desc:  "-summarize over a false config",
			given: Configurations{SummarizeConversations: false},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.QueryText.Summarize, "true") },
			want:  Configurations{SummarizeConversations: true},
		},
		{
			desc:  "omitted -summarize keeps the file value",
			given: Configurations{SummarizeConversations: true},
			want:  Configurations{SummarizeConversations: true},
		},
		{
			desc:  "-qs=false over a true config",
			given: Configurations{QuerySummary: true},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.QueryText.QuerySummary, "false") },
			want:  Configurations{QuerySummary: false},
		},
		{
			desc:  "-qs over a false config",
			given: Configurations{QuerySummary: false},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.QueryText.QuerySummary, "true") },
			want:  Configurations{QuerySummary: true},
		},
		{
			desc:  "omitted -qs keeps the file value",
			given: Configurations{QuerySummary: true},
			want:  Configurations{QuerySummary: true},
		},
		{
			desc:  "-sm overrides the file summary-model",
			given: Configurations{SummaryModel: "from-file"},
			mods:  func(tf *internal.TextFlags) { mustSet(t, &tf.QueryText.SummaryModel, "from-flag") },
			want:  Configurations{SummaryModel: "from-flag"},
		},
		{
			desc:  "omitted -sm keeps the file summary-model",
			given: Configurations{SummaryModel: "from-file"},
			want:  Configurations{SummaryModel: "from-file"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			given := tc.given
			ApplyFlagOverrides(&given, tfWith(t, tc.mods))
			testboil.FailTestIfDiff(t, debug.IndentedJsonFmt(given), debug.IndentedJsonFmt(tc.want))
		})
	}
}

func TestEnableQuerySummary_RequiresATextQuerier(t *testing.T) {
	err := enableQuerySummary(queryOnlyQuerier{})
	if err == nil || !strings.Contains(err.Error(), "query summary requires a text querier") {
		t.Fatalf("err = %v, want a typed refusal", err)
	}
}

func TestSetSummaryJoinTimeout_RequiresATextQuerier(t *testing.T) {
	err := setSummaryJoinTimeout(queryOnlyQuerier{}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "summary join timeout requires a text querier") {
		t.Fatalf("err = %v, want a typed refusal", err)
	}
}

func TestSetSummaryJoinTimeout_SetsTheBound(t *testing.T) {
	q := &Querier[*MockQuerier]{}
	if err := setSummaryJoinTimeout(q, 3*time.Second); err != nil {
		t.Fatalf("setSummaryJoinTimeout: %v", err)
	}
	if q.summaryJoinBound() != 3*time.Second {
		t.Fatalf("bound = %v, want 3s", q.summaryJoinBound())
	}
}
