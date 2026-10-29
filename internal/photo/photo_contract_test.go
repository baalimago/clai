package photo_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/imagegentest"
	"github.com/baalimago/clai/internal/imageinput"
	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

var (
	pngData  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR photo-png")
	jpegData = []byte("\xff\xd8\xff\xe0\x00\x10JFIF photo-jpeg")
)

const promptFormat = "draw: '%v'"

type fakeOpener struct {
	paths []string
	err   error
}

func (o *fakeOpener) Open(path string) error {
	o.paths = append(o.paths, path)
	return o.err
}

type fixture struct {
	t       *testing.T
	confDir string
	outDir  string
	conf    photo.Configurations

	gen        *imagegentest.Fake
	genConfigs []imagegen.GeneratorConfig
	newGenErr  error
	// newGenErrs fails the generator for one model, which is how a fallback
	// candidate that cannot be built is played.
	newGenErrs map[string]error

	opener      *fakeOpener
	openerCmds  []string
	forceTerm   bool
	presentOpts []present.Options

	prevImage asset.Image
	prevErr   error
	prevDirs  []string

	configPreps int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:       t,
		confDir: t.TempDir(),
		outDir:  t.TempDir(),
		gen:     &imagegentest.Fake{Images: []imagegen.GeneratedImage{{Image: asset.Image{Data: jpegData, MIME: "image/jpeg"}}}},
		opener:  &fakeOpener{},
		prevErr: photo.ErrNoPreviousImage,
	}
	f.conf = photo.Configurations{
		Model:        "gpt-image-1",
		PromptFormat: promptFormat,
		Output:       photo.Output{Type: photo.UNSET, Dir: f.outDir, Prefix: "pfx"},
		ImageOpener:  "my-viewer",
	}
	f.setStdin(nil)
	return f
}

// setStdin points os.Stdin at a file holding data, or at the null device (a terminal to clai) for nil.
func (f *fixture) setStdin(data []byte) {
	f.t.Helper()
	path := os.DevNull
	if data != nil {
		path = filepath.Join(f.t.TempDir(), "stdin")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			f.t.Fatalf("write stdin: %v", err)
		}
	}
	in, err := os.Open(path)
	if err != nil {
		f.t.Fatalf("open stdin: %v", err)
	}
	orig := os.Stdin
	os.Stdin = in
	f.t.Cleanup(func() {
		os.Stdin = orig
		_ = in.Close()
	})
}

func (f *fixture) deps() photo.CommandDeps {
	return photo.CommandDeps{
		ConfigPrep: func() (string, error) {
			f.configPreps++
			return f.confDir, nil
		},
		LoadConfig: func(confDir string) (photo.Configurations, error) {
			if confDir != f.confDir {
				f.t.Fatalf("LoadConfig got conf dir %q, want %q", confDir, f.confDir)
			}
			return f.conf, nil
		},
		NewGenerator: func(cfg imagegen.GeneratorConfig) (imagegen.ImageGenerator, error) {
			f.genConfigs = append(f.genConfigs, cfg)
			if err := f.newGenErrs[cfg.Model]; err != nil {
				return nil, err
			}
			if f.newGenErr != nil {
				return nil, f.newGenErr
			}
			return f.gen, nil
		},
		Collector: imageinput.New(),
		NewPresenter: func(opts present.Options) present.Presenter {
			f.presentOpts = append(f.presentOpts, opts)
			if f.forceTerm {
				opts.Terminal = true
			}
			return present.New(opts)
		},
		NewOpener: func(command string) present.Opener {
			f.openerCmds = append(f.openerCmds, command)
			return f.opener
		},
		PreviousImage: func(confDir string) (asset.Image, error) {
			f.prevDirs = append(f.prevDirs, confDir)
			return f.prevImage, f.prevErr
		},
	}
}

type outcome struct {
	err            error
	stdout, stderr string
}

func runCommand(t *testing.T, deps photo.CommandDeps, args ...string) outcome {
	t.Helper()
	var o outcome
	o.stderr = testboil.CaptureStderr(t, func(t *testing.T) {
		o.stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			c := photo.Command(deps)
			if o.err = c.Flagset().Parse(args); o.err != nil {
				return
			}
			if o.err = c.Setup(context.Background()); o.err != nil {
				return
			}
			o.err = c.Run(context.Background())
		})
	})
	return o
}

func (f *fixture) run(args ...string) outcome {
	f.t.Helper()
	return runCommand(f.t, f.deps(), args...)
}

