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
  -max-request-bytes    Per-request upload cap in bytes for split files (default 25 MiB)
  -max-request-seconds  Per-request audio cap for calibrated diarization (default 1400)
  -max-speakers         Registry cap for calibrated diarization (default 8)
  -strict-speakers      Fail instead of rendering unknown-N for unresolved speakers

Examples:
  clai audio transcribe meeting.wav
  clai a t -af text meeting.wav
  cat meeting.wav | clai a t -
`

// CommandDeps are the composition-root collaborators from internal/setup.
// ConfigPrep defers upgrade announcements: transcribe's stdout is the
// transcript (architecture/config.md).
type CommandDeps struct {
	ConfigPrep func() (confDir string, announcements []string, err error)
}

// Flags is the audio transcribe flag surface.
type Flags struct {
	Model             internal.StringFlag
	Format            internal.StringFlag
	Parallelism       internal.IntFlag
	MaxRequestBytes   internal.IntFlag
	MaxRequestSeconds internal.IntFlag
	MaxSpeakers       internal.IntFlag
	StrictSpeakers    internal.BoolFlag
}

// Register binds the audio flag surface onto fs.
func (f *Flags) Register(fs *flag.FlagSet) {
	f.Model.Register(fs, "Set the audio transcription model.", "am", "audio-model")
	f.Format.Register(fs, "Set the transcript output format: vtt|srt|text|json.", "af", "audio-format")
	f.Parallelism.Register(fs, "Set max parallel transcription requests for split audio files.", "parallelism")
	f.MaxRequestBytes.Register(fs, "Set the per-request upload cap in bytes for split audio files.", "max-request-bytes")
	f.MaxRequestSeconds.Register(fs, "Set the per-request audio duration cap in seconds for calibrated diarization.", "max-request-seconds")
	f.MaxSpeakers.Register(fs, "Set the maximum number of speaker identities for calibrated diarization.", "max-speakers")
	f.StrictSpeakers.Register(fs, "Fail instead of rendering unknown-N when a material speaker cannot be resolved.", "strict-speakers")
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
		confDir, announcements, err := deps.ConfigPrep()
		if err != nil {
			return err
		}
		for _, msg := range announcements {
			fmt.Fprintln(os.Stderr, msg)
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
// configuration (flags > file > default). A negative budget flag is an
// error naming the flag and its field.
func ApplyFlagOverrides(aConf *Configurations, f *Flags) error {
	if f.Model.Changed() {
		aConf.Transcribe.Model = f.Model.Value()
	}
	if f.Format.Changed() {
		aConf.Transcribe.OutputFormat = f.Format.Value()
	}
	if f.Parallelism.Changed() {
		aConf.Transcribe.Parallelism = f.Parallelism.Value()
	}
	for _, b := range []struct {
		flag  *internal.IntFlag
		name  string
		apply func(int)
	}{
		{&f.MaxRequestBytes, "max-request-bytes", func(v int) { aConf.Transcribe.MaxRequestBytes = int64(v) }},
		{&f.MaxRequestSeconds, "max-request-seconds", func(v int) { aConf.Transcribe.MaxRequestSeconds = v }},
		{&f.MaxSpeakers, "max-speakers", func(v int) { aConf.Transcribe.MaxSpeakers = v }},
	} {
		if !b.flag.Changed() {
			continue
		}
		if b.flag.Value() < 0 {
			return fmt.Errorf("flag -%v: transcribe.%v must not be negative, got %v", b.name, b.name, b.flag.Value())
		}
		b.apply(b.flag.Value())
	}
	if f.StrictSpeakers.Changed() {
		aConf.Transcribe.StrictSpeakers = f.StrictSpeakers.Value()
	}
	return nil
}
