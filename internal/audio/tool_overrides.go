package audio

import (
	"fmt"
	"slices"
	"sync"

	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/remote"
)

// transcribeOverride holds run-scoped configuration for the
// audio_transcribe tool, so a normal query (plain or loop mode) or chat can
// pick the transcription model its tools use (the tool engine otherwise reads
// audioConfig.json). CLI flags configure the tool runtime for the
// duration of the run.
var (
	transcribeOverrideMu sync.RWMutex
	transcribeOverride   ToolOverrides
)

// ToolOverrides are a run's audio_transcribe settings; an empty field leaves that one alone.
type ToolOverrides struct {
	// Model is a model, a model-aliases verb or a remote model list
	Model string
	// Models is the ordered list a remote Model resolved to
	Models []string
	Format string
	// NewResolver resolves a remote model audioConfig.json names; nil uses the default client
	NewResolver func(confDir string, prompt remote.Prompter) (remote.Resolver, error)
}

// SetTranscribeOverrides sets the model and transcript format the
// audio_transcribe tool uses, overriding audioConfig.json and the tool
// call's own output_format. The format is validated here so a bad value fails
// the run immediately rather than mid-conversation, inside a tool call.
func SetTranscribeOverrides(o ToolOverrides) error {
	if o.Format != "" {
		if _, err := ParseOutputFormat(o.Format); err != nil {
			return err
		}
	}
	if o.Model == "" && len(o.Models) > 0 {
		return fmt.Errorf("audio model list %v names no model it resolves", o.Models)
	}
	o.Models = slices.Clone(o.Models)
	transcribeOverrideMu.Lock()
	defer transcribeOverrideMu.Unlock()
	transcribeOverride = o
	return nil
}

// ResetTranscribeOverrides clears the overrides; the tool falls back to
// audioConfig.json and the tool call's own format.
func ResetTranscribeOverrides() {
	transcribeOverrideMu.Lock()
	defer transcribeOverrideMu.Unlock()
	transcribeOverride = ToolOverrides{}
}

func transcribeOverrides() ToolOverrides {
	transcribeOverrideMu.RLock()
	defer transcribeOverrideMu.RUnlock()
	o := transcribeOverride
	o.Models = slices.Clone(o.Models)
	return o
}

// apply layers the overrides onto the loaded config and returns the call's format.
func (o ToolOverrides) apply(aConf *Configurations, outputFormat string) string {
	if o.Model != "" {
		aConf.Transcribe.Model = o.Model
	}
	if len(o.Models) > 0 {
		aConf.ModelAliases = modelalias.Table{o.Model: o.Models}
	}
	if o.Format != "" {
		return o.Format
	}
	return outputFormat
}