// exit runs the command through the dispatcher internal/cli uses, which owns the exit status.
func (f *fixture) exit(args ...string) (int, string) {
	f.t.Helper()
	var status int
	stderr := testboil.CaptureStderr(f.t, func(t *testing.T) {
		_ = testboil.CaptureStdout(t, func(t *testing.T) {
			commands := map[string]cmd.Command{"photo|p": photo.Command(f.deps())}
			status = cmd.Run(context.Background(), append([]string{"clai", "photo"}, args...), commands, "%v")
		})
	})
	return status, stderr
}

func (f *fixture) savedFiles(dir string) []string {
	f.t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "pfx_*"))
	if err != nil {
		f.t.Fatalf("glob: %v", err)
	}
	return matches
}

func (f *fixture) onlyRequest() imagegen.ImageRequest {
	f.t.Helper()
	if len(f.gen.Requests()) != 1 {
		f.t.Fatalf("generator called %d times, want 1", len(f.gen.Requests()))
	}
	return f.gen.Requests()[0]
}

func handleOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:6]
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func writeGlobalScope(t *testing.T, confDir, messages string) {
	t.Helper()
	writeFile(t, filepath.Join(confDir, "conversations", "globalScope.json"),
		[]byte(`{"created":"2026-10-01T00:00:00Z","id":"globalScope","messages":`+messages+`}`))
}

// Proves photo.1–3, AC1.
func TestPhoto_GeneratesAndPresents(t *testing.T) {
	f := newFixture(t)
	o := f.run("a", "fox")
	if o.err != nil {
		t.Fatalf("photo: %v (stderr %q)", o.err, o.stderr)
	}

	if want := []imagegen.GeneratorConfig{{Model: "gpt-image-1", ConfigDir: f.confDir}}; !reflect.DeepEqual(f.genConfigs, want) {
		t.Fatalf("NewGenerator configs = %+v, want %+v", f.genConfigs, want)
	}
	req := f.onlyRequest()
	if want := fmt.Sprintf(promptFormat, "a fox"); req.Prompt != want {
		t.Fatalf("prompt = %q, want %q", req.Prompt, want)
	}
	if len(req.Inputs) != 0 {
		t.Fatalf("inputs = %d, want none", len(req.Inputs))
	}
	files := f.savedFiles(f.outDir)
	if len(files) != 1 || filepath.Ext(files[0]) != ".jpg" {
		t.Fatalf("saved files = %v, want one pfx_*.jpg", files)
	}
	got, err := os.ReadFile(files[0])
	if err != nil || string(got) != string(jpegData) {
		t.Fatalf("saved bytes = %q, %v; want the generated JPEG", got, err)
	}
	if o.stdout != files[0]+"\n" {
		t.Fatalf("stdout = %q, want %q", o.stdout, files[0]+"\n")
	}
	if o.stderr != "" {
		t.Fatalf("stderr = %q, want empty", o.stderr)
	}
	if len(f.presentOpts) != 1 || f.presentOpts[0].Terminal || f.presentOpts[0].AutoOpen {
		t.Fatalf("presenter options = %+v, want one non-terminal presenter without auto-open", f.presentOpts)
	}
	if len(f.openerCmds) != 1 || f.openerCmds[0] != "my-viewer" {
		t.Fatalf("NewOpener commands = %q, want the photoConfig image-opener", f.openerCmds)
	}

	t.Run("each image gets its own file and line", func(t *testing.T) {
		f := newFixture(t)
		f.gen.Images = []imagegen.GeneratedImage{
			{Image: asset.Image{Data: pngData, MIME: "image/png"}},
			{Image: asset.Image{Data: jpegData, MIME: "image/jpeg"}},
		}
		o := f.run("-pp", "pfx", "two")
		if o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		files := f.savedFiles(f.outDir)
		if len(files) != 2 {
			t.Fatalf("saved files = %v, want 2", files)
		}
		lines := strings.Split(strings.TrimSuffix(o.stdout, "\n"), "\n")
		if len(lines) != 2 || filepath.Ext(lines[0]) != ".png" || filepath.Ext(lines[1]) != ".jpg" {
			t.Fatalf("stdout lines = %q, want a .png line then a .jpg line", lines)
		}
	})
}

