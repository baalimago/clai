package text

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/route"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var queryTestPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 16)...)

func parsedQueryFlags(t *testing.T, args ...string) internal.TextFlags {
	t.Helper()
	tf := internal.NewTextFlags()
	c := newTextCommand("query", "", "", &tf, nil)
	if err := c.Flagset().Parse(args); err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	return tf
}

func TestQueryFlags_Image(t *testing.T) {
	tf := parsedQueryFlags(t, "-img", "a.png", "-img", "b/*.png", "-open", "-pm", "m", "hi")
	if got := tf.QueryText.Images.Value(); !slices.Equal(got, []string{"a.png", "b/*.png"}) {
		t.Fatalf("-img = %v, want both values in order", got)
	}
	if !tf.QueryText.OpenImages.Value() || tf.AgentText.MediaTools.PhotoModel.Value() != "m" {
		t.Fatalf("parsed flags: open=%v pm=%q", tf.QueryText.OpenImages.Value(), tf.AgentText.MediaTools.PhotoModel.Value())
	}

	conf := Default
	ApplyFlagOverrides(&conf, tf)
	if !slices.Equal(conf.ImageInputs, []string{"a.png", "b/*.png"}) {
		t.Fatalf("overrides: inputs=%v", conf.ImageInputs)
	}

	plain := Default
	ApplyFlagOverrides(&plain, parsedQueryFlags(t, "hi"))
	if plain.ImageInputs != nil {
		t.Fatalf("no image flags must keep the defaults: %+v", plain)
	}
}

func TestImagePresenterOptions(t *testing.T) {
	opts := imagePresenterOptions(io.Discard, false, &Default, parsedQueryFlags(t, "-open", "hi"), photo.Configurations{})
	if !opts.AutoOpen || opts.Opener == nil {
		t.Fatalf("-open: AutoOpen=%v Opener=%v, want auto-open with an opener", opts.AutoOpen, opts.Opener)
	}
	opts = imagePresenterOptions(io.Discard, false, &Default, parsedQueryFlags(t, "hi"), photo.Configurations{AutoOpenImages: true, ImageOpener: "viewer"})
	if !opts.AutoOpen || opts.Opener == nil {
		t.Fatalf("photoConfig auto-open: AutoOpen=%v Opener=%v, want auto-open with an opener", opts.AutoOpen, opts.Opener)
	}
	opts = imagePresenterOptions(io.Discard, false, &Default, parsedQueryFlags(t, "hi"), photo.Configurations{})
	if opts.AutoOpen || opts.Opener == nil {
		t.Fatalf("defaults: AutoOpen=%v Opener=%v, want no auto-open and the platform opener", opts.AutoOpen, opts.Opener)
	}
}

