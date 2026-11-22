package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// TestAudioCommand_TranscribeWithoutFile is Wire.7: transcribe with no file
// writes its own help with the error, as audio does with no verb.
func TestAudioCommand_TranscribeWithoutFile(t *testing.T) {
	for _, words := range [][]string{{"audio", "transcribe"}, {"audio", "transcribe", "-af", "text"}} {
		var prepared, resolved int
		c := Command(CommandDeps{
			ConfigPrep: func(context.Context) (string, []string, error) {
				prepared++
				return t.TempDir(), nil, nil
			},
			NewRemoteResolver: func(string, remote.Prompter) (remote.Resolver, error) {
				resolved++
				return remote.Resolver{}, errors.New("no resolver in this test")
			},
		})
		transcribe, ok := c.Subcommands()["transcribe|t"].(*internal.Command)
		if !ok {
			t.Fatal("transcribe sub missing")
		}
		var stdout, stderr bytes.Buffer
		streams := stdio.New(stdio.Config{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
		ctx := runenv.With(context.Background(), runenv.New(runenv.Config{Streams: streams}))
		status, err := cmd.RunSilentTo(ctx, append([]string{"clai"}, words...), map[string]cmd.Command{"audio": c}, io.Discard)
		if status != 1 || !errors.Is(err, errMissingAudioFile) || !strings.Contains(err.Error(), "missing audio file argument") {
			t.Fatalf("%v: status %d, err %v; want 1 and the missing file", words, status, err)
		}
		if help := transcribe.Help(); !strings.Contains(stderr.String(), help) || strings.Contains(stderr.String(), namespaceHelp) {
			t.Fatalf("%v: stderr %q, want transcribe's help %q alone", words, stderr.String(), help)
		}
		if stdout.Len() != 0 || prepared != 0 || resolved != 0 {
			t.Fatalf("%v: stdout %q, config prepared %d times, resolver built %d times; want nothing run", words, stdout.String(), prepared, resolved)
		}
	}
}

func Test_Command_tree(t *testing.T) {
	deps := CommandDeps{
		ConfigPrep: func(context.Context) (string, []string, error) { return t.TempDir(), nil, nil },
	}
	c := Command(deps)
	if c.Describe() == "" || !strings.Contains(c.Help(), "t|transcribe") || !strings.Contains(c.Help(), "c|calibrate") {
		t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
	}

	t.Run("no verb errors with namespace help", func(t *testing.T) {
		if err := c.Flagset().Parse(nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(procenv.Context(context.Background())); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		stderr := testboil.CaptureStderr(t, func(t *testing.T) {
			if err := c.Run(procenv.Context(context.Background())); err == nil || !strings.Contains(err.Error(), "missing audio verb") {
				t.Fatalf("expected missing-verb error, got: %v", err)
			}
		})
		if !strings.Contains(stderr, "usage: clai audio") {
			t.Fatalf("expected namespace help on stderr, got: %q", stderr)
		}
	})

	t.Run("transcribe sub owns the audio flags and errors on a missing file", func(t *testing.T) {
		sub, ok := c.Subcommands()["transcribe|t"].(*internal.Command)
		if !ok {
			t.Fatal("transcribe sub missing")
		}
		if sub.Flagset().Lookup("af") == nil || sub.Flagset().Lookup("am") == nil {
			t.Fatal("transcribe must own the audio flag group")
		}
		if err := sub.Flagset().Parse([]string{"does-not-exist.wav"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		err := sub.Setup(procenv.Context(context.Background()))
		if err == nil || !strings.Contains(err.Error(), "failed to find audio file") {
			t.Fatalf("expected missing-file error through the real setup path, got: %v", err)
		}
	})

	t.Run("calibrate measures and prints the threshold it stored", func(t *testing.T) {
		fakeFFmpeg(t)
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "silence-peak-db": -40})
		sub, ok := c.Subcommands()["calibrate|c"].(*internal.Command)
		if !ok {
			t.Fatal("calibrate sub missing")
		}
		if err := sub.Flagset().Parse(nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := sub.Setup(procenv.Context(context.Background())); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		stderr := testboil.CaptureStderr(t, func(t *testing.T) {
			out := testboil.CaptureStdout(t, func(t *testing.T) {
				if err := sub.Run(procenv.Context(context.Background())); err != nil {
					t.Fatalf("Run: %v", err)
				}
			})
			if !strings.Contains(out, "silence-peak-db -40 → -38") {
				t.Fatalf("stdout = %q, want the stored threshold", out)
			}
		})
		if !strings.Contains(stderr, "measuring the default microphone") {
			t.Fatalf("stderr = %q, want the measurement notice", stderr)
		}
	})

	t.Run("calibrate takes no arguments", func(t *testing.T) {
		sub, ok := Command(deps).Subcommands()["calibrate|c"].(*internal.Command)
		if !ok {
			t.Fatal("calibrate sub missing")
		}
		if err := sub.Flagset().Parse([]string{"extra"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := sub.Setup(procenv.Context(context.Background())); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if err := sub.Run(procenv.Context(context.Background())); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
			t.Fatalf("err = %v, want an argument error", err)
		}
	})

	t.Run("help sub prints the namespace help", func(t *testing.T) {
		sub, ok := c.Subcommands()["help|h"].(*internal.Command)
		if !ok {
			t.Fatal("help sub missing")
		}
		out := testboil.CaptureStdout(t, func(t *testing.T) {
			if err := sub.Run(procenv.Context(context.Background())); err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
		if !strings.Contains(out, "usage: clai audio") {
			t.Fatalf("expected namespace help, got: %q", out)
		}
	})
}
