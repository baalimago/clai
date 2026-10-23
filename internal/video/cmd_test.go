package video

import (
	"context"
	"flag"
	"os"
	"path/filepath"
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

	if err := ApplyFlagOverrides(&conf, &f); err != nil {
		t.Fatalf("ApplyFlagOverrides: %v", err)
	}

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

// TestApplyFlagOverridesExpandsOutputDir proves -vd resolves a leading ~, so a
// quoted path is not taken literally.
func TestApplyFlagOverridesExpandsOutputDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	conf := Configurations{Output: Output{Dir: "/file", Type: LOCAL}}
	fs := flag.NewFlagSet("video", flag.ContinueOnError)
	f := NewFlags()
	f.Register(fs)
	if err := fs.Parse([]string{"-vd", "~/Videos"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if err := ApplyFlagOverrides(&conf, &f); err != nil {
		t.Fatalf("ApplyFlagOverrides: %v", err)
	}
	if want := filepath.Join(home, "Videos"); conf.Output.Dir != want {
		t.Fatalf("output dir = %q, want %q", conf.Output.Dir, want)
	}
}

// Test_CommandExpandsOutputDir proves the config file's output.dir is a user
// path: a local run creates the directory under the home, never a literal ~.
func Test_CommandExpandsOutputDir(t *testing.T) {
	testCases := []struct {
		desc    string
		home    string
		wantErr bool
	}{
		{desc: "~ expands to the home", home: t.TempDir()},
		{desc: "~ without a home is an error", wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Setenv("HOME", tc.home)
			t.Setenv("OPENAI_API_KEY", "key")
			t.Chdir(t.TempDir())
			confDir := t.TempDir()
			t.Setenv("CLAI_CONFIG_DIR", confDir)
			if err := os.WriteFile(filepath.Join(confDir, "videoConfig.json"), []byte(`{"output":{"type":"local","dir":"~/Videos"}}`), 0o644); err != nil {
				t.Fatalf("write videoConfig.json: %v", err)
			}
			c := Command(CommandDeps{ConfigPrep: func() (string, error) { return confDir, nil }})
			if err := c.Flagset().Parse([]string{"-vm", "sora-2", "ocean", "waves"}); err != nil {
				t.Fatalf("Parse: %v", err)
			}

			err := c.Setup(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatal("Setup with an unresolvable ~ succeeded, want an error")
				}
			} else if err != nil {
				t.Fatalf("Setup: %v", err)
			}
			if _, err := os.Stat(filepath.Join("~", "Videos")); !os.IsNotExist(err) {
				t.Fatalf("literal ~ dir: stat err = %v, want it absent", err)
			}
			if tc.wantErr {
				return
			}
			info, err := os.Stat(filepath.Join(tc.home, "Videos"))
			if err != nil {
				t.Fatalf("expanded output dir: %v", err)
			}
			if !info.IsDir() {
				t.Fatalf("%s is not a directory", filepath.Join(tc.home, "Videos"))
			}
		})
	}
}
