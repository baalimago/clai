package photo

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imageinput"
	"github.com/baalimago/clai/internal/modelfallback"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// PreviousImageFunc returns the latest image of the previous query.
// PreviousImageFunc.1: it fails with ErrNoPreviousImage when that query holds no image.
type PreviousImageFunc func(confDir string) (asset.Image, error)

var ErrNoPreviousImage = errors.New("previous query holds no image")

// CommandDeps are the composition-root collaborators: config loading (with
// the old-config migration) lives in internal/setup, which photo cannot
// import (setup imports photo), so internal/cli injects it, together with
// the image generator, the input collector, the presenter and the opener.
type CommandDeps struct {
	ConfigPrep        func() (confDir string, err error)           // existing
	LoadConfig        func(confDir string) (Configurations, error) // existing
	NewGenerator      imagegen.NewGeneratorFunc
	Collector         imageinput.ImageCollector
	NewPresenter      func(present.Options) present.Presenter
	NewOpener         func(command string) present.Opener
	PreviousImage     PreviousImageFunc
	NewRemoteResolver func(confDir string, prompt remote.Prompter) (remote.Resolver, error)
}

var ErrPhotoUnconfigured = errors.New("photo: image dependencies not configured")

type URLOutputRemovedError struct{}

func (e *URLOutputRemovedError) Error() string {
	return `photoConfig.json output.type "url" was removed: images are always saved to output.dir; set output.type to "local"`
}

// Flags is the photo command's flag surface: the media group plus photo's own
// image inputs and -open (P14-2).
type Flags struct {
	internal.MediaFlags
	Images internal.StringsFlag
	Open   internal.BoolFlag
}

// NewFlags seeds the photo flag defaults. Its dir default is absolute, so an
// explicit -pd ~/Pictures stays distinct from it and still overrides
// photoConfig.json; photo.DEFAULT is the real default.
func NewFlags() Flags {
	return Flags{MediaFlags: internal.NewMediaFlags(internal.MediaFlagSpec{
		ModelNames:  []string{"pm", "photo-model"},
		ModelDesc:   "Set the image model to use, or a model-aliases verb such as 'strong'.",
		DirNames:    []string{"pd", "photo-dir"},
		DirDesc:     "Set the directory to store the generated pictures, with a leading ~ and $VAR expanded. Default is ~/Pictures",
		DirDefault:  path.Join(os.Getenv("HOME"), "Pictures"),
		PrefixNames: []string{"pp", "photo-prefix"},
		PrefixDesc:  "Set the prefix for the generated pictures. Default is 'clai'",
	})}
}

func (f *Flags) Register(fs *flag.FlagSet) {
	f.MediaFlags.Register(fs)
	f.Images.Register(fs, "Add an input image to edit or use as a reference: a path or a glob, '~' allowed. Repeatable.", "img", "image")
	f.Open.Register(fs, "Open each saved image with the image-opener of photoConfig.json. Same as its auto-open-images.", "open")
}

