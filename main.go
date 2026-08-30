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
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/version"
	"github.com/baalimago/clai/internal/video"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

// commands is the composition root: every command lives in its domain
// package and receives its cross-package collaborators here — config prep
// and the old-config migrations live in internal/setup, which the domain
// packages cannot import (setup imports them). See
// architecture/cmd-dispatch.md.
func commands() map[string]cmd.Command {
	configPrep := func() (string, error) {
		confDir, _, err := setup.ConfigRunPrep(false)
		return confDir, err
	}
	// Media-tool flags reach the domains owning those tools: an agent run
	// may call audio_transcribe, so -am/-af configure it for the run.
	applyMediaOverrides := func(f internal.MediaToolFlags) error {
		return audio.SetTranscribeOverrides(f.AudioModel.Value(), f.AudioFormat.Value())
	}
	return map[string]cmd.Command{
		"query|q": text.QueryCommand(text.QueryCommandDeps{
			ConfigPrep:          configPrep,
			TrustInput:          func() io.Reader { return setup.Input },
			ApplyMediaOverrides: applyMediaOverrides,
		}),
		"chat|c": chat.Command(chat.CommandDeps{ConfigPrep: configPrep}),
		"photo|p": photo.Command(photo.CommandDeps{
			ConfigPrep: configPrep,
			LoadConfig: setup.LoadPhotoConfig,
		}),
		"video|v": video.Command(video.CommandDeps{
			ConfigPrep: configPrep,
		}),
		"audio|a": audio.Command(audio.CommandDeps{
			ConfigPrep: configPrep,
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

// usageTemplate is the dispatcher usage, printed on bare clai and unknown
// commands: cmd.Run renders the generated command table into the single %v;
// run() interpolates CONFIG_DIR/CACHE_DIR beforehand. Per-command help
// (flags, examples, subcommands) lives on each command's -h — see
// each domain package's cmd.go and architecture/cmd-dispatch.md.
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
  - clai c list
  - clai -r c dirv2
  - clai c help
  - clai q -- -why does this fail   # '--' escapes a prompt starting with '-'
`

func run(args []string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Build in cancel into the context to allow it to be called downstream
	// Anti-pattern? Not sure, honestly, needed here to cleanly stop
	// clai in case of nested tool calls. Could've been solved by proper structure
	// but who has time for proper structure?
	ctx = context.WithValue(ctx, utils.ContextCancelKey, cancel)
	go func() { shutdown.Monitor(cancel) }()
	cfgDir, _ := utils.GetClaiConfigDir()
	cacheDir, _ := utils.GetClaiCacheDir()
	usage := strings.NewReplacer("CONFIG_DIR", cfgDir, "CACHE_DIR", cacheDir).Replace(usageTemplate)
	return cmd.Run(ctx, append([]string{"clai"}, args...), commands(), usage)
}

func main() {
	ancli.SetupSlog()
	if debugflags.Enabled("CPU") {
		f, err := os.Create("cpu_profile.prof")
		ok := true
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to create profiler file: %v", err))
		}
		if ok {
			defer f.Close()
			// Start the CPU profile
			err = pprof.StartCPUProfile(f)
			if err != nil {
				ancli.PrintErr(fmt.Sprintf("failed to start profiler : %v", err))
			}
			defer pprof.StopCPUProfile()
		}
	}

	os.Exit(run(os.Args[1:]))
}
