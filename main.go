package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/confdir"
	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/profiles"
	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/summary"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/version"
	"github.com/baalimago/clai/internal/video"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

// CONFIG_DIR and CACHE_DIR are interpolated by run; %v takes the generated
// command table. Per-command help lives on each command's -h.
const usageTemplate = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisites:
  - Set the environment variable to your API key according to the vendor you seek to use
  - (Optional) Set the NO_COLOR environment variable to disable ansi color output
  - (Optional) Install glow - https://github.com/charmbracelet/glow for formatted markdown output

Usage: clai [command flags] <command> [command flags] [args]

Commands:
%v
Run 'clai <command> -h' for a command's flags, examples and subcommands.

Config dir: CONFIG_DIR
Cache dir:  CACHE_DIR

Examples:
  - clai confdir
  - clai -t website_text query "What'\''\'''s the weather like in Tokyo? Use website_text to fetch data"
  - clai -glob "*.txt" query Please summarize these documents: 
  - clai -asc minimal q "what changed in this repo?"
  - clai -pm dall-e-2 photo A cat in space
  - docker logs example | clai -I LOG q "Find errors in these logs: LOG"
  - clai a t meeting.wav | clai q "Summarize these meeting notes: {}"
  - clai a t -am gpt-4o-transcribe-diarize -strict-speakers long-meeting.wav
  - clai c list
  - clai -r c dirv2
  - clai c summarize 7d   # label last week's conversations with a title and summary
  - clai c help
  - clai q -- -why does this fail   # '--' escapes a prompt starting with '-'
`

// Seams the root test binary replaces, so no test builds the real summarizer
// (which would spend money) or a real foreign index.
var (
	newSummarizer = summary.NewAgentSummarizer
	foreignCache  = chat.DefaultForeignCache
)

const cpuProfileFileName = "cpu_profile.prof"

// commands is the composition root: every command lives in its domain package
// and receives its cross-package collaborators here. internal/setup owns config
// prep and migrations, which the domain packages cannot import themselves.
// See architecture/cmd-dispatch.md.
func commands() map[string]cmd.Command {
	configPrep := func() (string, error) {
		confDir, _, err := setup.ConfigRunPrep(false)
		return confDir, err
	}
	// An agent run may call audio_transcribe, so -am/-af must reach it.
	applyMediaOverrides := func(f internal.MediaToolFlags) error {
		return audio.SetTranscribeOverrides(f.AudioModel.Value(), f.AudioFormat.Value())
	}
	return map[string]cmd.Command{
		"query|q": text.QueryCommand(text.QueryCommandDeps{
			ConfigPrep:          configPrep,
			TrustInput:          func() io.Reader { return setup.Input },
			ApplyMediaOverrides: applyMediaOverrides,
			NewSummarizer:       newSummarizer,
		}),
		"chat|c": chat.Command(chat.CommandDeps{
			ConfigPrep:    configPrep,
			NewSummarizer: newSummarizer,
			ParseSince:    summary.ParseSince,
			ForeignCache:  foreignCache,
		}),
		"photo|p": photo.Command(photo.CommandDeps{
			ConfigPrep: configPrep,
			LoadConfig: setup.LoadPhotoConfig,
		}),
		"video|v": video.Command(video.CommandDeps{
			ConfigPrep: configPrep,
		}),
		"audio|a": audio.Command(audio.CommandDeps{
			ConfigPrep: func() (string, []string, error) { return setup.ConfigRunPrep(true) },
		}),
		"setup|s":        setup.Command(),
		"version":        version.Command(),
		"replay|re":      chat.ReplayCommand(),
		"dir-replay|dre": chat.DirscopeReplayCommand(),
		"tools|t":        tools.Command(),
		"profiles":       profiles.Command(),
		"confdir":        confdir.Command(),
	}
}

func run(args []string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Nested tool calls stop clai through this cancel.
	ctx = context.WithValue(ctx, utils.ContextCancelKey, cancel)
	go func() { shutdown.Monitor(cancel) }()
	cfgDir, _ := utils.GetClaiConfigDir()
	cacheDir, _ := utils.GetClaiCacheDir()
	usage := strings.NewReplacer("CONFIG_DIR", cfgDir, "CACHE_DIR", cacheDir).Replace(usageTemplate)
	return cmd.Run(ctx, append([]string{"clai"}, args...), commands(), usage)
}

func startCPUProfile(name string) (func(), error) {
	f, err := os.Create(name)
	if err != nil {
		return nil, fmt.Errorf("create profile file %q: %w", name, err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return nil, fmt.Errorf("start profile %q: %w", name, err)
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}, nil
}

// runProfiled returns rather than exits: os.Exit runs no deferred function, so
// a profile stopped by a defer never flushed.
func runProfiled(args []string) int {
	if !debugflags.Enabled("CPU") {
		return run(args)
	}
	stop, err := startCPUProfile(cpuProfileFileName)
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to start cpu profile: %v\n", err))
		return run(args)
	}
	defer stop()
	return run(args)
}

func main() {
	ancli.SetupSlog()
	os.Exit(runProfiled(os.Args[1:]))
}
