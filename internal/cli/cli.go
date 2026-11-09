// Package cli is clai's composition root: it builds the command dispatch
// table, interpolates the usage template and owns the entry points that both
// the root main and the e2e suite call. It sits in internal/ so the tests can
// import it; a package main cannot be imported.
// See architecture/cmd-dispatch.md.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/route"
	"github.com/baalimago/clai/internal/imageinput"
	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/clai/internal/profiles"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/runenv"
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
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// CONFIG_DIR and CACHE_DIR are interpolated by Run; %v takes the generated
// command table. Per-command help lives on each command's -h.
//
// The diagnostic and format synopsis extends the usage below (the help text
// itself is pinned by e2e/testdata/diagnostics/help.golden):
//
//	LOG_LEVEL=<level>[,<subsystem>=<level>...] clai ...
//	  levels error, warn (warning), info (default), debug, trace; the
//	  legacy controls of architecture/output.md form the lowest layer
//	  results on stdout, diagnostics on stderr, spill files under
//	  CACHE_DIR/diagnostics; an invalid LOG_LEVEL or legacy control exits 1
//
//	clai -json [command flags] <command> [command flags] [args]
//	  -json/--json, before the command or among its flags before the first
//	  positional argument, makes every line on stdout and stderr one JSON
//	  record; destinations, input and exit statuses are unchanged, except
//	  that $EDITOR is refused with exit status 1; -json=<bool> takes the
//	  values Go's bool flags take, so -json=false selects text, and any
//	  other value is a usage error
const usageTemplate = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisites:
  - Set the environment variable to your API key according to the vendor you seek to use
  - (Optional) Set the NO_COLOR environment variable to disable ansi color output
  - (Optional) Install glow - https://github.com/charmbracelet/glow for formatted markdown output

Usage: clai [command flags] <command> [command flags] [args]

Commands:
%v
Each command has its own flags, examples and subcommands in its help:
'clai help <command>' or 'clai <command> -h'. For example, 'clai query -h'
has the details of a query. A prompt that starts with '-' goes after '--':
'clai query -- -why does this fail'.

For scripts, -json before the command prints every line, on stdout and on
stderr, as one JSON record: 'clai -json query hi'.

Investigating:
  clai reports what it does on stderr, by subsystem. LOG_LEVEL sets how much
  is shown: first a level for everything (error, warn, info, debug or trace),
  then subsystem=level for the ones to look closer at. A name also selects
  the names under it, so vendor=debug is every vendor.

    LOG_LEVEL=debug clai query hi                     # everything
    LOG_LEVEL=warn,mcp=debug clai query hi            # MCP servers in detail
    LOG_LEVEL=warn,vendor=debug,call=debug clai ...   # requests and tool calls

  Each line names its subsystem in brackets, such as 'debug [config] ...',
  and with -json it is the record's "subsystem" field. So one run at debug
  shows which subsystem to narrow to.

Subsystems:
SUBSYSTEMS

