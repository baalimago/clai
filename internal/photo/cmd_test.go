package photo

import (
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

func Test_Command(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")
	var gotConfDir string
	var loaded Configurations
	deps := CommandDeps{
		ConfigPrep: func() (string, error) { return "/stub/conf", nil },
		LoadConfig: func(confDir string) (Configurations, error) {
			gotConfDir = confDir
			loaded = Configurations{Model: "from-file", PromptFormat: "%v", Output: Output{Type: UNSET}}
			return loaded, nil
		},
	}
	c := Command(deps)
	if c.Describe() == "" || !strings.Contains(c.Help(), "photo <text>") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}
	for _, name := range []string{"pm", "photo-model", "pd", "pp", "r", "re", "I", "img", "image", "open"} {
		if c.Flagset().Lookup(name) == nil {
			t.Fatalf("expected flag %q registered", name)
		}
	}
	if err := c.Flagset().Parse([]string{"-pm", "dall-e-2", "a", "cat"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Config loading alone is not enough: the image dependencies are required (P14-3).
	if err := c.Setup(context.Background()); !errors.Is(err, ErrPhotoUnconfigured) {
		t.Fatalf("Setup: got %v, want ErrPhotoUnconfigured", err)
	}
	if gotConfDir != "" {
		t.Fatalf("LoadConfig ran before the dependency check (conf dir %q)", gotConfDir)
	}
	if items := c.CompleteFlagValue("img", ""); len(items) != 1 || items[0].Kind != cmd.CompletionKindFile {
		t.Fatalf("-img completion = %+v, want files", items)
	}
	if items := c.CompleteFlagValue("pd", ""); len(items) != 1 || items[0].Kind != cmd.CompletionKindDir {
		t.Fatalf("-pd completion = %+v, want dirs", items)
	}
}

// TestApplyFlagOverrides pins the binding of the shared media cascade onto
// the photo configuration fields; the cascade semantics themselves are
// covered by internal.Test_mediaFlagsApply.
func TestApplyFlagOverrides(t *testing.T) {
	conf := Configurations{
		Model:        "from-file",
		StdinReplace: "from-file",
		Output:       Output{Dir: "/file", Prefix: "file-", Type: "local"},
	}
	fs := flag.NewFlagSet("photo", flag.ContinueOnError)
	f := NewFlags()
	f.Register(fs)
	if err := fs.Parse([]string{"-pm", "from-flag", "-pd", "/flag", "-pp", "flag-", "-re", "-I", "FLAG"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ApplyFlagOverrides(&conf, &f)

	if conf.Model != "from-flag" || conf.Output.Dir != "/flag" || conf.Output.Prefix != "flag-" {
		t.Errorf("expected flag overrides, got: %+v", conf)
	}
	if !conf.ReplyMode || conf.StdinReplace != "FLAG" {
		t.Errorf("expected reply/stdin overrides, got: %+v", conf)
	}
	if conf.Output.Type != "local" {
		t.Errorf("expected untouched file fields to survive, got: %+v", conf)
	}
}
