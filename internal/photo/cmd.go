package photo

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	imagodebug "github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// CommandDeps are the composition-root collaborators: config loading (with
// the old-config migration) lives in internal/setup, which photo cannot
// import (setup imports photo), so main.go injects it.
type CommandDeps struct {
	ConfigPrep func() (confDir string, err error)
	LoadConfig func(confDir string) (Configurations, error)
}

// Flags is the photo command's flag surface: the media group named for
// pictures.
type Flags = internal.MediaFlags

// NewFlags seeds the photo flag defaults.
func NewFlags() Flags {
	return internal.NewMediaFlags(internal.MediaFlagSpec{
		ModelNames:  []string{"pm", "photo-model"},
		ModelDesc:   "Set the image model to use.",
		DirNames:    []string{"pd", "photo-dir"},
		DirDesc:     "Set the directory to store the generated pictures. Default is $HOME/Pictures",
		DirDefault:  path.Join(os.Getenv("HOME"), "Pictures"),
		PrefixNames: []string{"pp", "photo-prefix"},
		PrefixDesc:  "Set the prefix for the generated pictures. Default is 'clai'",
	})
}

// Command builds the photo command.
func Command(deps CommandDeps) *internal.Command {
	f := NewFlags()
	c := &internal.Command{
		Name: "photo",
		Desc: "Ask the photo model for a picture with the given prompt",
		HelpText: `photo <text>. Generates an image from the prompt and saves it to the
photo dir.

Examples:
  clai p a cat in space
  clai photo -pm dall-e-2 -pd ~/Pictures a watercolor fox`,
		Register:            f.Register,
		Raw:                 &f.Raw,
		CompleteFlagValueFn: internal.MediaFlagValues,
		CompleteArgsFn:      internal.SuppressArgs,
	}
	c.OnSetup = func(_ context.Context, c *internal.Command) error {
		confDir, err := deps.ConfigPrep()
		if err != nil {
			return err
		}
		pConf, err := deps.LoadConfig(confDir)
		if err != nil {
			return fmt.Errorf("failed to load configs: %w", err)
		}
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("photoConfig pre override: %+v\n", pConf))
		}
		ApplyFlagOverrides(&pConf, &f)
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("photoConfig post override: %+v\n", pConf))
		}
		if err := pConf.SetupPrompts(c.Args()); err != nil {
			return fmt.Errorf("failed to setup prompt: %v", err)
		}
		pq, err := CreateQuerier(pConf)
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("photo querier: %+v\n", imagodebug.IndentedJsonFmt(pq)))
		}
		if err != nil {
			return fmt.Errorf("failed to create photo querier: %v", err)
		}
		c.SetQuerier(pq)
		return nil
	}
	return c
}

// ApplyFlagOverrides applies the CLI flag values onto the file-loaded
// configuration (flags > file > default).
func ApplyFlagOverrides(pConf *Configurations, f *Flags) {
	f.Apply(internal.MediaConfig{
		Model:        &pConf.Model,
		ReplyMode:    &pConf.ReplyMode,
		StdinReplace: &pConf.StdinReplace,
		OutputDir:    &pConf.Output.Dir,
		OutputPrefix: &pConf.Output.Prefix,
	})
}
