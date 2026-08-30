package audio

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

const namespaceHelp = `usage: clai audio <verb> [verb flags] [args]

Verbs:
  t|transcribe <file>   Transcribe an audio file to text. Use '-' to read audio bytes from stdin.
  h|help                Show this help.

Transcribe flags (placed after the verb):
  -am, -audio-model     Set the transcription model (default in audioConfig.json)
  -af, -audio-format    Set the transcript output format: vtt|srt|text|json
  -parallelism          Max parallel requests when a large file is split into chunks

Examples:
  clai audio transcribe meeting.wav
  clai a t -af text meeting.wav
  cat meeting.wav | clai a t -
`

// CommandDeps are the composition-root collaborators: config prep lives
// in internal/setup, which audio cannot import (setup imports audio).
type CommandDeps struct {
	ConfigPrep func() (confDir string, err error)
}

// Flags is the audio transcribe flag surface.
type Flags struct {
	Model       internal.StringFlag
	Format      internal.StringFlag
	Parallelism internal.IntFlag
}

// Register binds the audio flag surface onto fs.
func (f *Flags) Register(fs *flag.FlagSet) {
	f.Model.Register(fs, "Set the audio transcription model.", "am", "audio-model")
	f.Format.Register(fs, "Set the transcript output format: vtt|srt|text|json.", "af", "audio-format")
	f.Parallelism.Register(fs, "Set max parallel transcription requests for split audio files.", "parallelism")
}

// Command builds the audio command tree.
func Command(deps CommandDeps) *internal.Command {
	raw := &internal.RawFlag{}
	c := &internal.Command{
		Name:     "audio",
		Desc:     "Transcribe audio: audio t|transcribe <file> ('-' reads stdin)",
		HelpText: namespaceHelp,
		Register: raw.Register,
		Raw:      raw,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		fmt.Fprint(os.Stderr, namespaceHelp)
		args := c.Args()
		if len(args) < 2 {
			return errors.New("missing audio verb")
		}
		return fmt.Errorf("unknown audio verb: %q", args[1])
	}
	f := &Flags{}
	transcribe := &internal.Command{
		Name: "transcribe",
		Desc: "Transcribe an audio file to text ('-' reads stdin)",
		HelpText: `audio transcribe <file>. Transcribes the audio file; use '-' to read
audio bytes from stdin.

Examples:
  clai a t meeting.wav
  clai a t -af text meeting.wav
  cat meeting.wav | clai a t -`,
		Register: func(fs *flag.FlagSet) {
			raw.Register(fs)
			f.Register(fs)
		},
		Raw: raw,
	}
	transcribe.OnSetup = func(_ context.Context, tc *internal.Command) error {
		confDir, err := deps.ConfigPrep()
		if err != nil {
			return err
		}
		q, err := setupTranscribeQuerier(confDir, f, tc.Args()[1:])
		if err != nil {
			return err
		}
		tc.SetQuerier(q)
		return nil
	}
	audioHelp := &internal.Command{
		Name:     "help",
		Desc:     "Show the audio namespace help",
		HelpText: namespaceHelp,
		Raw:      raw,
	}
	audioHelp.OnRun = func(_ context.Context, _ *internal.Command) error {
		fmt.Print(namespaceHelp)
		return nil
	}
	c.Subs = map[string]cmd.Command{
		"transcribe|t": transcribe,
		"help|h":       audioHelp,
	}
	return c
}

// ApplyFlagOverrides applies the CLI flag values onto the file-loaded
// configuration (flags > file > default).
func ApplyFlagOverrides(aConf *Configurations, f *Flags) {
	if f.Model.Changed() {
		aConf.Transcribe.Model = f.Model.Value()
	}
	if f.Format.Changed() {
		aConf.Transcribe.OutputFormat = f.Format.Value()
	}
	if f.Parallelism.Changed() {
		aConf.Transcribe.Parallelism = f.Parallelism.Value()
	}
}
