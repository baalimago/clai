package video

import (
	"context"
	"flag"
	"strings"
	"testing"
)

func Test_Command(t *testing.T) {
	t.Setenv("CLAI_CONFIG_DIR", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "key")
	deps := CommandDeps{
		ConfigPrep: func() (string, error) { return t.TempDir(), nil },
	}
	c := Command(deps)
	if c.Describe() == "" || !strings.Contains(c.Help(), "video <text>") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	for _, name := range []string{"vm", "video-model", "vd", "vp", "r", "re"} {
		if c.Flagset().Lookup(name) == nil {
			t.Fatalf("expected flag %q registered", name)
		}
	}
	if err := c.Flagset().Parse([]string{"-vm", "sora-2", "ocean", "waves"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := c.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}

// TestApplyFlagOverrides pins the binding of the shared media cascade onto
// the video configuration fields; the cascade semantics themselves are
// covered by internal.Test_mediaFlagsApply.
func TestApplyFlagOverrides(t *testing.T) {
	conf := Configurations{
		Model:        "from-file",
		StdinReplace: "from-file",
		Output:       Output{Dir: "/file", Prefix: "file-", Type: "url"},
	}
	fs := flag.NewFlagSet("video", flag.ContinueOnError)
	f := NewFlags()
	f.Register(fs)
	if err := fs.Parse([]string{"-vm", "from-flag", "-vd", "/flag", "-vp", "flag-", "-re", "-I", "FLAG"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ApplyFlagOverrides(&conf, &f)

	if conf.Model != "from-flag" || conf.Output.Dir != "/flag" || conf.Output.Prefix != "flag-" {
		t.Errorf("expected flag overrides, got: %+v", conf)
	}
	if !conf.ReplyMode || conf.StdinReplace != "FLAG" {
		t.Errorf("expected reply/stdin overrides, got: %+v", conf)
	}
	if conf.Output.Type != "url" {
		t.Errorf("expected untouched file fields to survive, got: %+v", conf)
	}
}
