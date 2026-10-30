package text

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/modelfallback"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/retry"
	"github.com/baalimago/clai/internal/runtally"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/pkg/claierr"
)

const queryHelp = `query <text>. Queries the chat model; piped stdin is appended to the
prompt, or replaces the -I/-i placeholder. A prompt whose first word starts
with '-' is read as a flag, so pass it after '--'.

Add -l / -loop to iterate fresh sessions toward the goal until complete_task
reports achieved or blocked, or -max-iterations is reached. An iteration that
ends without an accepted completion hands its final reply to the next one.
With -check, an achieved complete_task call is accepted only when that command
exits 0.

Examples:
  clai -query-summary query explain the files in this directory
  clai -reply -query-summary query "and how do I fix that?"
  docker logs my-app | clai -replace LOG -query-summary query "find errors in these logs: LOG"
  clai -tools '*' -query-summary query inspect this repo using tools
  clai -reply-only -tools 'rg,cat' query write release notes > NOTES.md
  clai query -- -why does this fail
  clai -loop query refactor the auth package and make the tests pass
  clai -loop -check 'go test ./...' query fix the failing build`

// QueryCommandDeps are the composition-root collaborators the query command
// needs in plain and loop mode: config prep lives in internal/setup (which
// imports text, so text cannot call it), and TrustInput hands over the
// wizard's mutable input reader for skill-trust prompts, read at setup time.
type QueryCommandDeps struct {
	ConfigPrep func() (confDir string, err error)
	TrustInput func() io.Reader
	// ApplyMediaOverrides hands the resolved media-tool settings to the domains
	// owning those tools (internal/audio today), which text must not import.
	ApplyMediaOverrides func(internal.MediaToolSettings) error
	// NewSummarizer builds the in-flight conversation summarizer; nil or a
	// constructor error attaches nothing (worklog
	// 2026-09-09-conversation-summaries, D26).
	NewSummarizer func(confDir string) (models.Summarizer, error)
	// SummaryJoinTimeout makes a plain query wait this long for its label;
	// zero keeps labelling best effort with no wait.
	SummaryJoinTimeout time.Duration
	// NewImageGenerator builds the generator behind generate_image; nil fails a
	// run that registers the tool with ErrNoImageGenerator.
	NewImageGenerator imagegen.NewGeneratorFunc
	// PhotoConfig returns photoConfig.json, the single source of the image
	// presenter's auto-open-images and image-opener and of the model that
	// route.ImageModelFor and -pm may override.
	PhotoConfig func(confDir string) (photo.Configurations, error)
	// NewRemoteResolver builds the resolver for -prp cchub:, gh: and https://
	// targets; nil refuses them.
	NewRemoteResolver NewRemoteResolverFunc
	// RunTallyEnv is the environment subagent tallies travel through; nil
	// counts no subagents. RunTallyTempRoot is where a run's tally
	// directory is made, "" being the OS default.
	RunTallyEnv      runtally.Env
	RunTallyTempRoot string
}

// QueryCommand builds the query command. With -l the same composition wraps
// its querier in a LoopRunner, so every existing prompt or alias becomes a
// loop by prepending the flag.
func QueryCommand(deps QueryCommandDeps) *internal.Command {
	tf := internal.NewTextFlags()
	lf := internal.NewLoopFlags()
	c := newTextCommand("query", "Query the chat model with the given text; -l iterates toward a goal", queryHelp, &tf, lf.Register)
	c.OnSetup = func(ctx context.Context, c *internal.Command) error {
		if err := lf.Validate(); err != nil {
			return err
		}
		session, err := newTextSession(ctx, c, tf, lf, deps)
		if err != nil {
			return err
		}
		querier, err := session.querier(ctx)
		if err != nil {
			return err
		}
		c.SetQuerier(querier)
		return nil
	}
	return c
}

// openRunTally opens the run's subagent tally when a summary or a parent wants
// one: the querier counts the workers it started into its summary and reports
// to its parent, and runTallyQuerier closes the tally once the run ends.
func openRunTally(deps QueryCommandDeps, summary bool) (*runtally.Scope, error) {
	if deps.RunTallyEnv == nil || !runtally.Wanted(deps.RunTallyEnv, summary) {
		return nil, nil
	}
	scope, err := runtally.Open(deps.RunTallyEnv, deps.RunTallyTempRoot)
	if err != nil {
		return nil, err
	}
	return scope, nil
}

type runTallyQuerier struct {
	inner models.Querier
	scope *runtally.Scope
}

func (r runTallyQuerier) Query(ctx context.Context) error {
	err := r.inner.Query(ctx)
	if closeErr := r.scope.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("close subagent tally: %w", closeErr))
	}
	return err
}

// Answer reports the run the tally wraps: closing the tally is bookkeeping, so
// the completion bell reads the output policy of the run inside it
// (architecture/model-aliases.md).
func (r runTallyQuerier) Answer() models.Querier { return r.inner }