func TestImageGenSetup(t *testing.T) {
	confDir := t.TempDir()

	t.Run("the chat vendor's image backend wins over a foreign photo model", func(t *testing.T) {
		for _, tc := range []struct {
			vendor          route.ChatVendor
			photo, pm, want string
		}{
			{route.Gemini, "gpt-image-1", "", gemini.DefaultImageModel},
			{route.OpenRouter, "gpt-image-1", "", "or:" + openrouter.DefaultImageModel},
			{route.OpenAI, "gpt-image-1-mini", "", "gpt-image-1-mini"},
			{route.OtherVendor, "gpt-image-1", "", "gpt-image-1"},
			{route.Gemini, "gpt-image-1", "or:x", "or:x"},
		} {
			conf := Default
			args := []string{"-t", "generate_image", "hi"}
			if tc.pm != "" {
				args = append([]string{"-pm", tc.pm}, args...)
			}
			gen := imageGenSetup(confDir, photo.Configurations{Model: tc.photo}, &conf, parsedQueryFlags(t, args...), tc.vendor)
			if gen.Config.Model != tc.want {
				t.Fatalf("vendor %q, photo %q, -pm %q: model = %q; want %q", tc.vendor, tc.photo, tc.pm, gen.Config.Model, tc.want)
			}
		}
		conf := Default
		gen := imageGenSetup(confDir, photo.Configurations{}, &conf, parsedQueryFlags(t, "-t", "generate_image", "hi"), route.OpenAI)
		if gen.Config.Model != openai.DefaultImageModel {
			t.Fatalf("no photo model: model = %q; want the vendor default", gen.Config.Model)
		}
	})

	t.Run("an explicit '*' in the profile survives -t", func(t *testing.T) {
		conf := Default
		conf.UseTools, conf.RequestedToolGlobs = true, []string{"*"}
		setupToolConfig(&conf, "ls")
		gen := imageGenSetup(confDir, photo.Configurations{Model: "p"}, &conf, parsedQueryFlags(t, "-t", "ls", "hi"), route.OtherVendor)
		if !gen.Requested {
			t.Fatal("profile '*' plus -t ls must request generate_image")
		}
		implicit := Default
		implicit.UseTools = true
		gen = imageGenSetup(confDir, photo.Configurations{Model: "p"}, &implicit, parsedQueryFlags(t, "hi"), route.OtherVendor)
		if gen.Requested {
			t.Fatal("use-tools without a list must leave generate_image opt-in")
		}
	})

	t.Run("a profile's tools select it like -t", func(t *testing.T) {
		for _, tc := range []struct {
			useTools bool
			globs    []string
			want     bool
		}{
			{true, []string{"ls", "*_image"}, true},
			{true, []string{"generate_image"}, true},
			{true, []string{"*cmd*", "mcp_slivingdoc*"}, false},
			{false, []string{"*_image"}, false},
		} {
			conf := Default
			conf.UseTools, conf.RequestedToolGlobs = tc.useTools, tc.globs
			gen := imageGenSetup(confDir, photo.Configurations{Model: "p"}, &conf, parsedQueryFlags(t, "hi"), route.OtherVendor)
			if gen.Requested != tc.want {
				t.Fatalf("UseTools %v, tools %v: Requested = %v; want %v", tc.useTools, tc.globs, gen.Requested, tc.want)
			}
		}
	})

	t.Run("selection takes the photoConfig model", func(t *testing.T) {
		for _, selection := range []string{"*", "generate_image", "ls, generate_image", "*_image", "gen*", "*image*", "view_image,generate_*"} {
			conf := Default
			gen := imageGenSetup(confDir, photo.Configurations{Model: "gpt-image-1"}, &conf, parsedQueryFlags(t, "-t", selection, "hi"), route.OtherVendor)
			want := imagegen.GeneratorConfig{Model: "gpt-image-1", ConfigDir: confDir}
			if !gen.Requested || gen.Config != want {
				t.Fatalf("-t %q: gen = %+v; want %+v", selection, gen, want)
			}
		}
	})

	t.Run("-pm overrides photoConfig", func(t *testing.T) {
		conf := Default
		gen := imageGenSetup(confDir, photo.Configurations{Model: "p"}, &conf, parsedQueryFlags(t, "-t", "*", "-pm", "or:x", "hi"), route.OtherVendor)
		if gen.Config.Model != "or:x" {
			t.Fatalf("model = %q; want -pm's or:x", gen.Config.Model)
		}
	})
}

func TestNewImageTurn_wiresTheQueryCommand(t *testing.T) {
	confDir := t.TempDir()
	built := 0
	deps := QueryCommandDeps{NewImageGenerator: func(imagegen.GeneratorConfig) (imagegen.ImageGenerator, error) {
		built++
		return nil, errors.New("unused")
	}}
	conf := Default
	conf.Out = &bytes.Buffer{}
	turn, err := newImageTurn(confDir, &conf, parsedQueryFlags(t, "-t", "generate_image", "hi"), deps, route.OtherVendor)
	if err != nil {
		t.Fatalf("newImageTurn: %v", err)
	}
	if err := turn.check(); err != nil {
		t.Fatalf("incomplete turn: %v", err)
	}
	if turn.NewGenerator == nil || built != 0 || !turn.Generation.Requested {
		t.Fatalf("generator wiring: set=%v built=%d requested=%v", turn.NewGenerator != nil, built, turn.Generation.Requested)
	}
	turn.Progress.Start()
	turn.Progress.Stop()
	if conf.Out.(*bytes.Buffer).Len() != 0 {
		t.Fatalf("the progress drew on a non-terminal output: %q", conf.Out.(*bytes.Buffer).String())
	}
	ref, err := turn.Store.Put(queryTestPNG, "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if path, err := turn.Store.Path(ref); err != nil || path != filepath.Join(confDir, "conversations", "assets", ref) {
		t.Fatalf("store path = %q, %v; want the README asset directory", path, err)
	}
}

