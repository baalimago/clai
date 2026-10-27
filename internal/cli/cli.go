// Package cli is clai's composition root: it builds the command dispatch
// table, interpolates the usage template and owns the entry points that both
// the root main and the e2e suite call. It sits in internal/ so the tests can
// import it; a package main cannot be imported.
// See architecture/cmd-dispatch.md.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudcmd"
	"github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/clai/internal/confdir"
	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/route"
	"github.com/baalimago/clai/internal/imageinput"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/profiles"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/runtally"
	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/summary"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/version"
	"github.com/baalimago/clai/internal/video"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

// CONFIG_DIR and CACHE_DIR are interpolated by Run; %v takes the generated
// command table. Per-command help lives on each command's -h.
const usageTemplate = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisites:
  - Set the environment variable to your API key according to the vendor you seek to use
  - (Optional) Set the NO_COLOR environment variable to disable ansi color output
  - (Optional) Install glow - https://github.com/charmbracelet/glow for formatted markdown output

Usage: clai [command flags] <command> [command flags] [args]

Commands:
%v
Run 'clai help <command>' or 'clai <command> -h' for a command's flags,
examples and subcommands.

Config dir: CONFIG_DIR
Cache dir:  CACHE_DIR

Examples:
  - clai --version   # same as 'clai version'
  - clai confdir
  - clai -tools website_text -query-summary query "What'\''\'''s the weather like in Tokyo? Use website_text to fetch data"
  - clai -glob "*.txt" -query-summary query Please summarize these documents: 
  - clai -add-shell-context minimal -query-summary query "what changed in this repo?"
  - clai -loop query "refactor the auth package and make the tests pass"
  - clai -reply-only -profile-path cchub:clai/reviewer query > review.md   # progress on stderr, only the reply in the file
  - clai -photo-model dall-e-2 photo A cat in space
  - clai -image screenshot.png -query-summary query "What is wrong in this dialog?"
  - clai -tools generate_image -query-summary query "Draw a fox in the snow"
  - docker logs example | clai -replace LOG -query-summary query "Find errors in these logs: LOG"
  - clai audio transcribe meeting.wav | clai -query-summary query "Summarize these meeting notes: {}"
  - clai audio transcribe -audio-model gpt-4o-transcribe-diarize -strict-speakers long-meeting.wav
  - clai chat list
  - clai -raw chat dirv2
  - clai chat summarize 7d   # label last week's conversations with a title and summary
  - clai chat help
  - clai query -- -why does this fail   # '--' escapes a prompt starting with '-'