// Proves photo.1.
func TestPhoto_GeneratorFails(t *testing.T) {
	f := newFixture(t)
	f.gen.Err = imagegen.ErrNoImage
	f.gen.Images = nil

	o := f.run("a", "fox")
	if !errors.Is(o.err, imagegen.ErrNoImage) {
		t.Fatalf("err = %v, want imagegen.ErrNoImage", o.err)
	}
	status, stderr := f.exit("a", "fox")
	if status != 1 || !strings.Contains(stderr, imagegen.ErrNoImage.Error()) {
		t.Fatalf("status %d, stderr %q; want 1 and the generator error", status, stderr)
	}
	if files := f.savedFiles(f.outDir); len(files) != 0 {
		t.Fatalf("saved files = %v, want none", files)
	}
}

// Proves photo.1, AC2.
func TestPhoto_ImgInputs(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	a := writeFile(t, filepath.Join(dir, "a.png"), append(append([]byte{}, pngData...), 'a'))
	b := writeFile(t, filepath.Join(dir, "b.png"), append(append([]byte{}, pngData...), 'b'))

	o := f.run("-img", a, "-image", b, "watercolour")
	if o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	req := f.onlyRequest()
	if len(req.Inputs) != 2 || string(req.Inputs[0].Data) != string(pngData)+"a" || string(req.Inputs[1].Data) != string(pngData)+"b" {
		t.Fatalf("inputs = %+v, want a.png then b.png", req.Inputs)
	}
	for _, in := range req.Inputs {
		if in.MIME != "image/png" {
			t.Fatalf("input MIME = %q, want image/png", in.MIME)
		}
	}
	if want := fmt.Sprintf(promptFormat, "watercolour"); req.Prompt != want {
		t.Fatalf("prompt = %q, want %q", req.Prompt, want)
	}

	t.Run("an -img that matches nothing fails before generation", func(t *testing.T) {
		f := newFixture(t)
		o := f.run("-img", filepath.Join(dir, "missing-*.png"), "watercolour")
		var noMatch *imageinput.NoMatchError
		if !errors.As(o.err, &noMatch) {
			t.Fatalf("err = %v, want *imageinput.NoMatchError", o.err)
		}
		if len(f.gen.Requests()) != 0 {
			t.Fatal("the generator must not run")
		}
	})
}

// Proves photo.1.
func TestPhoto_StdinImage(t *testing.T) {
	f := newFixture(t)
	f.setStdin(pngData)

	o := f.run("a", "fox")
	if o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	req := f.onlyRequest()
	if len(req.Inputs) != 1 || string(req.Inputs[0].Data) != string(pngData) || req.Inputs[0].MIME != "image/png" {
		t.Fatalf("inputs = %+v, want the stdin PNG", req.Inputs)
	}
	if want := fmt.Sprintf(promptFormat, "a fox"); req.Prompt != want {
		t.Fatalf("prompt = %q, want %q", req.Prompt, want)
	}

	t.Run("base64 stdin is an image too", func(t *testing.T) {
		f := newFixture(t)
		f.setStdin([]byte(base64.StdEncoding.EncodeToString(jpegData) + "\n"))
		if o := f.run("a", "fox"); o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		if req := f.onlyRequest(); len(req.Inputs) != 1 || string(req.Inputs[0].Data) != string(jpegData) {
			t.Fatalf("inputs = %+v, want the decoded JPEG", req.Inputs)
		}
	})

	t.Run("text stdin stays prompt text", func(t *testing.T) {
		f := newFixture(t)
		f.setStdin([]byte("a red barn"))
		if o := f.run(); o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		req := f.onlyRequest()
		if want := fmt.Sprintf(promptFormat, "a red barn"); req.Prompt != want || len(req.Inputs) != 0 {
			t.Fatalf("prompt %q with %d inputs, want %q and none", req.Prompt, len(req.Inputs), want)
		}
	})
}

