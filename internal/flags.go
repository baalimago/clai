package internal

import (
	"flag"
	"strconv"
)

// StringFlag, BoolFlag and IntFlag are alias-aware flag values: one value
// backs both a short and a long name (last one parsed wins), explicit-set
// is tracked, and each value carries its own default so override cascades
// can ask Changed() locally instead of comparing against a global defaults
// bag. Flags equal to their default are treated as unset by Changed() —
// the historical override semantics, preserved deliberately.
type StringFlag struct {
	val string
	def string
	set bool
}

// NewStringFlag returns a string flag whose zero reading is def.
func NewStringFlag(def string) StringFlag {
	return StringFlag{val: def, def: def}
}

func (f *StringFlag) String() string { return f.val }

func (f *StringFlag) Set(v string) error {
	f.val = v
	f.set = true
	return nil
}

// Register binds the flag onto fs under every given name.
func (f *StringFlag) Register(fs *flag.FlagSet, desc string, names ...string) {
	for _, name := range names {
		fs.Var(f, name, desc)
	}
}

// Value returns the current value (the default until parsed).
func (f *StringFlag) Value() string { return f.val }

// Explicit reports whether the flag was passed on the command line.
func (f *StringFlag) Explicit() bool { return f.set }

// Changed reports whether the value differs from the flag's default.
func (f *StringFlag) Changed() bool { return f.val != f.def }

type BoolFlag struct {
	val bool
	def bool
	set bool
}

func (f *BoolFlag) String() string { return strconv.FormatBool(f.val) }

func (f *BoolFlag) Set(v string) error {
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return err
	}
	f.val = parsed
	f.set = true
	return nil
}

// IsBoolFlag marks the flag bool-arity for stdlib flag and the upstream
// command scanner's arity union.
func (f *BoolFlag) IsBoolFlag() bool { return true }

func (f *BoolFlag) Register(fs *flag.FlagSet, desc string, names ...string) {
	for _, name := range names {
		fs.Var(f, name, desc)
	}
}

func (f *BoolFlag) Value() bool    { return f.val }
func (f *BoolFlag) Explicit() bool { return f.set }
func (f *BoolFlag) Changed() bool  { return f.val != f.def }

type IntFlag struct {
	val int
	def int
	set bool
}

func (f *IntFlag) String() string { return strconv.Itoa(f.val) }

func (f *IntFlag) Set(v string) error {
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return err
	}
	f.val = parsed
	f.set = true
	return nil
}

func (f *IntFlag) Register(fs *flag.FlagSet, desc string, names ...string) {
	for _, name := range names {
		fs.Var(f, name, desc)
	}
}

func (f *IntFlag) Value() int     { return f.val }
func (f *IntFlag) Explicit() bool { return f.set }
func (f *IntFlag) Changed() bool  { return f.val != f.def }

// RawFlag is the "common" group: raw output for every command that prints
// querier or conversation content.
type RawFlag struct {
	BoolFlag
}

func (g *RawFlag) Register(fs *flag.FlagSet) {
	g.BoolFlag.Register(fs, "Set to true to print raw output (don't attempt to use 'glow').", "r", "raw")
}

// NonInteractiveFlag disables the interactive-macro fallback.
type NonInteractiveFlag struct {
	BoolFlag
}

func (g *NonInteractiveFlag) Register(fs *flag.FlagSet) {
	g.BoolFlag.Register(fs, "Disable interactive stdin fallback after macro inputs; instead auto-exit with trailing quits.", "n", "non-interactive")
}

// ReplyStdinFlags holds the reply + stdin-substitution flags shared by the
// text, photo and video commands.
type ReplyStdinFlags struct {
	Reply         BoolFlag
	StdinReplace  StringFlag
	ExpectReplace BoolFlag
}

// NewReplyStdinFlags seeds the group's defaults ('{}' stdin placeholder).
func NewReplyStdinFlags() ReplyStdinFlags {
	return ReplyStdinFlags{StdinReplace: NewStringFlag("{}")}
}