// newTextCommand builds the flag surface shared by the text-querier commands:
// the four TextFlags groups, raw/non-interactive setup and completion hooks.
// registerExtra adds the command's own flags beside the shared groups. tf is a
// pointer because the caller's setup closure reads the values the flagset
// parses.
func newTextCommand(name, desc, helpText string, tf *internal.TextFlags, registerExtra func(*flag.FlagSet)) *internal.Command {
	sources := &internal.CompletionSources{ToolNames: tools.Names}
	return &internal.Command{
		Name:     name,
		Desc:     desc,
		HelpText: helpText,
		Register: func(fs *flag.FlagSet) {
			tf.Raw.Register(fs)
			tf.ReplyStdin.Register(fs)
			tf.AgentText.Register(fs)
			tf.QueryText.Register(fs)
			if registerExtra != nil {
				registerExtra(fs)
			}
		},
		Raw:                 &tf.Raw,
		NonInteractive:      &tf.AgentText.NonInteractive,
		CompleteFlagValueFn: sources.TextFlagValues,
		CompleteArgsFn:      internal.SuppressArgs,
	}
}

// textSession is one query setup: the resolved configuration, the run's
// subagent tally and the step that composes a complete run for any one model.
// A run is composed once per fallback attempt, so a model tried after another
// failed is decorated exactly like the first, while the tally belongs to the
// run and not to any one attempt (architecture/model-aliases.md).
type textSession struct {
	conf  *Configurations
	build modelfallback.Builder
	tally *runtally.Scope
}

// querier resolves the configured model, or the ordered models its alias names,
// into the run the command executes. The tally wraps the resolved chain, so an
// attempt is never closed by the chain and a failed attempt never hides the
// replay guard of the run it composed.
func (s *textSession) querier(ctx context.Context) (models.Querier, error) {
	q, err := modelfallback.New(ctx, s.conf.ModelAliases, s.conf.Model, s.build, modelfallback.NoticeTo(os.Stderr))
	if err != nil {
		if s.tally != nil {
			err = errors.Join(err, fmt.Errorf("close subagent tally: %w", s.tally.Close()))
		}
		return nil, err
	}
	if s.tally != nil {
		return runTallyQuerier{inner: q, scope: s.tally}, nil
	}
	return q, nil
}

// newTextSession runs the config, media and prompt composition query uses in
// plain and loop mode, and leaves the per-model run composition to build.
func newTextSession(ctx context.Context, c *internal.Command, tf internal.TextFlags, lf internal.LoopFlags, deps QueryCommandDeps) (*textSession, error) {
	confDir, err := deps.ConfigPrep()
	if err != nil {
		return nil, err
	}
	var trustInput io.Reader
	if deps.TrustInput != nil {
		trustInput = deps.TrustInput()
	}
	interactive := !tf.AgentText.NonInteractive.Value() && terminalAvailable()
	remoteTargets := &remoteRun{newResolver: deps.NewRemoteResolver, confDir: confDir, trustInput: trustInput, interactive: interactive}
	tConf, err := setupTextConfig(ctx, confDir, tf, c.Args(), trustInput, remoteTargets.profile, remoteTargets.skills)
	if err != nil {
		return nil, err
	}
	// The media tools read the values this run resolved, so the hand-off waits
	// for the profile to be applied by setupTextConfig.
	if deps.ApplyMediaOverrides != nil {
		if err := deps.ApplyMediaOverrides(mediaToolSettings(tf, tConf)); err != nil {
			return nil, fmt.Errorf("apply media tool overrides: %w", err)
		}
	}
	if err := resolveRemoteModel(ctx, tConf, remoteTargets.models); err != nil {
		return nil, err
	}
	// NewQuerier stamps the effective profile on the querier chat; mirror it on
	// the seed so loop iterations persist the same profile as query runs.
	tConf.InitialChat.Profile = tConf.UseProfile
	// Directory reply mode continues the directory-scoped conversation
	// in place (SetupQuerier forces reply mode for it); bind the
	// directory chat id so the continuation lands on the same chat.
	if tf.QueryText.DirReply.Value() {
		if err := applyDirReplyChatID(confDir, tConf); err != nil {
			return nil, fmt.Errorf("apply dir reply chat id: %w", err)
		}
	}
	var summarizer models.Summarizer
	if tConf.SummarizeConversations {
		summarizer = loadSummarizer(confDir, deps.NewSummarizer)
	}
	tally, err := openRunTally(deps, tConf.QuerySummary || lf.Loop.Value())
	if err != nil {
		return nil, err
	}
	return &textSession{
		conf:  tConf,
		tally: tally,
		build: func(ctx context.Context, model string) (models.Querier, error) {
			return buildTextRun(ctx, confDir, tConf, model, tf, lf, deps, summarizer, tally)
		},
	}, nil
}