// Proves photo.4, AC3.
func TestPhoto_ReplyAppendsPreviousImage(t *testing.T) {
	f := newFixture(t)
	legacy := base64.StdEncoding.EncodeToString(pngData)
	writeGlobalScope(t, f.confDir, `[{"role":"user","content":[{"type":"text","text":"a cat"},`+
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,`+legacy+`"}}]},`+
		`{"role":"assistant","content":"a cat it is"}]`)
	f.prevImage = asset.Image{Data: jpegData, MIME: "image/jpeg"}
	f.prevErr = nil
	in := writeFile(t, filepath.Join(t.TempDir(), "in.png"), pngData)

	o := f.run("-re", "-img", in, "make", "it", "purple")
	if o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	req := f.onlyRequest()
	if len(req.Inputs) != 2 || string(req.Inputs[0].Data) != string(pngData) || !reflect.DeepEqual(req.Inputs[1], f.prevImage) {
		t.Fatalf("inputs = %+v, want the -img input, then the previous image", req.Inputs)
	}
	if !reflect.DeepEqual(f.prevDirs, []string{f.confDir}) {
		t.Fatalf("PreviousImage conf dirs = %q, want [%q]", f.prevDirs, f.confDir)
	}
	if label := "[image " + handleOf(pngData) + "]"; !strings.Contains(req.Prompt, label) {
		t.Fatalf("prompt %q lacks the label %q", req.Prompt, label)
	}
	if strings.Contains(req.Prompt, legacy) || strings.Contains(req.Prompt, "base64") {
		t.Fatalf("prompt holds image bytes: %q", req.Prompt)
	}
	if !strings.HasSuffix(req.Prompt, fmt.Sprintf(promptFormat, "make it purple")) || !strings.Contains(req.Prompt, "a cat it is") {
		t.Fatalf("prompt %q, want the previous messages, then the formatted prompt", req.Prompt)
	}
}

// Proves photo.4.
func TestPhoto_ReplyNoPreviousImage(t *testing.T) {
	f := newFixture(t)
	writeGlobalScope(t, f.confDir, `[{"role":"user","content":"a red fox"},{"role":"assistant","content":"here is a fox"}]`)

	o := f.run("-re", "make", "it", "purple")
	if o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	req := f.onlyRequest()
	if len(req.Inputs) != 0 {
		t.Fatalf("inputs = %+v, want none", req.Inputs)
	}
	// The reply context as clai built it before image support.
	want := "You will be given a serie of messages from different roles, then a prompt descibing what to do with these messages. " +
		"Between the messages and the prompt, there will be this line: '-------------'." +
		"The format is json with the structure {\"role\": \"<role>\", \"content\": \"<content>\"}. " +
		"The roles are 'system' and 'user'. " +
		"Messages:\n" +
		`[{"content":"a red fox","role":"user"},{"content":"here is a fox","role":"assistant"}]` +
		"\n-------------\n" +
		fmt.Sprintf(promptFormat, "make it purple")
	if req.Prompt != want {
		t.Fatalf("prompt =\n%q\nwant\n%q", req.Prompt, want)
	}

	t.Run("any other PreviousImage error fails the command", func(t *testing.T) {
		f := newFixture(t)
		boom := errors.New("asset store unreadable")
		f.prevErr = boom
		o := f.run("-re", "again")
		if !errors.Is(o.err, boom) {
			t.Fatalf("err = %v, want %v", o.err, boom)
		}
		if len(f.gen.Requests()) != 0 {
			t.Fatal("the generator must not run")
		}
	})
}

// Proves photo.2.
func TestPhoto_OutputFallback(t *testing.T) {
	f := newFixture(t)
	blocker := writeFile(t, filepath.Join(t.TempDir(), "not-a-dir"), []byte("x"))
	f.conf.Output.Dir = filepath.Join(blocker, "pictures")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	o := f.run("a", "fox")
	if o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	files := f.savedFiles(tmp)
	if len(files) != 1 {
		t.Fatalf("files in TempDir = %v, want 1", files)
	}
	if o.stdout != files[0]+"\n" {
		t.Fatalf("stdout = %q, want the fallback path", o.stdout)
	}
	if !strings.Contains(o.stderr, files[0]) || !strings.Contains(o.stderr, f.conf.Output.Dir) {
		t.Fatalf("stderr %q must name the output dir and the fallback file", o.stderr)
	}

	t.Run("an unwritable TempDir too fails", func(t *testing.T) {
		f := newFixture(t)
		f.conf.Output.Dir = filepath.Join(blocker, "pictures")
		t.Setenv("TMPDIR", filepath.Join(blocker, "tmp"))
		if o := f.run("a", "fox"); o.err == nil {
			t.Fatal("want an error when no directory is writable")
		}
	})
}