`

// CPUProfileFileName is where RunProfiled writes when the CPU debug flag is on.
const CPUProfileFileName = "cpu_profile.prof"

// Deps carries the collaborators the entry point injects into the command
// table. Tests substitute the summarizer, foreign-cache and image-generator
// factories through it, so none of those packages holds mutable state.
type Deps struct {
	NewSummarizer func(confDir string) (models.Summarizer, error)
	// SummaryJoinTimeout makes a plain query wait this long for its label;
	// zero (production) keeps labelling best effort with no wait.
	SummaryJoinTimeout time.Duration
	ForeignCache       func() vendors.SourceCache
	NewImageGenerator  imagegen.NewGeneratorFunc
	// RemoteClient fetches -prp cchub:, gh: and https:// targets. Tests point
	// it at a local TLS server.
	RemoteClient remote.Doer
}

// DefaultDeps returns the production collaborators: the real summarizer, the
// real foreign conversation index and the real image generators.
func DefaultDeps() Deps {
	return Deps{
		NewSummarizer:     summary.NewAgentSummarizer,
		ForeignCache:      chat.DefaultForeignCache,
		NewImageGenerator: route.NewGenerator,
		RemoteClient:      remote.NewClient(),
	}
}

// remoteResolver builds the resolver for remote profile targets over the
// config directory a command prepared and the clai cache directory.
func remoteResolver(client remote.Doer) text.NewRemoteResolverFunc {
	return func(confDir string, prompt remote.Prompter) (remote.Resolver, error) {
		cacheDir, err := utils.GetClaiCacheDir()
		if err != nil {
			return remote.Resolver{}, err
		}
		note := func(s string) { fmt.Fprintf(os.Stderr, "notice: %s\n", s) }
		return remote.NewResolver(remote.EnvFrom(os.Getenv), confDir, cacheDir, client, prompt, note)
	}
}

// hubPublisher posts a profile to the Hub with this machine's clai.chat
// login, for `clai profiles publish` and `clai skills publish`.
func hubPublisher(confDir string) (profiles.HubPublisher, error) {
	creds, err := cloud.LoadCredentials(confDir)
	if err != nil {
		return nil, err
	}
	client := cloud.NewClient(creds.URL, creds.Token)
	return func(ctx context.Context, req profiles.PublishRequest) (profiles.Published, error) {
		var out profiles.Published
		err := client.PostJSON(ctx, "/api/v1/hub", req, &out)
		if apiErr, ok := cloud.AsAPIError(err); ok && apiErr.Code == "handle_required" {
			return out, &profiles.HandleRequiredError{Suggested: apiErr.Suggested}
		}
		return out, err
	}, nil
}

// Commands is the composition root: every command lives in its domain package
// and receives its cross-package collaborators here. internal/setup owns config
// prep and migrations, which the domain packages cannot import themselves.
// See architecture/cmd-dispatch.md.
func Commands(deps Deps) map[string]cmd.Command {
	return commands(deps, func(int) {})
}

// commands takes setExit, through which a command asks Run for an exit
// status other than the 0 or 1 cmd.Run can return.
func commands(deps Deps, setExit func(int)) map[string]cmd.Command {
	configPrep := func() (string, error) {
		confDir, _, err := setup.ConfigRunPrep(false)
		return confDir, err
	}
	// An agent run may call audio_transcribe, so -am/-af must reach it.
	applyMediaOverrides := func(f internal.MediaToolFlags) error {
		return audio.SetTranscribeOverrides(f.AudioModel.Value(), f.AudioFormat.Value())
	}
	queryDeps := text.QueryCommandDeps{
		ConfigPrep:          configPrep,
		TrustInput:          func() io.Reader { return setup.Input },
		ApplyMediaOverrides: applyMediaOverrides,
		NewSummarizer:       deps.NewSummarizer,
		SummaryJoinTimeout:  deps.SummaryJoinTimeout,
		NewImageGenerator:   deps.NewImageGenerator,
		PhotoConfig:         setup.LoadPhotoConfig,
		NewRemoteResolver:   remoteResolver(deps.RemoteClient),
		RunTallyEnv:         runtally.OSEnv{},
	}
	profileDeps := profiles.CommandDeps{
		NewResolver:     profiles.NewResolverFunc(remoteResolver(deps.RemoteClient)),
		ConfigDir:       func() (string, error) { return configPrep() },
		NewHubPublisher: hubPublisher,
		FindSecret: func(name string, b []byte) (string, bool) {
			s, found := sync.FindSecret(name, b)
			return s.String(), found
		},
		NewPrompt: func() remote.Prompter { return text.TerminalTrustPrompter(os.Stdin, os.Stderr) },
	}
	return map[string]cmd.Command{
		"query|q": text.QueryCommand(queryDeps),
		"chat|c": chat.Command(chat.CommandDeps{
			ConfigPrep:    configPrep,
			NewSummarizer: deps.NewSummarizer,
			ParseSince:    summary.ParseSince,
			ForeignCache:  deps.ForeignCache,
			SendPrompt:    text.DirReplySender(queryDeps),
		}),
		"photo|p": photo.Command(photo.CommandDeps{
			ConfigPrep:    configPrep,
			LoadConfig:    setup.LoadPhotoConfig,
			NewGenerator:  deps.NewImageGenerator,
			Collector:     imageinput.New(),
			NewPresenter:  present.New,
			NewOpener:     present.NewCommandOpener,
			PreviousImage: photo.PreviousQueryImage,
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
		"mcp":            mcp.Command(),
		"profiles":       profiles.Command(profileDeps),
		"skills":         profiles.SkillsCommand(profileDeps),
		"confdir":        confdir.Command(),
		"cloud": cloudcmd.Command(cloudcmd.Deps{
			ConfDir: utils.GetClaiConfigDir,
			SetExit: setExit,
			Hooks: sync.Hooks{
				Export: func(ctx context.Context, confDir string, progress func(done, total int)) error {
					return chat.ExportForeign(ctx, confDir, foreignCache(deps), progress)
				},
				Pending: func(ctx context.Context, confDir string) ([]sync.PendingImport, error) {
					found, err := chat.PendingForeign(ctx, confDir, foreignCache(deps))
					out := make([]sync.PendingImport, 0, len(found))
					for _, f := range found {
						out = append(out, sync.PendingImport{Source: f.Source, ID: f.SourceID, Change: sync.ImportChange(f.Change), Messages: f.Messages, RawBytes: f.RawBytes})
					}
					return out, err
				},
				Pulled: chat.ReindexPulled,
			},
		}),
	}
}

func foreignCache(deps Deps) vendors.SourceCache {
	if deps.ForeignCache == nil {
		return nil
	}
	return deps.ForeignCache()
}

// versionFlagAsCommand rewrites the special first-argument version flag
// ("--version", or its single-dash spelling, which Go flag syntax treats
// alike) into the version command, so both spellings share one dispatch path.
func versionFlagAsCommand(args []string) []string {
	if len(args) == 0 || (args[0] != "--version" && args[0] != "-version") {
		return args
	}
	return append([]string{"version"}, args[1:]...)
}

// Run dispatches args (without the program name) and returns the process exit
// status. It never exits; the caller owns os.Exit.
func Run(args []string, deps Deps) int {
	args = helpAsCommand(versionFlagAsCommand(args))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Nested tool calls stop clai through this cancel.
	ctx = context.WithValue(ctx, utils.ContextCancelKey, cancel)
	go func() { shutdown.Monitor(cancel) }()
	cfgDir, _ := utils.GetClaiConfigDir()
	cacheDir, _ := utils.GetClaiCacheDir()
	usage := strings.NewReplacer("CONFIG_DIR", cfgDir, "CACHE_DIR", cacheDir).Replace(usageTemplate)
	ctx, exit := internal.WithExitStatus(ctx)
	cmds := withHelp(commands(deps, exit.Set), usage)
	if err := checkArgs(args, cmds); err != nil {
		ancli.Errf("%v", err)
		return 1
	}
	return exit.Resolve(cmd.Run(ctx, append([]string{"clai"}, args...), cmds, usage))
}

// checkArgs lets a command named first vet its own arguments (see
// internal.Command.CheckArgs); every other command line is left to cmd.Run.
func checkArgs(args []string, cmds map[string]cmd.Command) error {
	if len(args) == 0 {
		return nil
	}
	for key, c := range cmds {
		for name := range strings.SplitSeq(key, "|") {
			if name != args[0] {
				continue
			}
			if ic, ok := c.(*internal.Command); ok && ic.CheckArgs != nil {
				return ic.CheckArgs(args[1:])
			}
			return nil
		}
	}
	return nil
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

// RunProfiled returns rather than exits: os.Exit runs no deferred function, so
// a profile stopped by a defer never flushed.
func RunProfiled(args []string, deps Deps) int {
	if !debugflags.Enabled("CPU") {
		return Run(args, deps)
	}
	stop, err := startCPUProfile(CPUProfileFileName)
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to start cpu profile: %v\n", err))
		return Run(args, deps)
	}
	defer stop()
	return Run(args, deps)
}
