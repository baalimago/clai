package internal

import (
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
)

func parseAgentText(t *testing.T, args ...string) (*AgentTextFlags, []string) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	g := &AgentTextFlags{}
	g.Register(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	return g, fs.Args()
}

// Test_aliasValues pins the shared flag.Value alias semantics: one value
// backs both names, last one parsed wins, and explicit-set survives values
// equal to the default (incl. zero).
func Test_aliasValues(t *testing.T) {
	t.Run("neither alias set uses default and stays unset", func(t *testing.T) {
		g, _ := parseAgentText(t)
		if g.MaxTokens.Value() != 0 || g.MaxTokens.Explicit() {
			t.Fatalf("got val=%d explicit=%v, want 0/unset", g.MaxTokens.Value(), g.MaxTokens.Explicit())
		}
	})
	t.Run("short alias only", func(t *testing.T) {
		g, _ := parseAgentText(t, "-mt", "5")
		if g.MaxTokens.Value() != 5 || !g.MaxTokens.Explicit() {
			t.Fatalf("got val=%d explicit=%v, want 5/set", g.MaxTokens.Value(), g.MaxTokens.Explicit())
		}
	})
	t.Run("long alias only", func(t *testing.T) {
		g, _ := parseAgentText(t, "-max-tokens", "6")
		if g.MaxTokens.Value() != 6 || !g.MaxTokens.Explicit() {
			t.Fatalf("got val=%d explicit=%v, want 6/set", g.MaxTokens.Value(), g.MaxTokens.Explicit())
		}
	})
	t.Run("both aliases last one wins", func(t *testing.T) {
		g, _ := parseAgentText(t, "-mt=0", "-max-tokens=5")
		if g.MaxTokens.Value() != 5 || !g.MaxTokens.Explicit() {
			t.Fatalf("got val=%d explicit=%v, want 5/set", g.MaxTokens.Value(), g.MaxTokens.Explicit())
		}
	})
	t.Run("explicit zero is still set", func(t *testing.T) {
		g, _ := parseAgentText(t, "-mt", "0")
		if g.MaxTokens.Value() != 0 || !g.MaxTokens.Explicit() {
			t.Fatalf("got val=%d explicit=%v, want 0/set", g.MaxTokens.Value(), g.MaxTokens.Explicit())
		}
	})
	t.Run("string aliases last one wins", func(t *testing.T) {
		g, _ := parseAgentText(t, "-cm", "a", "-chat-model", "b")
		if g.ChatModel.Value() != "b" {
			t.Fatalf("got %q, want b", g.ChatModel.Value())
		}
	})
	t.Run("non-integer value rejected", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		g := &AgentTextFlags{}
		g.Register(fs)
		err := fs.Parse([]string{"-max-tokens=abc"})
		if err == nil || !strings.Contains(err.Error(), "invalid value") {
			t.Fatalf("expected invalid-value error, got: %v", err)
		}
	})
	t.Run("bool alias reports IsBoolFlag for scanner arity", func(t *testing.T) {
		var b BoolFlag
		var v flag.Value = &b
		bf, ok := v.(interface{ IsBoolFlag() bool })
		if !ok || !bf.IsBoolFlag() {
			t.Fatal("BoolFlag must report IsBoolFlag() true")
		}
	})
	t.Run("positional args pass through after parsing", func(t *testing.T) {
		_, rest := parseAgentText(t, "-cm", "test", "q", "hello")
		if !slices.Equal(rest, []string{"q", "hello"}) {
			t.Fatalf("post-parse args: got %v", rest)
		}
	})
}