// Proves photo.3, AC4.
func TestPhoto_PipedBarePath(t *testing.T) {
	f := newFixture(t)
	o := f.run("a", "fox")
	if o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	pattern := "^" + regexp.QuoteMeta(f.outDir+string(filepath.Separator)) + `pfx_[A-Za-z0-9]{10}\.jpg\n$`
	if !regexp.MustCompile(pattern).MatchString(o.stdout) {
		t.Fatalf("stdout = %q, want %s", o.stdout, pattern)
	}
	if strings.Contains(o.stdout, "\x1b") {
		t.Fatalf("stdout holds escape codes: %q", o.stdout)
	}
}

// Proves photo.3.
func TestPhoto_OpenFlag(t *testing.T) {
	twoImages := []imagegen.GeneratedImage{
		{Image: asset.Image{Data: pngData, MIME: "image/png"}},
		{Image: asset.Image{Data: jpegData, MIME: "image/jpeg"}},
	}

	t.Run("-open on a terminal opens each image once", func(t *testing.T) {
		f := newFixture(t)
		f.forceTerm = true
		f.gen.Images = twoImages
		o := f.run("-open", "a", "fox")
		if o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		files := strings.Split(strings.TrimSuffix(o.stdout, "\n"), "\n")
		if !reflect.DeepEqual(f.opener.paths, files) {
			t.Fatalf("opened %q, want %q", f.opener.paths, files)
		}
		if len(f.presentOpts) != 1 || !f.presentOpts[0].AutoOpen || f.presentOpts[0].Opener != f.opener {
			t.Fatalf("presenter options = %+v, want AutoOpen with the injected opener", f.presentOpts)
		}
	})

	t.Run("auto-open-images in photoConfig opens without the flag", func(t *testing.T) {
		f := newFixture(t)
		f.forceTerm = true
		f.conf.AutoOpenImages = true
		if o := f.run("a", "fox"); o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		if len(f.opener.paths) != 1 {
			t.Fatalf("opened %q, want one image", f.opener.paths)
		}
	})

	t.Run("not a terminal never opens", func(t *testing.T) {
		f := newFixture(t)
		f.gen.Images = twoImages
		if o := f.run("-open", "a", "fox"); o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		if len(f.opener.paths) != 0 {
			t.Fatalf("opened %q on a pipe", f.opener.paths)
		}
	})

	t.Run("a failed opener is a notice and the command succeeds", func(t *testing.T) {
		f := newFixture(t)
		f.forceTerm = true
		f.opener.err = errors.New("viewer crashed")
		o := f.run("-open", "a", "fox")
		if o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		if !strings.Contains(o.stderr, "viewer crashed") || len(f.savedFiles(f.outDir)) != 1 {
			t.Fatalf("stderr %q, files %v; want the opener notice and the saved image", o.stderr, f.savedFiles(f.outDir))
		}
	})
}

// Proves photo.5, AC5.
func TestPhoto_URLModeRemoved(t *testing.T) {
	f := newFixture(t)
	f.conf.Output.Type = photo.URL

	o := f.run("a", "fox")
	var removed *photo.URLOutputRemovedError
	if !errors.As(o.err, &removed) {
		t.Fatalf("err = %v, want *photo.URLOutputRemovedError", o.err)
	}
	if !strings.Contains(o.err.Error(), `"local"`) {
		t.Fatalf("error %q must tell the user to set \"local\"", o.err)
	}
	if len(f.genConfigs) != 0 || len(f.gen.Requests()) != 0 {
		t.Fatal("NewGenerator must not be called")
	}

	t.Run("local saves like unset", func(t *testing.T) {
		f := newFixture(t)
		f.conf.Output.Type = photo.LOCAL
		if o := f.run("a", "fox"); o.err != nil || len(f.savedFiles(f.outDir)) != 1 {
			t.Fatalf("err %v, files %v; want one saved image", o.err, f.savedFiles(f.outDir))
		}
	})

	t.Run("an unknown output type fails", func(t *testing.T) {
		f := newFixture(t)
		f.conf.Output.Type = "bogus"
		if o := f.run("a", "fox"); o.err == nil || len(f.genConfigs) != 0 {
			t.Fatalf("err %v, NewGenerator calls %d; want an error before the generator", o.err, len(f.genConfigs))
		}
	})
}