func buildTextRun(ctx context.Context, confDir string, tConf *Configurations, model string, tf internal.TextFlags, lf internal.LoopFlags, deps QueryCommandDeps, summarizer models.Summarizer, tally *runtally.Scope) (models.Querier, error) {
	conf := *tConf
	conf.Model = model
	conf.InitialChat.Messages = slices.Clone(tConf.InitialChat.Messages)
	conf.InitialChat.Queries = slices.Clone(tConf.InitialChat.Queries)
	conf.BaseTools = maps.Clone(tConf.BaseTools)
	conf.RegisteredTools = maps.Clone(tConf.RegisteredTools)
	q, err := CreateQuerier(ctx, conf)
	if err != nil {
		if startups := mcpStartupErrors(err); len(startups) > 0 {
			return nil, retry.Final(errors.Join(startups...))
		}
		return nil, fmt.Errorf("failed to create text querier: %w", err)
	}
	if tf.QueryText.ReplyOnly.Value() {
		if err := enableReplyOnly(q, !lf.Loop.Value()); err != nil {
			return nil, retry.Final(err)
		}
	}
	if summarizer != nil {
		if setter, ok := q.(interface{ SetSummarizer(models.Summarizer) }); ok {
			setter.SetSummarizer(summarizer)
		}
	}
	if deps.SummaryJoinTimeout > 0 {
		if err := setSummaryJoinTimeout(q, deps.SummaryJoinTimeout); err != nil {
			return nil, retry.Final(err)
		}
	}
	if err := attachImageTurn(confDir, &conf, tf, deps, q); err != nil {
		return nil, retry.Final(err)
	}
	var top models.Querier = q
	if lf.Loop.Value() {
		runner, err := newLoopRunner(q, NewCompletionRecorder(lf.Check.Value()), LoopConfig{
			SeedChat:      conf.InitialChat,
			GoalPrompt:    conf.PostProccessedPrompt,
			MaxIterations: lf.MaxIterations.Value(),
		})
		if err != nil {
			return nil, retry.Final(err)
		}
		top = runner
	} else if conf.QuerySummary {
		if err := enableQuerySummary(q); err != nil {
			return nil, retry.Final(err)
		}
	}
	if err := attachRunTally(q, tally); err != nil {
		return nil, retry.Final(err)
	}
	return top, nil
}

func mcpStartupErrors(err error) []error {
	if err == nil {
		return nil
	}
	if startup, ok := err.(*claierr.McpServerStartupError); ok {
		return []error{startup}
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, child := range multi.Unwrap() {
			out = append(out, mcpStartupErrors(child)...)
		}
		return out
	}
	return mcpStartupErrors(errors.Unwrap(err))
}

// attachRunTally hands the run's tally to the querier that counts its workers.
// The scope itself belongs to the run, so the chain around the attempts keeps
// the replay guard of the querier it composed (architecture/model-aliases.md).
func attachRunTally(q models.Querier, tally *runtally.Scope) error {
	if tally == nil {
		return nil
	}
	setter, ok := q.(interface{ SetRunTally(*runtally.Scope) })
	if !ok {
		return fmt.Errorf("subagent tally requires a text querier, got %T", q)
	}
	setter.SetRunTally(tally)
	return nil
}

func setSummaryJoinTimeout(q models.Querier, d time.Duration) error {
	setter, ok := q.(interface{ SetSummaryJoinTimeout(time.Duration) })
	if !ok {
		return fmt.Errorf("summary join timeout requires a text querier, got %T", q)
	}
	setter.SetSummaryJoinTimeout(d)
	return nil
}

// enableReplyOnly keeps stdout for the reply. With showProgress, a terminal
// stderr shows the session's progress as a terminal stdout would.
func enableReplyOnly(q models.Querier, showProgress bool) error {
	setter, ok := q.(interface {
		SendTranscriptToStderr()
		ShowProgressOn(io.Writer)
	})
	if !ok {
		return fmt.Errorf("reply-only requires a text querier, got %T", q)
	}
	setter.SendTranscriptToStderr()
	if showProgress {
		setter.ShowProgressOn(os.Stderr)
	}
	return nil
}

func enableQuerySummary(q models.Querier) error {
	summarizer, ok := q.(interface{ EnableQuerySummary() })
	if !ok {
		return fmt.Errorf("query summary requires a text querier, got %T", q)
	}
	summarizer.EnableQuerySummary()
	return nil
}

// newLoopRunner adapts the text querier to a LoopQuerier. SetupQuerier returns
// every vendor branch as *Querier[C]; another implementation is an error, not
// a silently degraded loop.
func newLoopRunner(q models.Querier, recorder CompletionRecorder, cfg LoopConfig) (LoopRunner, error) {
	loopQuerier, ok := q.(LoopQuerier)
	if !ok {
		return nil, fmt.Errorf("loop requires a text querier, got %T", q)
	}
	return NewLoopRunner(loopQuerier, recorder, cfg), nil
}

