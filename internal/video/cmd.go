package video

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/utils"
)

// CommandDeps are the composition-root collaborators: config prep lives
// in internal/setup, which video cannot import (setup imports video).
type CommandDeps struct {
	ConfigPrep func() (confDir string, err error)
	// Client sends the vendor requests; nil means the vendor's own client.
	Client *http.Client
}

// Flags is the video command's flag surface: the media group named for
// videos, plus its input image.
type Flags struct {
	internal.MediaFlags
	Images internal.StringsFlag
}

func (f *Flags) Register(fs *flag.FlagSet) {
	f.MediaFlags.Register(fs)
	f.Images.Register(fs, "Add the input image the video starts from: a path or a glob, '~' allowed.", "img", "image")
}

// NewFlags seeds the video flag defaults.
func NewFlags() Flags {
	return Flags{MediaFlags: internal.NewMediaFlags(internal.MediaFlagSpec{
		ModelNames:  []string{"vm", "video-model"},
		ModelDesc:   "Set the video model.",
		DirNames:    []string{"vd", "video-dir"},
		DirDesc:     "Set dir for generated videos. Default $HOME/Videos",
		DirDefault:  path.Join(os.Getenv("HOME"), "Videos"),
		PrefixNames: []string{"vp", "video-prefix"},
		PrefixDesc:  "Set prefix for generated videos. Default 'clai'",
	})}
}

// Command builds the video command.
func Command(deps CommandDeps) *internal.Command {
	f := NewFlags()
	c := &internal.Command{
		Name: "video",
		Desc: "Ask the video model for a video with the given prompt",
		HelpText: `video <text>. Generates a video from the prompt. An input image, from
-img or from stdin that is wholly an image, is the video's starting point.

Examples:
  clai v a timelapse of a growing plant
  clai video -vd ~/Videos ocean waves at sunset
  clai video -img still.png make the waves roll`,
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
		vConf, err := utils.LoadConfigFromFile(confDir, "videoConfig.json", nil, &Default)
		if err != nil {
			return fmt.Errorf("failed to load configs: %w", err)
		}
		ApplyFlagOverrides(&vConf, &f)
		vConf.Client = deps.Client
		if err := vConf.SetupPrompts(c.Args()); err != nil {
			return fmt.Errorf("failed to setup prompt: %w", err)
		}
		vq, err := CreateQuerier(vConf)
		if err != nil {
			return fmt.Errorf("failed to create video querier: %w", err)
		}
		c.SetQuerier(vq)
		return nil
	}
	return c
}

// ApplyFlagOverrides applies the CLI flag values onto the file-loaded
// configuration (flags > file > default).
func ApplyFlagOverrides(vConf *Configurations, f *Flags) {
	f.Apply(internal.MediaConfig{
		Model:        &vConf.Model,
		ReplyMode:    &vConf.ReplyMode,
		StdinReplace: &vConf.StdinReplace,
		OutputDir:    &vConf.Output.Dir,
		OutputPrefix: &vConf.Output.Prefix,
	})
	vConf.Images = f.Images.Value()
}