func (g *ReplyStdinFlags) Register(fs *flag.FlagSet) {
	g.Reply.Register(fs, "Set to true to reply to the previous query, meaning that it will be used as context for your next query.", "re", "reply")
	g.StdinReplace.Register(fs, "Set the string to replace with stdin. (flag syntax borrowed from xargs)", "I", "replace")
	g.ExpectReplace.Register(fs, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. (flag syntax borrowed from xargs)", "i")
}

// MediaFlagSpec parameterizes one medium's flag surface: photo and video
// differ only in these names, descriptions and the output-dir default.
type MediaFlagSpec struct {
	ModelNames  []string
	ModelDesc   string
	DirNames    []string
	DirDesc     string
	DirDefault  string
	PrefixNames []string
	PrefixDesc  string
}

// MediaFlags is the flag surface shared by the media commands: raw output,
// the reply/stdin group and the model/dir/prefix triple.
type MediaFlags struct {
	Raw        RawFlag
	ReplyStdin ReplyStdinFlags
	Model      StringFlag
	Dir        StringFlag
	Prefix     StringFlag

	spec MediaFlagSpec
}

// NewMediaFlags seeds a medium's defaults: its output dir, the 'clai' file
// prefix and the group's '{}' stdin placeholder.
func NewMediaFlags(spec MediaFlagSpec) MediaFlags {
	return MediaFlags{
		ReplyStdin: NewReplyStdinFlags(),
		Dir:        NewStringFlag(spec.DirDefault),
		Prefix:     NewStringFlag("clai"),
		spec:       spec,
	}
}

func (f *MediaFlags) Register(fs *flag.FlagSet) {
	f.Raw.Register(fs)
	f.ReplyStdin.Register(fs)
	f.Model.Register(fs, f.spec.ModelDesc, f.spec.ModelNames...)
	f.Dir.Register(fs, f.spec.DirDesc, f.spec.DirNames...)
	f.Prefix.Register(fs, f.spec.PrefixDesc, f.spec.PrefixNames...)
}

// MediaConfig points at the configuration fields a media command's flags
// override; the photo and video configurations share this shape.
type MediaConfig struct {
	Model        *string
	ReplyMode    *bool
	StdinReplace *string
	OutputDir    *string
	OutputPrefix *string
}

// Apply writes the flag values onto the configuration, keeping the
// flags > file > default precedence: -i seeds the placeholder, an explicit
// -I/-replace overrules it, and a flag equal to its default counts as unset.
func (f *MediaFlags) Apply(c MediaConfig) {
	if f.ReplyStdin.ExpectReplace.Value() {
		*c.StdinReplace = f.ReplyStdin.StdinReplace.Value()
	}
	if f.ReplyStdin.Reply.Changed() {
		*c.ReplyMode = f.ReplyStdin.Reply.Value()
	}
	if f.ReplyStdin.StdinReplace.Changed() {
		*c.StdinReplace = f.ReplyStdin.StdinReplace.Value()
	}
	if f.Model.Changed() {
		*c.Model = f.Model.Value()
	}
	if f.Prefix.Changed() {
		*c.OutputPrefix = f.Prefix.Value()
	}
	if f.Dir.Changed() {
		*c.OutputDir = f.Dir.Value()
	}
}

// MediaToolFlags configures the media tools an agent run may call. The
// media commands own the same flag names one level down for their own
// runs; here they configure the tools, so a normal query or chat can pick
// the model its transcriptions use. Video and photo join when tools for
// them exist — a flag that silently does nothing is worse than no flag.
type MediaToolFlags struct {
	AudioModel  StringFlag
	AudioFormat StringFlag
}

func (g *MediaToolFlags) Register(fs *flag.FlagSet) {
	g.AudioModel.Register(fs, "Set the model the audio_transcribe tool uses for this run. Overrides audioConfig.json.", "am", "audio-model")
	g.AudioFormat.Register(fs, "Set the transcript format the audio_transcribe tool returns: vtt|srt|text|json. Overrides the tool's own choice.", "af", "audio-format")
}

// AgentTextFlags is the model/agent group shared by every text-querier
// command (query, chat and chat continue): model selection, tool and limit
// configuration, profiles.
type AgentTextFlags struct {
	ChatModel                 StringFlag
	UseTools                  StringFlag
	CmdBan                    StringFlag
	UseLookback               BoolFlag
	MaxTokens                 IntFlag
	MaxToolCalls              IntFlag
	MaxToolCallsAfterHandover IntFlag
	Glob                      StringFlag
	Profile                   StringFlag
	ProfilePath               StringFlag
	NonInteractive            NonInteractiveFlag
	MediaTools                MediaToolFlags
}

func (g *AgentTextFlags) Register(fs *flag.FlagSet) {
	g.ChatModel.Register(fs, "Set the chat model to use.", "cm", "chat-model")
	g.UseTools.Register(fs, "Enable tools. Use '*' for all tools or comma-separated list for specific tools.", "t", "tools")
	g.CmdBan.Register(fs, "Append comma-separated command ban entries for this run (e.g. 'rm,sudo'). Commands matching a ban are refused before they spawn.", "cmd-ban")
	g.UseLookback.Register(fs, "Enable conversation lookback (recent-conversations memory + search/inspect/read tools).", "lb", "lookback")
	g.MaxTokens.Register(fs, "Set the max context tokens for this run. 0 = unlimited. Overrides stoploss.max-tokens in textConfig.json.", "mt", "max-tokens")
	g.MaxToolCalls.Register(fs, "Set the max tool calls for this run. 0 = unlimited. Overrides max-tool-calls in textConfig.json.", "mtc", "max-tool-calls")
	g.MaxToolCallsAfterHandover.Register(fs, "Set the max tool calls for the post-handover phase of this run. 0 = unlimited. Overrides stoploss.max-tool-calls-after-handover in textConfig.json.", "max-tool-calls-after-handover")
	g.Glob.Register(fs, "Glob files into the query context, e.g. -g '*.go'.", "g", "glob")
	g.Profile.Register(fs, profileFlagDesc, "p", "profile")
	g.ProfilePath.Register(fs, "Set this to the path of a profile file to use.", "prp", "profile-path")
	g.NonInteractive.Register(fs)
	g.MediaTools.Register(fs)
}

const profileFlagDesc = "Set this to the override profile you'd like to use. Configure with 'clai setup' -> 2."

// ChatFlags is the chat tree's group. Every chat subcommand reads stored
// transcripts and runs no model, so the agent group's model, tool and
// limit flags would be inert there. Profile is the exception: continuing a
// chat stamps it onto the conversation, steering later -dre queries.
type ChatFlags struct {
	Raw            RawFlag
	NonInteractive NonInteractiveFlag
	Profile        StringFlag
}

func (g *ChatFlags) Register(fs *flag.FlagSet) {
	g.Raw.Register(fs)
	g.NonInteractive.Register(fs)
	g.Profile.Register(fs, profileFlagDesc, "p", "profile")
}

// QueryTextFlags holds the query-only flags: reply modes, skills,
// structured responses and prompt shell context — none of which apply to
// chat sessions.
type QueryTextFlags struct {
	DirReply       BoolFlag
	UseSkills      StringFlag
	ResponseFormat StringFlag
	ShellContext   StringFlag
}

func (g *QueryTextFlags) Register(fs *flag.FlagSet) {
	g.DirReply.Register(fs, "Set to true to reply to the previous directory-scoped conversation (bound to the current working directory).", "dre", "dir-reply")
	g.UseSkills.Register(fs, "Enable skills. Use '*' to enable or 'none' to disable for the current run.", "s", "skills")
	g.ResponseFormat.Register(fs, "Block streaming and print only the final structured response.", "rf", "response-format")
	g.ShellContext.Register(fs, "Auto-append shell context by name.", "asc", "add-shell-context")
}

// TextFlags composes the four text-path groups: the parameter type for
// text.SetupQuerier. Query registers all four; chat registers Raw and
// AgentText, leaving the others zero-valued (behavior-identical to unset).
type TextFlags struct {
	Raw        RawFlag
	ReplyStdin ReplyStdinFlags
	AgentText  AgentTextFlags
	QueryText  QueryTextFlags
}

// NewTextFlags seeds the composed groups' defaults.
func NewTextFlags() TextFlags {
	return TextFlags{ReplyStdin: NewReplyStdinFlags()}
}
