package text

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
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
	// ApplyMediaOverrides hands the media-tool flags to the domains owning
	// those tools (internal/audio today), which text must not import.
	ApplyMediaOverrides func(internal.MediaToolFlags) error
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
		q, tConf, err := setupTextQuerier(ctx, c, tf, deps)
		if err != nil {
			return err
		}
		if tf.QueryText.ReplyOnly.Value() {
			if err := enableReplyOnly(q, !lf.Loop.Value()); err != nil {
				return err
			}
		}
		var top models.Querier = q
		if lf.Loop.Value() {
			runner, err := newLoopRunner(q, NewCompletionRecorder(lf.Check.Value()), LoopConfig{
				SeedChat:      tConf.InitialChat,
				GoalPrompt:    tConf.PostProccessedPrompt,
				MaxIterations: lf.MaxIterations.Value(),
			})
			if err != nil {
				return err
			}
			top = runner
		} else if tConf.QuerySummary {
			if err := enableQuerySummary(q); err != nil {
				return err
			}
		}
		top, err = withRunTally(q, top, deps, tConf.QuerySummary || lf.Loop.Value())
		if err != nil {
			return err
		}
		c.SetQuerier(top)
		return nil
	}
	return c
}

// withRunTally opens the run's subagent tally when a summary or a parent
// wants one: q counts the workers it started into its summary and reports
// to its parent, and the returned querier closes the tally once top ends.
func withRunTally(q, top models.Querier, deps QueryCommandDeps, summary bool) (models.Querier, error) {
	if deps.RunTallyEnv == nil || !runtally.Wanted(deps.RunTallyEnv, summary) {
		return top, nil
	}
	setter, ok := q.(interface{ SetRunTally(*runtally.Scope) })
	if !ok {
		return nil, fmt.Errorf("subagent tally requires a text querier, got %T", q)
	}
	scope, err := runtally.Open(deps.RunTallyEnv, deps.RunTallyTempRoot)
	if err != nil {
		return nil, err
	}
	setter.SetRunTally(scope)
	return runTallyQuerier{inner: top, scope: scope}, nil
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

// setupTextQuerier runs the config, media, prompt and summarizer
// composition query uses in plain and loop mode.
func setupTextQuerier(ctx context.Context, c *internal.Command, tf internal.TextFlags, deps QueryCommandDeps) (models.Querier, *Configurations, error) {
	confDir, err := deps.ConfigPrep()
	if err != nil {
		return nil, nil, err
	}
	if deps.ApplyMediaOverrides != nil {
		if err := deps.ApplyMediaOverrides(tf.AgentText.MediaTools); err != nil {
			return nil, nil, fmt.Errorf("apply media tool overrides: %w", err)
		}
	}
	var trustInput io.Reader
	if deps.TrustInput != nil {
		trustInput = deps.TrustInput()
	}
	interactive := !tf.AgentText.NonInteractive.Value() && terminalAvailable()
	remoteTargets := &remoteRun{newResolver: deps.NewRemoteResolver, confDir: confDir, trustInput: trustInput, interactive: interactive}
	q, tConf, err := SetupQuerier(ctx, confDir, tf, c.Args(), trustInput, remoteTargets.profile, remoteTargets.skills)
	if err != nil {
		// The startup error names the server and the stage; the setup layers
		// it passed through add nothing a user can act on.
		var startup *claierr.McpServerStartupError
		if errors.As(err, &startup) {
			return nil, nil, startup
		}
		return nil, nil, err
	}
	// NewQuerier stamps the effective profile on the querier chat; mirror it on
	// the seed so loop iterations persist the same profile as query runs.
	tConf.InitialChat.Profile = tConf.UseProfile
	// Directory reply mode continues the directory-scoped conversation
	// in place (SetupQuerier forces reply mode for it); bind the
	// directory chat id so the continuation lands on the same chat.
	if tf.QueryText.DirReply.Value() {
		if err := applyDirReplyChatID(confDir, tConf, q); err != nil {
			return nil, nil, fmt.Errorf("apply dir reply chat id: %w", err)
		}
	}
	if tConf.SummarizeConversations {
		attachSummarizer(confDir, deps.NewSummarizer, q)
	}
	if deps.SummaryJoinTimeout > 0 {
		if err := setSummaryJoinTimeout(q, deps.SummaryJoinTimeout); err != nil {
			return nil, nil, err
		}
	}
	if err := attachImageTurn(confDir, tConf, tf, deps, q); err != nil {
		return nil, nil, err
	}
	return q, tConf, nil
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

// attachSummarizer hands the constructed summarizer to the querier through
// its setter; a failure is traced and never reaches the run. The caller
// skips it when summarize-conversations is off, so an opted-out run never
// loads the summarizer's config.
func attachSummarizer(confDir string, newSummarizer func(string) (models.Summarizer, error), q models.Querier) {
	if newSummarizer == nil {
		return
	}
	setter, ok := q.(interface{ SetSummarizer(models.Summarizer) })
	if !ok {
		return
	}
	s, err := newSummarizer(confDir)
	if err != nil {
		traceSummaryf("summarizer unavailable, continuing without: %v", err)
		return
	}
	setter.SetSummarizer(s)
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
}
