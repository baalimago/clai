package audio

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio/voicescmd"
	"github.com/baalimago/clai/internal/capture"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

const namespaceHelp = `usage: clai audio <verb> [verb flags] [args]

Verbs:
  t|transcribe <file>   Transcribe an audio file to text. Use '-' to read audio bytes from stdin.
  v|voices              Keep the library of known voices: a|add <wav>, l|list [person [clip [action]]].
  c|calibrate           Measure this machine's ambient level and set the silence threshold.
  h|help                Show this help.

Transcribe flags (placed after the verb, before the file; a flag after the file is not read):
  -am, -audio-model     Set the transcription model (default in audioConfig.json)
  -af, -audio-format    Set the transcript output format: vtt|srt|text|json
  -voices               The people to name, comma separated: -voices 'Lorentz,Christoph'
                        (default: everyone in the voice library with a confirmed clip,
                        up to transcribe.max-voices; nobody when that is 0)
  -parallelism          Max parallel requests when a large file is split into chunks
  -max-request-bytes    Per-request upload cap in bytes for split files (default 25 MiB)
  -max-request-seconds  Per-request audio cap for calibrated diarization (default 1400)
  -max-speakers         Registry cap for calibrated diarization (default 8)
  -strict-speakers      Fail instead of rendering unknown-N for unresolved speakers

Known voices: speakers whose voice is in the library are named instead of
labelled. In text, vtt and srt a name from a clip nobody confirmed reads
'<name> (unconfirmed)'; json is {"segments": [...], "naming": {...}}, each named
segment with its voice {clip, status}. Without a voice to name, the output is
as it always was.

Examples:
  clai audio transcribe meeting.wav
  clai a t -af text meeting.wav
  cat meeting.wav | clai a t -
  clai audio transcribe -voices 'Lorentz,Christoph' -af json meeting.wav
  clai audio voices add rec.wav -from 61.2 -to 69 -name Lorentz
  clai audio voices list
  clai a v l lorentz
  clai audio calibrate
`

// CommandDeps are the composition-root collaborators from internal/setup.
// ConfigPrep defers upgrade announcements: transcribe's stdout is the
// transcript (architecture/config.md).
type CommandDeps struct {
	ConfigPrep        func(ctx context.Context) (confDir string, announcements []string, err error)
	NewRemoteResolver func(confDir string, prompt remote.Prompter) (remote.Resolver, error)
	// OpenVoices is the opener audio's voices command runs with; nil is the
	// runtime's, over the clai config directory and ExecRunner.
	OpenVoices voicescmd.Opener
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
	Voices            internal.StringFlag
}

// Register binds the audio flag surface onto fs.
func (f *Flags) Register(fs *flag.FlagSet) {
	f.Model.Register(fs, "Set the audio transcription model, or a model-aliases verb such as 'fast'.", "am", "audio-model")
	f.Format.Register(fs, "Set the transcript output format: vtt|srt|text|json.", "af", "audio-format")
	f.Parallelism.Register(fs, "Set max parallel transcription requests for split audio files.", "parallelism")
	f.MaxRequestBytes.Register(fs, "Set the per-request upload cap in bytes for split audio files.", "max-request-bytes")
	f.MaxRequestSeconds.Register(fs, "Set the per-request audio duration cap in seconds for calibrated diarization.", "max-request-seconds")
	f.MaxSpeakers.Register(fs, "Set the maximum number of speaker identities for calibrated diarization.", "max-speakers")
	f.StrictSpeakers.Register(fs, "Fail instead of rendering unknown-N when a material speaker cannot be resolved.", "strict-speakers")
	f.Voices.Register(fs, internal.VoicesFlagDesc, "voices")
}

// calibrateWindow is how long one calibration listens to the microphone.
const calibrateWindow = 3 * time.Second

// Command builds the audio command tree.
func Command(deps CommandDeps) *internal.Command {
	raw := &internal.RawFlag{}
	c := &internal.Command{
		Name:     "audio",
		Desc:     "Transcribe and record audio: audio t|transcribe <file> ('-' reads stdin), audio v|voices, audio c|calibrate",
		HelpText: namespaceHelp,
		Register: raw.Register,
		Raw:      raw,
	}
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		fmt.Fprint(runenv.From(ctx).Stderr(), namespaceHelp)
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
		HelpText: `audio transcribe [flags] <file>. Transcribes the audio file; use '-' to read
audio bytes from stdin. Flags stand before <file>: a flag after it is not read.

Speakers whose voice is in the voice library are named instead of labelled:
-voices says whom to expect, and by default everyone with a confirmed clip is,
up to transcribe.max-voices in audioConfig.json. The library is kept with
clai audio voices.

Examples:
  clai a t meeting.wav
  clai a t -af text meeting.wav
  cat meeting.wav | clai a t -
  clai a t -voices 'Lorentz,Christoph' -af json meeting.wav`,
		Register: func(fs *flag.FlagSet) {
			raw.Register(fs)
			f.Register(fs)
		},
		Raw: raw,
	}
	transcribe.OnSetup = func(ctx context.Context, tc *internal.Command) error {
		if len(tc.Args()) < 2 {
			fmt.Fprintln(runenv.From(ctx).Stderr(), tc.Help())
			return errMissingAudioFile
		}
		confDir, announcements, err := deps.ConfigPrep(ctx)
		if err != nil {
			return err
		}
		for _, msg := range announcements {
			fmt.Fprintln(runenv.From(ctx).Stderr(), msg)
		}
		q, err := setupTranscribeQuerierWithResolver(ctx, confDir, f, tc.Args()[1:], deps.NewRemoteResolver)
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
	audioHelp.OnRun = func(ctx context.Context, _ *internal.Command) error {
		if _, err := fmt.Fprint(runenv.From(ctx).Stdout(), namespaceHelp); err != nil {
			return diag.Errorf("print audio help: %w", err)
		}
		return nil
	}
	calibrate := &internal.Command{
		Name: "calibrate",
		Desc: "Measure this machine's ambient level and set the silence threshold",
		HelpText: `audio calibrate. Measures the default microphone for 3 seconds, then
writes capture.silence-peak-db and capture.calibrated into audioConfig.json.
Run it in the room where you record, while the room is as quiet as it is when
a meeting starts.

Examples:
  clai audio calibrate`,
	}
	calibrate.OnRun = func(ctx context.Context, c *internal.Command) error {
		if len(c.Args()) > 1 {
			return fmt.Errorf("audio calibrate takes no arguments, got %q", c.Args()[1:])
		}
		env := runenv.From(ctx)
		fmt.Fprintf(env.Stderr(), "measuring the default microphone for %v\n", calibrateWindow)
		text, err := calibrateText(ctx, capture.New(), calibrateWindow)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprint(env.Stdout(), text); err != nil {
			return diag.Errorf("print calibration: %w", err)
		}
		return nil
	}
	openVoicesDeps := deps.OpenVoices
	if openVoicesDeps == nil {
		openVoicesDeps = openVoices
	}
	c.Subs = map[string]cmd.Command{
		"transcribe|t": transcribe,
		voicescmd.Key:  voicescmd.Command(openVoicesDeps),
		"calibrate|c":  calibrate,
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
