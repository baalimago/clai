package internal

import (
	"flag"
	"io"
	"slices"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

func TestStringsFlag_accumulatesAcrossNames(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f StringsFlag
	f.Register(fs, "images", "img", "image")
	if got := f.Value(); got != nil {
		t.Fatalf("unset value = %v, want nil", got)
	}
	if err := fs.Parse([]string{"-img", "a.png", "-image", "b/*.png", "-img", "c.jpg"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"a.png", "b/*.png", "c.jpg"}
	got := f.Value()
	if !slices.Equal(got, want) {
		t.Fatalf("Value() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if f.Value()[0] != "a.png" {
		t.Fatal("Value() must return a copy")
	}
	if f.String() != "a.png,b/*.png,c.jpg" {
		t.Fatalf("String() = %q", f.String())
	}
}

func TestTextFlags_imageSurface(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tf := NewTextFlags()
	tf.Raw.Register(fs)
	tf.ReplyStdin.Register(fs)
	tf.AgentText.Register(fs)
	tf.QueryText.Register(fs)
	for _, name := range []string{"img", "image", "open", "pm", "photo-model"} {
		if fs.Lookup(name) == nil {
			t.Fatalf("flag %q missing from the text surface", name)
		}
	}
	if fs.Lookup("image-generation") != nil {
		t.Fatal("image-generation was removed: generate_image always runs as clai's tool")
	}
	chat := flag.NewFlagSet("chat", flag.ContinueOnError)
	cf := &ChatFlags{}
	cf.Register(chat)
	for _, name := range []string{"img", "open", "image-generation", "pm"} {
		if chat.Lookup(name) != nil {
			t.Fatalf("flag %q does nothing on chat and must not be registered", name)
		}
	}
}

func TestCompletion_imageFlags(t *testing.T) {
	s := &CompletionSources{}
	for _, name := range []string{"img", "image"} {
		for _, got := range [][]cmd.CompletionItem{s.TextFlagValues(name, ""), MediaFlagValues(name, "")} {
			if len(got) != 1 || got[0].Kind != cmd.CompletionKindFile {
				t.Fatalf("%s: got %v, want one file-kinded item", name, got)
			}
		}
	}
	if got := s.TextFlagValues("image-generation", ""); got != nil {
		t.Fatalf("image-generation values = %v, want none: the flag was removed", got)
	}
}