// Proves photo.1.
func TestPhoto_UnknownModel(t *testing.T) {
	f := newFixture(t)
	f.conf.Model = "nope"
	f.newGenErr = &imagegen.UnknownImageModelError{Model: "nope"}

	o := f.run("a", "fox")
	var unknown *imagegen.UnknownImageModelError
	if !errors.As(o.err, &unknown) || unknown.Model != "nope" {
		t.Fatalf("err = %v, want *imagegen.UnknownImageModelError{nope}", o.err)
	}
	status, stderr := f.exit("a", "fox")
	if status != 1 || !strings.Contains(stderr, f.newGenErr.Error()) {
		t.Fatalf("status %d, stderr %q; want 1 and the unknown-model error", status, stderr)
	}
	if len(f.gen.Requests()) != 0 {
		t.Fatal("no generation may run")
	}
}

// Proves photo.6.
func TestPhoto_NoAnimationWhenPiped(t *testing.T) {
	f := newFixture(t)
	// Several animation frames' worth of generation time.
	f.gen.Latency = 3 * time.Second / 60

	o := f.run("a", "fox")
	if o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	files := f.savedFiles(f.outDir)
	if len(files) != 1 || o.stdout != files[0]+"\n" {
		t.Fatalf("stdout = %q, want only the presented line", o.stdout)
	}
}

// Proves photo.7, AC6.
func TestPhoto_Unconfigured(t *testing.T) {
	full := func(f *fixture) photo.CommandDeps { return f.deps() }
	cases := []struct {
		name  string
		deps  func(f *fixture) photo.CommandDeps
		args  []string
		valid bool
	}{
		{"no dependencies", func(*fixture) photo.CommandDeps { return photo.CommandDeps{} }, []string{"x"}, false},
		{"no NewGenerator", func(f *fixture) photo.CommandDeps { d := full(f); d.NewGenerator = nil; return d }, []string{"x"}, false},
		{"no Collector", func(f *fixture) photo.CommandDeps { d := full(f); d.Collector = nil; return d }, []string{"x"}, false},
		{"no NewPresenter", func(f *fixture) photo.CommandDeps { d := full(f); d.NewPresenter = nil; return d }, []string{"x"}, false},
		{"no NewOpener", func(f *fixture) photo.CommandDeps { d := full(f); d.NewOpener = nil; return d }, []string{"x"}, false},
		{"-re without PreviousImage", func(f *fixture) photo.CommandDeps { d := full(f); d.PreviousImage = nil; return d }, []string{"-re", "x"}, false},
		{"no PreviousImage without -re", func(f *fixture) photo.CommandDeps { d := full(f); d.PreviousImage = nil; return d }, []string{"x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			deps := tc.deps(f)
			if deps.ConfigPrep == nil {
				deps.ConfigPrep = func() (string, error) { f.configPreps++; return f.confDir, nil }
			}
			o := runCommand(t, deps, tc.args...)
			if tc.valid {
				if o.err != nil {
					t.Fatalf("photo: %v", o.err)
				}
				return
			}
			if !errors.Is(o.err, photo.ErrPhotoUnconfigured) {
				t.Fatalf("err = %v, want ErrPhotoUnconfigured", o.err)
			}
			if f.configPreps != 0 {
				t.Fatal("ConfigPrep ran before the dependency check")
			}
		})
	}
}

// Proves photo.8, I9, AC6.
func TestPhoto_OnlyInjectedGenerator(t *testing.T) {
	f := newFixture(t)
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("OPENAI_API_KEY", "")
	f.confDir = confDir
	writeGlobalScope(t, confDir, `[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,`+
		base64.StdEncoding.EncodeToString(pngData)+`"}}]}]`)

	deps := f.deps()
	deps.ConfigPrep = utils.GetClaiConfigDir
	deps.LoadConfig = func(confDir string) (photo.Configurations, error) {
		return utils.LoadConfigFromFile(confDir, "photoConfig.json", nil, &photo.DEFAULT)
	}
	o := runCommand(t, deps, "-re", "-pd", f.outDir, "-pp", "pfx", "a", "fox")
	if o.err != nil {
		t.Fatalf("photo: %v (stderr %q)", o.err, o.stderr)
	}
	if want := []imagegen.GeneratorConfig{{Model: photo.DEFAULT.Model, ConfigDir: confDir}}; !reflect.DeepEqual(f.genConfigs, want) {
		t.Fatalf("NewGenerator configs = %+v, want exactly %+v", f.genConfigs, want)
	}
	if len(f.gen.Requests()) != 1 || len(f.savedFiles(f.outDir)) != 1 {
		t.Fatalf("generations %d, files %v; want one of each", len(f.gen.Requests()), f.savedFiles(f.outDir))
	}
	if _, err := os.Stat(filepath.Join(confDir, "conversations", "assets")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conversations/assets: stat err = %v, want it absent", err)
	}
}