// loadSummarizer builds the in-flight conversation summarizer once per run, so
// every fallback attempt shares the same label index. A failure is traced and
// never reaches the run: the caller skips the load when
// summarize-conversations is off, so an opted-out run never loads the
// summarizer's config.
func loadSummarizer(confDir string, newSummarizer func(string) (models.Summarizer, error)) models.Summarizer {
	if newSummarizer == nil {
		return nil
	}
	s, err := newSummarizer(confDir)
	if err != nil {
		traceSummaryf("summarizer unavailable, continuing without: %v", err)
		return nil
	}
	return s
}

// ApplyFlagOverrides applies the CLI flag values onto the file-loaded
// configuration.
//
// The default flags are needed to ensure that the configuration isn't being overwritten by the default flags.
// Meaning: Only set the value of tConf to the flag, if it's not the default, leave the configuration found in file.
// If there is no check if the flagSet is default, there may be a case where default > file, which breaks
// the configuration convention flags > file > default
func ApplyFlagOverrides(tConf *Configurations, tf internal.TextFlags) {
	if tf.ReplyStdin.ExpectReplace.Value() || tf.ReplyStdin.StdinReplace.Explicit() {
		tConf.StdinReplace = tf.ReplyStdin.StdinReplace.Value()
	}
	if tf.AgentText.ChatModel.Changed() {
		tConf.Model = tf.AgentText.ChatModel.Value()
	}
	if tf.ReplyStdin.Reply.Changed() {
		tConf.ReplyMode = tf.ReplyStdin.Reply.Value()
	}
	if tf.QueryText.DirReply.Changed() {
		tConf.DirReplyMode = tf.QueryText.DirReply.Value()
		// Directory reply mode continues the bound conversation in place;
		// reply mode rides along so the system prompt is skipped.
		tConf.ReplyMode = true
	}
	if tf.Raw.Changed() {
		tConf.Raw = tf.Raw.Value()
	}
	// Tool selection is interpreted in the querier factory based on the -t/-tools value.
	if tf.AgentText.Profile.Changed() {
		tConf.UseProfile = tf.AgentText.Profile.Value()
	}
	if tf.AgentText.ProfilePath.Changed() {
		tConf.ProfilePath = tf.AgentText.ProfilePath.Value()
	}
	if tf.QueryText.ShellContext.Changed() {
		tConf.ShellContext = tf.QueryText.ShellContext.Value()
	}
	if tf.QueryText.Summarize.Explicit() {
		tConf.SummarizeConversations = tf.QueryText.Summarize.Value()
	}
	if tf.QueryText.QuerySummary.Explicit() {
		tConf.QuerySummary = tf.QueryText.QuerySummary.Value()
	}
	if tf.QueryText.SummaryModel.Changed() {
		tConf.SummaryModel = tf.QueryText.SummaryModel.Value()
	}
	tConf.ImageInputs = tf.QueryText.Images.Value()
	if tf.AgentText.MaxTokens.Explicit() {
		if tConf.Stoploss == nil {
			tConf.Stoploss = &Stoploss{}
		}
		tConf.Stoploss.MaxTokens = tf.AgentText.MaxTokens.Value()
	}
	if tf.AgentText.MaxToolCalls.Explicit() {
		v := tf.AgentText.MaxToolCalls.Value()
		tConf.MaxToolCalls = &v
	}
	if tf.AgentText.MaxToolCallsAfterHandover.Explicit() {
		if tConf.Stoploss == nil {
			tConf.Stoploss = &Stoploss{}
		}
		tConf.Stoploss.MaxToolCallsAfterHandover = tf.AgentText.MaxToolCallsAfterHandover.Value()
	}
}

// ApplyProfileOverrides re-applies the flags that may overrule profile
// configurations.
func ApplyProfileOverrides(tConf *Configurations, tf internal.TextFlags) {
	if tf.AgentText.ChatModel.Changed() {
		tConf.Model = tf.AgentText.ChatModel.Value()
	}
	if tf.AgentText.MediaTools.AudioModel.Changed() {
		tConf.AudioModel = tf.AgentText.MediaTools.AudioModel.Value()
	}
}

// mediaToolSettings are the media-tool values this run resolved: the audio
// model the flags or the profile chose, and the -af format, which no profile
// key sets.
func mediaToolSettings(tf internal.TextFlags, tConf *Configurations) internal.MediaToolSettings {
	return internal.MediaToolSettings{
		AudioModel:  tConf.AudioModel,
		AudioFormat: tf.AgentText.MediaTools.AudioFormat.Value(),
	}
}