// Command builds the photo command.
//
//	clai [photo flags] photo|p [-img <path|glob>]... [-open] <prompt>
//
//	  -pm, -photo-model <model>   image model (photoConfig.json "model")
//	  -pd, -photo-dir <dir>       output directory (photoConfig.json output.dir)
//	  -pp, -photo-prefix <p>      file name prefix (photoConfig.json output.prefix)
//	  -img, -image <path|glob>    input image(s) to edit or use as references; repeatable   (new, photo only)
//	  -open                       open each saved image (photoConfig.json "auto-open-images") (new, photo only)
//	  -re, -reply                 previous query as context, plus its latest image as an input
//	  -I, -replace <token> / -i   stdin replacement, unchanged
//	  -r, -raw                    no animation
//
//	stdin   wholly an image (raw or base64) → an input image; otherwise prompt text, as today
//	stdout  one presented line per saved image
//	stderr  errors; the opener notice; the output-dir fallback notice
//	exit    0 when every image is saved; 1 on any error
//
//	photo.1  The generator is deps.NewGenerator(GeneratorConfig{Model, ConfigDir}), built
//	         once per model the configured name resolves to, so a model alias builds its
//	         candidates in turn (architecture/model-aliases.md). It is called with
//	         Prompt = Collect(...).Prompt formatted through "prompt-format", as today, and
//	         Inputs = Collect(...).Images, then the -re image.
//	photo.2  Each returned image is written to <output.dir>/<prefix>_<random>.<ext>, where
//	         <ext> comes from the README MIME-to-extension parameter and <output.dir> has a
//	         leading ~ and $VAR expanded. When output.dir is not writable, the image goes to
//	         os.TempDir() and stderr names the fallback.
//	photo.3  Each written file is presented once, through deps.NewPresenter, with Handle = the
//	         first handle-length hex digits of its sha256, Path = the file and Details =
//	         present.DetailsOf(its bytes, the photo model, the generation time). The opener is
//	         deps.NewOpener(photoConfig "image-opener"), and AutoOpen is -open or
//	         photoConfig "auto-open-images" (README parameters).
//	photo.4  -re keeps today's text context, which is the JSON of the previous messages, with
//	         every ImagePart in that copy replaced by TextPart(Describe()). A text-only
//	         context is byte-identical to today's (I10). The image from
//	         deps.PreviousImage(confDir) is appended as the last input. ErrNoPreviousImage
//	         means there is no image input; any other error fails the command.
//	photo.5  output.type "url" fails before any request with *URLOutputRemovedError, whose
//	         message tells the user to set "local". "local" and "unset" both save locally.
//	photo.6  While Generate runs, a board.Status titled "generating image" shows on stdout,
//	         only when stdout is a terminal and -raw is off.
//	photo.7  A nil NewGenerator, Collector, NewPresenter or NewOpener fails before any work
//	         with ErrPhotoUnconfigured. A nil PreviousImage makes -re fail the same way.
//	photo.8  photo writes nothing to the conversation asset store (I6), and builds a generator
//	         only through deps.NewGenerator (I9).
func Command(deps CommandDeps) *internal.Command {
	f := NewFlags()
	c := &internal.Command{
		Name: "photo",
		Desc: "Ask the photo model for a picture with the given prompt",
		HelpText: `photo <text>. Generates an image from the prompt, saves it to the photo
dir and prints one line per saved image. Input images, from -img or from
stdin that is wholly an image, are edited or used as references. With -re,
the previous query is the context and its latest image the last input.

Examples:
  clai p a cat in space
  clai photo -pm gpt-image-1 -pd ~/Pictures a watercolor fox
  clai photo -img sketch.png -open make it a watercolour
  clai -re photo make it purple`,
		Register:            f.Register,
		Raw:                 &f.Raw,
		CompleteFlagValueFn: internal.MediaFlagValues,
		CompleteArgsFn:      internal.SuppressArgs,
	}
	c.OnSetup = func(ctx context.Context, c *internal.Command) error {
		base, err := newQuery(ctx, deps, &f, c.Args())
		if err != nil {
			return err
		}
		q, err := modelfallback.New(ctx, base.aliases, base.model, func(_ context.Context, model string) (models.Querier, error) {
			return base.forModel(model)
		}, modelfallback.NoticeTo(base.stderr))
		if err != nil {
			return err
		}
		c.SetQuerier(q)
		return nil
	}
	return c
}

func (d CommandDeps) check(reply bool) error {
	var missing []string
	for _, dep := range []struct {
		name  string
		unset bool
	}{
		{"ConfigPrep", d.ConfigPrep == nil},
		{"LoadConfig", d.LoadConfig == nil},
		{"NewGenerator", d.NewGenerator == nil},
		{"Collector", d.Collector == nil},
		{"NewPresenter", d.NewPresenter == nil},
		{"NewOpener", d.NewOpener == nil},
		{"PreviousImage", reply && d.PreviousImage == nil},
	} {
		if dep.unset {
			missing = append(missing, dep.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s unset", ErrPhotoUnconfigured, strings.Join(missing, ", "))
}

// loadConfig is the photo configuration after the flag overrides, with its
// output type checked (photo.5).
func loadConfig(deps CommandDeps, f *Flags, confDir string) (Configurations, error) {
	pConf, err := deps.LoadConfig(confDir)
	if err != nil {
		return Configurations{}, fmt.Errorf("failed to load configs: %w", err)
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("photoConfig pre override: %+v\n", pConf))
	}
	if err := ApplyFlagOverrides(&pConf, f); err != nil {
		return Configurations{}, err
	}
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("photoConfig post override: %+v\n", pConf))
	}
	if pConf.Output.Type == URL {
		return Configurations{}, &URLOutputRemovedError{}
	}
	if err := ValidateOutputType(pConf.Output.Type); err != nil {
		return Configurations{}, err
	}
	return pConf, nil
}

// ApplyFlagOverrides applies the CLI flag values onto the file-loaded
// configuration (flags > file > default).
func ApplyFlagOverrides(pConf *Configurations, f *Flags) error {
	return f.Apply(internal.MediaConfig{
		Model:        &pConf.Model,
		ReplyMode:    &pConf.ReplyMode,
		StdinReplace: &pConf.StdinReplace,
		OutputDir:    &pConf.Output.Dir,
		OutputPrefix: &pConf.Output.Prefix,
	})
}