// TestPhoto_OutputDirExpandsHome proves photo.2: output.dir is a user path, so
// a config-file "~/Pictures" lands in the home's Pictures, and a path whose
// home cannot resolve fails before any generation.
func TestPhoto_OutputDirExpandsHome(t *testing.T) {
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
			outDir := filepath.Join(tc.home, "Pictures")
			if !tc.wantErr {
				if err := os.MkdirAll(outDir, 0o755); err != nil {
					t.Fatalf("mkdir output dir: %v", err)
				}
			}
			f := newFixture(t)
			f.conf.Output.Dir = "~/Pictures"

			o := f.run("a", "fox")
			if tc.wantErr {
				if o.err == nil {
					t.Fatal("photo with an unresolvable ~ succeeded, want an error")
				}
				if requests := f.gen.Requests(); len(requests) != 0 {
					t.Fatalf("generator called %d times before the output dir failed", len(requests))
				}
				return
			}
			if o.err != nil {
				t.Fatalf("photo: %v (stderr %q)", o.err, o.stderr)
			}
			if files := f.savedFiles(outDir); len(files) != 1 {
				t.Fatalf("files in %s = %v, want one", outDir, files)
			}
		})
	}
}

type failingPresenter struct{ err error }

func (p failingPresenter) Present(present.Shown) error { return p.err }

func (p failingPresenter) Open(present.Shown) error { return p.err }

func (failingPresenter) Rows(present.Shown) []string { return nil }

// Proves photo.1–3 failure paths.
func TestPhoto_FailurePaths(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		arrange func(f *fixture, deps *photo.CommandDeps)
		want    error
	}{
		{"ConfigPrep fails", func(_ *fixture, d *photo.CommandDeps) {
			d.ConfigPrep = func() (string, error) { return "", boom }
		}, boom},
		{"LoadConfig fails", func(_ *fixture, d *photo.CommandDeps) {
			d.LoadConfig = func(string) (photo.Configurations, error) { return photo.Configurations{}, boom }
		}, boom},
		{"no image", func(f *fixture, _ *photo.CommandDeps) { f.gen.Images = nil }, imagegen.ErrNoImage},
		{"bytes that are not an image", func(f *fixture, _ *photo.CommandDeps) {
			f.gen.Images = []imagegen.GeneratedImage{{Image: asset.Image{Data: []byte("text"), MIME: "image/png"}}}
		}, asset.ErrNotImage},
		{"presenter fails", func(_ *fixture, d *photo.CommandDeps) {
			d.NewPresenter = func(present.Options) present.Presenter { return failingPresenter{err: boom} }
		}, boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			deps := f.deps()
			tc.arrange(f, &deps)
			o := runCommand(t, deps, "a", "fox")
			if !errors.Is(o.err, tc.want) {
				t.Fatalf("err = %v, want %v", o.err, tc.want)
			}
		})
	}

	t.Run("every image type gets its README extension", func(t *testing.T) {
		f := newFixture(t)
		f.gen.Images = []imagegen.GeneratedImage{
			{Image: asset.Image{Data: []byte("GIF89a gif"), MIME: "image/gif"}},
			{Image: asset.Image{Data: []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), MIME: "image/webp"}},
		}
		o := f.run("a", "fox")
		if o.err != nil {
			t.Fatalf("photo: %v", o.err)
		}
		lines := strings.Split(strings.TrimSuffix(o.stdout, "\n"), "\n")
		if len(lines) != 2 || filepath.Ext(lines[0]) != ".gif" || filepath.Ext(lines[1]) != ".webp" {
			t.Fatalf("stdout lines = %q, want a .gif then a .webp file", lines)
		}
	})
}

type recordingPresenter struct{ shown []present.Shown }

func (p *recordingPresenter) Present(img present.Shown) error {
	p.shown = append(p.shown, img)
	return nil
}

func (p *recordingPresenter) Open(present.Shown) error { return nil }

func (p *recordingPresenter) Rows(present.Shown) []string { return nil }

