package photo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imageinput"
	"github.com/baalimago/clai/internal/photo/generic"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// query is one photo run: generate, then save and present each image.
type query struct {
	gen       imagegen.ImageGenerator
	req       imagegen.ImageRequest
	output    Output
	presenter present.Presenter
	stderr    io.Writer
	progress  *board.Status
	terminal  bool
	model     string
	took      time.Duration
}

func newQuery(ctx context.Context, deps CommandDeps, f *Flags, args []string) (*query, error) {
	if err := deps.check(f.ReplyStdin.Reply.Value()); err != nil {
		return nil, err
	}
	confDir, err := deps.ConfigPrep()
	if err != nil {
		return nil, err
	}
	pConf, err := loadConfig(deps, f, confDir)
	if err != nil {
		return nil, err
	}
	gen, err := deps.NewGenerator(imagegen.GeneratorConfig{Model: pConf.Model, ConfigDir: confDir})
	if err != nil {
		return nil, fmt.Errorf("failed to create image generator: %w", err)
	}
	req, err := buildRequest(ctx, deps, f, pConf, confDir, args)
	if err != nil {
		return nil, err
	}
	out := os.Stdout
	terminal := utils.IsTerminalWriter(out)
	raw := f.Raw.Value() || pConf.Raw
	presenter := deps.NewPresenter(present.Options{
		Out:      out,
		Terminal: terminal,
		Color:    terminal && !table.NoColor() && !raw,
		AutoOpen: f.Open.Value() || pConf.AutoOpenImages,
		Opener:   deps.NewOpener(pConf.ImageOpener),
	})
	return &query{
		gen:       gen,
		req:       req,
		output:    pConf.Output,
		presenter: presenter,
		stderr:    os.Stderr,
		progress:  board.NewStatus(out, terminal && !raw, "generating image"),
		model:     pConf.Model,
		terminal:  terminal,
	}, nil
}

// buildRequest is the prompt and input images of photo.1 and photo.4.
func buildRequest(ctx context.Context, deps CommandDeps, f *Flags, pConf Configurations, confDir string, args []string) (imagegen.ImageRequest, error) {
	stdin, err := utils.PipedStdin(ctx)
	if err != nil {
		return imagegen.ImageRequest{}, err
	}
	collected, err := deps.Collector.Collect(imageinput.CollectRequest{
		Images:       f.Images.Value(),
		Stdin:        stdin,
		Args:         args,
		StdinReplace: pConf.StdinReplace,
	})
	if err != nil {
		return imagegen.ImageRequest{}, fmt.Errorf("failed to collect photo inputs: %w", err)
	}
	req := imagegen.ImageRequest{Prompt: fmt.Sprintf(pConf.PromptFormat, collected.Prompt)}
	for _, img := range collected.Images {
		req.Inputs = append(req.Inputs, asset.Image{Data: img.Data, MIME: img.MIME})
	}
	if !pConf.ReplyMode {
		return req, nil
	}
	return withReply(deps, req, confDir)
}

func withReply(deps CommandDeps, req imagegen.ImageRequest, confDir string) (imagegen.ImageRequest, error) {
	prev, err := chat.LoadPrevQuery(confDir)
	if err != nil {
		return imagegen.ImageRequest{}, fmt.Errorf("failed to load previous query: %w", err)
	}
	replyContext, err := generic.ReplyContext(prev.Messages)
	if err != nil {
		return imagegen.ImageRequest{}, err
	}
	req.Prompt = replyContext + req.Prompt
	img, err := deps.PreviousImage(confDir)
	if errors.Is(err, ErrNoPreviousImage) {
		return req, nil
	}
	if err != nil {
		return imagegen.ImageRequest{}, fmt.Errorf("failed to load the previous image: %w", err)
	}
	req.Inputs = append(req.Inputs, img)
	return req, nil
}

func (q *query) Query(ctx context.Context) error {
	images, err := q.generate(ctx)
	if err != nil {
		return err
	}
	for _, img := range images {
		path, err := q.save(img.Image.Data)
		if err != nil {
			return err
		}
		if err := q.show(path, img.Image.Data); err != nil {
			return err
		}
	}
	return nil
}

// SuppressCompletionNotification keeps the completion bell off piped output.
func (q *query) SuppressCompletionNotification() bool {
	return !q.terminal
}

func (q *query) generate(ctx context.Context) ([]imagegen.GeneratedImage, error) {
	q.progress.Start()
	began := time.Now()
	images, err := q.gen.Generate(ctx, q.req)
	q.took = time.Since(began)
	q.progress.Stop()
	if err != nil {
		return nil, fmt.Errorf("failed to generate image: %w", err)
	}
	if len(images) == 0 {
		return nil, imagegen.ErrNoImage
	}
	return images, nil
}

// save writes data into the output dir, or into os.TempDir() with a notice
// when the output dir is not writable (photo.2).
func (q *query) save(data []byte) (string, error) {
	ext, err := extension(data)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s_%s.%s", q.output.Prefix, utils.RandomPrefix(), ext)
	saved, err := writeImage(q.output.Dir, name, data)
	if err == nil {
		return saved, nil
	}
	fallback, fallbackErr := writeImage(os.TempDir(), name, data)
	if fallbackErr != nil {
		return "", fmt.Errorf("failed to save image: %w", errors.Join(err, fallbackErr))
	}
	if _, werr := fmt.Fprintf(q.stderr, "notice: %v; saved to %s instead\n", err, fallback); werr != nil {
		return "", fmt.Errorf("write output-dir notice: %w", werr)
	}
	return fallback, nil
}

func writeImage(dir, name string, data []byte) (string, error) {
	path, err := filepath.Abs(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("output dir %s: %w", dir, err)
	}
	if err := utils.WriteFileAtomic(path, data, 0o644); err != nil {
		return "", fmt.Errorf("output dir %s is not writable: %w", dir, err)
	}
	return path, nil
}

// extension maps the image's sniffed MIME type to its file extension (README
// MIME-to-extension parameter).
func extension(data []byte) (string, error) {
	mime, err := asset.Sniff(data)
	if err != nil {
		return "", fmt.Errorf("generated image: %w", err)
	}
	ext, ok := asset.MIMEExtensions()[mime]
	if !ok {
		return "", fmt.Errorf("generated image: %w: no file extension for %s", asset.ErrUnsupportedMIME, mime)
	}
	return ext, nil
}

// show presents a saved file; a failed opener is a notice, not a failure (photo.3, U2).
func (q *query) show(path string, data []byte) error {
	handle, err := pub_models.ImagePart{Data: data}.Handle()
	if err != nil {
		return fmt.Errorf("present %s: %w", path, err)
	}
	err = q.presenter.Present(present.Shown{Handle: handle, Path: path, Details: present.DetailsOf(data, q.model, q.took)})
	var openFailed *present.OpenFailedError
	if errors.As(err, &openFailed) {
		if _, werr := fmt.Fprintf(q.stderr, "notice: %v\n", err); werr != nil {
			return fmt.Errorf("write open notice for %s: %w", path, werr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("present %s: %w", path, err)
	}
	return nil
}
