package text

import (
	"flag"
	"slices"
	"testing"

	"github.com/baalimago/clai/internal"
)

// TestFlags_Voices is the query and chat half of the phase's flag row: -voices
// is read where -am and -af are, and the three reach the audio_transcribe
// tool's run together (Flag.1, Flag.3). The audio transcribe half is in
// internal/audio.
func TestFlags_Voices(t *testing.T) {
	people := []string{"Lorentz", "Christoph", "syrus"}
	t.Run("Flag.1 on query", func(t *testing.T) {
		got := mediaToolSettings(parsedQueryFlags(t, "-voices", "Lorentz, Christoph ,,syrus", "hi"), &Default)
		if !slices.Equal(got.Voices, people) {
			t.Fatalf("voices = %q, want %q", got.Voices, people)
		}
	})
	t.Run("Flag.1 on chat continue", func(t *testing.T) {
		cf := internal.NewContinueFlags()
		fs := flag.NewFlagSet("continue", flag.ContinueOnError)
		cf.Register(fs)
		if err := fs.Parse([]string{"-voices", "Lorentz, Christoph ,,syrus", "chat-id", "hi"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got := mediaToolSettings(parsedQueryFlags(t, append(cf.Tokens(fs), "hi")...), &Default)
		if !slices.Equal(got.Voices, people) {
			t.Fatalf("voices = %q, want %q", got.Voices, people)
		}
	})
	t.Run("Flag.4 an empty and an absent -voices name nobody", func(t *testing.T) {
		for _, args := range [][]string{{"-voices", "", "hi"}, {"hi"}} {
			if got := mediaToolSettings(parsedQueryFlags(t, args...), &Default); got.Voices != nil {
				t.Fatalf("%q: voices = %q, want nobody", args, got.Voices)
			}
		}
	})
	t.Run("Flag.3 -am, -af and -voices travel together", func(t *testing.T) {
		tf := parsedQueryFlags(t, "-am", "test-diarize", "-af", "json", "-voices", "Lorentz", "hi")
		conf := Default
		ApplyProfileOverrides(&conf, tf)
		got := mediaToolSettings(tf, &conf)
		if got.AudioModel != "test-diarize" || got.AudioFormat != "json" || !slices.Equal(got.Voices, []string{"Lorentz"}) {
			t.Fatalf("settings = %+v, want the three flags' values", got)
		}
	})
}