// Proves photo.3: the presented line carries the photo model, the file size and the
// generation time.
func TestPhoto_PresentsDetails(t *testing.T) {
	f := newFixture(t)
	f.gen.Latency = time.Millisecond
	rec := &recordingPresenter{}
	deps := f.deps()
	deps.NewPresenter = func(present.Options) present.Presenter { return rec }
	if o := runCommand(t, deps, "a", "fox"); o.err != nil {
		t.Fatalf("photo: %v", o.err)
	}
	if len(rec.shown) != 1 {
		t.Fatalf("presented %d images, want 1", len(rec.shown))
	}
	d := rec.shown[0].Details
	if d.Model != "gpt-image-1" || d.Bytes != len(jpegData) || d.Elapsed < time.Millisecond {
		t.Fatalf("details = %+v, want model gpt-image-1, %d bytes and the generation time", d, len(jpegData))
	}
}

// Proves the media alias seam: photoConfig "model" may name a verb, and a
// candidate this machine cannot compose is replaced by the next one
// (architecture/model-aliases.md).
func TestPhoto_ModelAliasFallsBackToTheNextModel(t *testing.T) {
	f := newFixture(t)
	f.conf.Model = "strong"
	f.conf.ModelAliases = modelalias.Table{"strong": {"no-such-image-model", "gpt-image-1"}}
	f.newGenErrs = map[string]error{"no-such-image-model": errors.New("no such image model")}

	o := f.run("a", "fox")
	if o.err != nil {
		t.Fatalf("photo: %v (stderr %q)", o.err, o.stderr)
	}
	want := []imagegen.GeneratorConfig{
		{Model: "no-such-image-model", ConfigDir: f.confDir},
		{Model: "gpt-image-1", ConfigDir: f.confDir},
	}
	if !reflect.DeepEqual(f.genConfigs, want) {
		t.Fatalf("NewGenerator configs = %+v, want %+v", f.genConfigs, want)
	}
	files := f.savedFiles(f.outDir)
	if len(files) != 1 {
		t.Fatalf("saved files = %v, want one", files)
	}
	if o.stdout != files[0]+"\n" {
		t.Fatalf("stdout = %q, want the saved path only", o.stdout)
	}
	for _, want := range []string{"▸ model no-such-image-model unavailable · ", "trying gpt-image-1"} {
		if !strings.Contains(o.stderr, want) {
			t.Fatalf("stderr = %q, want it to contain %q", o.stderr, want)
		}
	}
}

// Proves that a ladder whose every candidate cannot be composed fails the run
// and names each model, saving nothing.
func TestPhoto_ExhaustedModelAliasNamesEveryModel(t *testing.T) {
	f := newFixture(t)
	f.conf.Model = "strong"
	f.conf.ModelAliases = modelalias.Table{"strong": {"no-such-image-model", "another-unknown-model"}}
	f.newGenErrs = map[string]error{
		"no-such-image-model":   errors.New("no such image model"),
		"another-unknown-model": errors.New("still no such image model"),
	}

	o := f.run("a", "fox")
	if o.err == nil {
		t.Fatal("photo: no error, want every candidate reported")
	}
	for _, want := range []string{"no-such-image-model", "another-unknown-model"} {
		if !strings.Contains(o.err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", o.err, want)
		}
	}
	if files := f.savedFiles(f.outDir); len(files) != 0 {
		t.Fatalf("saved files = %v, want none", files)
	}
}

// Proves the replay guard: a run that already saved an image reports its
// failure as final, so the next model never re-saves it
// (architecture/model-aliases.md).
func TestPhoto_ASavedImageIsNeverReplayedOnTheNextModel(t *testing.T) {
	boom := errors.New("boom")
	f := newFixture(t)
	f.conf.Model = "strong"
	f.conf.ModelAliases = modelalias.Table{"strong": {"gpt-image-1", "gpt-image-1-mini"}}
	deps := f.deps()
	deps.NewPresenter = func(present.Options) present.Presenter { return failingPresenter{err: boom} }

	o := runCommand(t, deps, "a", "fox")
	if !errors.Is(o.err, boom) {
		t.Fatalf("err = %v, want the presenter failure", o.err)
	}
	if want := []imagegen.GeneratorConfig{{Model: "gpt-image-1", ConfigDir: f.confDir}}; !reflect.DeepEqual(f.genConfigs, want) {
		t.Fatalf("NewGenerator configs = %+v, want only the model that ran %+v", f.genConfigs, want)
	}
	if files := f.savedFiles(f.outDir); len(files) != 1 {
		t.Fatalf("saved files = %v, want exactly one", files)
	}
	if strings.Contains(o.stderr, "trying gpt-image-1-mini") {
		t.Fatalf("stderr = %q, want no fallback after a saved image", o.stderr)
	}
}