// Test_changedSemantics pins the override-cascade contract: a flag equal
// to its default reads as unchanged (so config files win), preserving the
// historical defaults-comparison behavior, while Explicit still records
// that it was typed.
func Test_changedSemantics(t *testing.T) {
	t.Run("default value is not Changed", func(t *testing.T) {
		f := NewStringFlag("clai")
		if f.Changed() || f.Explicit() {
			t.Fatal("untouched flag must be neither changed nor explicit")
		}
	})
	t.Run("explicitly passing the default is Explicit but not Changed", func(t *testing.T) {
		f := NewStringFlag("clai")
		if err := f.Set("clai"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if f.Changed() {
			t.Fatal("value equal to default must not read as changed")
		}
		if !f.Explicit() {
			t.Fatal("typed flag must read as explicit")
		}
	})
	t.Run("different value is Changed", func(t *testing.T) {
		f := NewStringFlag("clai")
		if err := f.Set("other"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if !f.Changed() || f.Value() != "other" {
			t.Fatalf("got changed=%v val=%q", f.Changed(), f.Value())
		}
	})
	t.Run("bool default false, -flag flips to changed", func(t *testing.T) {
		var f BoolFlag
		if err := f.Set("true"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if !f.Changed() || !f.Value() {
			t.Fatalf("got changed=%v val=%v", f.Changed(), f.Value())
		}
	})
}

// Test_replyStdinDefaults pins the '{}' stdin placeholder default and the
// -i shorthand semantics.
func Test_replyStdinDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	g := NewReplyStdinFlags()
	g.Register(fs)
	if err := fs.Parse([]string{"-i"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !g.ExpectReplace.Value() {
		t.Fatal("-i must set ExpectReplace")
	}
	if g.StdinReplace.Value() != "{}" {
		t.Fatalf("stdin replace default: got %q want {}", g.StdinReplace.Value())
	}
	if g.StdinReplace.Changed() {
		t.Fatal("untouched stdin replace must not read as changed")
	}
}

// Test_mediaFlagsApply pins the media override cascade shared by photo and
// video: flags beat file values, a flag equal to its default counts as
// unset, -i seeds the placeholder and an explicit -I/-replace overrules it.
func Test_mediaFlagsApply(t *testing.T) {
	spec := MediaFlagSpec{
		ModelNames:  []string{"xm", "x-model"},
		ModelDesc:   "model",
		DirNames:    []string{"xd", "x-dir"},
		DirDesc:     "dir",
		DirDefault:  "/default",
		PrefixNames: []string{"xp", "x-prefix"},
		PrefixDesc:  "prefix",
	}
	type target struct {
		model, stdinReplace, dir, prefix string
		replyMode                        bool
	}
	fileValues := target{model: "from-file", stdinReplace: "from-file", dir: "/file", prefix: "file-"}

	testCases := []struct {
		desc string
		args []string
		want target
	}{
		{
			desc: "no flags leaves the file values",
			args: nil,
			want: fileValues,
		},
		{
			desc: "flags beat file values",
			args: []string{"-xm", "from-flag", "-xd", "/flag", "-xp", "flag-", "-re", "-I", "FLAG"},
			want: target{model: "from-flag", stdinReplace: "FLAG", dir: "/flag", prefix: "flag-", replyMode: true},
		},
		{
			desc: "a flag equal to its default stays ignored",
			args: []string{"-xp", "clai", "-xd", "/default"},
			want: fileValues,
		},
		{
			desc: "-i seeds the '{}' placeholder",
			args: []string{"-i"},
			want: target{model: "from-file", stdinReplace: "{}", dir: "/file", prefix: "file-"},
		},
		{
			desc: "explicit -replace overrules the -i placeholder",
			args: []string{"-i", "-replace", "LOG"},
			want: target{model: "from-file", stdinReplace: "LOG", dir: "/file", prefix: "file-"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			f := NewMediaFlags(spec)
			f.Register(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("Parse(%v): %v", tc.args, err)
			}

			got := fileValues
			f.Apply(MediaConfig{
				Model:        &got.model,
				ReplyMode:    &got.replyMode,
				StdinReplace: &got.stdinReplace,
				OutputDir:    &got.dir,
				OutputPrefix: &got.prefix,
			})

			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Test_textFlagsRegistration pins every flag name of the composed text
// surface, so a group refactor cannot silently drop a name.
func Test_textFlagsRegistration(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tf := NewTextFlags()
	tf.Raw.Register(fs)
	tf.ReplyStdin.Register(fs)
	tf.AgentText.Register(fs)
	tf.QueryText.Register(fs)
	for _, name := range []string{
		"r", "raw", "re", "reply", "I", "replace", "i",
		"cm", "chat-model", "t", "tools", "cmd-ban", "lb", "lookback",
		"mt", "max-tokens", "mtc", "max-tool-calls", "max-tool-calls-after-handover",
		"g", "glob", "p", "profile", "prp", "profile-path", "n", "non-interactive",
		"dre", "dir-reply", "s", "skills", "rf", "response-format", "asc", "add-shell-context",
	} {
		if fs.Lookup(name) == nil {
			t.Fatalf("flag %q missing from the text surface", name)
		}
	}
}

// Test_chatFlagsRegistration pins the chat surface. Every chat subcommand
// reads stored transcripts and runs no model, so the agent group's model,
// tool and limit flags are inert there and must not be offered. Profile is
// the exception: continuing a chat stamps it for later -dre queries.
func Test_chatFlagsRegistration(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cf := &ChatFlags{}
	cf.Register(fs)
	for _, name := range []string{"r", "raw", "n", "non-interactive", "p", "profile"} {
		if fs.Lookup(name) == nil {
			t.Fatalf("flag %q missing from the chat surface", name)
		}
	}
	for _, name := range []string{
		"cm", "chat-model", "t", "tools", "mt", "max-tokens", "mtc",
		"max-tool-calls", "max-tool-calls-after-handover", "cmd-ban",
		"lb", "lookback", "g", "glob", "am", "audio-model", "af",
		"audio-format", "prp", "profile-path", "re", "reply", "dre",
	} {
		if fs.Lookup(name) != nil {
			t.Fatalf("flag %q does nothing on chat and must not be registered", name)
		}
	}
}