Config dir: CONFIG_DIR
Cache dir:  CACHE_DIR
`

// CPUProfileFileName is where RunProfiled writes when the CPU debug flag is on.
const CPUProfileFileName = "cpu_profile.prof"

// Deps carries the collaborators the entry point injects into the command
// table. Tests substitute the summarizer, foreign-cache and image-generator
// factories through it, so none of those packages holds mutable state.
type Deps struct {
	NewSummarizer func(ctx context.Context, confDir string) (models.Summarizer, error)
	// SummaryJoinTimeout makes a plain query wait this long for its label;
	// zero (production) keeps labelling best effort with no wait.
	SummaryJoinTimeout time.Duration
	ForeignCache       func() vendors.SourceCache
	NewImageGenerator  imagegen.NewGeneratorFunc
	// RemoteClient fetches -prp cchub:, gh: and https:// targets. Tests point
	// it at a local TLS server.
	RemoteClient remote.Doer
	// StepRetry resends a model request that failed before its stream opened.
	StepRetry text.StepRetry
}

// DefaultDeps returns the production collaborators: the real summarizer, the
// real foreign conversation index and the real image generators.
func DefaultDeps() Deps {
	return Deps{
		NewSummarizer:     summary.NewAgentSummarizer,
		ForeignCache:      chat.DefaultForeignCache,
		NewImageGenerator: route.NewGenerator,
		RemoteClient:      remote.NewClient(),
		StepRetry:         text.DefaultStepRetry(),
	}
}

// remoteResolver builds the resolver for remote profile targets over the
// config directory a command prepared and the clai cache directory. envAt
// supplies the checker every route shares, which the resolver runs before a
// body is trusted.
func remoteResolver(client remote.Doer, envAt func(confDir string) (profile.Env, error), notices func() io.Writer) text.NewRemoteResolverFunc {
	return func(confDir string, prompt remote.Prompter) (remote.Resolver, error) {
		cacheDir, err := utils.GetClaiCacheDir()
		if err != nil {
			return remote.Resolver{}, err
		}
		note := func(s string) { fmt.Fprintf(notices(), "notice: %s\n", s) }
		r, err := remote.NewResolver(remote.EnvFrom(os.Getenv), confDir, cacheDir, client, prompt, note)
		if err != nil {
			return remote.Resolver{}, err
		}
		env, err := envAt(confDir)
		if err != nil {
			return remote.Resolver{}, err
		}
		r.CheckProfile = func(body []byte) remote.ProfileCheck { return checkProfileBody(body, env) }
		return r, nil
	}
}

// profileEnvFunc returns a builder of the profile checker's environment. Its
// first caller names the config dir it is built over; every later caller in
// the same run gets the value back, so a run that checks a file and fetches a
// remote target reads the alias table and the tool registry once.
func profileEnvFunc() func(confDir string) (profile.Env, error) {
	var (
		built bool
		env   profile.Env
		err   error
	)
	return func(confDir string) (profile.Env, error) {
		if !built {
			env, err = newProfileEnv(confDir)
			built = true
		}
		return env, err
	}
}

// newProfileEnv is the machine-local knowledge a profile check needs: the
// config's model aliases, the shell contexts in its config dir, and the tool
// registry of this binary.
func newProfileEnv(confDir string) (profile.Env, error) {
	conf, err := utils.ReadConfigFile(filepath.Join(confDir, "textConfig.json"), &text.Default)
	if err != nil {
		return profile.Env{}, diag.Errorf("read the model aliases for the profile check: %w", err)
	}
	return profile.Env{
		ResolveModel: modelRouter(conf.ModelAliases),
		HasShellContext: func(name string) bool {
			_, err := text.LoadShellContextDefinition(confDir, name)
			return err == nil
		},
		ToolNames: tools.Names(),
	}, nil
}

// modelRouter routes a model string the way a run does: a cchub:, gh: or
// https: model list is a route of its own, which no alias table can expand
// offline, and anything else is an alias in the table or a model name.
func modelRouter(table modelalias.Table) func(model string) ([]string, error) {
	return func(model string) ([]string, error) {
		if _, err := remote.ParseModelListTarget(model); err == nil {
			return []string{model}, nil
		} else if !errors.Is(err, remote.ErrNotRemote) {
			return nil, err
		}
		return table.Resolve(model)
	}
}

// checkProfileBody is the one checker on the remote route: the problems a run
// would refuse, and the top-level members nothing reads, which a trust prompt
// lists.
func checkProfileBody(body []byte, env profile.Env) remote.ProfileCheck {
	findings, err := profile.Validate(body, profile.Remote, env)
	if err != nil {
		// The document itself cannot be applied, which a shipped binary
		// never hits; refuse rather than run unexamined bytes.
		return remote.ProfileCheck{Problems: []string{fmt.Sprintf("check the profile: %v", err)}}
	}
	return remote.ProfileCheck{Problems: findings.Problems(profile.Remote), Ignored: unreadMembers(findings)}
}

// unreadMembers names every member the checker reports as never read, at any
// depth, so a nested member such as audio.language reaches the trust prompt.
func unreadMembers(findings profile.Findings) []string {
	var names []string
	for _, finding := range findings {
		if finding.Unread {
			names = append(names, finding.Member)
		}
	}
	return names
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
	return commands(deps, func(int) {}, func() io.Writer { return io.Discard })
}

// commands takes setExit, through which a command asks Run for an exit
// status other than the 0 or 1 cmd.Run can return.
func commands(deps Deps, setExit func(int), notices func() io.Writer) map[string]cmd.Command {
	configPrep := func(ctx context.Context) (string, error) {
		confDir, _, err := setup.ConfigRunPrep(ctx, false)
		return confDir, err
	}
	// An agent run may call audio_transcribe, so -am/-af and a profile's
	// audio.model must reach it.
	applyMediaOverrides := func(s internal.MediaToolSettings) error {
		return audio.SetTranscribeOverrides(s.AudioModel, s.AudioFormat)
	}
	envAt := profileEnvFunc()
	queryDeps := text.QueryCommandDeps{
		ConfigPrep:          configPrep,
		TrustInput:          func() io.Reader { return setup.Input },
		ApplyMediaOverrides: applyMediaOverrides,
		NewSummarizer:       deps.NewSummarizer,
		SummaryJoinTimeout:  deps.SummaryJoinTimeout,
		NewImageGenerator:   deps.NewImageGenerator,
		PhotoConfig:         setup.LoadPhotoConfig,
		NewRemoteResolver:   remoteResolver(deps.RemoteClient, envAt, notices),
		ProfileEnv:          envAt,
		RunTallyEnv:         runtally.OSEnv{},
		StepRetry:           deps.StepRetry,
	}
	profileDeps := profiles.CommandDeps{
		NewResolver:     profiles.NewResolverFunc(remoteResolver(deps.RemoteClient, envAt, notices)),
		ConfigDir:       configPrep,
		ConfigDirLookup: utils.GetClaiConfigDir,
		NewHubPublisher: hubPublisher,
		ProfileEnv:      envAt,
		SetExit:         setExit,
		FindSecret: func(name string, b []byte) (string, bool) {
			s, found := sync.FindSecret(name, b)
			return s.String(), found
		},
		NewPrompt: func(ctx context.Context) remote.Prompter {
			env := runenv.From(ctx)
			return text.TerminalTrustPrompter(env.Stdin(), env.StdinIsTerminal(), env.Stderr())
		},
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
			ConfigPrep:        configPrep,
			LoadConfig:        setup.LoadPhotoConfig,
			NewGenerator:      deps.NewImageGenerator,
			Collector:         imageinput.New(),
			NewPresenter:      present.New,
			NewOpener:         present.NewCommandOpener,
			PreviousImage:     photo.PreviousQueryImage,
			NewRemoteResolver: remoteResolver(deps.RemoteClient, envAt, notices),
		}),
		"video|v": video.Command(video.CommandDeps{
			ConfigPrep: configPrep,
		}),
		"audio|a": audio.Command(audio.CommandDeps{
			ConfigPrep:        func(ctx context.Context) (string, []string, error) { return setup.ConfigRunPrep(ctx, true) },
			NewRemoteResolver: remoteResolver(deps.RemoteClient, envAt, notices),
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
			Publish: cloudPublish(profileDeps),
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

func cloudPublish(deps profiles.CommandDeps) func(ctx context.Context, args []string, opts cloudcmd.PublishOptions) error {
	return func(ctx context.Context, args []string, opts cloudcmd.PublishOptions) error {
		return deps.PublishInteractive(ctx, opts.In, args, profiles.PublishOptions{
			Name:    opts.Name,
			Title:   opts.Title,
			Summary: opts.Summary,
			Note:    opts.Note,
			Handle:  opts.Handle,
		})
	}
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
// status. It never exits; the caller owns os.Exit. It reads the process
// streams, their terminal states, the environment and the signals once.
func Run(args []string, deps Deps) int {
	return run(args, deps, osProcess())
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
		return nil, diag.Errorf("create profile file %q: %w", name, err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return nil, diag.Errorf("start profile %q: %w", name, err)
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
		p := osProcess()
		p.startupWarnings = append(p.startupWarnings, fmt.Sprintf("failed to start cpu profile: %v", err))
		return run(args, deps, p)
	}
	defer stop()
	return Run(args, deps)
}
