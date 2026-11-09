package runenv

import (
	"strings"

	"github.com/baalimago/clai/pkg/diagnostic"
)

// Kind is the generic diagnostic kind every subsystem registers.
type Kind string

const (
	KindError   Kind = "error"
	KindWarning Kind = "warning"
	KindNotice  Kind = "notice"
	KindDebug   Kind = "debug"
)

// Subsystem names. The legacy ones are the lower-cased DEBUG_<SUBSYSTEM>
// suffixes of the README's Legacy controls table; vendors sit under vendor.
const (
	SubsystemClai                   = "clai"
	SubsystemConfig                 = "config"
	SubsystemSetup                  = "setup"
	SubsystemMCP                    = "mcp"
	SubsystemGlob                   = "glob"
	SubsystemTools                  = "tools"
	SubsystemAudio                  = "audio"
	SubsystemPhoto                  = "photo"
	SubsystemVideo                  = "video"
	SubsystemCloud                  = "cloud"
	SubsystemCall                   = "call"
	SubsystemChat                   = "chat"
	SubsystemClaudifiedMsgs         = "claudified_msgs"
	SubsystemCostManager            = "cost_manager"
	SubsystemDirscope               = "dirscope"
	SubsystemLookback               = "lookback"
	SubsystemOpenrouterModelCatalog = "openrouter_model_catalog"
	SubsystemProfiles               = "profiles"
	SubsystemReplyMode              = "reply_mode"
	SubsystemSkills                 = "skills"
	SubsystemStoploss               = "stoploss"
	SubsystemSummary                = "summary"
	SubsystemTextQuerier            = "text_querier"
	SubsystemToolsRegistrySet       = "tools_registry_set"
	SubsystemVerbose                = "verbose"
	SubsystemVendorOpenAI           = "vendor.openai"
	SubsystemVendorOpenRouter       = "vendor.openrouter"
	SubsystemVendorMistral          = "vendor.mistral"
	SubsystemVendorHuggingFace      = "vendor.huggingface"
	SubsystemVendorAnthropic        = "vendor.anthropic"
)

// Named types beyond the generic kinds.
const (
	// TypeSignal reports an interrupt signal; the user must see it at every
	// level.
	TypeSignal = "clai.signal"
	// TypeSchemaCacheFailed is a failed MCP schema cache write: a low-urgency
	// background failure, buffered until the run ends (F4).
	TypeSchemaCacheFailed = "mcp.schema_cache_failed"
	// TypeSkillTrust asks the user to trust a skill; it is shown at every level.
	TypeSkillTrust = "skills.trust_required"
	// TypeRunSummary is the summary of a query (-qs) or a loop run under the
	// JSON format: the user asked for it, so it is shown at every level.
	TypeRunSummary = "clai.run_summary"
)

// Subsystem is one source of diagnostics: the name LOG_LEVEL selects it by
// and, for the usage, what it reports.
type Subsystem struct {
	Name    string
	Purpose string
}