func TestNewImageTurn_readsPhotoConfig(t *testing.T) {
	confDir := t.TempDir()
	calls := 0
	deps := QueryCommandDeps{PhotoConfig: func(dir string) (photo.Configurations, error) {
		calls++
		if dir != confDir {
			t.Fatalf("PhotoConfig dir = %q, want %q", dir, confDir)
		}
		return photo.Configurations{Model: "photo-model", AutoOpenImages: true, ImageOpener: "viewer"}, nil
	}}
	conf := Default
	conf.Out = &bytes.Buffer{}
	turn, err := newImageTurn(confDir, &conf, parsedQueryFlags(t, "-t", "generate_image", "hi"), deps, route.OtherVendor)
	if err != nil {
		t.Fatalf("newImageTurn: %v", err)
	}
	if calls != 1 {
		t.Fatalf("PhotoConfig calls = %d, want one load shared by the presenter and the model", calls)
	}
	if turn.Generation.Config.Model != "photo-model" {
		t.Fatalf("model = %q, want photoConfig's", turn.Generation.Config.Model)
	}
}

func TestNewImageTurn_photoConfigError(t *testing.T) {
	boom := errors.New("boom")
	deps := QueryCommandDeps{PhotoConfig: func(string) (photo.Configurations, error) {
		return photo.Configurations{}, boom
	}}
	conf := Default
	conf.Out = &bytes.Buffer{}
	_, err := newImageTurn(t.TempDir(), &conf, parsedQueryFlags(t, "hi"), deps, route.OtherVendor)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

type notATextQuerier struct{}

func (notATextQuerier) Query(context.Context) error { return nil }

func TestAttachImageTurn_needsATextQuerier(t *testing.T) {
	conf := Default
	conf.Out = &bytes.Buffer{}
	err := attachImageTurn(t.TempDir(), &conf, parsedQueryFlags(t, "hi"), QueryCommandDeps{}, models.Querier(notATextQuerier{}))
	if err == nil || !strings.Contains(err.Error(), "not a text querier") {
		t.Fatalf("err = %v, want the text-querier error", err)
	}
	q := &Querier[*MockQuerier]{}
	if err := attachImageTurn(t.TempDir(), &conf, parsedQueryFlags(t, "hi"), QueryCommandDeps{}, q); err != nil || q.imageTurn == nil {
		t.Fatalf("attach: err = %v, turn set = %v", err, q.imageTurn != nil)
	}
	q.setImageTurn(nil)
	if q.imageTurn != nil {
		t.Fatal("setImageTurn(nil) must restore the default")
	}
}

func TestSetupToolConfig_generateImageIsKnown(t *testing.T) {
	for _, selection := range []string{"generate_image", " generate_image ,date", "*_image", "gen*"} {
		conf := Configurations{}
		var warned string
		_, warned = captureStdoutStderrText(t, func() { setupToolConfig(&conf, selection) })
		if !conf.UseTools || !slices.Contains(conf.RequestedToolGlobs, "generate_image") {
			t.Fatalf("-t %q: UseTools=%v globs=%v", selection, conf.UseTools, conf.RequestedToolGlobs)
		}
		if strings.Contains(warned, "unknown tool") || strings.Contains(warned, "disabling tools") {
			t.Fatalf("-t %q warned: %q", selection, warned)
		}
	}
}

func TestToolSelection_starSelectsBothImageTools(t *testing.T) {
	conf := Configurations{}
	setupToolConfig(&conf, "*")
	if !conf.UseTools || conf.RequestedToolGlobs != nil {
		t.Fatalf("'*' must select every registry tool: %+v", conf)
	}
	tools.Init()
	viewImage, ok := tools.Registry.Get("view_image")
	if !ok {
		t.Fatal("view_image is not a registered native tool")
	}
	if _, ok := viewImage.(pub_models.PartsCaller); !ok {
		t.Fatalf("view_image %T is not a PartsCaller", viewImage)
	}
	if !selectsGenerateImage("*") || selectsGenerateImage("ls", "view_image") {
		t.Fatal("'*' must select generate_image, and names that do not match it may not")
	}
	star := Configurations{}
	setupToolConfig(&star, "*_image")
	if !slices.Contains(star.RequestedToolGlobs, "view_image") || !slices.Contains(star.RequestedToolGlobs, "generate_image") {
		t.Fatalf("'*_image' must select both image tools, got %v", star.RequestedToolGlobs)
	}
}

func TestSetupInitialChat_collectsImages(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "cat.png")
	if err := os.WriteFile(img, queryTestPNG, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	conf := Default
	conf.ConfigDir = t.TempDir()
	conf.ImageInputs = []string{img}
	if err := conf.SetupInitialChat([]string{"q", "what", "breed?"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}
	last := conf.InitialChat.Messages[len(conf.InitialChat.Messages)-1]
	if last.Content != "" || len(last.Parts) != 2 || last.Parts[1] != (pub_models.TextPart{Text: "what breed?"}) {
		t.Fatalf("user message = %+v", last)
	}
	part, ok := last.Parts[0].(pub_models.ImagePart)
	if !ok || part.Origin != pub_models.OriginUser || !bytes.Equal(part.Data, queryTestPNG) || part.Asset != "" {
		t.Fatalf("image part = %+v, want the pending user image", last.Parts[0])
	}
	if conf.PostProccessedPrompt != "what breed?" {
		t.Fatalf("prompt = %q", conf.PostProccessedPrompt)
	}

	text := Default
	text.ConfigDir = t.TempDir()
	if err := text.SetupInitialChat([]string{"q", "plain"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}
	if last := text.InitialChat.Messages[len(text.InitialChat.Messages)-1]; last.Content != "plain" || last.Parts != nil {
		t.Fatalf("text-only user message = %+v, want Content (I10)", last)
	}

	missing := Default
	missing.ConfigDir = t.TempDir()
	missing.ImageInputs = []string{filepath.Join(dir, "nope*.png")}
	if err := missing.SetupInitialChat([]string{"q", "hi"}); err == nil || !strings.Contains(err.Error(), "nope*.png") {
		t.Fatalf("err = %v, want the collector's no-match error", err)
	}
}

func TestSetupInitialChat_globFramesCollectedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.png"), queryTestPNG, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	conf := Default
	conf.ConfigDir = t.TempDir()
	conf.Glob = filepath.Join(dir, "*")
	if err := conf.SetupInitialChat([]string{"q", "sum"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}
	msgs := conf.InitialChat.Messages
	if conf.InitialChat.ID != "glob_"+conf.Glob || len(msgs) != 4 || msgs[0].Role != "system" || msgs[2].Content != "#####" {
		t.Fatalf("glob chat = %s %+v", conf.InitialChat.ID, msgs)
	}
	if !strings.Contains(msgs[1].Content, "alpha") || strings.Contains(msgs[1].Content, "b.png") {
		t.Fatalf("glob file message = %q", msgs[1].Content)
	}
	if _, ok := msgs[3].Parts[0].(pub_models.ImagePart); !ok || len(msgs[3].Parts) != 2 {
		t.Fatalf("prompt message = %+v, want the png as an image part", msgs[3])
	}
}
