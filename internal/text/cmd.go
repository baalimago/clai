package text

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/tools"
)

const queryHelp = `query <text>. Queries the chat model; piped stdin is appended to the
prompt, or replaces the -I/-i placeholder. A prompt whose first word starts
with '-' is read as a flag, so pass it after '--'.

Examples:
  clai q explain the files in this directory
  clai -re q "and how do I fix that?"
  docker logs my-app | clai -I LOG q "find errors in these logs: LOG"
  clai -t '*' q inspect this repo using tools
  clai q -- -why does this fail`

// QueryCommandDeps are the composition-root collaborators the query command
// needs: config prep lives in internal/setup (which imports text, so text
// cannot call it), and TrustInput hands over the wizard's mutable input
// reader for skill-trust prompts, read at setup time.
type QueryCommandDeps struct {
	ConfigPrep func() (confDir string, err error)
	TrustInput func() io.Reader
	// ApplyMediaOverrides hands the media-tool flags to the domains owning
	// those tools (internal/audio today), which text must not import.
	ApplyMediaOverrides func(internal.MediaToolFlags) error
}

// QueryCommand builds the query command.
func QueryCommand(deps QueryCommandDeps) *internal.Command {
	sources := &internal.CompletionSources{ToolNames: tools.Names}
	tf := internal.NewTextFlags()
	c := &internal.Command{
		Name:     "query",
		Desc:     "Query the chat model with the given text",
		HelpText: queryHelp,
		Register: func(fs *flag.FlagSet) {
			tf.Raw.Register(fs)
			tf.ReplyStdin.Register(fs)
			tf.AgentText.Register(fs)
			tf.QueryText.Register(fs)
		},
		Raw:                 &tf.Raw,
		NonInteractive:      &tf.AgentText.NonInteractive,
		CompleteFlagValueFn: sources.TextFlagValues,
		CompleteArgsFn:      internal.SuppressArgs,
	}
	c.OnSetup = func(ctx context.Context, c *internal.Command) error {
		confDir, err := deps.ConfigPrep()
		if err != nil {
			return err
		}
		if deps.ApplyMediaOverrides != nil {
			if err := deps.ApplyMediaOverrides(tf.AgentText.MediaTools); err != nil {
				return fmt.Errorf("apply media tool overrides: %w", err)
			}
		}
		var trustInput io.Reader
		if deps.TrustInput != nil {
			trustInput = deps.TrustInput()
		}
		q, tConf, err := SetupQuerier(ctx, confDir, tf, c.Args(), trustInput)
		if err != nil {
			return err
		}
		// Directory reply mode continues the directory-scoped conversation
		// in place (SetupQuerier forces reply mode for it); bind the
		// directory chat id so the continuation lands on the same chat.
		if tf.QueryText.DirReply.Value() {
			if err := applyDirReplyChatID(confDir, tConf, q); err != nil {
				return fmt.Errorf("apply dir reply chat id: %w", err)
			}
		}
		c.SetQuerier(q)
		return nil
	}
	return c
}

// ApplyFlagOverrides applies the CLI flag values onto the file-loaded
// configuration.
//
// The default flags are needed to ensure that the configuration isn't being overwritten by the default flags.
// Meaning: Only set the value of tConf to the flag, if it's not the default, leave the configuration found in file.
// If there is no check if the flagSet is default, there may be a case where default > file, which breaks
// the configuration convention flags > file > default
func ApplyFlagOverrides(tConf *Configurations, tf internal.TextFlags) {
	if tf.ReplyStdin.ExpectReplace.Value() {
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