// Subsystems is the CLI's subsystem registry. A subsystem is registered
// here, with its purpose, and nowhere else: the diagnostic types, the
// levels LOG_LEVEL accepts and the usage's table all come from this list.
func Subsystems() []Subsystem {
	return []Subsystem{
		{SubsystemClai, "the run itself: start, signals, shutdown"},
		{SubsystemConfig, "configuration files: loading and migrating"},
		{SubsystemSetup, "the setup wizard and what it writes"},
		{SubsystemChat, "stored conversations: saving, listing, continuing"},
		{SubsystemDirscope, "which conversation belongs to the directory"},
		{SubsystemReplyMode, "replay and reply: the conversation being continued"},
		{SubsystemProfiles, "profiles: loading, validating, installing"},
		{SubsystemSkills, "skills: discovery, trust and loading"},
		{SubsystemCloud, "the clai.chat account and its sync"},
		{SubsystemTextQuerier, "a query: prompt setup, steps, retries, fallback"},
		{SubsystemCall, "each tool call and its result"},
		{SubsystemSummary, "conversation titles and summaries"},
		{SubsystemLookback, "recent conversations surfaced to the model"},
		{SubsystemStoploss, "the token and tool-call limits of a run"},
		{SubsystemCostManager, "token prices and the cost of a run"},
		{SubsystemOpenrouterModelCatalog, "OpenRouter's model and price list"},
		{SubsystemTools, "which built-in tools a run is given"},
		{SubsystemToolsRegistrySet, "each tool as it is registered"},
		{SubsystemMCP, "MCP servers: start, handshake, schema cache"},
		{SubsystemGlob, "the files a -glob pattern adds to the prompt"},
		{SubsystemAudio, "recording and transcription"},
		{SubsystemPhoto, "image generation"},
		{SubsystemVideo, "video generation"},
		{SubsystemVerbose, "extra detail: saved chat paths, raw stream tokens"},
		{SubsystemClaudifiedMsgs, "the messages as sent to Anthropic"},
		{SubsystemVendorOpenAI, "requests to and responses from OpenAI"},
		{SubsystemVendorAnthropic, "requests to and responses from Anthropic"},
		{SubsystemVendorOpenRouter, "requests to and responses from OpenRouter"},
		{SubsystemVendorMistral, "requests to and responses from Mistral"},
		{SubsystemVendorHuggingFace, "requests to and responses from Hugging Face"},
		{"vendor.berget", "requests to and responses from Berget"},
		{"vendor.deepseek", "requests to and responses from DeepSeek"},
		{"vendor.gemini", "requests to and responses from Gemini"},
		{"vendor.inception", "requests to and responses from Inception"},
		{"vendor.novita", "requests to and responses from Novita"},
		{"vendor.ollama", "requests to and responses from Ollama"},
		{"vendor.xai", "requests to and responses from xAI"},
	}
}

// TypeName is the registered name of kind for subsystem.
func TypeName(subsystem string, kind Kind) string {
	return subsystem + "." + string(kind)
}

// Types is the CLI's diagnostic type registry: the four generic kinds of
// every subsystem plus the named types. Tool-call debug detail is buffered:
// it is high-volume and would interleave with the streamed reply (F4).
func Types() []diagnostic.Type {
	levels := map[Kind]diagnostic.Level{
		KindError:   diagnostic.LevelError,
		KindWarning: diagnostic.LevelWarn,
		KindNotice:  diagnostic.LevelInfo,
		KindDebug:   diagnostic.LevelDebug,
	}
	var types []diagnostic.Type
	for _, registered := range Subsystems() {
		subsystem := registered.Name
		for _, kind := range []Kind{KindError, KindWarning, KindNotice, KindDebug} {
			mode := diagnostic.Immediate
			if subsystem == SubsystemCall && kind == KindDebug {
				mode = diagnostic.Buffered
			}
			types = append(types, diagnostic.Type{
				Name:           TypeName(subsystem, kind),
				Subsystem:      subsystem,
				Level:          levels[kind],
				Mode:           mode,
				ActionRequired: kind == KindError,
			})
		}
	}
	return append(types,
		diagnostic.Type{Name: TypeSignal, Subsystem: SubsystemClai, Level: diagnostic.LevelWarn, Mode: diagnostic.Immediate, ActionRequired: true},
		diagnostic.Type{Name: TypeSchemaCacheFailed, Subsystem: SubsystemMCP, Level: diagnostic.LevelWarn, Mode: diagnostic.Buffered},
		diagnostic.Type{Name: TypeSkillTrust, Subsystem: SubsystemSkills, Level: diagnostic.LevelWarn, Mode: diagnostic.Immediate, ActionRequired: true},
		diagnostic.Type{Name: TypeRunSummary, Subsystem: SubsystemClai, Level: diagnostic.LevelInfo, Mode: diagnostic.Immediate, ActionRequired: true},
	)
}

// VendorSubsystem is the subsystem of the vendor whose debug control is env,
// such as DEBUG_OPENAI or XAI_DEBUG; it is vendor.<name>.
func VendorSubsystem(env string) string {
	name := strings.TrimSuffix(strings.TrimPrefix(env, "DEBUG_"), "_DEBUG")
	return "vendor." + strings.ToLower(name)
}
