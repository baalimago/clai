package audio

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voicescmd"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/utils"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// knownVoices is how one transcription names its speakers: the selection,
// resolved once before the recording is transcribed, and the library's
// tally of the naming. The zero value names nobody and leaves the output as
// it is without voices.
type knownVoices struct {
	selection voices.Selection
	tally     func([]voices.ClipOutcome) error
	// warn: the run says on stderr what its naming did not do. The
	// audio_transcribe tool does not: its result is its whole answer.
	warn bool
}

// VoicesNeedFFmpegError says people were named for a transcription on a
// machine without ffmpeg. Origin says who named them.
type VoicesNeedFFmpegError struct {
	Origin voices.Origin
	Err    *voices.MissingToolError
}

func (e *VoicesNeedFFmpegError) Error() string {
	named := "-voices"
	if e.Origin == voices.OriginTool {
		named = "voices"
	}
	return fmt.Sprintf("%s needs ffmpeg to name people, and ffmpeg is not on PATH: install ffmpeg, or transcribe without %s", named, named)
}

func (e *VoicesNeedFFmpegError) Unwrap() error { return e.Err }

// resolveKnownVoices is whom one transcription names. When people are named
// and ffmpeg does not resolve, the run stops here, before any request: a
// missing ffmpeg with nobody named stops nothing.
func resolveKnownVoices(runner CommandRunner, library *voices.Library, asked voices.Asked) (knownVoices, error) {
	if len(asked.Tool) > 0 || len(asked.Flag) > 0 {
		if _, err := runner.LookPath(ffmpegBin); err != nil {
			origin := voices.OriginFlag
			if len(asked.Tool) > 0 {
				origin = voices.OriginTool
			}
			return knownVoices{}, &VoicesNeedFFmpegError{Origin: origin, Err: &voices.MissingToolError{Tool: ffmpegBin}}
		}
	}
	return knownVoices{selection: library.Select(asked), tally: library.Tally}, nil
}

// name names the transcribed segments with the route's own transcriber,
// runner and budgets, and tallies how the picked clips fared. Neither fails
// the run: a failed tally is the report's Unrecorded.
func (k knownVoices) name(ctx context.Context, route *Splitter, recording string, segs []Segment) Naming {
	budgets := route.Budgets
	if budgets.MaxRequestDuration <= 0 || budgets.MaxRequestBytes <= 0 {
		if defaults, err := ResolveBudgets(Default.Transcribe); err == nil {
			budgets = defaults
		}
	}
	naming := Namer{Transcriber: route.Transcriber, Runner: route.Runner, Budgets: budgets}.Name(ctx, recording, segs, k.selection)
	if len(naming.Outcomes) > 0 {
		naming.Report.Unrecorded = k.tally(naming.Outcomes)
	}
	return naming
}

// namingWarnings is one message per thing the naming did not do. A run that
// named nobody itself hears only of a failed request: the rest is what a
// library with voices in it does every day.
func namingWarnings(r NamingReport) []string {
	named := r.Origin != voices.OriginLibrary
	var msgs []string
	for _, v := range r.Voices {
		if named && v.Result == VoiceUnknown {
			msgs = append(msgs, fmt.Sprintf("no voice %q in the library", v.Name))
		}
	}
	reason := "no reason was given"
	if r.Err != nil {
		reason = strings.Join(strings.Fields(r.Err.Error()), " ")
	}
	switch {
	case r.Status == NamingRequestFailed:
		return append(msgs, "naming failed: "+reason+"; speakers left as labels")
	case !named:
	case r.Status == NamingLibraryUnreadable:
		return append(msgs, "voice library unreadable: "+reason+"; speakers left as labels")
	case r.Status == NamingNotDiarized:
		return append(msgs, "the transcript has no speaker labels; nobody named")
	case r.Status == NamingNoCandidate:
		return append(msgs, "no speaker talks long enough to compare; nobody named")
	case r.Status == NamingNamed, r.Status == NamingNoAgreement:
		for _, v := range r.Voices {
			if v.Result == VoiceAbsent || v.Result == VoiceNotPlaced {
				msgs = append(msgs, "failed to find a match for "+v.Name+", were they in the meeting?")
			}
		}
	}
	return msgs
}

// runtimeVoices is the library under the clai config directory and an
// Inspector over ExecRunner, as every voices member of the runtime uses
// them.
func runtimeVoices() (*voices.Library, voices.Inspector, error) {
	confDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil, voices.Inspector{}, diag.Errorf("find the voice library: %w", err)
	}
	return voices.NewLibrary(confDir, time.Now), voices.Inspector{Runner: ExecRunner{}}, nil
}

func openVoices(context.Context) (voicescmd.Deps, error) {
	library, inspector, err := runtimeVoices()
	if err != nil {
		return voicescmd.Deps{}, err
	}
	return voicescmd.Deps{Library: library, Inspect: inspector.Inspect, Runner: ExecRunner{}}, nil
}

func rememberVoiceDeps(context.Context) (pkgtools.RememberVoiceDeps, error) {
	library, inspector, err := runtimeVoices()
	if err != nil {
		return pkgtools.RememberVoiceDeps{}, err
	}
	return pkgtools.RememberVoiceDeps{Inspect: inspector.Inspect, Library: library}, nil
}
